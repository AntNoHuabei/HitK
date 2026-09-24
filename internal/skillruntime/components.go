package skillruntime

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	// ErrNetworkApprovalRequired is returned before a component download starts.
	// The agent keeps the pending attachment alive while the UI asks the user.
	ErrNetworkApprovalRequired = errors.New("文档组件尚未安装，需要确认后下载")
	// ErrComponentCancelled is returned when the user cancels an initialization.
	ErrComponentCancelled = errors.New("文档组件初始化已取消")
)

type componentOperation struct {
	cancel          context.CancelFunc
	cancelRequested bool
}

type componentReadyRecord struct {
	Status SkillEnvironmentStatus `json:"status"`
}

// Close cancels in-flight component downloads so application shutdown does not
// leave a goroutine writing after the data directory begins tearing down.
func (m *Manager) Close() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.componentOps))
	for _, op := range m.componentOps {
		if op != nil && op.cancel != nil {
			cancels = append(cancels, op.cancel)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// WaitSkillEnvironment waits until a component becomes ready or reaches a
// terminal failure/cancellation. A missing component emits approval_required
// so the frontend can present the first-use confirmation.
func (m *Manager) WaitSkillEnvironment(ctx context.Context, name string) (SkillEnvironmentStatus, error) {
	name = normalizeName(name)
	if name == "" {
		return SkillEnvironmentStatus{}, errors.New("Skill 名称无效")
	}
	m.mu.Lock()
	startVersion := m.componentSequence
	m.mu.Unlock()
	for {
		status := m.GetSkillEnvironment(name)
		switch status.State {
		case "ready":
			return status, nil
		case "failed":
			return status, errors.New(status.Error)
		case "approval_required":
			m.publishComponentStatus(status)
		}

		waiter := make(chan SkillEnvironmentStatus, 1)
		m.mu.Lock()
		if current, ok := m.componentStatuses[name]; ok && isTerminalComponentState(current.State) && m.componentVersions[name] > startVersion {
			sendLatestStatus(waiter, current)
		}
		m.componentWaiters[name] = append(m.componentWaiters[name], waiter)
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			m.removeComponentWaiter(name, waiter)
			return SkillEnvironmentStatus{}, ctx.Err()
		case next := <-waiter:
			switch next.State {
			case "ready":
				return next, nil
			case "failed":
				return next, errors.New(next.Error)
			case "cancelled":
				return next, ErrComponentCancelled
			}
		}
	}
}

// CancelSkillEnvironment stops an in-flight download or releases a pending
// first-use wait. It is safe to call when no download is active.
func (m *Manager) CancelSkillEnvironment(name string) error {
	name = normalizeName(name)
	if name == "" {
		return errors.New("Skill 名称无效")
	}
	m.mu.Lock()
	if cancel := m.planOps[name]; cancel != nil {
		cancel()
		m.mu.Unlock()
		return nil
	}
	if op := m.componentOps[name]; op != nil {
		op.cancelRequested = true
		if op.cancel != nil {
			op.cancel()
		}
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	status := m.GetSkillEnvironment(name)
	status.State = "cancelled"
	status.Error = ErrComponentCancelled.Error()
	status.UpdatedAt = nowMillis()
	m.notifyComponentStatus(name, status)
	return nil
}

func (m *Manager) componentForSkill(skill string) (ComponentSpec, bool, error) {
	manifest, err := m.Manifest()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ComponentSpec{}, false, nil
		}
		return ComponentSpec{}, false, err
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	skill = normalizeName(skill)
	for _, component := range manifest.Components {
		if normalizeName(component.Skill) != skill {
			continue
		}
		if component.Platform != "" && component.Platform != platform {
			continue
		}
		if normalizeName(component.ID) == "" {
			return ComponentSpec{}, false, errors.New("组件 ID 无效")
		}
		return component, true, nil
	}
	return ComponentSpec{}, false, nil
}

func (m *Manager) componentSkillStatus(manifest SkillManifest, component ComponentSpec, planHash string) SkillEnvironmentStatus {
	status := SkillEnvironmentStatus{
		Skill:            manifest.Name,
		State:            "approval_required",
		PlanHash:         planHash,
		RuntimeVersion:   m.runtimeVersion(manifest.Runtime.Kind),
		ComponentVersion: component.Version,
		Source:           component.Source,
		BytesTotal:       component.CompressedBytes,
		UpdatedAt:        nowMillis(),
	}
	if component.URL == "" || len(component.SHA256) != sha256.Size*2 {
		status.State = "failed"
		status.Error = "文档组件发布信息不完整"
		return status
	}
	if mismatch := m.componentRuntimeMismatch(component); mismatch != "" {
		status.State = "failed"
		status.Error = mismatch
		return status
	}

	m.mu.Lock()
	if m.componentOps[manifest.Name] != nil {
		if active, ok := m.componentStatuses[manifest.Name]; ok {
			m.mu.Unlock()
			return active
		}
	}
	m.mu.Unlock()

	target := m.componentTarget(component)
	marker := m.componentMarker(component)
	if data, err := os.ReadFile(marker); err == nil {
		var record componentReadyRecord
		if json.Unmarshal(data, &record) == nil && record.Status.State == "ready" && dirExists(target) {
			record.Status.Skill = manifest.Name
			record.Status.PlanHash = planHash
			record.Status.RuntimeVersion = m.runtimeVersion(manifest.Runtime.Kind)
			if record.Status.ComponentVersion == "" {
				record.Status.ComponentVersion = component.Version
			}
			if record.Status.Source == "" {
				record.Status.Source = component.Source
			}
			record.Status.BytesReceived = component.CompressedBytes
			record.Status.BytesTotal = component.CompressedBytes
			record.Status.SpeedBytesPerSecond = 0
			record.Status.UpdatedAt = nowMillis()
			return record.Status
		}
	}
	if _, err := os.Stat(target); err == nil {
		_ = os.RemoveAll(target)
	}

	partial := m.componentArchive(component) + ".partial"
	if info, err := os.Stat(partial); err == nil && info.Size() > 0 {
		status.BytesReceived = info.Size()
	}
	return status
}

func (m *Manager) prepareComponent(ctx context.Context, manifest SkillManifest, component ComponentSpec, allowNetwork bool) (SkillEnvironmentStatus, error) {
	current := m.componentSkillStatus(manifest, component, "")
	if current.State == "ready" {
		return current, nil
	}
	if current.State == "downloading" || current.State == "verifying" || current.State == "installing" {
		return m.WaitSkillEnvironment(ctx, manifest.Name)
	}
	if !allowNetwork {
		current.State = "approval_required"
		current.Error = ""
		current.UpdatedAt = nowMillis()
		m.publishComponentStatus(current)
		return current, ErrNetworkApprovalRequired
	}

	m.mu.Lock()
	if m.componentOps[manifest.Name] != nil {
		m.mu.Unlock()
		return m.WaitSkillEnvironment(ctx, manifest.Name)
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.componentOps[manifest.Name] = &componentOperation{cancel: cancel}
	m.componentStatuses[manifest.Name] = SkillEnvironmentStatus{
		Skill:            manifest.Name,
		State:            "downloading",
		ComponentVersion: component.Version,
		Source:           component.Source,
		BytesTotal:       component.CompressedBytes,
		UpdatedAt:        nowMillis(),
	}
	m.mu.Unlock()

	status, err := m.installComponent(runCtx, manifest, component)
	cancel()

	m.mu.Lock()
	op := m.componentOps[manifest.Name]
	cancelRequested := op != nil && op.cancelRequested
	delete(m.componentOps, manifest.Name)
	m.mu.Unlock()

	if err != nil {
		if cancelRequested || errors.Is(err, context.Canceled) {
			status.State = "cancelled"
			status.Error = ErrComponentCancelled.Error()
			err = ErrComponentCancelled
		} else {
			status.State = "failed"
			status.Error = err.Error()
		}
		status.UpdatedAt = nowMillis()
		m.notifyComponentStatus(manifest.Name, status)
		return status, err
	}
	status.State = "ready"
	status.Error = ""
	status.UpdatedAt = nowMillis()
	m.notifyComponentStatus(manifest.Name, status)
	return status, nil
}

func (m *Manager) installComponent(ctx context.Context, manifest SkillManifest, component ComponentSpec) (SkillEnvironmentStatus, error) {
	status := SkillEnvironmentStatus{
		Skill:            manifest.Name,
		State:            "downloading",
		ComponentVersion: component.Version,
		Source:           component.Source,
		BytesTotal:       component.CompressedBytes,
		UpdatedAt:        nowMillis(),
	}
	if component.ArchiveType != "" && component.ArchiveType != "zip" {
		return status, fmt.Errorf("不支持的组件压缩格式: %s", component.ArchiveType)
	}
	if err := os.MkdirAll(m.componentRoot(component), 0o755); err != nil {
		return status, err
	}
	archive := m.componentArchive(component)
	if err := m.downloadComponent(ctx, component, archive, &status); err != nil {
		return status, err
	}

	status.State = "verifying"
	status.BytesReceived = component.CompressedBytes
	status.SpeedBytesPerSecond = 0
	status.UpdatedAt = nowMillis()
	m.publishComponentStatus(status)
	if err := m.downloader.VerifyChecksum(archive, component.SHA256); err != nil {
		_ = os.Remove(archive)
		return status, err
	}

	status.State = "installing"
	status.UpdatedAt = nowMillis()
	m.publishComponentStatus(status)
	target := m.componentTarget(component)
	temporary := target + ".partial"
	_ = os.RemoveAll(temporary)
	if err := extractComponentZip(archive, temporary, component.UnpackedBytes); err != nil {
		_ = os.RemoveAll(temporary)
		return status, err
	}
	for _, rel := range manifest.Assets {
		if _, err := os.Stat(filepath.Join(temporary, filepath.FromSlash(rel))); err != nil {
			_ = os.RemoveAll(temporary)
			return status, fmt.Errorf("组件缺少 Skill 资源: %s", rel)
		}
	}
	_ = os.RemoveAll(target)
	if err := os.Rename(temporary, target); err != nil {
		_ = os.RemoveAll(temporary)
		return status, err
	}
	status.State = "ready"
	status.Environment = target
	status.BytesReceived = component.CompressedBytes
	status.BytesTotal = component.CompressedBytes
	status.UpdatedAt = nowMillis()
	if err := m.writeComponentMarker(component, status); err != nil {
		return status, err
	}
	_ = os.Remove(archive)
	return status, nil
}

// downloadComponent follows the Herdsman runtime installer: the downloader
// owns retries, resume checkpoints and segmented transfers, while the runtime
// layer polls its snapshot once a second and translates it into install state.
func (m *Manager) downloadComponent(ctx context.Context, component ComponentSpec, archive string, status *SkillEnvironmentStatus) error {
	if m.downloader == nil {
		return errors.New("downloader not configured")
	}
	expectedSize := component.CompressedBytes
	downloadID := m.downloader.GenerateDownloadID(component.URL, archive)
	pollCtx, stopPolling := context.WithCancel(ctx)
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				progress, err := m.downloader.GetProgress(downloadID)
				if err != nil {
					continue
				}
				status.State = "downloading"
				status.BytesReceived = progress.Downloaded
				status.BytesTotal = progress.Size
				status.SpeedBytesPerSecond = int64(progress.Speed)
				status.UpdatedAt = nowMillis()
				m.publishComponentStatus(*status)
			}
		}
	}()

	err := m.downloader.DownloadWithContext(ctx, component.URL, archive, component.SHA256, expectedSize)
	stopPolling()
	<-pollDone
	if progress, progressErr := m.downloader.GetProgress(downloadID); progressErr == nil {
		status.State = "downloading"
		status.BytesReceived = progress.Downloaded
		status.BytesTotal = progress.Size
		status.SpeedBytesPerSecond = int64(progress.Speed)
		status.UpdatedAt = nowMillis()
		m.publishComponentStatus(*status)
	}
	if err != nil {
		return err
	}
	return nil
}

func extractComponentZip(archive, destination string, expectedBytes int64) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("打开组件压缩包失败: %w", err)
	}
	defer reader.Close()
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	var extracted int64
	for _, entry := range reader.File {
		rel := filepath.Clean(filepath.FromSlash(entry.Name))
		if rel == "." {
			continue
		}
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("组件包含越界路径: %s", entry.Name)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("组件包含不支持的符号链接: %s", entry.Name)
		}
		target := filepath.Join(destination, rel)
		if !within(destination, target) {
			return fmt.Errorf("组件包含越界路径: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = source.Close()
			return err
		}
		written, copyErr := io.Copy(output, source)
		closeErr := output.Close()
		_ = source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		extracted += written
		if expectedBytes > 0 && extracted > expectedBytes+(16<<20) {
			return errors.New("组件解压后超过声明大小")
		}
	}
	return nil
}

func (m *Manager) componentRuntimeMismatch(component ComponentSpec) string {
	for name, expected := range component.RequiredRuntimes {
		if actual := m.runtimeVersion(name); actual != expected {
			return fmt.Sprintf("组件要求 %s %s，当前为 %s", name, expected, actual)
		}
	}
	return ""
}

func (m *Manager) componentRoot(component ComponentSpec) string {
	return filepath.Join(m.dataRoot, "runtime-components", normalizeName(component.Skill))
}

func (m *Manager) componentTarget(component ComponentSpec) string {
	hash := strings.ToLower(component.SHA256)
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return filepath.Join(m.componentRoot(component), normalizeName(component.ID)+"-"+component.Version+"-"+hash)
}

func (m *Manager) componentArchive(component ComponentSpec) string {
	hash := strings.ToLower(component.SHA256)
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return filepath.Join(m.componentRoot(component), normalizeName(component.ID)+"-"+hash+".zip")
}

func (m *Manager) componentMarker(component ComponentSpec) string {
	return m.componentTarget(component) + ".json"
}

func (m *Manager) writeComponentMarker(component ComponentSpec, status SkillEnvironmentStatus) error {
	if err := os.MkdirAll(m.componentRoot(component), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(componentReadyRecord{Status: status}, "", "  ")
	if err != nil {
		return err
	}
	temporary := m.componentMarker(component) + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, m.componentMarker(component))
}

func (m *Manager) removeComponent(skill string) error {
	component, ok, err := m.componentForSkill(skill)
	if err != nil || !ok {
		return err
	}
	m.mu.Lock()
	active := m.componentOps[skill] != nil
	m.mu.Unlock()
	if active {
		return errors.New("组件正在下载，无法移除")
	}
	return os.RemoveAll(m.componentRoot(component))
}

func (m *Manager) publishComponentStatus(status SkillEnvironmentStatus) {
	m.notifyComponentStatus(status.Skill, status)
}

func (m *Manager) notifyComponentStatus(skill string, status SkillEnvironmentStatus) {
	skill = normalizeName(skill)
	m.mu.Lock()
	m.componentSequence++
	m.componentVersions[skill] = m.componentSequence
	m.componentStatuses[skill] = status
	waiters := m.componentWaiters[skill]
	m.componentWaiters[skill] = nil
	publish := m.progress
	m.mu.Unlock()
	for _, waiter := range waiters {
		sendLatestStatus(waiter, status)
		close(waiter)
	}
	if publish != nil {
		publish(RuntimeProgress{Skill: skill, Status: status})
	}
}

func (m *Manager) removeComponentWaiter(skill string, target chan SkillEnvironmentStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	waiters := m.componentWaiters[skill]
	for i, waiter := range waiters {
		if waiter == target {
			m.componentWaiters[skill] = append(waiters[:i], waiters[i+1:]...)
			return
		}
	}
}

func sendLatestStatus(ch chan SkillEnvironmentStatus, status SkillEnvironmentStatus) {
	select {
	case ch <- status:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- status:
	default:
	}
}

func isTerminalComponentState(state string) bool {
	return state == "ready" || state == "failed" || state == "cancelled"
}

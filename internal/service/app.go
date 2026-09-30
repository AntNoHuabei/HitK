// Package service exposes HitK's backend to the frontend as Wails services.
//
// Each service is a plain struct with exported methods; Wails generates a typed
// TypeScript binding for every method so the frontend never hand-writes a fetch
// call. Services share one [Container] holding the knowledge base, agent,
// provider registry and configuration.
package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hitk/hitk/internal/agent"
	"github.com/hitk/hitk/internal/companion"
	"github.com/hitk/hitk/internal/config"
	"github.com/hitk/hitk/internal/document"
	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
	"github.com/hitk/hitk/internal/skillcatalog"
	"github.com/hitk/hitk/internal/skillruntime"
	"github.com/hitk/hitk/internal/tools"
)

// Container wires the backend components together.
type Container struct {
	Paths     config.Paths
	Config    *config.Store
	KB        *kb.Store
	Agent     *agent.Agent
	Providers *provider.Registry
	Registry  *tools.Registry
	Sessions  *agent.SessionStore
	Runtime   *skillruntime.Manager
	Characters *companion.Registry
	Documents *document.Engine
	Catalog   *skillcatalog.Catalog
	Resolver  *skillcatalog.Resolver
	PlanStore skillcatalog.PlanStore
	Installer *skillcatalog.Installer

	// emit publishes protocol events to the frontend.
	emit func(*protocol.Event)
}

// Emitter is the callback the container uses to publish events.
func (c *Container) Emitter() func(*protocol.Event) { return c.emit }

// NewContainer builds the backend, opening the knowledge base and thread store.
func NewContainer(paths config.Paths, store *config.Store, emit func(*protocol.Event)) (*Container, error) {
	kbStore, err := kb.Open(paths.KBFile)
	if err != nil {
		return nil, fmt.Errorf("打开知识库失败: %w", err)
	}
	sessions, err := agent.NewSessionStore(paths.ThreadsDir)
	if err != nil {
		kbStore.Close()
		return nil, fmt.Errorf("打开会话目录失败: %w", err)
	}
	companionPath := paths.CompanionFile
	if companionPath == "" {
		companionPath = filepath.Join(paths.DataDir, "companion.db")
	}
	registryPath := paths.CompanionRegistryFile
	if registryPath == "" {
		registryPath = filepath.Join(paths.DataDir, "companions.db")
	}
	companionsDir := paths.CompanionDir
	if companionsDir == "" {
		companionsDir = filepath.Join(paths.DataDir, "companions")
	}
	characters, err := companion.OpenRegistry(registryPath, companionsDir, companionPath)
	if err != nil {
		kbStore.Close()
		return nil, fmt.Errorf("打开陪伴角色注册表失败: %w", err)
	}

	providers := provider.NewRegistry()
	cfg := store.Get()
	providers.Set(cfg.Vendors)
	if emb := providers.EmbedderFor(cfg.EmbeddingVendor); emb != nil && cfg.EmbeddingModel != "" {
		kbStore.SetEmbedder(remoteEmbedder{inner: emb})
	}

	runtimeManager := skillruntime.NewManager(skillruntime.DefaultRuntimeRoot(), paths.DataDir)
	documents := document.NewEngine(runtimeManager)
	workspace := cfg.Workspace
	if workspace == "" || !dirExists(workspace) {
		if wd, err := os.Getwd(); err == nil {
			workspace = wd
		}
	}
	catalog := skillcatalog.NewCatalog(skillruntime.DefaultRuntimeRoot(), paths.DataDir)
	catalog.SetWorkspace(workspace)
	resolver := skillcatalog.NewResolver(skillruntime.DefaultRuntimeRoot())
	installer := skillcatalog.NewInstaller(paths.DataDir)
	registry := tools.NewDefaultRegistryWithSkills(tools.SkillRunDependencies{
		Catalog:  catalog,
		Resolver: resolver,
		Executor: runtimeManager,
	})
	kbStore.SetFileParser(func(ctx context.Context, path string) (*kb.ParsedDocument, error) {
		parsed, parseErr := extractDocumentWithRuntime(ctx, documents, runtimeManager, path, workspace)
		if parseErr != nil {
			return nil, parseErr
		}
		return &kb.ParsedDocument{Title: parsed.Title, Text: parsed.Text, Mime: parsed.Mime, SkippedReason: parsed.SkippedReason}, nil
	}, documents.Supported)

	c := &Container{
		Paths:     paths,
		Config:    store,
		KB:        kbStore,
		Providers: providers,
		Registry:  registry,
		Sessions:  sessions,
		Runtime:   runtimeManager,
		Characters: characters,
		Documents: documents,
		Catalog:   catalog,
		Resolver:  resolver,
		PlanStore: skillcatalog.NewPlanStore(paths.DataDir),
		Installer: installer,
		emit:      emit,
	}
	c.Agent = agent.New(agent.Options{
		KB:           kbStore,
		Documents:    documents,
		Runtime:      runtimeManager,
		Tools:        registry,
		Providers:    providers,
		Sessions:     sessions,
		Companion:    companionRouter{registry: characters},
		Emit:         emit,
		Workspace:    workspace,
		ContextLimit: cfg.ContextLimit,
		MaxSteps:     cfg.MaxSteps,
	})
	if ctxLimit := contextLimitFor(cfg.Provider, cfg.Model); ctxLimit > 0 {
		c.Agent.SetContextLimit(ctxLimit)
	}
	// Both bridges are character-agnostic: the profile travels with every call,
	// so one pair serves every companion world.
	characters.Configure(
		&companionModelBridge{providers: providers, config: store},
		&companionConversationBridge{config: store, sessions: sessions, agent: c.Agent, providers: providers},
	)
	return c, nil
}

// companionRouter sends agent-side companion callbacks to whichever character
// owns the thread the turn is running in.
type companionRouter struct{ registry *companion.Registry }

func (rt companionRouter) PrepareCompanionTurn(
	ctx context.Context, threadID, turnID, taskID, userText string,
) (string, error) {
	manager, err := rt.registry.ManagerForThread(threadID)
	if err != nil {
		return "", err
	}
	return manager.PrepareCompanionTurn(ctx, threadID, turnID, taskID, userText)
}

func (rt companionRouter) CompleteCompanionTurn(threadID, turnID, taskID, userText, assistantText string) {
	if manager, err := rt.registry.ManagerForThread(threadID); err == nil {
		manager.CompleteCompanionTurn(threadID, turnID, taskID, userText, assistantText)
	}
}

func (rt companionRouter) FailCompanionTurn(threadID, turnID, taskID, message string) {
	if manager, err := rt.registry.ManagerForThread(threadID); err == nil {
		manager.FailCompanionTurn(threadID, turnID, taskID, message)
	}
}

// ActiveCompanion returns the manager behind the one active character.
func (c *Container) ActiveCompanion() (*companion.Manager, error) {
	if c.Characters == nil {
		return nil, fmt.Errorf("陪伴模块未初始化")
	}
	manager, err := c.Characters.Active()
	if err != nil {
		return nil, err
	}
	if manager == nil {
		return nil, fmt.Errorf("陪伴模块未初始化")
	}
	return manager, nil
}

type documentExtractor interface {
	Extract(context.Context, string, string) (document.Result, error)
}

type skillEnvironmentWaiter interface {
	WaitSkillEnvironment(context.Context, string) (skillruntime.SkillEnvironmentStatus, error)
}

func extractDocumentWithRuntime(
	ctx context.Context,
	extractor documentExtractor,
	waiter skillEnvironmentWaiter,
	path, workspace string,
) (document.Result, error) {
	result, err := extractor.Extract(ctx, path, workspace)
	if !errors.Is(err, skillruntime.ErrNetworkApprovalRequired) {
		return result, err
	}
	if waiter == nil {
		return result, err
	}
	if _, err := waiter.WaitSkillEnvironment(ctx, "document"); err != nil {
		return document.Result{}, err
	}
	return extractor.Extract(ctx, path, workspace)
}

// Close releases backend resources.
// Close releases the container's resources. It stops the agent first: turns run
// in the background and keep writing as they finish, so closing the knowledge
// store underneath them would race the tail of an in-flight turn.
func (c *Container) Close() {
	// Stop the companion registry first so no ticker can append a proactive
	// message while the generic agent is shutting down underneath it.
	if c.Characters != nil {
		_ = c.Characters.Close()
	}
	if c.Agent != nil {
		c.Agent.Shutdown()
	}
	if c.Runtime != nil {
		c.Runtime.Close()
	}
	if c.KB != nil {
		_ = c.KB.Close()
	}
}

// SetCompanionEmitter wires companion state updates to a dedicated Wails event
// for every character world.
func (c *Container) SetCompanionEmitter(emit func(*companion.Event)) {
	if c.Characters == nil {
		return
	}
	c.Characters.SetEmitter(func(event companion.Event) {
		if emit != nil {
			emit(&event)
		}
	})
}

// SetLogger gives the companion module the same structured application logger.
func (c *Container) SetLogger(logger *slog.Logger) {
	if c.Characters != nil {
		c.Characters.SetLogger(logger)
	}
}

// StartCompanion starts the active character's catch-up and Tick loop. Inactive
// characters stay frozen until they are switched to.
func (c *Container) StartCompanion() {
	if c.Characters != nil {
		c.Characters.Start(context.Background())
	}
}

// SetRuntimeProgressEmitter wires Skill component download updates to a
// dedicated typed Wails event.
func (c *Container) SetRuntimeProgressEmitter(emit func(*skillruntime.RuntimeProgress)) {
	if c.Runtime == nil {
		return
	}
	c.Runtime.SetProgressPublisher(func(progress skillruntime.RuntimeProgress) {
		if emit != nil {
			emit(&progress)
		}
	})
}

// remoteEmbedder bridges provider.Embedder to kb.Embedder without the kb
// package importing concrete provider clients.
type remoteEmbedder struct{ inner provider.Embedder }

// ID implements [kb.Embedder].
func (r remoteEmbedder) ID() string { return r.inner.ID() }

// Dimensions implements [kb.Embedder].
func (r remoteEmbedder) Dimensions() int { return r.inner.Dimensions() }

// Embed implements [kb.Embedder].
func (r remoteEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return r.inner.Embed(ctx, texts)
}

// ---------------------------------------------------------------------------
// AppService
// ---------------------------------------------------------------------------

// AppService exposes application-level operations.
type AppService struct{ c *Container }

// ArtifactPreview is a bounded, workspace-confined file preview for the chat UI.
type ArtifactPreview struct {
	Path      string `json:"path"`
	Mime      string `json:"mime"`
	Content   string `json:"content,omitempty"`
	Data      string `json:"data,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// NewAppService builds the service.
func NewAppService(c *Container) *AppService { return &AppService{c: c} }

// ServiceName implements [application.ServiceName].
func (*AppService) ServiceName() string { return "App" }

// Info describes the running application.
type Info struct {
	Version     string   `json:"version"`
	DataDir     string   `json:"dataDir"`
	Workspace   string   `json:"workspace"`
	OS          string   `json:"os"`
	Arch        string   `json:"arch"`
	GoVersion   string   `json:"goVersion"`
	Mode        string   `json:"mode"`
	Policy      string   `json:"policy"`
	KBStats     kb.Stats `json:"kbStats"`
	ThreadCount int      `json:"threadCount"`
}

// Version is the release identifier, overridden at build time.
var Version = "0.1.0"

// GetInfo returns a snapshot of application state for the shell UI.
func (s *AppService) GetInfo() Info {
	cfg := s.c.Config.Get()
	info := Info{
		Version:   Version,
		DataDir:   s.c.Paths.DataDir,
		Workspace: s.c.Agent.Workspace(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		Mode:      string(cfg.Mode),
		Policy:    string(cfg.Policy),
	}
	if s.c.KB != nil {
		info.KBStats = s.c.KB.Stats()
	}
	if threads, err := s.c.Sessions.List(); err == nil {
		info.ThreadCount = len(threads)
	}
	return info
}

// PickDirectory opens the native folder picker and returns the chosen path.
// An empty string means the user cancelled.
func (s *AppService) PickDirectory(title string) (string, error) {
	if title == "" {
		title = "选择工作区目录"
	}
	dir, err := pickDirectory(title)
	if err != nil {
		return "", nil // a cancelled dialog is not an error the UI should show
	}
	return dir, nil
}

// PickFiles opens the native file picker for knowledge-base import.
func (s *AppService) PickFiles(title string) ([]string, error) {
	if title == "" {
		title = "选择要导入的文件"
	}
	files, err := pickFiles(title)
	if err != nil {
		return nil, nil
	}
	return files, nil
}

// SetWorkspace switches the active project root.
func (s *AppService) SetWorkspace(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("目录不能为空")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !dirExists(abs) {
		return fmt.Errorf("目录不存在: %s", abs)
	}
	s.c.Agent.SetWorkspace(abs)
	if s.c.Catalog != nil {
		s.c.Catalog.SetWorkspace(abs)
	}
	return s.c.Config.AddRecentWorkspace(abs)
}

// SetMode records the active agent mode.
func (s *AppService) SetMode(mode string) error {
	m := protocol.Mode(mode)
	if !m.Valid() {
		return fmt.Errorf("未知模式: %s", mode)
	}
	return s.c.Config.Update(func(c *config.Config) { c.Mode = m })
}

// SetPolicy records the approval policy.
func (s *AppService) SetPolicy(policy string) error {
	p := protocol.ApprovalPolicy(policy)
	switch p {
	case protocol.ApprovalReadOnly, protocol.ApprovalAuto, protocol.ApprovalManual:
	default:
		return fmt.Errorf("未知审批策略: %s", policy)
	}
	return s.c.Config.Update(func(c *config.Config) { c.Policy = p })
}

// RevealInFileManager opens the data directory in the OS file browser.
func (s *AppService) RevealInFileManager(path string) error {
	if path == "" {
		path = s.c.Paths.DataDir
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

// PreviewArtifact returns a small preview only for files under the active
// workspace. The UI never gets arbitrary local-file read access.
func (s *AppService) PreviewArtifact(path string) (ArtifactPreview, error) {
	abs, err := s.resolveWorkspaceArtifact(path)
	if err != nil {
		return ArtifactPreview{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ArtifactPreview{}, err
	}
	const limit = 512 << 10
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	mime := artifactMime(abs)
	preview := ArtifactPreview{Path: abs, Mime: mime, Truncated: truncated}
	if strings.HasPrefix(mime, "image/") {
		preview.Data = base64.StdEncoding.EncodeToString(data)
	} else {
		preview.Content = string(data)
	}
	return preview, nil
}

// RevealArtifact locates a workspace-confined artifact in the system file
// manager. It is intentionally separate from the general data-directory
// action so artifact paths cannot escape through junctions or symlinks.
func (s *AppService) RevealArtifact(path string) error {
	abs, err := s.resolveWorkspaceArtifact(path)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,"+abs).Start()
	case "darwin":
		return exec.Command("open", "-R", abs).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(abs)).Start()
	}
}

func (s *AppService) resolveWorkspaceArtifact(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	workspace, err := filepath.EvalSymlinks(s.c.Agent.Workspace())
	if err != nil {
		return "", fmt.Errorf("工作区不可用: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(workspace, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("产物不在当前工作区内")
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("产物文件不可用")
	}
	return resolved, nil
}

func artifactMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	default:
		return "text/plain"
	}
}

// ---------------------------------------------------------------------------
// SettingsService
// ---------------------------------------------------------------------------

// SettingsService manages configuration and vendor credentials.
type SettingsService struct{ c *Container }

// NewSettingsService builds the service.
func NewSettingsService(c *Container) *SettingsService { return &SettingsService{c: c} }

// ServiceName implements [application.ServiceName].
func (*SettingsService) ServiceName() string { return "Settings" }

// VendorView is a catalog entry joined with the user's saved settings.
type VendorView struct {
	provider.Vendor
	Configured bool `json:"configured"`
	Ready      bool `json:"ready"`
	HasKey     bool `json:"hasKey"`
	// MaskedKey shows the last four characters so a user can recognise which
	// key is stored without exposing it.
	MaskedKey string `json:"maskedKey,omitempty"`
	ActiveURL string `json:"activeUrl"`
}

// Settings is the app-level settings payload. Model providers live in
// [ModelService] because they have their own deeper editing surface.
type Settings struct {
	Mode             string            `json:"mode"`
	Policy           string            `json:"policy"`
	MaxSteps         int               `json:"maxSteps"`
	ContextLimit     int               `json:"contextLimit"`
	EmbeddingVendor  string            `json:"embeddingVendor"`
	EmbeddingModel   string            `json:"embeddingModel"`
	Workspace        string            `json:"workspace"`
	RecentWorkspaces []string          `json:"recentWorkspaces"`
	Onboarded        bool              `json:"onboarded"`
	Preferences      map[string]string `json:"preferences"`
	KBEmbedder       string            `json:"kbEmbedder"`
	Versions         map[string]string `json:"versions"`
}

// Get returns the full settings payload.
func (s *SettingsService) Get() Settings {
	cfg := s.c.Config.Get()
	out := Settings{
		Mode:             string(cfg.Mode),
		Policy:           string(cfg.Policy),
		MaxSteps:         cfg.MaxSteps,
		ContextLimit:     cfg.ContextLimit,
		EmbeddingVendor:  cfg.EmbeddingVendor,
		EmbeddingModel:   cfg.EmbeddingModel,
		Workspace:        s.c.Agent.Workspace(),
		RecentWorkspaces: cfg.RecentWorkspaces,
		Onboarded:        cfg.Onboarded,
		Preferences:      cfg.Preferences,
		Versions:         map[string]string{"hitk": Version},
	}
	if s.c.KB != nil {
		out.KBEmbedder = s.c.KB.EmbedderID()
	}
	return out
}

// SaveOptions updates behavioural settings.
func (s *SettingsService) SaveOptions(maxSteps, contextLimit int, embeddingVendor, embeddingModel string) error {
	if maxSteps < 1 {
		maxSteps = 1
	}
	if maxSteps > protocol.MaxStepsCeiling {
		maxSteps = protocol.MaxStepsCeiling
	}
	if contextLimit < 0 {
		contextLimit = 0
	}
	if err := s.c.Config.Update(func(c *config.Config) {
		c.MaxSteps = maxSteps
		c.ContextLimit = contextLimit
		c.EmbeddingVendor = embeddingVendor
		c.EmbeddingModel = embeddingModel
	}); err != nil {
		return err
	}

	// Rebuild the agent so the new step budget takes effect without a restart,
	// and re-point the embedder so the next ingest uses the new model.
	cfg := s.c.Config.Get()
	if emb := s.c.Providers.EmbedderFor(embeddingVendor); emb != nil && embeddingModel != "" {
		s.c.KB.SetEmbedder(remoteEmbedder{inner: emb})
	} else {
		s.c.KB.SetEmbedder(nil)
	}
	limit := contextLimit
	if limit <= 0 {
		limit = contextLimitFor(cfg.Provider, cfg.Model)
	}
	s.c.Agent.SetContextLimit(limit)
	s.c.Agent.SetMaxSteps(maxSteps)
	return nil
}

// TestResult reports a connectivity probe outcome.
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Latency int64  `json:"latencyMs"`
	Reply   string `json:"reply,omitempty"`
	Model   string `json:"model,omitempty"`
}

// CompleteOnboard marks the first-run wizard as finished.
func (s *SettingsService) CompleteOnboard() error {
	return s.c.Config.Update(func(c *config.Config) { c.Onboarded = true })
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func maskKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) <= 8 {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", 6) + string(r[len(r)-4:])
}

func friendlyError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "401"), strings.Contains(msg, "invalid_api_key"), strings.Contains(msg, "no API key"):
		return "鉴权失败：API Key 无效或已过期，请检查后重试"
	case strings.Contains(msg, "404"):
		return "端点或模型不存在：请检查 Base URL 与模型名"
	case strings.Contains(msg, "429"):
		return "已触发限流或额度不足，请稍后重试"
	case strings.Contains(msg, "connect"), strings.Contains(msg, "timeout"), strings.Contains(msg, "no such host"):
		return "网络连接失败：请检查网络或代理设置。原始信息：" + msg
	}
	return msg
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

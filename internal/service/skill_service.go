package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hitk/hitk/internal/skillcatalog"
	"github.com/hitk/hitk/internal/skillmodel"
)

// ListSkills returns the merged compatibility catalog.
func (s *RuntimeService) ListSkills() []skillmodel.SkillSummary {
	if s.c.Catalog == nil {
		return []skillmodel.SkillSummary{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := s.c.Catalog.List(ctx)
	if err != nil {
		return []skillmodel.SkillSummary{}
	}
	return items
}

// ReloadSkills invalidates the source snapshot and re-discovers packages.
func (s *RuntimeService) ReloadSkills() error {
	if s.c.Catalog == nil {
		return errors.New("Skill Catalog 不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.c.Catalog.ReloadAll(ctx)
}

// ResolveSkill produces a side-effect-free dependency plan and persists it for
// inspection by the runtime settings UI.
func (s *RuntimeService) ResolveSkill(name string) (skillmodel.ResolvedPlan, error) {
	if s.c.Catalog == nil || s.c.Resolver == nil {
		return skillmodel.ResolvedPlan{}, errors.New("Skill 解析器不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	started := time.Now()
	pkg, err := s.c.Catalog.Get(ctx, name)
	if err != nil {
		return skillmodel.ResolvedPlan{}, err
	}
	analysis, err := s.c.Resolver.Analyze(ctx, pkg)
	if err != nil {
		return skillmodel.ResolvedPlan{}, err
	}
	if err := s.c.PlanStore.WriteAnalysis(pkg, analysis); err != nil {
		return skillmodel.ResolvedPlan{}, err
	}
	plan, resolveErr := s.c.Resolver.Resolve(ctx, pkg, analysis)
	plan.ID = pkg.ID
	plan.SourceHash = pkg.SourceHash
	plan.SourceRoot = pkg.Root
	if writeErr := s.c.PlanStore.WriteResolved(pkg, plan); writeErr != nil {
		return skillmodel.ResolvedPlan{}, writeErr
	}
	if resolveErr != nil {
		slog.Warn("skill resolution incomplete", "skill", pkg.ID, "unresolved", len(plan.Unresolved), "durationMs", time.Since(started).Milliseconds())
		return plan, resolveErr
	}
	slog.Info("skill resolution complete", "skill", pkg.ID, "dependencies", len(plan.Dependencies), "planHash", plan.PlanHash, "durationMs", time.Since(started).Milliseconds())
	return plan, nil
}

// GetSkillPlan returns the persisted resolved plan when present, otherwise it
// returns an empty plan without triggering network or filesystem changes.
func (s *RuntimeService) GetSkillPlan(name string) (skillmodel.ResolvedPlan, error) {
	if s.c.Catalog == nil {
		return skillmodel.ResolvedPlan{}, errors.New("Skill Catalog 不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pkg, err := s.c.Catalog.Get(ctx, name)
	if err != nil {
		return skillmodel.ResolvedPlan{}, err
	}
	return s.c.PlanStore.ReadResolved(pkg)
}

// PrepareResolvedSkill locks dependencies and materializes the isolated
// environment. allowNetwork must come from an explicit UI confirmation.
func (s *RuntimeService) PrepareResolvedSkill(name string, allowNetwork bool) (skillmodel.SkillEnvironmentStatus, error) {
	plan, err := s.ResolveSkill(name)
	if err != nil && !errors.Is(err, skillmodel.ErrUnresolvedDependencies) {
		return skillmodel.SkillEnvironmentStatus{}, err
	}
	if s.c.Runtime == nil {
		return skillmodel.SkillEnvironmentStatus{}, errors.New("运行时管理器不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	return s.c.Runtime.PrepareResolvedPlan(ctx, plan, allowNetwork)
}

// InstallSkill imports a local, ZIP, Git or previously resolved SkillHub
// package into HitK's immutable catalog root.
func (s *RuntimeService) InstallSkill(request skillcatalog.InstallRequest) (string, error) {
	if s.c.Installer == nil {
		return "", errors.New("Skill 安装器不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	path, err := s.c.Installer.Install(ctx, request)
	if err != nil {
		return "", err
	}
	if s.c.Catalog != nil {
		_ = s.c.Catalog.ReloadAll(ctx)
	}
	return path, nil
}

// ResolveSkillHubFeed downloads and validates a marketplace feed. Installation
// remains a separate user action for each selected entry.
func (s *RuntimeService) ResolveSkillHubFeed(url string, allowNetwork bool) (skillcatalog.SkillHubFeed, error) {
	if s.c.Installer == nil {
		return skillcatalog.SkillHubFeed{}, errors.New("Skill 安装器不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return s.c.Installer.ResolveFeed(ctx, url, allowNetwork)
}

func (s *RuntimeService) RollbackSkill(name string) error {
	if s.c.Installer == nil {
		return errors.New("Skill 安装器不可用")
	}
	if err := s.c.Installer.Rollback(name); err != nil {
		return err
	}
	if s.c.Catalog != nil {
		return s.c.Catalog.ReloadAll(context.Background())
	}
	return nil
}

func (s *RuntimeService) PruneSkill(name string) error {
	if s.c.Installer == nil {
		return errors.New("Skill 安装器不可用")
	}
	return s.c.Installer.Prune(name)
}

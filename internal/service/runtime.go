package service

import (
	"context"
	"errors"
	"time"

	"github.com/hitk/hitk/internal/skillruntime"
)

// RuntimeService exposes only lifecycle operations. Skill execution remains
// behind the agent/tool boundary and cannot be invoked directly by the UI.
type RuntimeService struct{ c *Container }

func NewRuntimeService(c *Container) *RuntimeService { return &RuntimeService{c: c} }
func (*RuntimeService) ServiceName() string          { return "Runtime" }

func (s *RuntimeService) ListRuntimes() []skillruntime.RuntimeStatus {
	if s.c.Runtime == nil {
		return []skillruntime.RuntimeStatus{}
	}
	return s.c.Runtime.ListRuntimes()
}

func (s *RuntimeService) GetSkillEnvironment(name string) skillruntime.SkillEnvironmentStatus {
	if s.c.Runtime == nil {
		return skillruntime.SkillEnvironmentStatus{Skill: name, State: "failed", Error: "运行时管理器不可用"}
	}
	return s.c.Runtime.GetSkillEnvironment(name)
}

func (s *RuntimeService) PrepareSkillEnvironment(name string, allowNetwork bool) (skillruntime.SkillEnvironmentStatus, error) {
	if s.c.Runtime == nil {
		return skillruntime.SkillEnvironmentStatus{}, errors.New("运行时管理器不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return s.c.Runtime.PrepareSkillEnvironment(ctx, name, allowNetwork)
}

func (s *RuntimeService) RepairSkillEnvironment(name string, allowNetwork bool) (skillruntime.SkillEnvironmentStatus, error) {
	if s.c.Runtime == nil {
		return skillruntime.SkillEnvironmentStatus{}, errors.New("运行时管理器不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return s.c.Runtime.RepairSkillEnvironment(ctx, name, allowNetwork)
}

func (s *RuntimeService) RemoveSkillEnvironment(name string) error {
	if s.c.Runtime == nil {
		return errors.New("运行时管理器不可用")
	}
	return s.c.Runtime.RemoveSkillEnvironment(name)
}

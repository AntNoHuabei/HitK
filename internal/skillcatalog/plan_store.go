package skillcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
)

type planStore struct {
	dataRoot string
}

type PlanStore = planStore

func NewPlanStore(dataRoot string) planStore {
	return planStore{dataRoot: filepath.Clean(dataRoot)}
}

func (s planStore) AnalysisPath(pkg skillmodel.Package) string {
	return filepath.Join(s.root(pkg), "analysis.json")
}

func (s planStore) ResolvedPath(pkg skillmodel.Package) string {
	return filepath.Join(s.root(pkg), "resolved-skill.json")
}

func (s planStore) WriteAnalysis(pkg skillmodel.Package, plan skillmodel.AnalysisPlan) error {
	return writeJSONAtomic(s.AnalysisPath(pkg), plan)
}

func (s planStore) ReadAnalysis(pkg skillmodel.Package) (skillmodel.AnalysisPlan, error) {
	var plan skillmodel.AnalysisPlan
	err := readJSON(s.AnalysisPath(pkg), &plan)
	return plan, err
}

func (s planStore) WriteResolved(pkg skillmodel.Package, plan skillmodel.ResolvedPlan) error {
	return writeJSONAtomic(s.ResolvedPath(pkg), plan)
}

func (s planStore) ReadResolved(pkg skillmodel.Package) (skillmodel.ResolvedPlan, error) {
	var plan skillmodel.ResolvedPlan
	err := readJSON(s.ResolvedPath(pkg), &plan)
	return plan, err
}

func (s planStore) root(pkg skillmodel.Package) string {
	source := normalizeID(string(pkg.Source))
	id := normalizeID(pkg.ID)
	hash := pkg.SourceHash
	if len(hash) > 16 {
		hash = hash[:16]
	}
	return filepath.Join(s.dataRoot, "skill-resolutions", source, id, hash)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("解析计划失败: %w", err)
	}
	return nil
}

func ComputePlanHash(plan skillmodel.ResolvedPlan) string {
	hash := sha256.New()
	write := func(value string) {
		_, _ = io.WriteString(hash, value)
		_, _ = io.WriteString(hash, "\x00")
	}
	write(plan.Analysis.SourceHash)
	write(plan.ResolverVersion)
	write(plan.Runtime.Kind)
	write(plan.Runtime.Constraint)
	write(plan.LockDigest)
	write(plan.Entry)
	write(plan.Environment)
	for _, dependency := range plan.Dependencies {
		write(strings.ToLower(dependency.Ecosystem + ":" + dependency.PackageName + "@" + dependency.Version))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

func IsPlanNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

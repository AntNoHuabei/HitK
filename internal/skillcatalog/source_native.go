package skillcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
)

type nativeSkillJSON struct {
	SchemaVersion  int                          `json:"schemaVersion"`
	Name           string                       `json:"name"`
	DisplayName    string                       `json:"displayName,omitempty"`
	Description    string                       `json:"description,omitempty"`
	Version        string                       `json:"version,omitempty"`
	Builtin        bool                         `json:"builtin,omitempty"`
	Kind           skillmodel.SkillKind         `json:"kind,omitempty"`
	Invocation     skillmodel.InvocationMode    `json:"invocation,omitempty"`
	Runtime        skillmodel.RuntimeConstraint `json:"runtime"`
	Entry          string                       `json:"entry"`
	Assets         []string                     `json:"assets,omitempty"`
	PackageManager skillmodel.PackageManager    `json:"packageManager,omitempty"`
	Permissions    skillmodel.SkillPermissions  `json:"permissions,omitempty"`
	Budget         skillmodel.SkillBudget       `json:"budget,omitempty"`
}

func loadNativePackage(root string, spec rootSpec) (skillmodel.Package, error) {
	data, err := os.ReadFile(filepath.Join(root, "skill.json"))
	if err != nil {
		return skillmodel.Package{}, err
	}
	var raw nativeSkillJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return skillmodel.Package{}, fmt.Errorf("skill.json 无效: %w", err)
	}
	if strings.TrimSpace(raw.Name) == "" {
		return skillmodel.Package{}, fmt.Errorf("skill.json 缺少 name")
	}
	kind := raw.Kind
	if kind == "" {
		if raw.Runtime.Kind != "" || raw.Entry != "" {
			kind = skillmodel.SkillKindScript
		} else {
			kind = skillmodel.SkillKindPrompt
		}
	}
	invocation := raw.Invocation
	if invocation == "" && kind == skillmodel.SkillKindScript {
		invocation = skillmodel.InvocationJSON
	}
	files, sourceHash, err := collectPackageFiles(contextBackground(), root)
	if err != nil {
		return skillmodel.Package{}, err
	}
	manifest := skillmodel.SkillManifest{
		SchemaVersion:  raw.SchemaVersion,
		Name:           strings.TrimSpace(raw.Name),
		Version:        strings.TrimSpace(raw.Version),
		Builtin:        raw.Builtin,
		Kind:           kind,
		Invocation:     invocation,
		Runtime:        raw.Runtime,
		Entry:          filepath.ToSlash(strings.TrimSpace(raw.Entry)),
		Assets:         append([]string(nil), raw.Assets...),
		PackageManager: raw.PackageManager,
		Permissions:    raw.Permissions,
		Budget:         raw.Budget,
		Source:         spec.source,
		SourceID:       normalizeID(raw.Name),
		SourceHash:     sourceHash,
	}
	sourceMetadata := readSourceMetadata(root)
	if manifest.Version == "" {
		manifest.Version = sourceMetadata.Version
	}
	return skillmodel.Package{
		ID:           normalizeID(raw.Name),
		Name:         manifest.Name,
		DisplayName:  firstNonEmpty(raw.DisplayName, manifest.Name),
		Description:  raw.Description,
		Version:      manifest.Version,
		Publisher:    sourceMetadata.Publisher,
		SourceURL:    sourceMetadata.URL,
		Ref:          sourceMetadata.Ref,
		SourceSHA256: sourceMetadata.SHA256,
		Kind:         kind,
		Source:       spec.source,
		Trust:        spec.trust,
		Entry:        manifest.Entry,
		Manifest:     manifest,
		Files:        files,
		SourceHash:   sourceHash,
		Root:         root,
		SkillFile:    filepath.Join(root, "skill.json"),
	}, nil
}

func contextBackground() context.Context {
	// Kept as a small helper so native loading stays testable without threading
	// a context through the old JSON-only path.
	return context.Background()
}

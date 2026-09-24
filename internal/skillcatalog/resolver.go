package skillcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
	toml "github.com/pelletier/go-toml/v2"
)

const ResolverVersion = "hitk-skill-resolver-v1"

var pythonPackageMap = map[string]string{
	"PIL":       "Pillow",
	"cv2":       "opencv-python",
	"fitz":      "PyMuPDF",
	"pymupdf":   "PyMuPDF",
	"docx":      "python-docx",
	"pptx":      "python-pptx",
	"yaml":      "PyYAML",
	"bs4":       "beautifulsoup4",
	"sklearn":   "scikit-learn",
	"OpenSSL":   "pyOpenSSL",
	"dateutil":  "python-dateutil",
	"serial":    "pyserial",
	"tesseract": "pytesseract",
}

type Resolver struct {
	pythonPath string
	nodePath   string
}

func NewResolver(runtimeRoot string) *Resolver {
	return &Resolver{
		pythonPath: filepath.Join(runtimeRoot, "python", "python.exe"),
		nodePath:   filepath.Join(runtimeRoot, "node", "node.exe"),
	}
}

func (r *Resolver) Analyze(ctx context.Context, pkg skillmodel.Package) (skillmodel.AnalysisPlan, error) {
	plan := skillmodel.AnalysisPlan{
		SchemaVersion: 1,
		SourceHash:    pkg.SourceHash,
		Kind:          pkg.Kind,
		Invocation:    pkg.Manifest.Invocation,
		Entry:         pkg.Entry,
		Runtime:       pkg.Manifest.Runtime,
		Metadata:      map[string]string{},
	}
	if pkg.Kind == skillmodel.SkillKindPrompt {
		return plan, nil
	}
	if plan.Invocation == "" {
		plan.Invocation = skillmodel.InvocationJSON
	}
	if pkg.RuntimeRootMissing() {
		return plan, errors.New("script Skill 缺少源目录")
	}
	if plan.Runtime.Kind == "" {
		return plan, errors.New("script Skill 缺少 runtime.kind")
	}

	declared, declaredFiles := declaredDependencies(pkg)
	plan.Dependencies = append(plan.Dependencies, declared...)
	if len(declaredFiles) > 0 {
		plan.Metadata["declared"] = strings.Join(declaredFiles, ",")
	}

	switch plan.Runtime.Kind {
	case "python":
		scan, err := analyzePython(ctx, pkg.Root, r.pythonPath)
		if err != nil {
			return plan, err
		}
		plan.Hints = append(plan.Hints, scan.Hints...)
		for _, importName := range scan.Imports {
			candidate, ok := pythonCandidate(importName)
			if !ok {
				plan.Unresolved = append(plan.Unresolved, skillmodel.DependencyCandidate{
					Ecosystem:  "python",
					ImportName: importName,
					Source:     "ast",
					Confidence: 20,
				})
				continue
			}
			plan.Dependencies = append(plan.Dependencies, candidate)
		}
	case "node":
		imports, hints, err := analyzeNode(ctx, pkg.Root)
		if err != nil {
			return plan, err
		}
		plan.Hints = append(plan.Hints, hints...)
		for _, importName := range imports {
			plan.Dependencies = append(plan.Dependencies, skillmodel.DependencyCandidate{
				Ecosystem:   "node",
				ImportName:  importName,
				PackageName: importName,
				Source:      "import",
				Confidence:  75,
			})
		}
	default:
		return plan, fmt.Errorf("不支持的 Skill runtime: %s", plan.Runtime.Kind)
	}

	for _, hint := range plan.Hints {
		if hint.Package == "" {
			continue
		}
		candidate := skillmodel.DependencyCandidate{
			Ecosystem:   hint.Ecosystem,
			PackageName: hint.Package,
			Source:      "embedded",
			Confidence:  90,
		}
		if hint.Ecosystem == "python" {
			candidate.ImportName = hint.Package
		}
		plan.Dependencies = append(plan.Dependencies, candidate)
	}
	plan.Dependencies = mergeCandidates(plan.Dependencies)
	plan.Unresolved = mergeCandidates(plan.Unresolved)
	return plan, nil
}

func (r *Resolver) Resolve(_ context.Context, pkg skillmodel.Package, analysis skillmodel.AnalysisPlan) (skillmodel.ResolvedPlan, error) {
	resolved := skillmodel.ResolvedPlan{
		SchemaVersion:   1,
		Analysis:        analysis,
		Runtime:         analysis.Runtime,
		Entry:           analysis.Entry,
		Kind:            analysis.Kind,
		Invocation:      analysis.Invocation,
		Environment:     "none",
		Permissions:     pkg.Manifest.Permissions,
		Budget:          pkg.Manifest.Budget,
		Dependencies:    append([]skillmodel.DependencyCandidate(nil), analysis.Dependencies...),
		Unresolved:      append([]skillmodel.DependencyCandidate(nil), analysis.Unresolved...),
		ResolverVersion: ResolverVersion,
	}
	if analysis.Kind == skillmodel.SkillKindScript {
		resolved.Environment = "venv"
		if analysis.Runtime.Kind == "python" {
			resolved.PackageManager = skillmodel.PackageManager{Name: "uv", DependencyFile: "pyproject.toml", Lockfile: "uv.lock"}
		} else {
			resolved.PackageManager = skillmodel.PackageManager{Name: "pnpm", DependencyFile: "package.json", Lockfile: "pnpm-lock.yaml"}
		}
	}
	resolved.PlanHash = ComputePlanHash(resolved)
	if len(resolved.Unresolved) > 0 {
		return resolved, skillmodel.ErrUnresolvedDependencies
	}
	return resolved, nil
}

func declaredDependencies(pkg skillmodel.Package) ([]skillmodel.DependencyCandidate, []string) {
	var candidates []skillmodel.DependencyCandidate
	var files []string
	if pkg.Manifest.Runtime.Kind == "python" {
		path := filepath.Join(pkg.Root, "pyproject.toml")
		if data, err := os.ReadFile(path); err == nil {
			files = append(files, "pyproject.toml")
			var doc struct {
				Project struct {
					Dependencies []string `toml:"dependencies"`
				} `toml:"project"`
			}
			if toml.Unmarshal(data, &doc) == nil {
				for _, dep := range doc.Project.Dependencies {
					name, version := splitRequirement(dep)
					if name != "" {
						candidates = append(candidates, skillmodel.DependencyCandidate{Ecosystem: "python", PackageName: name, Version: version, Source: "declared", Confidence: 100})
					}
				}
			}
		}
	}
	if pkg.Manifest.Runtime.Kind == "node" {
		path := filepath.Join(pkg.Root, "package.json")
		if data, err := os.ReadFile(path); err == nil {
			files = append(files, "package.json")
			var doc struct {
				Dependencies map[string]string `json:"dependencies"`
			}
			if json.Unmarshal(data, &doc) == nil {
				for name, version := range doc.Dependencies {
					candidates = append(candidates, skillmodel.DependencyCandidate{Ecosystem: "node", PackageName: name, Version: version, Source: "declared", Confidence: 100})
				}
			}
		}
	}
	candidates = mergeCandidates(candidates)
	sort.Strings(files)
	return candidates, files
}

func pythonCandidate(importName string) (skillmodel.DependencyCandidate, bool) {
	pkg, ok := pythonPackageMap[importName]
	if !ok {
		return skillmodel.DependencyCandidate{}, false
	}
	return skillmodel.DependencyCandidate{
		Ecosystem:   "python",
		ImportName:  importName,
		PackageName: pkg,
		Source:      "import-map",
		Confidence:  80,
	}, true
}

func splitRequirement(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	for i, r := range value {
		if strings.ContainsRune("<>=!~;[@", r) {
			return strings.TrimSpace(value[:i]), strings.TrimSpace(value[i:])
		}
	}
	return value, ""
}

func mergeCandidates(values []skillmodel.DependencyCandidate) []skillmodel.DependencyCandidate {
	merged := map[string]skillmodel.DependencyCandidate{}
	for _, candidate := range values {
		if strings.TrimSpace(candidate.PackageName) == "" {
			key := candidate.Ecosystem + ":" + candidate.ImportName
			if _, exists := merged[key]; !exists {
				merged[key] = candidate
			}
			continue
		}
		key := strings.ToLower(candidate.Ecosystem) + ":" + strings.ToLower(candidate.PackageName)
		previous, exists := merged[key]
		if !exists || candidate.Confidence >= previous.Confidence {
			merged[key] = candidate
		}
	}
	out := make([]skillmodel.DependencyCandidate, 0, len(merged))
	for _, candidate := range merged {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ecosystem != out[j].Ecosystem {
			return out[i].Ecosystem < out[j].Ecosystem
		}
		return out[i].PackageName < out[j].PackageName
	})
	return out
}

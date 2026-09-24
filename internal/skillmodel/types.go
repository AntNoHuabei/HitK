// Package skillmodel contains the stable domain types shared by Skill
// discovery, dependency resolution and runtime execution.
//
// It intentionally has no dependency on HitK's filesystem, process, downloader
// or agent packages. Keeping this package leaf-level prevents the catalog and
// runtime layers from developing import cycles as compatibility support grows.
package skillmodel

import "strings"

const ManifestSchemaVersion = 1

type Download struct {
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	ArchiveRoot string `json:"archiveRoot,omitempty"`
	ArchiveType string `json:"archiveType,omitempty"`
	LicenseURL  string `json:"licenseUrl,omitempty"`
}

type RuntimeSpec struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Download Download `json:"download"`
}

type RuntimeManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	BundleVersion string          `json:"bundleVersion"`
	Platform      string          `json:"platform"`
	PayloadDigest string          `json:"payloadDigest,omitempty"`
	Runtimes      []RuntimeSpec   `json:"runtimes"`
	Components    []ComponentSpec `json:"components,omitempty"`
}

// ComponentSpec describes a large, optional Skill payload. The small runtime
// manifest and Skill launcher ship with HitK; the immutable payload is fetched
// only after the user asks for the capability.
type ComponentSpec struct {
	ID               string            `json:"id"`
	Skill            string            `json:"skill"`
	Version          string            `json:"version"`
	Platform         string            `json:"platform"`
	Source           string            `json:"source"`
	URL              string            `json:"url"`
	SHA256           string            `json:"sha256"`
	ArchiveType      string            `json:"archiveType"`
	CompressedBytes  int64             `json:"compressedBytes"`
	UnpackedBytes    int64             `json:"unpackedBytes"`
	InstallDirectory string            `json:"installDirectory"`
	RequiredRuntimes map[string]string `json:"requiredRuntimes,omitempty"`
}

type RuntimeStatus struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Path      string `json:"path"`
	Available bool   `json:"available"`
	Verified  bool   `json:"verified"`
	Message   string `json:"message,omitempty"`
}

type RuntimeConstraint struct {
	Kind       string `json:"kind"` // python | node
	Constraint string `json:"constraint"`
}

type PackageManager struct {
	Name           string `json:"name"` // uv | pnpm | npm
	DependencyFile string `json:"dependencyFile,omitempty"`
	Lockfile       string `json:"lockfile,omitempty"`
}

type SkillPermissions struct {
	InstallNetwork bool `json:"installNetwork"`
	RuntimeNetwork bool `json:"runtimeNetwork"`
}

type SkillBudget struct {
	TimeoutSeconds int   `json:"timeoutSeconds,omitempty"`
	MaxOutputBytes int   `json:"maxOutputBytes,omitempty"`
	MemoryBytes    int64 `json:"memoryBytes,omitempty"`
}

type SkillKind string

const (
	SkillKindPrompt        SkillKind = "prompt"
	SkillKindScript        SkillKind = "script"
	SkillKindPluginCommand SkillKind = "plugin-command"
	SkillKindPluginHook    SkillKind = "plugin-hook"
	SkillKindPluginMCP     SkillKind = "plugin-mcp"
)

type InvocationMode string

const (
	InvocationJSON InvocationMode = "json_stdin"
	InvocationArgv InvocationMode = "argv"
)

type SourceKind string

const (
	SourceNative    SourceKind = "native"
	SourceWorkBuddy SourceKind = "workbuddy"
	SourceClaude    SourceKind = "claude"
	SourceSkillHub  SourceKind = "skillhub"
)

type TrustLevel string

const (
	TrustBuiltin     TrustLevel = "builtin"
	TrustProject     TrustLevel = "project"
	TrustUser        TrustLevel = "user"
	TrustPlugin      TrustLevel = "plugin"
	TrustMarketplace TrustLevel = "marketplace"
)

type SkillManifest struct {
	SchemaVersion  int               `json:"schemaVersion"`
	Name           string            `json:"name"`
	Version        string            `json:"version,omitempty"`
	Builtin        bool              `json:"builtin,omitempty"`
	Kind           SkillKind         `json:"kind,omitempty"`
	Invocation     InvocationMode    `json:"invocation,omitempty"`
	Runtime        RuntimeConstraint `json:"runtime,omitempty"`
	Entry          string            `json:"entry,omitempty"`
	Assets         []string          `json:"assets,omitempty"`
	PackageManager PackageManager    `json:"packageManager,omitempty"`
	Permissions    SkillPermissions  `json:"permissions,omitempty"`
	Budget         SkillBudget       `json:"budget,omitempty"`

	// Source metadata is persisted only in generated plans. Native skill.json
	// files may omit all three fields.
	Source     SourceKind `json:"source,omitempty"`
	SourceID   string     `json:"sourceId,omitempty"`
	SourceHash string     `json:"sourceHash,omitempty"`
}

type SkillSummary struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	DisplayName  string     `json:"displayName,omitempty"`
	Description  string     `json:"description,omitempty"`
	Kind         SkillKind  `json:"kind"`
	Source       SourceKind `json:"source"`
	Version      string     `json:"version,omitempty"`
	Publisher    string     `json:"publisher,omitempty"`
	SourceURL    string     `json:"sourceUrl,omitempty"`
	Ref          string     `json:"ref,omitempty"`
	SourceSHA256 string     `json:"sourceSha256,omitempty"`
	Trust        TrustLevel `json:"trust"`
	State        string     `json:"state,omitempty"`
	Mutating     bool       `json:"mutating,omitempty"`
}

// Package is an immutable Skill source. Root is deliberately excluded from
// JSON so plans never persist machine-specific source paths.
type Package struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	DisplayName  string         `json:"displayName,omitempty"`
	Description  string         `json:"description,omitempty"`
	Version      string         `json:"version,omitempty"`
	Publisher    string         `json:"publisher,omitempty"`
	SourceURL    string         `json:"sourceUrl,omitempty"`
	Ref          string         `json:"ref,omitempty"`
	SourceSHA256 string         `json:"sourceSha256,omitempty"`
	Kind         SkillKind      `json:"kind"`
	Source       SourceKind     `json:"source"`
	Trust        TrustLevel     `json:"trust"`
	Entry        string         `json:"entry,omitempty"`
	Manifest     SkillManifest  `json:"manifest"`
	Body         string         `json:"body,omitempty"`
	Files        []string       `json:"files,omitempty"`
	Plugin       *PluginPackage `json:"plugin,omitempty"`
	SourceHash   string         `json:"sourceHash,omitempty"`
	Root         string         `json:"-"`
	SkillFile    string         `json:"-"`
}

// RuntimeRootMissing reports whether a script package lacks a source root.
// This helper keeps analyzers from importing filesystem helpers just to guard
// malformed in-memory test packages.
func (p Package) RuntimeRootMissing() bool {
	return strings.TrimSpace(p.Root) == ""
}

type PluginDependency struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Marketplace string `json:"marketplace,omitempty"`
}

type PluginPackage struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	Version        string             `json:"version,omitempty"`
	Root           string             `json:"-"`
	DataDir        string             `json:"-"`
	Dependencies   []PluginDependency `json:"dependencies,omitempty"`
	Commands       []string           `json:"commands,omitempty"`
	Hooks          string             `json:"hooks,omitempty"`
	MCPServers     string             `json:"mcpServers,omitempty"`
	DefaultEnabled bool               `json:"defaultEnabled,omitempty"`
}

type DependencyCandidate struct {
	Ecosystem   string `json:"ecosystem"` // python | node
	ImportName  string `json:"importName,omitempty"`
	PackageName string `json:"packageName"`
	Version     string `json:"version,omitempty"`
	Confidence  int    `json:"confidence,omitempty"`
	Source      string `json:"source,omitempty"`
}

type EmbeddedInstallHint struct {
	Ecosystem string `json:"ecosystem"`
	Command   string `json:"command,omitempty"`
	Package   string `json:"package,omitempty"`
}

type AnalysisPlan struct {
	SchemaVersion int                   `json:"schemaVersion"`
	SourceHash    string                `json:"sourceHash"`
	Kind          SkillKind             `json:"kind"`
	Invocation    InvocationMode        `json:"invocation,omitempty"`
	Entry         string                `json:"entry,omitempty"`
	Runtime       RuntimeConstraint     `json:"runtime,omitempty"`
	Dependencies  []DependencyCandidate `json:"dependencies,omitempty"`
	Hints         []EmbeddedInstallHint `json:"hints,omitempty"`
	Unresolved    []DependencyCandidate `json:"unresolved,omitempty"`
	Metadata      map[string]string     `json:"metadata,omitempty"`
}

type ResolvedPlan struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	Analysis        AnalysisPlan          `json:"analysis"`
	Runtime         RuntimeConstraint     `json:"runtime"`
	Entry           string                `json:"entry"`
	Kind            SkillKind             `json:"kind"`
	Invocation      InvocationMode        `json:"invocation,omitempty"`
	Environment     string                `json:"environment"` // venv | component | none
	PackageManager  PackageManager        `json:"packageManager,omitempty"`
	Permissions     SkillPermissions      `json:"permissions,omitempty"`
	Budget          SkillBudget           `json:"budget,omitempty"`
	Dependencies    []DependencyCandidate `json:"dependencies,omitempty"`
	Unresolved      []DependencyCandidate `json:"unresolved,omitempty"`
	LockDigest      string                `json:"lockDigest,omitempty"`
	ResolverVersion string                `json:"resolverVersion"`
	PlanHash        string                `json:"planHash,omitempty"`

	// Runtime-only identity. Source roots are machine-specific and therefore
	// never persisted in JSON.
	ID         string `json:"-"`
	SourceHash string `json:"-"`
	SourceRoot string `json:"-"`
}

type SkillEnvironmentStatus struct {
	Skill               string `json:"skill"`
	State               string `json:"state"`
	Environment         string `json:"environment,omitempty"`
	PlanHash            string `json:"planHash,omitempty"`
	RuntimeVersion      string `json:"runtimeVersion,omitempty"`
	ComponentVersion    string `json:"componentVersion,omitempty"`
	Source              string `json:"source,omitempty"`
	BytesReceived       int64  `json:"bytesReceived,omitempty"`
	BytesTotal          int64  `json:"bytesTotal,omitempty"`
	SpeedBytesPerSecond int64  `json:"speedBytesPerSecond,omitempty"`
	Error               string `json:"error,omitempty"`
	UpdatedAt           int64  `json:"updatedAt"`
}

const RuntimeProgressEventName = "hitk:runtime-progress"

// RuntimeProgress is emitted for both first-use approval and every material
// download/install transition. The frontend can reconnect by reading the
// current SkillEnvironmentStatus, so events are deliberately stateless.
type RuntimeProgress struct {
	Skill  string                 `json:"skill"`
	Status SkillEnvironmentStatus `json:"status"`
}

type SkillRequest struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type SkillArtifact struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Mime      string `json:"mime,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Operation string `json:"operation,omitempty"`
}

type SkillExecutionResult struct {
	Content   string                 `json:"content,omitempty"`
	Display   string                 `json:"display,omitempty"`
	Artifacts []SkillArtifact        `json:"artifacts,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Truncated bool                   `json:"truncated,omitempty"`
}

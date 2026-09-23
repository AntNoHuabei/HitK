package skillruntime

import "time"

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
	SchemaVersion int           `json:"schemaVersion"`
	BundleVersion string        `json:"bundleVersion"`
	Platform      string        `json:"platform"`
	PayloadDigest string        `json:"payloadDigest,omitempty"`
	Runtimes      []RuntimeSpec `json:"runtimes"`
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

type SkillManifest struct {
	SchemaVersion  int               `json:"schemaVersion"`
	Name           string            `json:"name"`
	Version        string            `json:"version,omitempty"`
	Builtin        bool              `json:"builtin,omitempty"`
	Runtime        RuntimeConstraint `json:"runtime"`
	Entry          string            `json:"entry"`
	Assets         []string          `json:"assets,omitempty"`
	PackageManager PackageManager    `json:"packageManager,omitempty"`
	Permissions    SkillPermissions  `json:"permissions,omitempty"`
	Budget         SkillBudget       `json:"budget,omitempty"`
}

type SkillEnvironmentStatus struct {
	Skill          string `json:"skill"`
	State          string `json:"state"` // none | preparing | ready | failed
	Environment    string `json:"environment,omitempty"`
	PlanHash       string `json:"planHash,omitempty"`
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
	Error          string `json:"error,omitempty"`
	UpdatedAt      int64  `json:"updatedAt"`
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

type environmentRecord struct {
	SkillEnvironmentStatus
	Manifest SkillManifest `json:"manifest"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

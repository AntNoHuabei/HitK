package skillruntime

import (
	"time"

	"github.com/hitk/hitk/internal/skillmodel"
)

// Domain type aliases preserve the existing skillruntime API and Wails JSON
// shape while the shared model lives in the leaf-level skillmodel package.
type Download = skillmodel.Download
type RuntimeSpec = skillmodel.RuntimeSpec
type RuntimeManifest = skillmodel.RuntimeManifest
type ComponentSpec = skillmodel.ComponentSpec
type RuntimeStatus = skillmodel.RuntimeStatus
type RuntimeConstraint = skillmodel.RuntimeConstraint
type PackageManager = skillmodel.PackageManager
type SkillPermissions = skillmodel.SkillPermissions
type SkillBudget = skillmodel.SkillBudget
type SkillManifest = skillmodel.SkillManifest
type SkillEnvironmentStatus = skillmodel.SkillEnvironmentStatus
type RuntimeProgress = skillmodel.RuntimeProgress
type SkillRequest = skillmodel.SkillRequest
type SkillArtifact = skillmodel.SkillArtifact
type SkillExecutionResult = skillmodel.SkillExecutionResult

const ManifestSchemaVersion = skillmodel.ManifestSchemaVersion
const RuntimeProgressEventName = skillmodel.RuntimeProgressEventName

type environmentRecord struct {
	SkillEnvironmentStatus
	Manifest SkillManifest `json:"manifest"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

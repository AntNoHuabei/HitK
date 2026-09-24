package skillmodel

import "context"

// Source discovers immutable Skill packages. Sources must never install
// packages, create environments or execute Skill code.
type Source interface {
	Kind() SourceKind
	List(context.Context) ([]Package, error)
	Load(context.Context, string) (Package, error)
	Reload(context.Context, string) error
}

type Catalog interface {
	List(context.Context) ([]SkillSummary, error)
	Get(context.Context, string) (Package, error)
	Reload(context.Context, string) error
	Mode() CompatibilityMode
}

type Resolver interface {
	Analyze(context.Context, Package) (AnalysisPlan, error)
	Resolve(context.Context, Package, AnalysisPlan) (ResolvedPlan, error)
}

// Executor is the narrow runtime surface consumed by tools and services.
type Executor interface {
	Prepare(context.Context, ResolvedPlan, bool) (SkillEnvironmentStatus, error)
	Execute(context.Context, string, SkillRequest) (SkillExecutionResult, error)
	ExecuteResolved(context.Context, ResolvedPlan, SkillRequest) (SkillExecutionResult, error)
	Cancel(string) error
	Remove(string) error
}

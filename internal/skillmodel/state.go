package skillmodel

import "errors"

const (
	StateDiscovered       = "discovered"
	StateResolving        = "resolving"
	StateApprovalRequired = "approval_required"
	StateDownloading      = "downloading"
	StateVerifying        = "verifying"
	StateInstalling       = "installing"
	StateReady            = "ready"
	StateFailed           = "failed"
	StateCancelled        = "cancelled"
)

var (
	ErrDependencyApprovalRequired = errors.New("Skill 依赖安装需要用户确认")
	ErrUnresolvedDependencies     = errors.New("Skill 存在无法解析的依赖")
	ErrRuntimeApprovalRequired    = errors.New("Skill 运行时初始化需要用户确认")
)

type CompatibilityMode string

const (
	CompatOff     CompatibilityMode = "off"
	CompatPrompt  CompatibilityMode = "prompt"
	CompatResolve CompatibilityMode = "resolve"
	CompatFull    CompatibilityMode = "full"
)

func ParseCompatibilityMode(value string) CompatibilityMode {
	switch CompatibilityMode(value) {
	case CompatOff, CompatPrompt, CompatResolve, CompatFull:
		return CompatibilityMode(value)
	default:
		return CompatResolve
	}
}

func (m CompatibilityMode) AllowsPrompt() bool {
	return m == CompatPrompt || m == CompatResolve || m == CompatFull
}

func (m CompatibilityMode) AllowsResolve() bool {
	return m == CompatResolve || m == CompatFull
}

func (m CompatibilityMode) AllowsScriptExecution() bool {
	return m == CompatFull
}

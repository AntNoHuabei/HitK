//go:build !windows

package skillruntime

import "os/exec"

type managedProcess struct{ cmd *exec.Cmd }

func startManagedProcess(cmd *exec.Cmd, _ int64) (*managedProcess, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &managedProcess{cmd: cmd}, nil
}
func (p *managedProcess) Kill() error  { return p.cmd.Process.Kill() }
func (p *managedProcess) Close() error { return nil }

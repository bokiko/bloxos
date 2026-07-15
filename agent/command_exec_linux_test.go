//go:build linux

package main

import (
	"context"
	"os/exec"
	"testing"
)

// TestConfigureCommandSetsProcessGroup locks in item 2: on Linux, commands run
// in their own process group with a cancel hook, so a timeout kills the whole
// tree (systemctl/docker may fork children) rather than just the direct child.
func TestConfigureCommandSetsProcessGroup(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	configureCommand(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("expected Setpgid=true so the command runs in its own process group")
	}
	if cmd.Cancel == nil {
		t.Fatal("expected a Cancel hook to kill the process group on timeout")
	}
}

//go:build linux

package main

import "os/exec"

// resolveNvidiaSmiPath returns an absolute path to nvidia-smi, or "" if
// not found. On Linux we lean on the user's PATH.
func resolveNvidiaSmiPath() string {
	if path, err := exec.LookPath("nvidia-smi"); err == nil {
		return path
	}
	return ""
}

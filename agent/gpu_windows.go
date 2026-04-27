//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveNvidiaSmiPath finds nvidia-smi.exe on Windows. Resolution order:
//
//  1. PATH lookup (handles user installs and CUDA on PATH).
//  2. C:\Windows\System32\nvidia-smi.exe — the driver installer drops a
//     thin shim here so it works without CUDA.
//  3. C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe — older
//     driver layout that some sysadmins still pin.
//  4. A directory walk under %ProgramFiles%\NVIDIA* picking up any
//     nvidia-smi.exe — covers GeForce Experience, Studio, etc.
//
// Returns "" if no candidate exists.
func resolveNvidiaSmiPath() string {
	if path, err := exec.LookPath("nvidia-smi.exe"); err == nil {
		return path
	}
	if path, err := exec.LookPath("nvidia-smi"); err == nil {
		return path
	}

	candidates := []string{
		`C:\Windows\System32\nvidia-smi.exe`,
		`C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`,
	}
	for _, p := range candidates {
		if fileExists(p) {
			return p
		}
	}

	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	entries, err := os.ReadDir(pf)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(e.Name()), "nvidia") {
			continue
		}
		base := filepath.Join(pf, e.Name())
		var found string
		_ = filepath.Walk(base, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if info == nil || info.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Base(path), "nvidia-smi.exe") {
				found = path
				return filepath.SkipDir
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

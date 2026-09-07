package main

import (
	"os"
	"strconv"
	"strings"
)

type pendingMarker struct {
	ExpectedSHA string
	Signature   string
	// Release is the embedded release sequence performUpdateWindows read
	// out of the staged binary. Informational: validatePendingUpdate
	// re-extracts it from the bytes and only uses this to detect a marker
	// and staged file that no longer belong together. 0 when absent or
	// unparseable (a marker written by an older agent).
	Release uint64
}

// parsePendingMarker reads the marker file and extracts sha256, signature
// and release. It handles arbitrary ordering, unknown fields, and duplicate
// fields (takes the last one).
func parsePendingMarker(path string) (pendingMarker, error) {
	var pm pendingMarker
	data, err := os.ReadFile(path)
	if err != nil {
		return pm, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "sha256=") {
			pm.ExpectedSHA = strings.TrimPrefix(line, "sha256=")
		} else if strings.HasPrefix(line, "signature=") {
			pm.Signature = strings.TrimPrefix(line, "signature=")
		} else if strings.HasPrefix(line, "release=") {
			seq, err := strconv.ParseUint(strings.TrimPrefix(line, "release="), 10, 64)
			if err != nil {
				seq = 0
			}
			pm.Release = seq
		}
	}
	return pm, nil
}

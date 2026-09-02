package main

import (
	"os"
	"strings"
)

type pendingMarker struct {
	ExpectedSHA string
	Signature   string
}

// parsePendingMarker reads the marker file and extracts sha256 and signature.
// It handles arbitrary ordering, unknown fields, and duplicate fields (takes the last one).
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
		}
	}
	return pm, nil
}

//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestBuildHelperBatchMovesNotDeletes(t *testing.T) {
	helper := buildHelperBatch("target.exe", "new.exe", "marker.pending")
	if strings.Contains(helper, "del \"target.exe\"") {
		t.Errorf("buildHelperBatch contains del target: %s", helper)
	}
	if !strings.Contains(helper, "move /Y \"new.exe\" \"target.exe\"") {
		t.Errorf("buildHelperBatch does not contain move /Y: %s", helper)
	}
}

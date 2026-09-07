package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePendingMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "marker.pending")

	tests := []struct {
		name        string
		content     string
		expectedSHA string
		expectedSig string
		expectedRel uint64
	}{
		{
			name:        "normal",
			content:     "source=a\ntarget=b\nat=time\nsha256=123456\nsignature=base64pad==\n",
			expectedSHA: "123456",
			expectedSig: "base64pad==",
		},
		{
			name:        "with release",
			content:     "sha256=123456\nsignature=sig=\nrelease=42\n",
			expectedSHA: "123456",
			expectedSig: "sig=",
			expectedRel: 42,
		},
		{
			name:        "unparseable release reads as absent",
			content:     "sha256=123456\nsignature=sig=\nrelease=forty-two\n",
			expectedSHA: "123456",
			expectedSig: "sig=",
			expectedRel: 0,
		},
		{
			name:        "different order with unknown fields",
			content:     "unknown=value\nsignature=sig=\ntarget=b\nsha256=abcdef\nfoo=bar\n",
			expectedSHA: "abcdef",
			expectedSig: "sig=",
		},
		{
			name:        "duplicate fields take last",
			content:     "sha256=first\nsha256=last\nsignature=sig1\nsignature=sig2\n",
			expectedSHA: "last",
			expectedSig: "sig2",
		},
		{
			name:        "missing signature",
			content:     "sha256=123456\n",
			expectedSHA: "123456",
			expectedSig: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("write failed: %v", err)
			}
			pm, err := parsePendingMarker(path)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if pm.ExpectedSHA != tt.expectedSHA {
				t.Errorf("expected sha256 %q, got %q", tt.expectedSHA, pm.ExpectedSHA)
			}
			if pm.Signature != tt.expectedSig {
				t.Errorf("expected signature %q, got %q", tt.expectedSig, pm.Signature)
			}
			if pm.Release != tt.expectedRel {
				t.Errorf("expected release %d, got %d", tt.expectedRel, pm.Release)
			}
		})
	}
}

package main

import (
	"fmt"
	"io"
)

// maxAgentBinarySize caps the bytes the agent will accept from the
// hub when downloading a self-update. Real agent binaries are ~10 MB;
// 250 MB leaves 25x headroom for future growth while shutting down
// a hub-side attack that announces a pathologically large payload to
// fill the agent's disk.
const maxAgentBinarySize int64 = 250 * 1024 * 1024

// downloadWithLimit copies r into w but rejects the copy if r had
// more than maxBytes available. The "+1" trick — read up to
// maxBytes+1 bytes through io.LimitReader — lets us distinguish
// "source exactly at the limit" from "source went past it" with a
// single io.Copy.
//
// Used by self-update download paths in agent/updater.go and
// agent/updater_windows.go. Pre-fix, both used a bare io.Copy(out,
// resp.Body) with no bound: a compromised hub announcing a
// multi-gigabyte response would fill /tmp on the agent host and
// wedge the system before the SHA verification step ever ran.
func downloadWithLimit(w io.Writer, r io.Reader, maxBytes int64) error {
	n, err := io.Copy(w, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return fmt.Errorf("download exceeded size limit (%d bytes)", maxBytes)
	}
	return nil
}

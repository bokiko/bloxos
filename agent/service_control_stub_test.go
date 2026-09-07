//go:build !windows

package main

// testServiceModes is the Windows-only hook that lets the test binary act as
// a service or as the detached restart helper; nothing to do elsewhere.
func testServiceModes() bool { return false }

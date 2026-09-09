package main

import (
	cryptoRand "crypto/rand"
	"encoding/hex"
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
)

// buildVersion and buildRevision identify the hub executable. They are stamped
// at link time by the release build (see the -X ldflags in the Dockerfile /
// build tooling, owned outside hub/); an unstamped build reports the defaults
// below so a developer or source run is never mislabelled as a release.
//
//	go build -ldflags "-X main.buildVersion=v1.2.2 -X main.buildRevision=<sha>"
var (
	buildVersion  = "development"
	buildRevision = "unknown"
)

// hubInstanceID is a cryptographically random identifier minted once per hub
// process at startup. It is NOT a secret and carries no host information (no
// paths, env, hostname, or keys): its only purpose is to let a verified upgrade
// confirm that the instance answering the public URL is the very process it
// just started — two processes on the identical version and revision still have
// distinct instance IDs. It is stable for the life of the process and changes
// on every restart.
var hubInstanceID = newHubInstanceID()

func newHubInstanceID() string {
	var b [16]byte
	if _, err := cryptoRand.Read(b[:]); err != nil {
		// crypto/rand failing is catastrophic and never expected; fail loud
		// rather than emit a predictable or empty identifier.
		log.Fatalf("build-info: cannot generate instance id: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// handleBuildInfo reports the hub executable's identity for upgrade
// verification. It is public and unauthenticated by design — a client must be
// able to confirm which build answers the public URL before it can log in —
// and reveals nothing sensitive. It is never cached, so a check always sees the
// process currently serving the endpoint. Distinct from /health (liveness only)
// and from the agent-version fields (which describe served agent payloads, not
// this executable).
func handleBuildInfo(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]string{
		"component":   "hub",
		"version":     buildVersion,
		"revision":    buildRevision,
		"instance_id": hubInstanceID,
	})
}

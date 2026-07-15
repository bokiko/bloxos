//go:build insecure

package main

import (
	"crypto/tls"
	"log"
	"os"
)

// buildAgentTLSConfig (insecure dev build) honors BLOXOS_TLS_INSECURE=1 to skip
// TLS verification entirely, with a loud warning. This file only compiles under
// `-tags insecure`; release/default builds use tls_secure.go, which can never
// disable verification. Do NOT ship binaries built with this tag.
func buildAgentTLSConfig(host string) (*tls.Config, error) {
	if os.Getenv("BLOXOS_TLS_INSECURE") == "1" {
		log.Printf("WARNING: BLOXOS_TLS_INSECURE=1 disables TLS verification for %s (insecure dev build)", host)
		return &tls.Config{InsecureSkipVerify: true}, nil
	}
	rootCAs, caPath, err := loadRootCAs()
	if err != nil {
		return nil, err
	}
	if caPath != "" {
		log.Printf("agent TLS: trusting additional CA from %s", caPath)
	}
	return &tls.Config{RootCAs: rootCAs}, nil
}

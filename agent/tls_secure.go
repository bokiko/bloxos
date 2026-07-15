//go:build !insecure

package main

import (
	"crypto/tls"
	"log"
)

// buildAgentTLSConfig returns the agent's TLS client config for wss:// and
// https:// connections. Release/default builds ALWAYS verify the server
// certificate using the system roots plus the optional BLOXOS_CA_CERT.
// BLOXOS_TLS_INSECURE is intentionally not honored here — the verification
// bypass only exists in `-tags insecure` dev builds (see tls_insecure.go).
func buildAgentTLSConfig(host string) (*tls.Config, error) {
	rootCAs, caPath, err := loadRootCAs()
	if err != nil {
		return nil, err
	}
	if caPath != "" {
		log.Printf("agent TLS: trusting additional CA from %s", caPath)
	}
	return &tls.Config{RootCAs: rootCAs}, nil
}

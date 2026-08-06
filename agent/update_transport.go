package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

/* ============================================================================
 * Self-update transport gate
 *
 * The download URL used to be derived from hubURL with "anything that isn't
 * wss becomes plain http". That made the entire self-update chain
 * unauthenticated on an HTTP-served hub: /download/agent is a public,
 * unauthenticated route, the SHA that authorises the swap arrives over the
 * same plaintext WebSocket, and the installed unit runs the agent as root.
 * Anyone on the network path could serve their own binary plus a matching
 * SHA and own every machine in the fleet.
 *
 * agentDownloadURL is the single place that decision is made now. Plaintext
 * is refused outright unless the hub is on loopback, where there is no
 * network path to sit on.
 * ============================================================================ */

// agentDownloadURL converts the agent's hub WebSocket URL into the URL of the
// hub's agent-binary download endpoint, refusing any transport that an
// off-host attacker could tamper with.
//
// osQuery, when non-empty, is appended as ?os=<value> so a Windows agent
// fetches the Windows binary.
func agentDownloadURL(rawHubURL, osQuery string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawHubURL))
	if err != nil {
		return "", fmt.Errorf("parse hub URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("hub URL %q has no host", rawHubURL)
	}

	var scheme string
	switch strings.ToLower(u.Scheme) {
	case "wss", "https":
		scheme = "https"
	case "ws", "http":
		if !isLoopbackHost(u.Host) {
			return "", fmt.Errorf(
				"refusing to self-update: hub URL %q uses plaintext %q, so both the "+
					"binary and the SHA authorising it would arrive unauthenticated; "+
					"re-enroll the agent against a wss:// hub",
				rawHubURL, u.Scheme)
		}
		// Loopback: the hub is on this machine, there is no network path
		// between us to intercept. Keep dev and single-box installs working.
		scheme = "http"
	default:
		return "", fmt.Errorf("unsupported hub URL scheme %q", u.Scheme)
	}

	downloadURL := scheme + "://" + u.Host + "/download/agent"
	if osQuery != "" {
		downloadURL += "?os=" + url.QueryEscape(osQuery)
	}
	return downloadURL, nil
}

// refuseRedirects is an http.Client.CheckRedirect that follows none. Without
// it, Go's default client follows up to 10 redirects — including an
// https→http downgrade — and only the first hop passes through
// agentDownloadURL's scheme check. The download is still SHA- and
// signature-verified regardless, so a followed redirect could not smuggle a
// different binary in; but a hub that redirects a signed-transport download
// through plaintext is a downgrade/DoS surface that has no business existing
// next to a gate whose entire point is refusing plaintext transports.
func refuseRedirects(req *http.Request, via []*http.Request) error {
	return fmt.Errorf("refusing to follow redirect to %s", req.URL)
}

// isLoopbackHost reports whether a URL host (with or without a port, IPv6
// literals included) refers to the local machine.
func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

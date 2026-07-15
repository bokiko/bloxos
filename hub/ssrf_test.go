package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		name    string
		ip      string
		homelab bool
		blocked bool
	}{
		{"ipv4 loopback", "127.0.0.1", true, true},   // never allowed, even with opt-in
		{"ipv6 loopback", "::1", true, true},         // never allowed
		{"unspecified v4", "0.0.0.0", true, true},    // never allowed
		{"unspecified v6", "::", true, true},         // never allowed
		{"cloud metadata", "169.254.169.254", true, true},
		{"link-local v4", "169.254.1.1", true, true},
		{"link-local v6", "fe80::1", true, true},
		{"multicast", "224.0.0.1", true, true},
		{"rfc1918 10/8 default", "10.20.30.40", false, true},
		{"rfc1918 192.168 default", "192.168.1.50", false, true},
		{"rfc1918 172.16 default", "172.20.5.10", false, true},
		{"ula v6 default", "fc00::1", false, true},
		{"rfc1918 10/8 homelab", "10.20.30.40", true, false},
		{"rfc1918 192.168 homelab", "192.168.1.50", true, false},
		{"ula v6 homelab", "fc00::1", true, false},
		{"public v4", "8.8.8.8", false, false},
		{"public v4 with homelab", "93.184.216.34", true, false},
		{"public v6", "2606:4700:4700::1111", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.homelab {
				t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "1")
			} else {
				t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "")
			}
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("bad test IP %q", tc.ip)
			}
			if got := isBlockedIP(ip); got != tc.blocked {
				t.Fatalf("isBlockedIP(%s) homelab=%v = %v, want %v", tc.ip, tc.homelab, got, tc.blocked)
			}
		})
	}
}

// withFakeResolver swaps lookupIPFunc for a static map and restores it.
func withFakeResolver(t *testing.T, m map[string][]string) {
	t.Helper()
	prev := lookupIPFunc
	t.Cleanup(func() { lookupIPFunc = prev })
	lookupIPFunc = func(_ context.Context, host string) ([]net.IPAddr, error) {
		ips, ok := m[host]
		if !ok {
			return nil, &net.DNSError{Err: "not found", Name: host, IsNotFound: true}
		}
		out := make([]net.IPAddr, 0, len(ips))
		for _, s := range ips {
			out = append(out, net.IPAddr{IP: net.ParseIP(s)})
		}
		return out, nil
	}
}

func TestResolveAndValidate(t *testing.T) {
	withFakeResolver(t, map[string][]string{
		"public.example":   {"93.184.216.34"},
		"evil-loopback":    {"127.0.0.1"},
		"evil-internal":    {"10.0.0.5"},
		"evil-metadata":    {"169.254.169.254"},
		"mixed":            {"93.184.216.34", "10.0.0.5"}, // one bad IP taints the set
	})

	t.Run("public host allowed", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "")
		ips, err := resolveAndValidate(context.Background(), "public.example")
		if err != nil {
			t.Fatalf("expected public host allowed, got %v", err)
		}
		if len(ips) != 1 || ips[0].String() != "93.184.216.34" {
			t.Fatalf("unexpected ips: %v", ips)
		}
	})

	t.Run("hostname resolving to loopback rejected", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "1")
		if _, err := resolveAndValidate(context.Background(), "evil-loopback"); err == nil {
			t.Fatal("expected rejection for host resolving to loopback")
		}
	})

	t.Run("hostname resolving to metadata rejected", func(t *testing.T) {
		if _, err := resolveAndValidate(context.Background(), "evil-metadata"); err == nil {
			t.Fatal("expected rejection for host resolving to metadata IP")
		}
	})

	t.Run("hostname resolving to private rejected by default", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "")
		if _, err := resolveAndValidate(context.Background(), "evil-internal"); err == nil {
			t.Fatal("expected rejection for host resolving to private IP by default")
		}
	})

	t.Run("hostname resolving to private allowed with opt-in", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "1")
		if _, err := resolveAndValidate(context.Background(), "evil-internal"); err != nil {
			t.Fatalf("expected private host allowed with opt-in, got %v", err)
		}
	})

	t.Run("mixed public+private rejected fail-closed", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "")
		if _, err := resolveAndValidate(context.Background(), "mixed"); err == nil {
			t.Fatal("expected rejection when any resolved IP is unsafe")
		}
	})
}

func TestCheckAPIRedirect(t *testing.T) {
	mkReq := func(u string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, u, nil)
		return r
	}

	t.Run("redirect to loopback rejected", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "1")
		if err := checkAPIRedirect(mkReq("http://127.0.0.1/x"), nil); err == nil {
			t.Fatal("expected redirect to loopback rejected")
		}
	})

	t.Run("redirect to private literal rejected by default", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "")
		if err := checkAPIRedirect(mkReq("http://10.0.0.9/x"), nil); err == nil {
			t.Fatal("expected redirect to private literal rejected")
		}
	})

	t.Run("redirect to public allowed", func(t *testing.T) {
		t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "")
		if err := checkAPIRedirect(mkReq("https://example.com/x"), nil); err != nil {
			t.Fatalf("expected public redirect allowed, got %v", err)
		}
	})

	t.Run("too many redirects rejected", func(t *testing.T) {
		via := make([]*http.Request, 6)
		if err := checkAPIRedirect(mkReq("https://example.com/x"), via); err == nil {
			t.Fatal("expected too-many-redirects rejection")
		}
	})
}

// TestAPIHTTPClientUsesSafeDialer verifies the poll client is wired with the
// SSRF-guarding dialer and redirect check.
func TestAPIHTTPClientUsesSafeDialer(t *testing.T) {
	client, err := apiHTTPClient("https://example.com", nil)
	if err != nil {
		t.Fatalf("apiHTTPClient: %v", err)
	}
	if client.CheckRedirect == nil {
		t.Fatal("api poll client must set a CheckRedirect guard")
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Fatal("api poll client transport must set a DialContext guard")
	}
	// The dialer must reject a hostname that resolves to a blocked IP.
	withFakeResolver(t, map[string][]string{"rebind.example": {"127.0.0.1"}})
	if _, err := tr.DialContext(context.Background(), "tcp", "rebind.example:443"); err == nil {
		t.Fatal("dialer should reject a host resolving to loopback")
	} else if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected a 'blocked address' error, got %v", err)
	}
}

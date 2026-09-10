package main

// Hub-side coverage for the scalar power domains (system, cpu, dram) and
// their backend labels: they are validated independently, never compared or
// summed, and a bucket from an agent that predates them stays valid forever.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

func stats(mean, peak float64, samples int) map[string]any {
	return map[string]any{"mean_watts": mean, "peak_watts": peak, "samples": samples}
}

func TestPowerHistoryScalarDomainsStoredAndServed(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	start, end := nowWindowMS()
	bucket := powerBucket(1, start, end, 100, 200, 30, 30)
	bucket["system"] = stats(210, 240, 30)
	bucket["dram"] = stats(7.5, 9, 30)
	bucket["sources"] = []any{
		map[string]any{"domain": "system", "source": "rapl-psys"},
		map[string]any{"domain": "cpu", "source": "rapl-package"},
		map[string]any{"domain": "dram", "source": "rapl-dram"},
	}
	writeFrame(t, conn, powerBatch("stream-1", 1, bucket))
	expectPowerAck(t, conn, "stream-1", 1)

	code, hist, body := powerHistoryGet(t, e, adminToken, "machine-A", "")
	if code != http.StatusOK || len(hist.Points) != 1 {
		t.Fatalf("GET: %d %s", code, body)
	}
	p := hist.Points[0]
	if p.System == nil || *p.System.MeanWatts != 210 || p.System.Samples != 30 {
		t.Fatalf("system not preserved: %+v", p.System)
	}
	if p.DRAM == nil || *p.DRAM.MeanWatts != 7.5 {
		t.Fatalf("dram not preserved: %+v", p.DRAM)
	}
	if p.CPU == nil || *p.CPU.MeanWatts != 65 {
		t.Fatalf("cpu must be unaffected by the new domains: %+v", p.CPU)
	}
	for domain, want := range map[string]string{
		powerhistory.DomainSystem: powerhistory.SourceRAPLPsys,
		powerhistory.DomainCPU:    powerhistory.SourceRAPLPackage,
		powerhistory.DomainDRAM:   powerhistory.SourceRAPLDRAM,
	} {
		if got := p.SourceFor(domain); got != want {
			t.Fatalf("%s label %q, want %q", domain, got, want)
		}
	}
	// system is stored as measured; the hub never recomputes it from the
	// component domains, nor the components from it.
	if *p.System.MeanWatts <= *p.CPU.MeanWatts+*p.DRAM.MeanWatts {
		t.Log("informational only: no arithmetic relation is enforced between domains")
	}

	// Replaying the identical bucket is idempotent, not a conflict: the
	// stored payload normalizes through the same schema on both sides.
	writeFrame(t, conn, powerBatch("stream-1", 1, bucket))
	expectPowerAck(t, conn, "stream-1", 1)
	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("replay duplicated rows: %d", n)
	}
}

// The fleet runs agents that cannot be updated. Their buckets carry cpu with
// no label and no system/dram at all, and must stay valid indefinitely.
func TestPowerHistoryOldBucketWithoutDomainsOrSourcesAccepted(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-old")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-old", "host-old")

	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 1)

	code, hist, body := powerHistoryGet(t, e, adminToken, "machine-old", "")
	if code != http.StatusOK || len(hist.Points) != 1 {
		t.Fatalf("GET: %d %s", code, body)
	}
	p := hist.Points[0]
	if p.CPU == nil || *p.CPU.MeanWatts != 65 {
		t.Fatalf("old cpu reading lost: %+v", p.CPU)
	}
	if p.System != nil || p.DRAM != nil || len(p.Sources) != 0 {
		t.Fatalf("absent domains must stay absent, not become zero: %+v", p)
	}
	if p.SourceFor(powerhistory.DomainCPU) != "" {
		t.Fatal("an unlabelled reading must not be given an invented label")
	}
}

func TestPowerHistoryDomainAndSourceValidation(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	fresh := func() map[string]any { return powerBucket(1, start, end, 100, 200, 30, 30) }

	bad := map[string]func() map[string]any{
		"negative system watts": func() map[string]any {
			b := fresh()
			b["system"] = stats(-1, 10, 30)
			return b
		},
		"system peak below mean": func() map[string]any {
			b := fresh()
			b["system"] = stats(100, 50, 30)
			return b
		},
		"system stats with zero samples": func() map[string]any {
			b := fresh()
			b["system"] = map[string]any{"mean_watts": 10, "peak_watts": 20, "samples": 0}
			return b
		},
		"system samples exceed expected": func() map[string]any {
			b := fresh()
			b["system"] = stats(10, 20, 31)
			return b
		},
		"dram watts out of range": func() map[string]any {
			b := fresh()
			b["dram"] = stats(powerMaxWattsDomain+1, powerMaxWattsDomain+2, 30)
			return b
		},
		"label for a domain with no statistics": func() map[string]any {
			b := fresh()
			b["sources"] = []any{map[string]any{"domain": "system", "source": "rapl-psys"}}
			return b
		},
		"duplicate domain label": func() map[string]any {
			b := fresh()
			b["sources"] = []any{
				map[string]any{"domain": "cpu", "source": "rapl-package"},
				map[string]any{"domain": "cpu", "source": "battery"},
			}
			return b
		},
		"empty source": func() map[string]any {
			b := fresh()
			b["sources"] = []any{map[string]any{"domain": "cpu", "source": ""}}
			return b
		},
		"source with path traversal": func() map[string]any {
			b := fresh()
			b["sources"] = []any{map[string]any{"domain": "cpu", "source": "../../etc/passwd"}}
			return b
		},
		"too many labels": func() map[string]any {
			b := fresh()
			many := make([]any, 0, powerhistory.MaxDomainSources+1)
			for i := 0; i <= powerhistory.MaxDomainSources; i++ {
				many = append(many, map[string]any{"domain": string(rune('a' + i)), "source": "x"})
			}
			b["sources"] = many
			return b
		},
	}
	for name, mk := range bad {
		t.Run(name, func(t *testing.T) {
			writeFrame(t, conn, powerBatch("stream-1", 1, mk()))
			expectNoPowerAck(t, conn)
			if n := powerRowCount(t, s, "machine-A"); n != 0 {
				t.Fatalf("invalid batch mutated storage: %d rows", n)
			}
		})
	}
}

// A newer agent may report a domain this hub does not interpret. Bounded
// unknown labels are stored rather than rejected, so a hub upgrade is never
// a precondition for an agent upgrade.
func TestPowerHistoryUnknownDomainLabelAccepted(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	b := powerBucket(1, start, end, 100, 200, 30, 30)
	b["sources"] = []any{
		map[string]any{"domain": "cpu", "source": "rapl-package"},
		map[string]any{"domain": "storage", "source": "nvme-smart"},
	}
	writeFrame(t, conn, powerBatch("stream-1", 1, b))
	expectPowerAck(t, conn, "stream-1", 1)
	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

// system and cpu come from different backends on different schedules, so no
// arithmetic relation between them may be assumed — including the intuitive
// "whole is larger than the part". A window where the two disagree is a
// measurement fact, not a validation failure.
func TestPowerHistoryDomainsNeverComparedToEachOther(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	b := powerBucket(1, start, end, 100, 200, 30, 30)
	b["system"] = stats(20, 25, 6) // battery: 6 samples, below the cpu figure
	b["cpu"] = stats(65, 80, 30)
	b["sources"] = []any{
		map[string]any{"domain": "system", "source": "ipmi-dcmi"},
		map[string]any{"domain": "cpu", "source": "rapl-package"},
	}
	writeFrame(t, conn, powerBatch("stream-1", 1, b))
	expectPowerAck(t, conn, "stream-1", 1)
	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

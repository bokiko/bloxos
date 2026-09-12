package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

func fpCurrent(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, token string) fleetPowerCurrent {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/fleet/power/current", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET current: %d %s", rec.Code, rec.Body.String())
	}
	var out fleetPowerCurrent
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	return out
}

func fpCurrentDomain(t *testing.T, out fleetPowerCurrent, domain string) fleetPowerCurrentDomain {
	t.Helper()
	for _, d := range out.Domains {
		if d.Domain == domain {
			return d
		}
	}
	t.Fatalf("domain %q missing from current response", domain)
	return fleetPowerCurrentDomain{}
}

// The defect this endpoint exists to remove: a stale machine must not be
// carried into a current reading by a fresh one standing beside it.
func TestFleetPowerCurrentExcludesStaleMachinesFromTheSum(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	for _, id := range []string{"fresh", "dark"} {
		s.seedTestMachine(t, id)
	}

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "fresh", 1, now-30_000, now, fpSystem(100, powerhistory.SourceRAPLPsys))
	// Reported four hours ago and has said nothing since.
	old := now - 4*3_600_000
	insertFleetPowerRow(t, s, "dark", 1, old-30_000, old, fpSystem(900, powerhistory.SourceRAPLPsys))

	system := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if system.Measured.Watts == nil {
		t.Fatal("the fresh machine should still produce a current reading")
	}
	if got := *system.Measured.Watts; got != 100 {
		t.Fatalf("watts = %v, want 100 — the dark machine's 900 W must not be carried forward", got)
	}
	if system.Measured.Machines != 1 {
		t.Fatalf("machines = %d, want 1", system.Measured.Machines)
	}
}

// Nothing fresh anywhere is UNAVAILABLE, not zero. A fleet that went dark did
// not start drawing no power.
func TestFleetPowerCurrentIsUnavailableNotZeroWhenAllStale(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "dark")

	old := time.Now().UnixMilli() - 4*3_600_000
	insertFleetPowerRow(t, s, "dark", 1, old-30_000, old, fpSystem(900, powerhistory.SourceRAPLPsys))

	system := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if system.Measured.Watts != nil {
		t.Fatalf("a dark fleet reported %v W as current", *system.Measured.Watts)
	}
	if system.Measured.Machines != 0 {
		t.Fatalf("machines = %d, want 0", system.Measured.Machines)
	}
}

// The current answer must not change with the history period, because it is not
// drawn from the history window at all.
func TestFleetPowerCurrentIsIndependentOfHistoryPeriod(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "m1", 1, now-30_000, now, fpSystem(142, powerhistory.SourceRAPLPsys))

	base := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if base.Measured.Watts == nil {
		t.Fatal("expected a current reading")
	}

	// Ask the history endpoint for every period in turn; the current endpoint's
	// answer must be unmoved by any of it.
	for _, period := range []string{"30m", "1h", "6h", "24h"} {
		req := httptest.NewRequest(http.MethodGet, "/api/fleet/power/history?period="+period, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("history %s: %d", period, rec.Code)
		}

		again := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
		if again.Measured.Watts == nil {
			t.Fatalf("current went nil after asking history for %s", period)
		}
		if *again.Measured.Watts != *base.Measured.Watts {
			t.Fatalf("period %s changed the current reading: %v then %v",
				period, *base.Measured.Watts, *again.Measured.Watts)
		}
		if again.Measured.Machines != base.Measured.Machines {
			t.Fatalf("period %s changed the contributor count", period)
		}
	}
}

// Measured and modelled are reported side by side and never added.
func TestFleetPowerCurrentKeepsModelledSeparate(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	for _, id := range []string{"measured", "modelled"} {
		s.seedTestMachine(t, id)
	}

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "measured", 1, now-30_000, now, fpSystem(142, powerhistory.SourceRAPLPsys))
	insertFleetPowerRow(t, s, "modelled", 1, now-30_000, now, fpSystem(12, powerhistory.SourceEstimateUtil))

	system := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if system.Measured.Watts == nil || *system.Measured.Watts != 142 {
		t.Fatalf("measured = %v, want 142", system.Measured.Watts)
	}
	if system.Estimated.Watts == nil || *system.Estimated.Watts != 12 {
		t.Fatalf("estimated = %v, want 12", system.Estimated.Watts)
	}
	// 154 must appear nowhere: the two are different kinds of claim.
	if system.Measured.Machines != 1 || system.Estimated.Machines != 1 {
		t.Fatalf("machines measured=%d estimated=%d, want 1/1",
			system.Measured.Machines, system.Estimated.Machines)
	}
}

// A domain absent from a machine's latest window is unavailable for that
// machine. Reaching back to a previous window would revive a reading the
// machine has stopped producing.
func TestFleetPowerCurrentDoesNotReviveADomainTheLatestWindowLacks(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	now := time.Now().UnixMilli()
	// Older window HAS system power; the newest window does not.
	insertFleetPowerRow(t, s, "m1", 1, now-90_000, now-60_000, fpSystem(500, powerhistory.SourceRAPLPsys))
	insertFleetPowerRow(t, s, "m1", 2, now-30_000, now, powerhistory.Bucket{
		CPU: fpStats(40, 40, 30),
	})

	out := fpCurrent(t, e, token)
	system := fpCurrentDomain(t, out, powerhistory.DomainSystem)
	if system.Measured.Watts != nil {
		t.Fatalf("system revived a stopped reading: %v W", *system.Measured.Watts)
	}
	cpu := fpCurrentDomain(t, out, powerhistory.DomainCPU)
	if cpu.Measured.Watts == nil || *cpu.Measured.Watts != 40 {
		t.Fatalf("cpu = %v, want 40 — the domain the latest window does carry", cpu.Measured.Watts)
	}
}

// Unknown provenance is counted, never summed.
func TestFleetPowerCurrentCountsUnknownProvenanceWithoutSummingIt(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "m1", 1, now-30_000, now, fpSystem(77, "some-future-backend"))

	system := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if system.Measured.Watts != nil || system.Estimated.Watts != nil {
		t.Fatal("an unclassifiable backend was summed into a series")
	}
	if system.UnknownMachines != 1 {
		t.Fatalf("UnknownMachines = %d, want 1", system.UnknownMachines)
	}
}

// The "now" endpoint has to apply the same scope rule as the history, or the
// headline number and the chart disagree about what the fleet is drawing.
//
// These rows keep arriving: agents older than the change still send them, and
// journal replay delivers backlogs written before it.
func TestFleetPowerCurrentExcludesSystemReadingsOfUnverifiedScope(t *testing.T) {
	for _, source := range []string{
		powerhistory.SourceHwmonPrefix + "ina226",
		powerhistory.SourceHwmonPrefix + "power_meter",
		powerhistory.SourceBattery,
	} {
		t.Run(source, func(t *testing.T) {
			e, s := setupTestServer(t)
			s.markCredentialsRotated(t)
			token := loginAndGetToken(t, e)
			s.seedTestMachine(t, "m1")

			now := time.Now().UnixMilli()
			insertFleetPowerRow(t, s, "m1", 1, now-30_000, now, fpSystem(77, source))

			current := fpCurrent(t, e, token)
			system := fpCurrentDomain(t, current, powerhistory.DomainSystem)
			if system.Measured.Watts != nil {
				t.Fatalf("%s was summed into the current system total: %v W", source, *system.Measured.Watts)
			}
			if system.UnknownMachines != 1 {
				t.Fatalf("%s must be counted as excluded, got %d", source, system.UnknownMachines)
			}
			// It contributed nothing, so it is not a reporting machine.
			if current.MachinesReporting != 0 {
				t.Fatalf("MachinesReporting = %d; a machine excluded from every domain reports nothing",
					current.MachinesReporting)
			}
		})
	}

	// CONTROL: DCMI in the same position is summed, so the exclusions above
	// are about scope rather than about the endpoint refusing system power.
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")
	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "m1", 1, now-30_000, now, fpSystem(77, powerhistory.SourceIPMIDCMI))
	system := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if system.Measured.Watts == nil || *system.Measured.Watts != 77 {
		t.Fatalf("control: a DCMI reading must still be the current system total: %v", system.Measured.Watts)
	}
}

// A frozen RAPL window is not 0 W of CPU. The agent no longer produces one;
// the stored rows and older agents still do.
func TestFleetPowerCurrentFrozenRAPLWindowIsNotAValidZero(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	now := time.Now().UnixMilli()
	bk := powerhistory.Bucket{CPU: fpStats(0, 0, 30)}
	bk.Sources = []powerhistory.DomainSource{
		{Domain: powerhistory.DomainCPU, Source: powerhistory.SourceRAPLPackage},
	}
	insertFleetPowerRow(t, s, "m1", 1, now-30_000, now, bk)

	cpu := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainCPU)
	if cpu.Measured.Watts != nil {
		t.Fatalf("a frozen counter window became %v W of current CPU power", *cpu.Measured.Watts)
	}
	if cpu.UnknownMachines != 1 {
		t.Fatalf("UnknownMachines = %d, want 1", cpu.UnknownMachines)
	}
}

// The sum is only as current as its OLDEST contributor, and says so.
func TestFleetPowerCurrentReportsOldestContributor(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	for _, id := range []string{"a", "b"} {
		s.seedTestMachine(t, id)
	}

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "a", 1, now-30_000, now, fpSystem(100, powerhistory.SourceRAPLPsys))
	older := now - 100_000 // still inside the lookback, but not the newest
	insertFleetPowerRow(t, s, "b", 1, older-30_000, older, fpSystem(50, powerhistory.SourceRAPLPsys))

	system := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if system.Measured.Machines != 2 {
		t.Fatalf("machines = %d, want 2 — both are inside the lookback", system.Measured.Machines)
	}
	if system.Measured.OldestContributorEndUnixMS != older {
		t.Fatalf("oldest = %d, want %d", system.Measured.OldestContributorEndUnixMS, older)
	}
	if system.Measured.NewestContributorEndUnixMS != now {
		t.Fatalf("newest = %d, want %d", system.Measured.NewestContributorEndUnixMS, now)
	}
}

// The data-selection trap: a machine's NEWEST row is future-skewed while an
// older row looks perfectly fresh. Choosing candidates before classifying would
// pick the older row and present a superseded reading as current.
func TestFleetPowerCurrentDoesNotFallBackPastASkewedNewestRow(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	now := time.Now().UnixMilli()
	// An older, entirely plausible reading.
	insertFleetPowerRow(t, s, "m1", 1, now-60_000, now-30_000, fpSystem(100, powerhistory.SourceRAPLPsys))
	// The newest row is stamped well ahead of the hub clock.
	future := now + 600_000
	insertFleetPowerRow(t, s, "m1", 2, future-30_000, future, fpSystem(900, powerhistory.SourceRAPLPsys))

	system := fpCurrentDomain(t, fpCurrent(t, e, token), powerhistory.DomainSystem)
	if system.Measured.Watts != nil {
		t.Fatalf("fell back past a skewed newest row and reported %v W", *system.Measured.Watts)
	}
	if system.SkewedMachines != 1 {
		t.Fatalf("SkewedMachines = %d, want 1 — the skew must be counted, not filtered away", system.SkewedMachines)
	}
}

// A machine whose only history is stale must be COUNTED as stale, not vanish.
func TestFleetPowerCurrentCountsAStaleOnlyMachine(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	old := time.Now().UnixMilli() - 3_600_000
	insertFleetPowerRow(t, s, "m1", 1, old-30_000, old, fpSystem(100, powerhistory.SourceRAPLPsys))

	out := fpCurrent(t, e, token)
	system := fpCurrentDomain(t, out, powerhistory.DomainSystem)
	if system.StaleMachines != 1 {
		t.Fatalf("StaleMachines = %d, want 1", system.StaleMachines)
	}
	if system.Measured.Watts != nil {
		t.Fatal("a stale-only machine must contribute no watts")
	}
	if out.MachinesReporting != 0 {
		t.Fatalf("MachinesReporting = %d, want 0 — a row on disk is not a report", out.MachinesReporting)
	}
}

// MachinesReporting counts fresh valid classifiable readings, so a machine whose
// only reading is unclassifiable is not counted as reporting.
func TestFleetPowerCurrentUnknownOnlyMachineIsNotReporting(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "m1", 1, now-30_000, now, fpSystem(77, "some-future-backend"))

	out := fpCurrent(t, e, token)
	system := fpCurrentDomain(t, out, powerhistory.DomainSystem)
	if system.UnknownMachines != 1 {
		t.Fatalf("UnknownMachines = %d, want 1", system.UnknownMachines)
	}
	if out.MachinesReporting != 0 {
		t.Fatalf("MachinesReporting = %d, want 0 — unclassified is not a reading", out.MachinesReporting)
	}
}

// A corrupt newest row makes the machine unavailable. It must NOT quietly fall
// back to an older row, which would answer "now" with superseded data.
func TestFleetPowerCurrentCorruptNewestRowIsUnavailableNotAFallback(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "m1", 1, now-60_000, now-30_000, fpSystem(100, powerhistory.SourceRAPLPsys))
	if _, err := s.db.Exec(`INSERT INTO power_history_records
		(machine_id, stream_id, seq, start_unix_ms, end_unix_ms, expected_samples, payload)
		VALUES (?, 'stream-1', ?, ?, ?, 30, ?)`,
		"m1", 2, now-30_000, now, "{not valid json"); err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}

	out := fpCurrent(t, e, token)
	system := fpCurrentDomain(t, out, powerhistory.DomainSystem)
	if system.Measured.Watts != nil {
		t.Fatalf("a corrupt newest row revived an older reading: %v W", *system.Measured.Watts)
	}
	if out.MachinesUnreadable != 1 {
		t.Fatalf("MachinesUnreadable = %d, want 1", out.MachinesUnreadable)
	}
	if out.MachinesReporting != 0 {
		t.Fatalf("MachinesReporting = %d, want 0", out.MachinesReporting)
	}
}

// Orphaned rows for a machine no longer registered must not appear at all.
func TestFleetPowerCurrentIgnoresOrphanedRecords(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "registered")

	now := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "registered", 1, now-30_000, now, fpSystem(100, powerhistory.SourceRAPLPsys))

	out := fpCurrent(t, e, token)
	if out.MachinesTotal != 1 {
		t.Fatalf("MachinesTotal = %d, want 1", out.MachinesTotal)
	}
	if out.MachinesReporting != 1 {
		t.Fatalf("MachinesReporting = %d, want 1", out.MachinesReporting)
	}
	if out.MachinesReporting > out.MachinesTotal {
		t.Fatal("reporting exceeded the fleet size")
	}
}

// A broken read must not become a confident "nothing is reporting". This is the
// alert-count defect in another costume: a failed query rendered as a number.
func TestFleetPowerCurrentFailsLoudlyWhenTheQueryBreaks(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	// The machines table stays readable; the power history does not.
	if _, err := s.db.Exec(`DROP TABLE power_history_records`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/fleet/power/current", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("a failed power query returned 200 and a snapshot: %s", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// Reading fleet power requires fleet.read like every other power route.
func TestFleetPowerCurrentRequiresAuth(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)

	req := httptest.NewRequest(http.MethodGet, "/api/fleet/power/current", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET: %d, want 401", rec.Code)
	}
}

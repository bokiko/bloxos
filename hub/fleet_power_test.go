package main

// Fleet power aggregation (hub/fleet_power.go).
//
// The aggregation is a pure function over stored buckets, so most of this
// exercises it directly rather than through a socket: measured and estimated
// must never merge, coverage must be counted on machines rather than on
// readings, gaps must survive to the response, and unobserved time must stay
// unobserved instead of being interpolated into a total.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"github.com/labstack/echo/v4"
)

// --- helpers ---

func fpStats(mean, peak float64, samples int) *powerhistory.Stats {
	return &powerhistory.Stats{MeanWatts: &mean, PeakWatts: &peak, Samples: samples}
}

// fpRecord is one stored 30-second window for a machine, landing at endMS.
func fpRecord(machineID string, endMS int64, bk powerhistory.Bucket) fleetPowerRecord {
	bk.StartUnixMS = endMS - 30_000
	bk.EndUnixMS = endMS
	if bk.ExpectedSamples == 0 {
		bk.ExpectedSamples = 30
	}
	return fleetPowerRecord{
		MachineID: machineID,
		StartMS:   bk.StartUnixMS,
		EndMS:     bk.EndUnixMS,
		Expected:  bk.ExpectedSamples,
		Bucket:    bk,
	}
}

// fpSystem is a whole-platform reading from the named backend.
func fpSystem(watts float64, source string) powerhistory.Bucket {
	bk := powerhistory.Bucket{System: fpStats(watts, watts, 30)}
	if source != "" {
		bk.Sources = []powerhistory.DomainSource{{Domain: powerhistory.DomainSystem, Source: source}}
	}
	return bk
}

// fpInputs describes a 30-minute window (1-minute buckets) starting at
// startMS. Two buckets is enough to prove per-bucket behaviour.
func fpInputs(machines []string, startMS int64, buckets int) fleetPowerInputs {
	return fleetPowerInputs{
		Period:      "30m",
		Spec:        fleetPowerPeriods["30m"],
		StartMS:     startMS,
		BucketCount: buckets,
		Now:         startMS,
		MachineIDs:  machines,
	}
}

func fpDomain(t *testing.T, hist FleetPowerHistory, domain string) FleetPowerDomain {
	t.Helper()
	for _, d := range hist.Domains {
		if d.Domain == domain {
			return d
		}
	}
	t.Fatalf("domain %q missing from response; every domain must always be present", domain)
	return FleetPowerDomain{}
}

func fpWatts(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("watts nil, want %v — nil means unavailable, not zero", want)
	}
	if diff := *got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("watts %v, want %v", *got, want)
	}
}

// --- measured vs estimated ---

// A measured reading and a modelled one land in the same bucket. They must
// stay two numbers: nothing in the response may present their sum, because
// "160 W" would be a measurement claim about a machine that has no counter.
func TestFleetPowerKeepsMeasuredAndEstimatedApart(t *testing.T) {
	start := int64(1_700_000_000_000)
	records := []fleetPowerRecord{
		fpRecord("m-measured", start+30_000, fpSystem(142, powerhistory.SourceRAPLPsys)),
		fpRecord("m-estimated", start+30_000, fpSystem(18, powerhistory.SourceEstimateUtil)),
	}
	hist := aggregateFleetPower(records, fpInputs([]string{"m-measured", "m-estimated"}, start, 2))

	system := fpDomain(t, hist, powerhistory.DomainSystem)
	b := system.Buckets[0]
	fpWatts(t, b.MeasuredWatts, 142)
	fpWatts(t, b.EstimatedWatts, 18)
	if b.MeasuredMachines != 1 || b.EstimatedMachines != 1 {
		t.Fatalf("machines measured=%d estimated=%d, want 1 and 1", b.MeasuredMachines, b.EstimatedMachines)
	}
	if len(system.Measured.Sources) != 1 || system.Measured.Sources[0] != powerhistory.SourceRAPLPsys {
		t.Fatalf("measured sources %v, want [%s]", system.Measured.Sources, powerhistory.SourceRAPLPsys)
	}
	if len(system.Estimated.Sources) != 1 || system.Estimated.Sources[0] != powerhistory.SourceEstimateUtil {
		t.Fatalf("estimated sources %v, want [%s]", system.Estimated.Sources, powerhistory.SourceEstimateUtil)
	}
	if system.Measured.Machines != 1 || system.Estimated.Machines != 1 {
		t.Fatalf("window machines measured=%d estimated=%d", system.Measured.Machines, system.Estimated.Machines)
	}
	// Both machines reported, so the domain's reporting count is the union.
	if system.ReportingMachines != 2 {
		t.Fatalf("reporting machines = %d, want 2", system.ReportingMachines)
	}

	// The wire format must not carry a merged total for anything to latch on to.
	raw, err := json.Marshal(hist)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{`"total_watts"`, `"watts":`, `"combined`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("response exposes a merged figure %s: %s", forbidden, raw)
		}
	}
}

// An unlabelled reading is a measurement: only agents predating source
// labelling emit one, and what they had was a counter. This is the fail-open
// direction, so it is pinned deliberately — an estimator that forgets to label
// itself would land here and be summed as measured.
func TestFleetPowerTreatsUnlabelledReadingAsMeasured(t *testing.T) {
	start := int64(1_700_000_000_000)
	records := []fleetPowerRecord{fpRecord("m-old", start+30_000, fpSystem(75, ""))}
	hist := aggregateFleetPower(records, fpInputs([]string{"m-old"}, start, 2))

	system := fpDomain(t, hist, powerhistory.DomainSystem)
	fpWatts(t, system.Buckets[0].MeasuredWatts, 75)
	if system.Buckets[0].EstimatedWatts != nil {
		t.Fatal("an unlabelled reading must not be filed as an estimate")
	}
	if len(system.Measured.Sources) != 1 || system.Measured.Sources[0] != fleetPowerSourceUnlabelled {
		t.Fatalf("sources %v, want [%s]", system.Measured.Sources, fleetPowerSourceUnlabelled)
	}
}

// Domains are disjoint scopes. system already contains cpu where both exist,
// so they are aggregated side by side and never added.
func TestFleetPowerNeverSumsDomains(t *testing.T) {
	start := int64(1_700_000_000_000)
	bk := fpSystem(200, powerhistory.SourceRAPLPsys)
	bk.CPU = fpStats(65, 80, 30)
	bk.DRAM = fpStats(7, 9, 30)
	bk.GPUTotal = fpStats(120, 150, 30)
	bk.Sources = append(bk.Sources, powerhistory.DomainSource{
		Domain: powerhistory.DomainCPU, Source: powerhistory.SourceRAPLPackage,
	})
	hist := aggregateFleetPower([]fleetPowerRecord{fpRecord("m1", start+30_000, bk)}, fpInputs([]string{"m1"}, start, 2))

	for domain, want := range map[string]float64{
		powerhistory.DomainSystem: 200,
		powerhistory.DomainCPU:    65,
		powerhistory.DomainDRAM:   7,
		fleetPowerDomainGPU:       120,
	} {
		fpWatts(t, fpDomain(t, hist, domain).Buckets[0].MeasuredWatts, want)
	}
	// GPU carries no scalar source label, so it is attributed to itself
	// rather than being reported as unlabelled.
	gpu := fpDomain(t, hist, fleetPowerDomainGPU)
	if len(gpu.Measured.Sources) != 1 || gpu.Measured.Sources[0] != fleetPowerDomainGPU {
		t.Fatalf("gpu sources %v", gpu.Measured.Sources)
	}
}

// --- coverage ---

// Three machines, two reporting. The response must make the shortfall
// impossible to miss: the count, the ids, and Complete = false.
func TestFleetPowerCoverageCountsSilentMachines(t *testing.T) {
	start := int64(1_700_000_000_000)
	records := []fleetPowerRecord{
		fpRecord("m1", start+30_000, fpSystem(100, powerhistory.SourceRAPLPsys)),
		fpRecord("m2", start+30_000, fpSystem(50, powerhistory.SourceIPMIDCMI)),
	}
	hist := aggregateFleetPower(records, fpInputs([]string{"m1", "m2", "m3"}, start, 1))

	c := hist.Coverage
	if c.MachinesTotal != 3 || c.MachinesReporting != 2 || c.MachinesSilent != 1 {
		t.Fatalf("coverage total=%d reporting=%d silent=%d, want 3/2/1",
			c.MachinesTotal, c.MachinesReporting, c.MachinesSilent)
	}
	if len(c.SilentMachineIDs) != 1 || c.SilentMachineIDs[0] != "m3" {
		t.Fatalf("silent ids %v, want [m3]", c.SilentMachineIDs)
	}
	if fpDomain(t, hist, powerhistory.DomainSystem).Complete {
		t.Fatal("a total drawn from 2 of 3 machines must not be marked complete")
	}
	// The partial sum itself is still returned — the caller shows it WITH the
	// coverage, rather than being denied the number.
	fpWatts(t, fpDomain(t, hist, powerhistory.DomainSystem).Buckets[0].MeasuredWatts, 150)
}

// Complete is the fleet-scale gpuPowerComplete: every machine, every bucket.
func TestFleetPowerCompleteRequiresEveryMachineInEveryBucket(t *testing.T) {
	start := int64(1_700_000_000_000)
	full := []fleetPowerRecord{
		fpRecord("m1", start+30_000, fpSystem(100, powerhistory.SourceRAPLPsys)),
		fpRecord("m2", start+30_000, fpSystem(50, powerhistory.SourceRAPLPsys)),
		fpRecord("m1", start+90_000, fpSystem(110, powerhistory.SourceRAPLPsys)),
		fpRecord("m2", start+90_000, fpSystem(55, powerhistory.SourceRAPLPsys)),
	}
	hist := aggregateFleetPower(full, fpInputs([]string{"m1", "m2"}, start, 2))
	if !fpDomain(t, hist, powerhistory.DomainSystem).Complete {
		t.Fatal("every machine reported in every bucket; Complete must be true")
	}

	// Drop m2 from the second bucket — a machine that joined or dropped out
	// mid-window. The series is still returned, but not as a fleet total.
	partial := full[:3]
	hist = aggregateFleetPower(partial, fpInputs([]string{"m1", "m2"}, start, 2))
	system := fpDomain(t, hist, powerhistory.DomainSystem)
	if system.Complete {
		t.Fatal("a machine missing from one bucket must clear Complete")
	}
	if system.Buckets[1].MeasuredMachines != 1 {
		t.Fatalf("bucket 1 machines = %d, want 1", system.Buckets[1].MeasuredMachines)
	}
	fpWatts(t, system.Buckets[1].MeasuredWatts, 110)
	// The window roll-up still counts both machines: m2 reported somewhere.
	if system.Measured.Machines != 2 {
		t.Fatalf("window machines = %d, want 2", system.Measured.Machines)
	}
}

// A bucket nobody covered is nil, not zero. Zero watts is a real reading.
func TestFleetPowerEmptyBucketIsNilNotZero(t *testing.T) {
	start := int64(1_700_000_000_000)
	records := []fleetPowerRecord{fpRecord("m1", start+30_000, fpSystem(100, powerhistory.SourceRAPLPsys))}
	hist := aggregateFleetPower(records, fpInputs([]string{"m1"}, start, 3))

	system := fpDomain(t, hist, powerhistory.DomainSystem)
	if len(system.Buckets) != 3 {
		t.Fatalf("buckets = %d, want 3 — the axis must not collapse to the data", len(system.Buckets))
	}
	fpWatts(t, system.Buckets[0].MeasuredWatts, 100)
	for _, idx := range []int{1, 2} {
		if system.Buckets[idx].MeasuredWatts != nil {
			t.Fatalf("bucket %d = %v, want nil", idx, *system.Buckets[idx].MeasuredWatts)
		}
		if system.Buckets[idx].MeasuredMachines != 0 {
			t.Fatalf("bucket %d claims %d machines", idx, system.Buckets[idx].MeasuredMachines)
		}
	}
	// A genuine zero-watt reading is preserved as zero.
	zero := aggregateFleetPower(
		[]fleetPowerRecord{fpRecord("m1", start+30_000, fpSystem(0, powerhistory.SourceBattery))},
		fpInputs([]string{"m1"}, start, 1),
	)
	fpWatts(t, fpDomain(t, zero, powerhistory.DomainSystem).Buckets[0].MeasuredWatts, 0)
}

// Readings outside the window are dropped rather than folded into an edge
// bucket, which would spike the first or last point.
func TestFleetPowerIgnoresReadingsOutsideWindow(t *testing.T) {
	start := int64(1_700_000_000_000)
	records := []fleetPowerRecord{
		fpRecord("m1", start-30_000, fpSystem(999, powerhistory.SourceRAPLPsys)),
		fpRecord("m1", start+120_000, fpSystem(999, powerhistory.SourceRAPLPsys)),
		fpRecord("m1", start+30_000, fpSystem(100, powerhistory.SourceRAPLPsys)),
	}
	hist := aggregateFleetPower(records, fpInputs([]string{"m1"}, start, 2))
	system := fpDomain(t, hist, powerhistory.DomainSystem)
	fpWatts(t, system.Buckets[0].MeasuredWatts, 100)
	if system.Buckets[1].MeasuredWatts != nil {
		t.Fatalf("out-of-window reading leaked into bucket 1: %v", *system.Buckets[1].MeasuredWatts)
	}
}

// --- gaps ---

func TestFleetPowerSurfacesDeclaredGaps(t *testing.T) {
	start := int64(1_700_000_000_000)
	gapped := fpSystem(100, powerhistory.SourceRAPLPsys)
	gapped.GapBefore = true
	records := []fleetPowerRecord{
		fpRecord("m1", start+30_000, fpSystem(100, powerhistory.SourceRAPLPsys)),
		fpRecord("m1", start+90_000, gapped),
	}
	in := fpInputs([]string{"m1"}, start, 2)
	in.GapsDeclared = 4
	hist := aggregateFleetPower(records, in)

	system := fpDomain(t, hist, powerhistory.DomainSystem)
	if system.Buckets[0].Gap {
		t.Fatal("bucket 0 carries no gap")
	}
	if !system.Buckets[1].Gap {
		t.Fatal("a GapBefore bucket must mark its output bucket")
	}
	if hist.Coverage.GapBuckets != 1 {
		t.Fatalf("gap buckets = %d, want 1", hist.Coverage.GapBuckets)
	}
	if hist.Coverage.GapsDeclared != 4 {
		t.Fatalf("declared gaps = %d, want 4", hist.Coverage.GapsDeclared)
	}
	// Missing data is not zero power: the gapped bucket still reports its
	// reading rather than being blanked or zeroed by the gap flag.
	fpWatts(t, system.Buckets[1].MeasuredWatts, 100)
}

// --- weighting and energy ---

// Two 30-second windows inside one 1-minute bucket are one machine, averaged
// over time — not two machines, and not the later reading alone.
func TestFleetPowerWeightsSubWindowsByTime(t *testing.T) {
	start := int64(1_700_000_000_000)
	records := []fleetPowerRecord{
		// Both windows end inside bucket 0, which spans [start, start+60s).
		fpRecord("m1", start+25_000, fpSystem(100, powerhistory.SourceRAPLPsys)),
		fpRecord("m1", start+55_000, fpSystem(200, powerhistory.SourceRAPLPsys)),
	}
	hist := aggregateFleetPower(records, fpInputs([]string{"m1"}, start, 2))
	system := fpDomain(t, hist, powerhistory.DomainSystem)
	fpWatts(t, system.Buckets[0].MeasuredWatts, 150)
	if system.Buckets[0].MeasuredMachines != 1 {
		t.Fatalf("machines = %d — two readings from one machine is still one machine",
			system.Buckets[0].MeasuredMachines)
	}
}

// Energy counts observed time only, so it is a floor and the response says how
// much time it actually saw.
func TestFleetPowerEnergyIsAFloorOverObservedTime(t *testing.T) {
	start := int64(1_700_000_000_000)
	records := []fleetPowerRecord{
		fpRecord("m1", start+30_000, fpSystem(120, powerhistory.SourceRAPLPsys)),
		fpRecord("m2", start+30_000, fpSystem(60, powerhistory.SourceEstimateUtil)),
	}
	hist := aggregateFleetPower(records, fpInputs([]string{"m1", "m2"}, start, 2))
	system := fpDomain(t, hist, powerhistory.DomainSystem)

	// 120 W for 30 s = 1 W·h = 0.001 kWh.
	if diff := system.Measured.EnergyKWh - 0.001; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("measured energy = %v kWh, want 0.001", system.Measured.EnergyKWh)
	}
	if diff := system.Estimated.EnergyKWh - 0.0005; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("estimated energy = %v kWh, want 0.0005", system.Estimated.EnergyKWh)
	}
	if system.Measured.ObservedMachineSeconds != 30 || system.Estimated.ObservedMachineSeconds != 30 {
		t.Fatalf("observed seconds measured=%v estimated=%v, want 30 and 30",
			system.Measured.ObservedMachineSeconds, system.Estimated.ObservedMachineSeconds)
	}
}

// A stored payload outside the ingest bounds is skipped rather than becoming a
// fleet total. Stored rows were validated once; they are not trusted twice.
func TestFleetPowerRejectsOutOfBoundsStoredStats(t *testing.T) {
	start := int64(1_700_000_000_000)
	bogus := fpSystem(powerMaxWattsDomain*10, powerhistory.SourceRAPLPsys)
	records := []fleetPowerRecord{
		fpRecord("m1", start+30_000, bogus),
		fpRecord("m2", start+30_000, fpSystem(40, powerhistory.SourceRAPLPsys)),
	}
	hist := aggregateFleetPower(records, fpInputs([]string{"m1", "m2"}, start, 1))
	system := fpDomain(t, hist, powerhistory.DomainSystem)
	fpWatts(t, system.Buckets[0].MeasuredWatts, 40)
	if system.Buckets[0].MeasuredMachines != 1 {
		t.Fatalf("machines = %d, want 1 — the bogus row must not count", system.Buckets[0].MeasuredMachines)
	}
}

// --- period resolution ---

func TestFleetPowerPeriodResolution(t *testing.T) {
	for raw, want := range map[string]string{
		"30m": "30m", "1h": "1h", "6h": "6h", "24h": "24h",
		// 7d exists for metrics history but power history is retained for 24
		// hours, so it must not silently answer with a week-long axis.
		"7d": fleetPowerDefaultPeriod,
		"":   fleetPowerDefaultPeriod,
		"🙂":  fleetPowerDefaultPeriod,
	} {
		got, spec := resolveFleetPowerPeriod(raw)
		if got != want {
			t.Fatalf("period %q resolved to %q, want %q", raw, got, want)
		}
		if spec.bucket <= 0 || spec.window <= 0 || spec.window%spec.bucket != 0 {
			t.Fatalf("period %q has a bucket that does not divide its window: %+v", raw, spec)
		}
		if points := int(spec.window / spec.bucket); points < 24 || points > 128 {
			t.Fatalf("period %q yields %d points; the panel needs a bounded series", raw, points)
		}
	}
}

// --- RBAC and HTTP ---

func TestFleetPowerRouteIsRegisteredWithFleetReadScope(t *testing.T) {
	key := routeScopeKey(http.MethodGet, "/api/fleet/power/history")
	scope, ok := routeScopeRequirements[key]
	if !ok {
		t.Fatalf("%s has no RBAC scope mapping", key)
	}
	if scope != scopeFleetRead {
		t.Fatalf("%s requires %q, want %q — it is the per-machine power endpoint's scope", key, scope, scopeFleetRead)
	}
	// The route must actually be registered, and the audit must accept the
	// pair. auditRBACRouteCoverage fails a boot on either half being missing.
	s := newServer(nil)
	e := echo.New()
	e.HideBanner = true
	s.registerRoutes(e)
	registered := false
	for _, r := range e.Routes() {
		if routeScopeKey(r.Method, r.Path) == key {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("%s is mapped but never registered", key)
	}
	if err := auditRBACRouteCoverage(e, routeScopeRequirements); err != nil {
		t.Fatalf("RBAC audit failed: %v", err)
	}
}

func TestFleetPowerRequiresAuthentication(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)

	req := httptest.NewRequest(http.MethodGet, "/api/fleet/power/history", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET returned %d, want 401", rec.Code)
	}
}

// End to end over stored rows: three machines exist, two have power history,
// one of those is an estimate. The response must carry the split series and a
// coverage block that names the shortfall.
func TestFleetPowerHistoryEndpointReportsSplitSeriesAndCoverage(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	for _, id := range []string{"m1", "m2", "m3"} {
		s.seedTestMachine(t, id)
	}

	end := time.Now().UnixMilli()
	insertFleetPowerRow(t, s, "m1", 1, end-30_000, end, fpSystem(142, powerhistory.SourceRAPLPsys))
	insertFleetPowerRow(t, s, "m2", 1, end-30_000, end, fpSystem(18, powerhistory.SourceEstimateUtil))

	req := httptest.NewRequest(http.MethodGet, "/api/fleet/power/history?period=1h", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}

	var hist FleetPowerHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &hist); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if hist.Period != "1h" || hist.BucketSeconds != 60 {
		t.Fatalf("period=%q bucket=%ds, want 1h/60s", hist.Period, hist.BucketSeconds)
	}
	if len(hist.Domains) != len(fleetPowerDomains) {
		t.Fatalf("domains = %d, want %d", len(hist.Domains), len(fleetPowerDomains))
	}

	system := fpDomain(t, hist, powerhistory.DomainSystem)
	var measured, estimated float64
	for _, b := range system.Buckets {
		if b.MeasuredWatts != nil {
			measured += *b.MeasuredWatts
		}
		if b.EstimatedWatts != nil {
			estimated += *b.EstimatedWatts
		}
	}
	if measured != 142 || estimated != 18 {
		t.Fatalf("measured=%v estimated=%v, want 142 and 18 kept apart", measured, estimated)
	}
	if system.Complete {
		t.Fatal("2 of 3 machines is not a complete fleet total")
	}

	c := hist.Coverage
	if c.MachinesTotal != 3 || c.MachinesReporting != 2 || c.MachinesSilent != 1 {
		t.Fatalf("coverage %+v, want 3 total / 2 reporting / 1 silent", c)
	}
	if len(c.SilentMachineIDs) != 1 || c.SilentMachineIDs[0] != "m3" {
		t.Fatalf("silent ids %v, want [m3]", c.SilentMachineIDs)
	}
	if c.Truncated {
		t.Fatal("two rows cannot truncate")
	}
	// Empty-contract fields must serialize as [] so a client need not guard.
	if !strings.Contains(rec.Body.String(), `"silent_machine_ids":[`) ||
		!strings.Contains(rec.Body.String(), `"sources":[`) {
		t.Fatalf("list fields must serialize as arrays: %s", rec.Body.String())
	}
}

// A hub with machines but no power history at all must answer 200 with an
// explicit "nothing reported", not an error and not an empty object the client
// has to interpret.
func TestFleetPowerHistoryEndpointWithNoData(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")

	req := httptest.NewRequest(http.MethodGet, "/api/fleet/power/history", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}

	var hist FleetPowerHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &hist); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if hist.Period != fleetPowerDefaultPeriod {
		t.Fatalf("period = %q, want the default %q", hist.Period, fleetPowerDefaultPeriod)
	}
	if hist.Coverage.MachinesTotal != 1 || hist.Coverage.MachinesReporting != 0 {
		t.Fatalf("coverage %+v, want 1 total / 0 reporting", hist.Coverage)
	}
	for _, d := range hist.Domains {
		if d.Complete {
			t.Fatalf("%s marked complete with no data at all", d.Domain)
		}
		if d.ReportingMachines != 0 {
			t.Fatalf("%s claims %d reporting machines", d.Domain, d.ReportingMachines)
		}
		for _, b := range d.Buckets {
			if b.MeasuredWatts != nil || b.EstimatedWatts != nil {
				t.Fatalf("%s invented a reading with no data", d.Domain)
			}
		}
	}
}

func insertFleetPowerRow(t *testing.T, s *Server, machineID string, seq uint64, startMS, endMS int64, bk powerhistory.Bucket) {
	t.Helper()
	bk.Seq = seq
	bk.StartUnixMS = startMS
	bk.EndUnixMS = endMS
	bk.ExpectedSamples = 30
	payload, err := json.Marshal(bk)
	if err != nil {
		t.Fatalf("marshal bucket: %v", err)
	}
	_, err = s.db.Exec(`INSERT INTO power_history_records
		(machine_id, stream_id, seq, start_unix_ms, end_unix_ms, expected_samples, payload)
		VALUES (?, 'stream-1', ?, ?, ?, 30, ?)`, machineID, seq, startMS, endMS, string(payload))
	if err != nil {
		t.Fatalf("insert power row: %v", err)
	}
}

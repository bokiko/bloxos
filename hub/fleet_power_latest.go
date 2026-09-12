package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"github.com/labstack/echo/v4"
)

// GET /api/fleet/power/current — what the fleet is drawing NOW.
//
// This exists because "now" cannot be read off the history endpoint, and trying
// to do so was producing a confidently wrong number.
//
// The old approach walked backwards through the charted buckets and headlined
// the last non-null one. That has three separate defects, and they compound:
//
//  1. It depends on the selected chart period. A 30m view buckets by the
//     minute and a 24h view by fifteen, so the same fleet produced a different
//     "current" figure — with different contributors behind it — depending on
//     which tab the reader had clicked. A current reading whose meaning changes
//     with the axis is not a current reading.
//
//  2. It reached arbitrarily far back. A fleet that went dark at 02:00 still
//     headlined its last known value at noon, with no age beside it, because
//     "the last non-null bucket" is always findable inside a 24-hour window.
//
//  3. It aged values against BUCKET edges. A bucket boundary is an axis
//     coordinate; the final one sits in the future by up to a bucket width.
//
// So this endpoint answers the question directly, from raw per-machine windows:
//
//   - A FIXED lookback, independent of period and of the history record cap.
//   - The NEWEST window per machine per domain, and no reaching further back:
//     if a machine's latest window has no CPU reading, its CPU is unavailable.
//     A domain that stopped reporting must go quiet, not resurrect an older
//     value.
//   - EACH CONTRIBUTOR gated individually against the shared freshness window.
//     One machine still reporting cannot make five dark ones current: stale
//     machines are excluded from the sum and counted separately, so a shrinking
//     contributor count is visible rather than silently absorbed.
//   - Only KNOWN kinds are summed. An unclassifiable backend is counted, never
//     added.
//
// What it deliberately does NOT do: report energy or cost. Instantaneous power
// says nothing about energy without durations — see fleet_power.go.

// fleetPowerCurrentLookbackMS bounds how far back a "current" reading may come
// from. It is fixed, so the answer never depends on the chart period. Five
// agent windows allows for a missed report and hub aggregation lag without
// reviving genuinely stale data.
const fleetPowerCurrentLookbackMS = 150_000

// fleetPowerCurrentSeries is one domain/kind sum at this instant.
type fleetPowerCurrentSeries struct {
	// Watts is the sum of the fresh contributors' latest means. It is a SUM OF
	// PER-MACHINE MEANS, not an instantaneous fleet measurement: each machine's
	// newest window is its own 30s mean, and those windows are not synchronised
	// across machines. Nil when no contributor is fresh — never 0.
	Watts *float64 `json:"watts"`
	// Machines is how many machines are behind Watts.
	Machines int `json:"machines"`
	// Sources names the backends contributing, so the figure can say what
	// measured it.
	Sources []string `json:"sources"`
	// OldestContributorEndUnixMS is the oldest window end among the
	// contributors. The sum is only as current as this.
	OldestContributorEndUnixMS int64 `json:"oldest_contributor_end_unix_ms"`
	// NewestContributorEndUnixMS is the newest, for reference only. It must not
	// be used to age the sum: that is what let one fresh machine speak for a
	// dark fleet.
	NewestContributorEndUnixMS int64 `json:"newest_contributor_end_unix_ms"`
}

// fleetPowerCurrentDomain carries one domain's series plus why machines are
// missing from them.
type fleetPowerCurrentDomain struct {
	Domain    string                  `json:"domain"`
	Measured  fleetPowerCurrentSeries `json:"measured"`
	Estimated fleetPowerCurrentSeries `json:"estimated"`
	// UnknownMachines had a reading this hub will not add up: an unrecognised
	// backend, or a recognised one whose SCOPE it cannot vouch for — a battery
	// that may be carrying only part of the load, a shunt whose rail its chip
	// name does not identify, or a RAPL window whose counters never moved.
	// Their watts are excluded from both series; the count keeps that visible.
	UnknownMachines int `json:"unknown_machines"`
	// StaleMachines reported this domain, but not recently enough to count.
	// Their last value is deliberately NOT carried forward.
	StaleMachines int `json:"stale_machines"`
	// UnreadableMachines had a newest row that could not be parsed or failed
	// validation. They are unavailable rather than replaced by an older row.
	UnreadableMachines int `json:"unreadable_machines"`
	// SkewedMachines stamped a window implausibly ahead of the hub clock, so
	// their age is unknown and they cannot qualify as current.
	SkewedMachines int `json:"skewed_machines"`
}

type fleetPowerCurrent struct {
	GeneratedUnixMS int64 `json:"generated_unix_ms"`
	// LookbackMS is the fixed window this answer was drawn from, stated so a
	// client need not assume it.
	LookbackMS int64                     `json:"lookback_ms"`
	Domains    []fleetPowerCurrentDomain `json:"domains"`
	// MachinesTotal / MachinesReporting describe coverage of the whole fleet, so
	// a sum over two of nine machines cannot read as a fleet total.
	// MachinesReporting counts machines that contributed a FRESH, VALID,
	// classifiable reading — not machines that merely have a row on disk.
	MachinesTotal     int `json:"machines_total"`
	MachinesReporting int `json:"machines_reporting"`
	// MachinesUnreadable had a newest stored row that could not be decoded.
	MachinesUnreadable int `json:"machines_unreadable"`
}

// currentAcc accumulates one domain/kind while scanning machines.
type currentAcc struct {
	sum      float64
	machines int
	sources  map[string]bool
	oldest   int64
	newest   int64
}

func (a *currentAcc) add(watts float64, source string, endMS int64) {
	a.sum += watts
	a.machines++
	if a.sources == nil {
		a.sources = map[string]bool{}
	}
	a.sources[source] = true
	if a.oldest == 0 || endMS < a.oldest {
		a.oldest = endMS
	}
	if endMS > a.newest {
		a.newest = endMS
	}
}

func (a *currentAcc) series() fleetPowerCurrentSeries {
	s := fleetPowerCurrentSeries{
		Machines:                   a.machines,
		Sources:                    sortedCapped(a.sources, fleetPowerMaxSourcesListed),
		OldestContributorEndUnixMS: a.oldest,
		NewestContributorEndUnixMS: a.newest,
	}
	// Nil, never zero: no fresh contributor is "we cannot say", not "0 watts".
	if a.machines > 0 {
		sum := a.sum
		s.Watts = &sum
	}
	return s
}

func (s *Server) handleFleetPowerCurrent(c echo.Context) error {
	now := time.Now().UnixMilli()

	machineIDs, err := s.fleetPowerMachineIDs()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query machines"})
	}

	// Selection order matters, and getting it wrong is how a current reading
	// quietly becomes a historic one.
	//
	// The row chosen per machine is its LATEST STORED ROW, full stop — not its
	// latest row that happens to be fresh and valid. Filtering candidates
	// before choosing would mean a machine whose newest window is stale, or
	// future-skewed, or corrupt, would silently fall through to an OLDER window
	// and present that as current. It would also make the stale and skew counts
	// unreachable, since nothing that qualifies for them would survive the
	// filter to be counted.
	//
	// So: pick the newest row first, then classify it. A machine whose newest
	// row does not qualify contributes a REASON, never a number.
	//
	// The query is one indexed seek per registered machine. The index is
	// (machine_id, end_unix_ms), so each is a descending index read of a single
	// row, and iterating registered machines also bounds the work by the fleet
	// — orphaned rows for a deleted machine can never inflate the denominator.
	type latest struct {
		endMS    int64
		expected int
		bucket   powerhistory.Bucket
		corrupt  bool
	}
	newest := map[string]*latest{}
	machinesUnreadable := 0

	for _, id := range machineIDs {
		var startMS, endMS int64
		var expected int
		var payload string
		// The id tie-break keeps the choice deterministic when two rows share
		// an end timestamp.
		row := s.db.QueryRow(`SELECT start_unix_ms, end_unix_ms, expected_samples, payload
			FROM power_history_records
			WHERE machine_id = ?
			ORDER BY end_unix_ms DESC, id DESC
			LIMIT 1`, id)
		if err := row.Scan(&startMS, &endMS, &expected, &payload); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // this machine genuinely has no power history
			}
			// Any other error is a QUERY FAILURE, not an absence of data.
			// Swallowing it here would turn a broken read into a confident
			// "0 machines reporting" — a number nothing verified, presented as
			// fact. Fail loudly instead.
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query power history"})
		}
		var bk powerhistory.Bucket
		if err := json.Unmarshal([]byte(payload), &bk); err != nil {
			// A corrupt newest row makes this machine UNAVAILABLE. Falling back
			// to an older row would answer "what is it drawing now" with a
			// reading it has already superseded.
			newest[id] = &latest{endMS: endMS, corrupt: true}
			machinesUnreadable++
			continue
		}
		newest[id] = &latest{endMS: endMS, expected: expected, bucket: bk}
	}

	out := fleetPowerCurrent{
		GeneratedUnixMS:    now,
		LookbackMS:         fleetPowerCurrentLookbackMS,
		MachinesTotal:      len(machineIDs),
		MachinesUnreadable: machinesUnreadable,
		Domains:            make([]fleetPowerCurrentDomain, 0, len(fleetPowerDomains)),
	}

	// A machine counts as reporting when it contributed at least one fresh,
	// valid, classifiable reading in ANY domain — not merely when a row for it
	// exists.
	contributing := map[string]bool{}

	for _, domain := range fleetPowerDomains {
		d := fleetPowerCurrentDomain{Domain: domain}
		var measured, estimated currentAcc

		for id, rec := range newest {
			if rec.corrupt {
				d.UnreadableMachines++
				continue
			}
			stats, source, maxWatts := fleetPowerDomainStats(&rec.bucket, domain)
			if stats == nil || stats.Samples <= 0 || stats.MeanWatts == nil {
				continue // not reporting this domain in its newest window
			}

			// Freshness is judged BEFORE validity so a stale machine is counted
			// as stale rather than disappearing.
			if rec.endMS > now+fleetPowerFutureSkewToleranceMS {
				d.SkewedMachines++
				continue
			}
			if now-rec.endMS > fleetPowerCurrentLookbackMS {
				d.StaleMachines++
				continue
			}
			if err := validPowerStats(stats, rec.expected, maxWatts); err != nil {
				d.UnreadableMachines++
				continue
			}

			label := fleetPowerSourceLabel(domain, source)
			switch fleetPowerKindFor(domain, source, stats) {
			case fleetPowerKindMeasured:
				measured.add(*stats.MeanWatts, label, rec.endMS)
				contributing[id] = true
			case fleetPowerKindEstimated:
				estimated.add(*stats.MeanWatts, label, rec.endMS)
				contributing[id] = true
			default:
				d.UnknownMachines++
			}
		}

		d.Measured = measured.series()
		d.Estimated = estimated.series()
		out.Domains = append(out.Domains, d)
	}

	out.MachinesReporting = len(contributing)
	return c.JSON(http.StatusOK, out)
}

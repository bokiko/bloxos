package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
)

// describeCPUCores renders one honest model string for a package whose cores
// are not all the same.
//
// The bug this exists to prevent: reading cpuInfos[0].ModelName and pairing it
// with the whole-package core count. On a heterogeneous ("big.LITTLE") SoC core
// 0 is routinely the SMALL core — an RK3588's core 0 is a Cortex-A55 — so the
// inventory described that machine as "8x Cortex-A55" when it is really
// 4x Cortex-A76 + 4x Cortex-A55. The same misreport is available on every
// Raspberry Pi 5 class part, RK3399, and Intel P/E-core designs whose per-core
// model strings differ.
//
// A uniform package is unaffected: the single distinct name is returned as-is,
// byte-identical to the previous behaviour, so nothing changes for the x86
// server case that dominates the fleet.
//
// Distinct names are ordered by first core index, not by count, so the string
// follows the SoC's own core numbering and two machines of the same model
// always render identically.
func describeCPUCores(infos []cpu.InfoStat) string {
	if len(infos) == 0 {
		return ""
	}

	type group struct {
		name  string
		count int
		first int
	}
	order := map[string]*group{}
	var groups []*group

	for i, info := range infos {
		name := strings.TrimSpace(info.ModelName)
		if name == "" {
			continue
		}
		g, ok := order[name]
		if !ok {
			g = &group{name: name, first: i}
			order[name] = g
			groups = append(groups, g)
		}
		g.count++
	}

	if len(groups) == 0 {
		return strings.TrimSpace(infos[0].ModelName)
	}
	// Uniform package: report the name alone, exactly as before. Appending a
	// count here would churn every existing inventory row for no new fact.
	if len(groups) == 1 {
		return groups[0].name
	}

	sort.SliceStable(groups, func(a, b int) bool { return groups[a].first < groups[b].first })

	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, strconv.Itoa(g.count)+"x "+g.name)
	}
	return strings.Join(parts, " + ")
}

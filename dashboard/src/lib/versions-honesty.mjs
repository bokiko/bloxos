// Versions page honesty helpers. Everything here is a pure mapping from the
// hub's existing /api/versions fields to truthful labels. Availability is not
// trust: a resolved binary only proves the hub found bytes on a validated
// path; signing is a separate, global property shown by its own banner.

// binaryReleaseLabel: release > 0 is a numbered release; an explicit 0 from
// the hub means legacy/unnumbered bytes; a missing field (older hub) is
// unknown — never guess "legacy" for it.
export function binaryReleaseLabel(binary, releaseFieldPresent) {
  if (!releaseFieldPresent) return "Release unknown (older hub)";
  if (!binary || binary.release == null) return "Release unknown (older hub)";
  if (binary.release > 0) return `Release ${binary.release}`;
  return "Legacy (unnumbered)";
}

// binaryStateLabel: availability only — never "trusted". Safe on a missing
// platform entry (undefined binary from an absent legacy platform).
export function binaryStateLabel(binary) {
  const available = Boolean(binary && binary.sha) && !binary?.error;
  return {
    available,
    label: available ? "Available" : "Unavailable",
    detail: available ? null : binary?.error || "No binary resolved for this platform.",
  };
}

// buildBinaryCards returns one card per platform the hub tracks. A platform
// the hub has no binary for still gets a card that says so — a missing
// Linux ARM64 build must be visible, not silently absent. Older hubs without
// agent_binaries_by_arch fall back to the two unlabeled legacy states.
export function buildBinaryCards(data) {
  if (!data) return [];
  if (data.agent_binaries_by_arch) {
    const byArch = data.agent_binaries_by_arch;
    const cards = [];
    for (const [arch, label] of [["amd64", "Linux · x86-64"], ["arm64", "Linux · ARM64"]]) {
      const binary = byArch.linux?.[arch] ?? { path: "", source: "", sha: "", mtime: "", error: `No ${arch} agent binary is served by this hub.` };
      cards.push({ key: `linux-${arch}`, label, binary });
    }
    const win = byArch.windows?.amd64 ?? { path: "", source: "", sha: "", mtime: "", error: "No Windows amd64 agent binary is served by this hub." };
    cards.push({ key: "windows-amd64", label: "Windows · x86-64", binary: win });
    return cards;
  }
  if (!data.agent_binaries) {
    // Oldest hubs expose only the legacy hub_sha/hub_windows_sha fields;
    // surface those as unlabeled cards instead of an empty grid.
    if (!data.hub_sha && !data.hub_windows_sha) return [];
    const legacyNote = "Resolver details require an updated hub.";
    return [
      { key: "linux", label: "Linux", binary: { path: "", source: "legacy API", sha: data.hub_sha ?? "", mtime: data.hub_mtime ?? "", error: legacyNote } },
      { key: "windows", label: "Windows", binary: { path: "", source: "legacy API", sha: data.hub_windows_sha ?? "", mtime: data.hub_windows_mtime ?? "", error: legacyNote } },
    ];
  }
  // A platform absent from the legacy map still gets a placeholder card —
  // downstream labels never dereference an undefined binary.
  const missing = (platform) => ({ path: "", source: "", sha: "", mtime: "", error: `No ${platform} binary is served by this hub.` });
  return [
    { key: "linux", label: "Linux", binary: data.agent_binaries.linux ?? missing("Linux") },
    { key: "windows", label: "Windows", binary: data.agent_binaries.windows ?? missing("Windows") },
  ];
}

// agentStatusLabel: with a current hub, no-blocker + no-pending already
// implies running SHA == offered SHA (handleListVersions computes
// update_pending = expected != "" && running_sha != expected). The label only
// refuses a green match when running_sha is absent or the hub predates
// per-architecture availability/blocker reporting. Older hubs can report
// no pending update even when no build is offered. Never infer a match then.
export function agentStatusLabel(agent, data) {
  if (agent.update_blocked_reason) {
    return { kind: "blocked", label: agent.update_pending ? "Withheld" : "Unavailable" };
  }
  if (agent.update_pending) return { kind: "pending", label: "Update pending" };
  if (!agent.running_sha) return { kind: "unknown", label: "Version unknown" };
  if (!data?.agent_binaries_by_arch) return { kind: "unknown", label: "Status unknown (older hub)" };
  return { kind: "current", label: "Matches offered build" };
}

// agentProtocolNote: protocol-1 agents verify signatures but do not enforce
// the protocol-2 release floor, so a signed older build is not rejected by
// them. An absent field (older hub) is unknown, not protocol 0.
export function agentProtocolNote(agent) {
  if (agent.update_protocol == null) return "Protocol unknown (older hub)";
  if (agent.update_protocol >= 2) return null;
  if (agent.update_protocol === 1) return "No rollback floor (protocol 1)";
  return "Pre-signature agent (no update verification)";
}

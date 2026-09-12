package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

// Managed agent bundle: agent payloads that travel with the hub release, so
// upgrading the hub upgrades what the fleet is offered.
//
// Agent binaries were served from fixed system paths that
// nothing in the release ever refreshed. `bloxos-update` replaces the hub and
// the dashboard and deliberately does not touch agent files, so a hub could run
// v1.7.1 while handing out agents built weeks earlier. Every machine then
// reported "matches offered build" — correctly, because it matched what the hub
// offered. The offer was simply stale, and that single fact accounted for
// missing power on ARM boards, absent source labels, and wrong CPU inventory.
//
// Placement alone is not enough: the resolver already had a
// `hub-executable-directory` candidate — and it sits LAST, behind the system
// defaults. Worse, the project's own systemd unit sets BLOXOS_AGENT_BINARY,
// which for amd64 is the authoritative, fail-closed first candidate. Dropping
// files beside the hub binary would therefore have changed nothing on a standard
// native install: a fix that looks right and is inert. The precedence contract
// below is the actual mechanism; the packaging is only its transport.
//
// Deliberately absent: no new manifest schema for the updater to
// understand, no updater install logic, no self-refresh. The existing archive
// checksum, safe extraction and copy already carry arbitrary files; the NEW hub
// consumes and verifies its own bundle. That keeps delivery logic in one
// component and avoids a two-step rollout.

// agentBundleDirName is the bundle's directory, a direct child of the hub
// executable's own directory.
//
// A direct child, not a sibling reached through "..": every path this file
// touches is then a plain join under a directory we already trust, and no
// traversal component ever enters a filesystem call.
const agentBundleDirName = "agents"

// agentBundleManifestName reuses the manifest scripts/agent_bundle.py already
// produces, rather than inventing a parallel catalog format.
const agentBundleManifestName = "agent-manifest.json"

// agentBundleRequiredPlatforms is every platform a packaged bundle must carry.
// Both server architectures ship all three, so a hub can serve any agent.
var agentBundleRequiredPlatforms = []string{"linux/amd64", "linux/arm64", "windows/amd64"}

const (
	agentBundleManifestMaxBytes = 64 << 10
	agentBundlePayloadMaxBytes  = 256 << 20
)

// agentBundleRequired is set at build time (-ldflags) on packaged hub builds.
//
// A plain marker, not a content hash. The obvious
// alternative — embedding the manifest's SHA — cannot work: that manifest
// includes the hub image digest, so the hub binary would have to contain a hash
// of a document that contains a hash of the hub binary. A build-time flag
// distinguishes "packaged, a bundle is mandatory" from "source build, a bundle
// is optional" with no cycle.
//
// Empty means a development or source build: a missing bundle is then normal
// and legacy discovery still applies.
var agentBundleRequired = ""

// agentBundleIsRequired reports whether this build must have a valid bundle.
func agentBundleIsRequired() bool {
	return strings.TrimSpace(agentBundleRequired) != ""
}

// agentDeliveryMode selects how agent binaries are resolved.
const (
	// agentDeliveryAuto is the default: a valid managed bundle takes precedence
	// over the project's own shipped default path.
	agentDeliveryAuto = "auto"
	// agentDeliveryExternal preserves the FULL legacy resolver, including an
	// intentional override that happens to equal the shipped default path. It
	// is the escape hatch for an operator who really does manage agent binaries
	// themselves at that location.
	agentDeliveryExternal = "external"
)

const agentDeliveryEnv = "BLOXOS_AGENT_DELIVERY"

// resolveAgentDeliveryMode reads the mode, failing closed on anything it does
// not recognise. A misspelled mode must be visible, not silently treated as the
// default — an operator who typed "externl" meant to change behaviour.
func resolveAgentDeliveryMode(getenv func(string) string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(getenv(agentDeliveryEnv)))
	switch raw {
	case "":
		return agentDeliveryAuto, nil
	case agentDeliveryAuto, agentDeliveryExternal:
		return raw, nil
	default:
		return "", fmt.Errorf("%s=%q is not a known delivery mode (want %q or %q)",
			agentDeliveryEnv, raw, agentDeliveryAuto, agentDeliveryExternal)
	}
}

// agentBundleArtifact is one platform's payload as the manifest declares it.
type agentBundleArtifact struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// agentBundleManifest is the subset of agent-manifest.json this hub reads.
type agentBundleManifest struct {
	Schema       int                            `json:"schema"`
	Version      string                         `json:"version"`
	Source       string                         `json:"source"`
	AgentRelease uint64                         `json:"agent_release"`
	Artifacts    map[string]agentBundleArtifact `json:"artifacts"`
}

// agentBundle is a validated bundle: every declared artifact has been checked.
type agentBundle struct {
	Dir      string
	Manifest agentBundleManifest
	// paths maps "<os>/<arch>" to the verified absolute payload path.
	paths map[string]string
	// expected maps the same key to the sha256 the catalog declares. Resolution
	// is captured once at package init, so a payload altered afterwards would
	// otherwise be served on its recomputed hash. The catalog's identity is the
	// authority, and it is re-checked on every resolution.
	expected map[string]string
}

// expectedSHA returns the catalog's declared hash for a platform.
func (b *agentBundle) expectedSHA(platform agentPlatform) (string, bool) {
	if b == nil {
		return "", false
	}
	sum, ok := b.expected[platform.OS+"/"+platform.Arch]
	return sum, ok
}

// verifyPayloadIdentity re-checks a managed payload against the catalog.
func (b *agentBundle) verifyPayloadIdentity(platform agentPlatform, path string) error {
	want, ok := b.expectedSHA(platform)
	if !ok {
		return fmt.Errorf("no catalog entry for %s", platform)
	}
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("payload sha256 %s no longer matches the catalog entry %s", got, want)
	}
	return nil
}

// pathFor returns the verified payload for a platform, if the bundle declares
// one. A bundle that does not declare a platform is not an error — it simply
// has nothing to offer there, and legacy discovery continues.
func (b *agentBundle) pathFor(platform agentPlatform) (string, bool) {
	if b == nil {
		return "", false
	}
	p, ok := b.paths[platform.OS+"/"+platform.Arch]
	return p, ok
}

// loadAgentBundle reads and fully validates the bundle beside the hub executable.
//
// Validation is all-or-nothing. A bundle that is present but invalid
// must fail closed rather than fall through to the frozen system defaults:
// falling through is precisely how a hub ends up quietly serving months-old
// agents while reporting success. The caller decides what a missing bundle
// means, via agentBundleIsRequired.
func loadAgentBundle(executableDir string, validate func(string) (string, error)) (*agentBundle, error) {
	dir := filepath.Join(executableDir, agentBundleDirName)
	manifestPath := filepath.Join(dir, agentBundleManifestName)

	// A missing directory is a source build; a directory without a manifest is a
	// broken install. Treating the second as absent would fall through to the
	// frozen system defaults, which is the staleness this replaces.
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat agent bundle directory: %w", err)
	}

	// Distinguish "no manifest" from "unreadable manifest" BEFORE the trust
	// check, so the error can say which of the two an operator is looking at.
	if _, err := os.Lstat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("agent bundle directory %s has no %s", dir, agentBundleManifestName)
		}
		return nil, fmt.Errorf("stat agent bundle manifest: %w", err)
	}

	// The manifest is the document that decides which bytes this hub will hand
	// the entire fleet, so it goes through the SAME trust check as the payloads
	// it describes: a regular file, not a symlink, root-owned with no
	// group/other write anywhere on its path.
	//
	// Stat-then-ReadFile was not enough, and the gap is not theoretical. Stat
	// follows symlinks, so a link planted in the bundle directory would have
	// been read from wherever it pointed. A FIFO stats as zero bytes, sails
	// under the size limit, and then blocks or streams without end. And even
	// for an ordinary file the size read by Stat is a different observation
	// from the bytes read afterwards — a file being written grows between them.
	manifestFile, err := validate(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("agent bundle manifest is not trusted: %w", err)
	}

	raw, err := readBoundedFile(manifestFile, agentBundleManifestMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("read agent bundle manifest: %w", err)
	}

	var manifest agentBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("agent bundle manifest is not valid JSON: %w", err)
	}
	if manifest.Schema != 1 {
		return nil, fmt.Errorf("agent bundle manifest schema %d is not supported", manifest.Schema)
	}
	if manifest.AgentRelease == 0 {
		return nil, fmt.Errorf("agent bundle manifest declares no agent release")
	}
	// Every server archive carries every agent platform. A bundle short of one
	// would let a platform quietly keep resolving to a frozen system default
	// while the feature reported success for the others.
	for _, required := range agentBundleRequiredPlatforms {
		if _, ok := manifest.Artifacts[required]; !ok {
			return nil, fmt.Errorf("agent bundle manifest does not declare %s", required)
		}
	}

	bundle := &agentBundle{Dir: dir, Manifest: manifest, paths: map[string]string{}, expected: map[string]string{}}

	for platform, artifact := range manifest.Artifacts {
		osName, arch, ok := strings.Cut(platform, "/")
		if !ok {
			return nil, fmt.Errorf("agent bundle declares malformed platform %q", platform)
		}
		if _, err := agentPlatformFor(osName, arch); err != nil {
			return nil, fmt.Errorf("agent bundle declares unsupported platform %q: %w", platform, err)
		}

		// The manifest is data, not a path source. A file name carrying a
		// separator or a parent reference would otherwise let a crafted bundle
		// point the hub at a file outside its own directory.
		if artifact.File == "" || artifact.File != filepath.Base(artifact.File) ||
			strings.ContainsAny(artifact.File, `/\`) || artifact.File == "." || artifact.File == ".." {
			return nil, fmt.Errorf("agent bundle artifact %q has an unsafe file name %q", platform, artifact.File)
		}
		if len(artifact.SHA256) != 64 {
			return nil, fmt.Errorf("agent bundle artifact %q has no usable sha256", platform)
		}
		// A declared size is mandatory and bounded: zero would make the size
		// check vacuous, and an absurd value signals a corrupt manifest.
		if artifact.Size <= 0 || artifact.Size > agentBundlePayloadMaxBytes {
			return nil, fmt.Errorf("agent bundle artifact %q declares an implausible size %d",
				platform, artifact.Size)
		}

		payload := filepath.Join(dir, artifact.File)

		// Reuse the SAME trust check every other candidate goes through: root
		// ownership, no group/other write anywhere on the path, a regular file.
		verified, err := validate(payload)
		if err != nil {
			return nil, fmt.Errorf("agent bundle artifact %q is not trusted: %w", platform, err)
		}

		payloadInfo, err := os.Stat(verified)
		if err != nil {
			return nil, fmt.Errorf("agent bundle artifact %q: %w", platform, err)
		}
		if payloadInfo.Size() != artifact.Size {
			return nil, fmt.Errorf("agent bundle artifact %q is %d bytes, manifest says %d",
				platform, payloadInfo.Size(), artifact.Size)
		}

		sum, err := fileSHA256(verified)
		if err != nil {
			return nil, fmt.Errorf("agent bundle artifact %q: %w", platform, err)
		}
		if !strings.EqualFold(sum, artifact.SHA256) {
			return nil, fmt.Errorf("agent bundle artifact %q sha256 %s does not match manifest %s",
				platform, sum, artifact.SHA256)
		}

		// The payload must be built for the architecture it is filed under.
		// A sha256 proves the bytes are the file the catalog names; it says
		// nothing about what those bytes ARE. Without reading the image
		// header, the only thing keeping amd64 bytes from being handed to an
		// arm64 machine is the manifest's own label — a claim made by the same
		// document the bundle ships. The resolver already gated Linux at
		// resolution time; a bundle is checked for every platform it declares,
		// Windows included, and at load, so a mislabelled payload stops the
		// hub instead of reaching one unlucky architecture later.
		if err := verifyAgentPayloadArch(osName, arch, verified); err != nil {
			return nil, fmt.Errorf("agent bundle artifact %q: %w", platform, err)
		}

		// The payload must actually BE the release the catalog claims. Without
		// this, a bundle could advertise a new release number while carrying
		// older bytes — which is precisely how the fleet came to run agents
		// months behind the hub while every check reported agreement.
		embedded, err := agentPayloadRelease(verified)
		if err != nil {
			return nil, fmt.Errorf("agent bundle artifact %q: %w", platform, err)
		}
		if embedded != manifest.AgentRelease {
			return nil, fmt.Errorf("agent bundle artifact %q carries release %d, catalog says %d",
				platform, embedded, manifest.AgentRelease)
		}

		bundle.paths[osName+"/"+arch] = verified
		bundle.expected[osName+"/"+arch] = strings.ToLower(artifact.SHA256)
	}

	return bundle, nil
}

// readBoundedFile reads at most max bytes and fails if the file has more.
//
// The bound is enforced by the READ, not by a prior Stat. That ordering is the
// whole point: a size observed before the read is a claim about a different
// moment, and it is not a claim a FIFO or a growing file honours.
func readBoundedFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// max+1 so hitting the limit exactly is distinguishable from overrunning it.
	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("file is larger than the %d byte limit", max)
	}
	return raw, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// agentPayloadRelease reads the release sequence compiled into a payload.
// A payload with no marker is rejected: unnumbered builds cannot be reasoned
// about by the release floor, so they must not be served as a managed bundle.
func agentPayloadRelease(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	seq, err := updatesigning.ExtractReleaseReader(f)
	if err != nil {
		return 0, fmt.Errorf("read release marker: %w", err)
	}
	if seq == 0 {
		return 0, fmt.Errorf("payload carries no release marker")
	}
	return seq, nil
}

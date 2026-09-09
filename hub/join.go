package main

// One-line Linux onboarding.
//
// POST /api/tokens returns a short command that fetches GET /join/<code> and
// runs what comes back once. The join code is the 15-minute install token
// itself: the token is already random, already expires, and is already
// consumed exactly once — by enrollment_committed, when the durable
// credential is stored — so a second secret or table would only add a
// second lifecycle to keep in step with the first. What /join serves is the
// complete Linux bootstrap (the same text /api/tokens returns as
// advanced_command), so nothing about what runs on the machine changes;
// only how it gets there does.
//
// GET never consumes the code. An install that dies after the download can
// be re-run with the same command until the agent commits its credential or
// the token expires; after either, the link is dead. Unknown, expired and
// used codes all get the same 404 body so the endpoint cannot be used to
// tell which tokens exist, and the code is never written to the hub log.
//
// Authenticating the fetch. Behind a publicly trusted certificate the
// command is plain verified TLS. Behind the private CA the hub is deployed
// with (Caddy's internal CA in docker/), a machine that has never seen the
// hub cannot verify its chain yet, so the command carries curl's
// --pinnedpubkey with the SPKI SHA-256 of the leaf certificate PUBLIC_URL
// presented at mint time, verified by the hub against BLOXOS_CA_CERT. curl
// then aborts before sending a byte if the key differs. That is a
// 15-minute pin of a key the hub itself just verified, not a permanent
// leaf-key pin: the token expires before the pin can go stale in any
// ordinary deployment. Caddy's internal leaves are re-keyed on renewal
// (12-hour lifetime, renewed around two thirds in), so a command minted
// shortly before a renewal can fail with curl exit 90 "public key does not
// match pinned public key"; the fix is to generate a fresh command. If the
// hub cannot obtain a pin it trusts, /api/tokens refuses to mint at all
// rather than emit an unauthenticated download.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// installTokenTTL is how long an install token, and therefore a join link
// and the pin its command carries, stays usable.
const installTokenTTL = 15 * time.Minute

// joinPinDialTimeout bounds the single TLS handshake the hub performs
// against PUBLIC_URL to learn the presented leaf key. Minting is an
// interactive dashboard action; a hub that cannot reach its own public
// address in this long has a configuration problem the operator needs to
// see, not a retry to wait on.
const joinPinDialTimeout = 5 * time.Second

// joinUnavailableBody is the one answer /join gives for every code it will
// not serve: never minted, expired, consumed, or bound to a Windows
// re-enrollment. Deliberately identical so the endpoint leaks nothing about
// which of those it was.
const joinUnavailableBody = "This BloxOS join link is invalid, expired, or already used.\n" +
	"Generate a new Add Machine command from the dashboard and run that instead.\n"

// joinPinResolver returns the base64 SHA-256 of the SubjectPublicKeyInfo of
// the TLS leaf certificate publicURL currently presents, after verifying the
// chain against the CA certificates in caPEM. It performs exactly one TLS
// handshake with the host and port named by publicURL and no HTTP request,
// so there are no redirects to follow and nothing beyond the
// operator-configured PUBLIC_URL is ever contacted. Tests replace it; see
// stubJoinPinResolver.
type joinPinResolver func(ctx context.Context, publicURL *url.URL, caPEM []byte) (string, error)

var resolveJoinPin joinPinResolver = pinPresentedLeafSPKI

func pinPresentedLeafSPKI(ctx context.Context, publicURL *url.URL, caPEM []byte) (string, error) {
	if publicURL == nil || publicURL.Scheme != "https" {
		return "", fmt.Errorf("PUBLIC_URL is not https; a TLS key pin only applies to https")
	}
	host := publicURL.Hostname()
	if host == "" {
		return "", fmt.Errorf("PUBLIC_URL has no host")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return "", fmt.Errorf("the bootstrap CA file holds no PEM certificate the hub can verify PUBLIC_URL with")
	}
	dialAddr, err := pinDialAddress(publicURL)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, joinPinDialTimeout)
	defer cancel()
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: joinPinDialTimeout},
		Config: &tls.Config{
			// SNI and verification always use PUBLIC_URL's hostname against
			// the configured CA, even when BLOXOS_PIN_DIAL_ADDR sends the TCP
			// connection somewhere else (Compose: caddy:443). host may be an
			// IP literal; Go then sends no SNI and verifies against the
			// certificate's IP SANs, which is what Caddy's default_sni
			// deployment presents for IP HUB_HOSTs. A wrong dial target
			// cannot downgrade trust — it can only fail the handshake.
			ServerName: host,
			RootCAs:    roots,
			MinVersion: tls.VersionTLS12,
		},
	}
	rawConn, err := dialer.DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		return "", fmt.Errorf("TLS handshake with %s (verifying SNI %q) using the configured CA failed: %w", dialAddr, host, err)
	}
	defer rawConn.Close()
	conn, ok := rawConn.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("TLS dial to %s returned a non-TLS connection", dialAddr)
	}
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("%s presented no certificate", dialAddr)
	}
	leaf := state.PeerCertificates[0]
	if remaining := time.Until(leaf.NotAfter); remaining < installTokenTTL {
		return "", fmt.Errorf("the certificate %s presents expires at %s, before a join command minted now would; wait for it to renew",
			dialAddr, leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// pinDialAddrEnv optionally overrides the TCP address the pin handshake
// connects to, without changing what is verified. The hub computes the join
// pin by connecting to its own PUBLIC_URL, but in some topologies the public
// address is not reachable from inside the hub process: in the Compose stack
// Caddy is a separate service, so PUBLIC_URL (which routes to Caddy from a
// browser or agent) resolves to the hub's own loopback from inside the hub
// container. Set this to the internal reverse-proxy address (Compose:
// "caddy:443") so the handshake reaches the proxy while SNI and certificate
// verification still use PUBLIC_URL's hostname against the configured CA.
const pinDialAddrEnv = "BLOXOS_PIN_DIAL_ADDR"

// pinDialAddress is the host:port the pin handshake dials. It defaults to
// PUBLIC_URL's host and port; BLOXOS_PIN_DIAL_ADDR overrides only the dial
// target. The override is operator/deployment configuration, never derived
// from a request, and it does not affect verification.
func pinDialAddress(publicURL *url.URL) (string, error) {
	host := publicURL.Hostname()
	port := publicURL.Port()
	if port == "" {
		port = "443"
	}
	override := strings.TrimSpace(os.Getenv(pinDialAddrEnv))
	if override == "" {
		return net.JoinHostPort(host, port), nil
	}
	if strings.Contains(override, "://") || strings.Contains(override, "/") {
		return "", fmt.Errorf("%s=%q must be host:port, not a URL", pinDialAddrEnv, override)
	}
	h, p, err := net.SplitHostPort(override)
	if err != nil || h == "" || p == "" {
		return "", fmt.Errorf("%s=%q must be host:port", pinDialAddrEnv, override)
	}
	return net.JoinHostPort(h, p), nil
}

// parsePublicURL validates the operator's PUBLIC_URL as the base of a join
// link: http or https, a host, and no credentials, query or fragment that
// would end up inside a copied shell command.
func parsePublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("PUBLIC_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("PUBLIC_URL must start with http:// or https://")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("PUBLIC_URL has no host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("PUBLIC_URL must not carry credentials, a query string, or a fragment")
	}
	// Checked on the raw value: that is what joinURLFor pastes into the
	// command, and url.String() would have percent-escaped the very
	// characters this is refusing.
	if !joinURLSafe.MatchString(strings.TrimSpace(raw)) {
		return nil, fmt.Errorf("PUBLIC_URL contains characters that cannot be placed in a shell command")
	}
	return u, nil
}

// bootstrapCASource records where a bootstrap CA candidate came from, because
// the two are held to different standards: an explicitly-configured CA is
// authoritative (a mismatch or an unreadable/malformed file fails the mint
// closed), while an auto-discovered one is a best-effort guess that may be
// overruled by system trust.
type bootstrapCASource int

const (
	caSourceNone     bootstrapCASource = iota // no candidate on this hub
	caSourceExplicit                          // operator set BLOXOS_CA_CERT
	caSourceAuto                              // discovered on a default host path
)

// loadBootstrapCAClassified selects the bootstrap CA and reports its source.
// An explicit BLOXOS_CA_CERT is authoritative: it is never silently skipped for
// an auto-discovered path, and if it is missing, unreadable, or not a PEM
// certificate the mint fails closed rather than falling back. Auto-discovered
// candidates on the default host paths are best-effort: a stray file that is
// absent, unreadable, or not a certificate is skipped, so an unrelated file on
// disk never turns a public hub private.
func (s *Server) loadBootstrapCAClassified() ([]byte, string, bootstrapCASource, error) {
	if env := strings.TrimSpace(os.Getenv("BLOXOS_CA_CERT")); env != "" {
		data, err := os.ReadFile(env)
		if err != nil {
			return nil, env, caSourceExplicit, fmt.Errorf("BLOXOS_CA_CERT %s cannot be read: %w", env, err)
		}
		if !x509.NewCertPool().AppendCertsFromPEM(data) {
			return nil, env, caSourceExplicit, fmt.Errorf("BLOXOS_CA_CERT %s holds no PEM certificate", env)
		}
		return data, env, caSourceExplicit, nil
	}
	for _, path := range s.caCertCandidatePaths() {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue // absent or unreadable stray file: not this hub's CA
		}
		if !x509.NewCertPool().AppendCertsFromPEM(data) {
			continue // present but not a certificate: ignore it
		}
		return data, path, caSourceAuto, nil
	}
	return nil, "", caSourceNone, nil
}

// bootstrapCADecision is how a join command will authenticate the fetch,
// computed once per mint from a single classification of PUBLIC_URL's TLS
// trust and reused for the whole response. It is never cached across requests,
// so a re-keyed leaf is re-pinned on the next mint.
type bootstrapCADecision struct {
	caURL    string // "/download/ca.crt" URL when private; "" when public/http
	caSHA256 string // sha256 of the bound CA PEM when private; "" otherwise
	joinPin  string // leaf SPKI pin when private; "" otherwise
}

// errJoinCAChainMismatch marks a pin resolution that failed because the leaf
// the endpoint presented does not chain to (or match the hostname of) the CA
// it was verified against — a completed handshake with a mismatching cert — as
// opposed to a network/handshake failure that never established what the
// endpoint presents. Only a genuine mismatch against an auto-discovered CA
// falls back to system trust; a network failure never does. Tests inject this
// to exercise that fallback without a real endpoint.
var errJoinCAChainMismatch = errors.New("presented certificate does not verify against the candidate CA")

// isCAChainMismatch reports whether err is a certificate-verification failure
// (the endpoint answered but its chain/hostname does not verify) rather than a
// failure to reach or handshake with the endpoint at all.
func isCAChainMismatch(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errJoinCAChainMismatch) {
		return true
	}
	var cve *tls.CertificateVerificationError
	if errors.As(err, &cve) {
		return true
	}
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	var he x509.HostnameError
	return errors.As(err, &ua) || errors.As(err, &ci) || errors.As(err, &he)
}

// joinSystemTrustVerifier verifies PUBLIC_URL's presented chain against the
// hub's OS trust store (and the URL hostname) in one TLS handshake, returning
// nil only if that store verifies it. Verifying there means the hub's own
// operating system trusts the issuer — not proof the CA is globally public —
// which is the right signal for whether this hub needs a bootstrap CA.
type joinSystemTrustVerifier func(ctx context.Context, publicURL *url.URL) error

// verifySystemTrust runs the per-server system-trust probe (tests inject one
// via s.systemTrustProbe) or the default live handshake.
func (s *Server) verifySystemTrust(ctx context.Context, publicURL *url.URL) error {
	if s != nil && s.systemTrustProbe != nil {
		return s.systemTrustProbe(ctx, publicURL)
	}
	return verifyEndpointSystemTrust(ctx, publicURL)
}

// verifyEndpointSystemTrust performs a single TLS handshake to PUBLIC_URL's
// endpoint verified against the hub's OS trust store. Like the pin resolver it
// makes no HTTP request and dials only PUBLIC_URL (or the operator-configured
// BLOXOS_PIN_DIAL_ADDR), so nothing beyond the configured endpoint is
// contacted, and a wrong dial target can only fail the handshake.
func verifyEndpointSystemTrust(ctx context.Context, publicURL *url.URL) error {
	if publicURL == nil || publicURL.Scheme != "https" {
		return fmt.Errorf("PUBLIC_URL is not https")
	}
	host := publicURL.Hostname()
	if host == "" {
		return fmt.Errorf("PUBLIC_URL has no host")
	}
	dialAddr, err := pinDialAddress(publicURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, joinPinDialTimeout)
	defer cancel()
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: joinPinDialTimeout},
		Config: &tls.Config{
			ServerName: host, // RootCAs nil = system trust; full verification
			MinVersion: tls.VersionTLS12,
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		return fmt.Errorf("system-trust TLS handshake with %s (verifying SNI %q) failed: %w", dialAddr, host, err)
	}
	conn.Close()
	return nil
}

// classifyBootstrapCA decides, once per request, how a join command for
// PUBLIC_URL authenticates its fetch. The contract:
//
//   - http or no PUBLIC_URL: plain, no CA material, no probe.
//   - https with no CA candidate: publicly trusted HTTPS, no probe added.
//   - https with a CA candidate: verify the live leaf against it (one
//     handshake).
//   - verifies → private CA: bind its SHA-256 and pin the verified leaf.
//   - explicit CA that does not verify (mismatch, unreachable, or invalid):
//     fail closed — an operator-configured CA is authoritative and is never
//     downgraded to system trust.
//   - auto-discovered CA that the leaf does not chain to: consult the hub's
//     OS trust store with the same endpoint. Verified there → classify public
//     (plain verified TLS, no CA, no -k). Not verified there either → refuse.
//   - auto-discovered CA but the endpoint is unreachable: refuse — we never
//     learned what it presents, so we do not guess.
//
// A refusal returns an error the caller surfaces as an actionable 503 with no
// token minted. The single verified handshake is reused within the request; no
// SPKI is cached across requests.
func (s *Server) classifyBootstrapCA(ctx context.Context, httpBase string, publicURL *url.URL) (bootstrapCADecision, error) {
	if !strings.HasPrefix(httpBase, "https://") {
		return bootstrapCADecision{}, nil
	}
	caPEM, caPath, source, err := s.loadBootstrapCAClassified()
	if err != nil {
		return bootstrapCADecision{}, err // explicit CA invalid: fail closed
	}
	if source == caSourceNone {
		return bootstrapCADecision{}, nil // publicly trusted HTTPS, no probe
	}

	pin, perr := resolveJoinPin(ctx, publicURL, caPEM)
	if perr == nil {
		sum := sha256.Sum256(caPEM)
		return bootstrapCADecision{
			caURL:    httpBase + "/download/ca.crt",
			caSHA256: hex.EncodeToString(sum[:]),
			joinPin:  pin,
		}, nil
	}

	if source == caSourceExplicit {
		return bootstrapCADecision{}, fmt.Errorf(
			"PUBLIC_URL's certificate does not verify against BLOXOS_CA_CERT (%s): %w", caPath, perr)
	}

	// Auto-discovered candidate. Only a completed-but-mismatching handshake is
	// a reason to consult the hub's OS trust store; an unreachable endpoint is
	// refused rather than guessed.
	if !isCAChainMismatch(perr) {
		return bootstrapCADecision{}, fmt.Errorf(
			"cannot reach PUBLIC_URL over TLS to classify its certificate: %w", perr)
	}
	if verr := s.verifySystemTrust(ctx, publicURL); verr != nil {
		return bootstrapCADecision{}, fmt.Errorf(
			"PUBLIC_URL's certificate verifies against neither the discovered local CA (%s) nor the hub's OS trust store: %w", caPath, verr)
	}
	// The hub's OS trust store verifies the leaf, so the discovered local CA is
	// unrelated to how this hub is actually served: plain verified TLS, no CA
	// material.
	return bootstrapCADecision{}, nil
}

// currentJoinCABinding recomputes the CA-binding value to compare against a
// join token's mint-time binding, WITHOUT re-pinning the leaf. It returns the
// current value and whether it could be established at all.
//
// The distinction matters at serve time: curl already authenticated the
// mint-time leaf (via the pinned command for a private hub, or ordinary TLS
// for a public one) before this GET runs, so re-probing/re-pinning here would
// wrongly reject a still-valid link on a near-expiry leaf or a transient hub
// outage. So:
//
//   - Private mint binding (non-empty): recompute the SELECTED CA's SHA-256
//     using the same strict source rules as mint (explicit-authoritative,
//     PEM-validated, malformed ambient skipped) — but no TLS probe and no
//     leaf-lifetime guard. A removed or now-unreadable CA is genuine drift.
//   - Public mint binding (empty): classify only to tell an unrelated ambient
//     CA (still public → "") from a real private CA the leaf now chains to
//     (drift). With no CA candidate this does no probe and returns "".
func (s *Server) currentJoinCABinding(ctx context.Context, httpBase, mintTimeCASHA256 string) (string, bool) {
	if mintTimeCASHA256 != "" {
		caPEM, _, source, err := s.loadBootstrapCAClassified()
		if err != nil || source == caSourceNone {
			return "", false
		}
		sum := sha256.Sum256(caPEM)
		return hex.EncodeToString(sum[:]), true
	}
	publicURL, err := parsePublicURL(httpBase)
	if err != nil {
		return "", false
	}
	decision, err := s.classifyBootstrapCA(ctx, httpBase, publicURL)
	if err != nil {
		return "", false
	}
	return decision.caSHA256, true
}

// joinURLFor is the link the short command fetches. The code is the install
// token, placed in the path so the hub can look it up without the client
// sending anything else.
//
// Newly minted links use the canonical /api/join/ path: old reverse proxies
// already forward /api/* to the hub but may not forward a bare /join, so the
// /api-prefixed path onboards through those deployments with no proxy change.
// The legacy GET /join/:code route is retained for links minted earlier; both
// routes share the same handler and public status. The code stays a path
// segment (never a query parameter), so redactJoinCodes scrubs it from logs
// and it never leaks in a query string.
func joinURLFor(httpBase, code string) string {
	return strings.TrimRight(httpBase, "/") + "/api/join/" + code
}

// shellBareWord is what can sit inside the single-quoted wrapper of the
// join command unquoted and unescaped: no whitespace, no quotes, nothing
// the inner shell expands or globs. A base64 pin ("sha256//" + [A-Za-z0-9+/=])
// always matches; a URL matches unless PUBLIC_URL is an IPv6 literal or
// carries a percent-escape, which shellWord then double-quotes. The words
// are never single-quoted, because the wrapper already is.
var shellBareWord = regexp.MustCompile(`^[A-Za-z0-9._~:/+=-]+$`)

// joinURLSafe is the whole set of characters a join URL may contain and
// still be pasted safely inside the wrapper: the bare-word set plus IPv6
// brackets and percent-escapes. parsePublicURL enforces it, so a PUBLIC_URL
// that cannot be placed in a command fails at mint rather than producing
// one that a shell might misread.
var joinURLSafe = regexp.MustCompile(`^[A-Za-z0-9._~:/+=%\[\]-]+$`)

func shellWord(value string) string {
	if shellBareWord.MatchString(value) {
		return value
	}
	return `"` + value + `"`
}

// buildLinuxJoinCommand is the short command an operator pastes:
//
//	bash -c 's=$(curl -fsS <url>) && bash -c "$s"'
//
// The download completes before any of it runs, and it runs only if curl
// succeeded: an expired link (curl exit 22 on the 404), a key that does not
// match the pin (exit 90), or any connection failure short-circuits the &&,
// so the command exits non-zero with curl's message on the terminal and
// nothing executed — not even a partial script. Otherwise the exit status
// is the bootstrap's own.
//
//   - Private CA: -k drops chain verification, which the fresh machine
//     cannot perform yet, and --pinnedpubkey replaces it with a check that
//     the server holds the key the hub verified minutes ago. Either flag
//     alone would be wrong; together they are curl's documented pattern.
//   - Publicly trusted https: ordinary verified TLS, nothing else.
//   - http: plaintext, the same trust level as the verbose command's own
//     install.sh fetch, which prints the existing unencrypted-bootstrap
//     warning when it runs. Nothing is added for http; nothing is relaxed.
func buildLinuxJoinCommand(joinURL, pin string) string {
	fetch := "curl -fsS " + shellWord(joinURL)
	if pin != "" && strings.HasPrefix(joinURL, "https://") {
		fetch = "curl -fsSk --pinnedpubkey " + shellWord("sha256//"+pin) + " " + shellWord(joinURL)
	}
	return `bash -c 's=$(` + fetch + `) && bash -c "$s"'`
}

// linuxJoinScript is what GET /join serves: the verbose bootstrap as a
// runnable file. It is byte-identical to advanced_command apart from the
// shebang and trailing newline, and the test suite pins that.
func linuxJoinScript(httpBase, wsBase, token, caURL, caSHA256 string) string {
	return "#!/bin/bash\n" + buildLinuxInstallCommand(httpBase, wsBase, token, caURL, caSHA256) + "\n"
}

// joinTokenInfo holds the mint-time binding values for a usable join token.
// The bootstrap script is not stored — it would embed the raw install token —
// but rebuilt at serve time from these values plus the request token. Fields
// are populated only when the token is usable.
type joinTokenInfo struct {
	MintTimeHTTPBase string
	MintTimeCASHA256 string
}

// joinCodeUsable reports whether code is an unexpired, unconsumed, unbound
// install token with stored mint-time binding values. It reads the same row
// the enrollment transactions consume (consumeTokenAndStoreCredential sets used
// = TRUE only when the durable credential is stored), so a link goes dead at
// exactly the moment a retry could no longer succeed, and never before. Any
// failure is "not usable"; the code is not logged.
func (s *Server) joinCodeUsable(code string) (bool, joinTokenInfo) {
	if code == "" || len(code) > 128 {
		return false, joinTokenInfo{}
	}
	h := sha256.Sum256([]byte(code))
	tokenHash := hex.EncodeToString(h[:])

	var expiresAt string
	var used bool
	var targetMachineID sql.NullString
	var mintTimeHTTPBase, mintTimeCASHA256 sql.NullString
	err := s.db.QueryRow(`SELECT expires_at, used, target_machine_id, mint_time_http_base, mint_time_ca_sha256 FROM tokens WHERE token_hash = ?`, tokenHash).
		Scan(&expiresAt, &used, &targetMachineID, &mintTimeHTTPBase, &mintTimeCASHA256)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("join: token lookup failed: %v", err)
		}
		return false, joinTokenInfo{}
	}
	// A Windows re-enrollment token is bound to one machine and to
	// install.ps1; it is not a Linux join code.
	if used || targetMachineID.Valid {
		return false, joinTokenInfo{}
	}
	// mint_time_http_base is the config binding the script is rebuilt from and
	// the drift check compares against. Tokens minted before the binding
	// columns were added have no stored base and cannot be served; they expire
	// naturally (installTokenTTL = 15 minutes).
	if !mintTimeHTTPBase.Valid || mintTimeHTTPBase.String == "" {
		return false, joinTokenInfo{}
	}
	// mint_time_ca_sha256 must be present (bound), though an empty string is a
	// valid binding — it is what a publicly trusted HTTPS or plain HTTP mint
	// stores. A NULL means the binding was never recorded (a pre-binding row),
	// so the token cannot be served.
	if !mintTimeCASHA256.Valid {
		return false, joinTokenInfo{}
	}
	if checkTokenExpiry(expiresAt) != nil {
		return false, joinTokenInfo{}
	}
	info := joinTokenInfo{
		MintTimeHTTPBase: mintTimeHTTPBase.String,
		MintTimeCASHA256: mintTimeCASHA256.String, // may be empty for http or publicly trusted https
	}
	return true, info
}

// rebuildLinuxJoinScript reconstructs the mint-time bootstrap script from the
// stored config binding plus the token supplied in the join request. It never
// reads a stored script, so the raw install token is not persisted; the
// derivations mirror publicAndWebsocketBase and the mint-time CA classification
// exactly, so the output is byte-identical to what was minted.
func rebuildLinuxJoinScript(httpBase, caSHA256, token string) string {
	wsBase := strings.Replace(httpBase, "https://", "wss://", 1)
	wsBase = strings.Replace(wsBase, "http://", "ws://", 1)
	caURL := ""
	if caSHA256 != "" {
		caURL = httpBase + "/download/ca.crt"
	}
	return linuxJoinScript(httpBase, wsBase, token, caURL, caSHA256)
}

// handleJoinScript serves the Linux bootstrap for a usable join code, after
// verifying the current configuration matches the mint-time binding. If
// PUBLIC_URL or the bootstrap CA have changed since mint, the join is rejected
// with the same opaque 404 as any other unusable code, so an operator who
// changes PUBLIC_URL or BLOXOS_CA_CERT after minting a securely pinned join
// command cannot redirect agents to a different authority or CA.
//
// Responses are plain text, never cached, and never sniffed into anything else.
func (s *Server) handleJoinScript(c echo.Context) error {
	h := c.Response().Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")

	// Check PUBLIC_URL before looking up the token: a hub with no PUBLIC_URL
	// cannot serve join links at all, even ones minted before it was unset.
	currentHTTPBase, _ := publicAndWebsocketBase()
	if currentHTTPBase == "" {
		// Missing configuration makes every link unavailable. Preserve the
		// same opaque response as all other unusable codes.
		return c.String(http.StatusNotFound, joinUnavailableBody)
	}

	usable, info := s.joinCodeUsable(c.Param("code"))
	if !usable {
		return c.String(http.StatusNotFound, joinUnavailableBody)
	}

	// Config-drift check: verify the current PUBLIC_URL and CA still match what
	// was captured at mint. If they differ, reject the join — the command carries
	// a pin for the mint-time cert and a URL for the mint-time hub, and serving
	// a script with different values would either fail the pin check or redirect
	// the agent to a different authority than what was authenticated at mint.

	// Origin drift first (cheap, no I/O).
	if currentHTTPBase != strings.TrimRight(info.MintTimeHTTPBase, "/") {
		// PUBLIC_URL has changed. The join command has the mint-time URL
		// embedded, but if it somehow reaches this hub on the new URL, reject
		// rather than serve a script that points to a different authority.
		return c.String(http.StatusNotFound, joinUnavailableBody)
	}

	// CA drift. How we recompute the current binding depends on what was bound:
	currentCASHA256, ok := s.currentJoinCABinding(c.Request().Context(), currentHTTPBase, info.MintTimeCASHA256)
	if !ok {
		// The hub cannot currently establish the binding (CA removed/unreadable,
		// or — for a public binding with an ambient CA — PUBLIC_URL unreachable):
		// treat the link as unavailable, like any other unusable code.
		return c.String(http.StatusNotFound, joinUnavailableBody)
	}
	if currentCASHA256 != info.MintTimeCASHA256 {
		// The bootstrap CA has changed. The join command has a pin for the
		// mint-time leaf, and a script with a different CA would fail bootstrap
		// or redirect the agent to trust a different CA than was pinned.
		return c.String(http.StatusNotFound, joinUnavailableBody)
	}

	// Config matches mint-time binding: rebuild the script from the stored
	// binding and the token from the request path. The raw token was never
	// stored; the rebuilt script is byte-identical to what was minted.
	// Rebuild from the normalized origin so a legacy binding stored with a
	// trailing slash cannot put "//install.sh" back into the served script.
	return c.String(http.StatusOK, rebuildLinuxJoinScript(strings.TrimRight(info.MintTimeHTTPBase, "/"), info.MintTimeCASHA256, c.Param("code")))
}

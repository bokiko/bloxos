#!/usr/bin/env bash
# End-to-end smoke test for the Compose hub stack on a disposable Linux host.
#
# Brings up docker/compose.yaml, completes first-boot setup through the API,
# mints an install token, runs the generated Linux paste block ON THIS HOST
# (so this host becomes an enrolled agent: it installs a systemd service,
# /etc/bloxos, and /usr/local/bin/bloxos-agent), and asserts the machine
# comes online with the CA pinned. Then it removes the agent and the stack.
#
# Run only on a host you can throw away (a CI runner, a Lima VM):
#   SMOKE_CONFIRM_DISPOSABLE=1 scripts/smoke/compose-enroll.sh
#
# Environment:
#   HUB_HOST   hostname or IP this host reaches the stack on (default 127.0.0.1)
#   KEEP=1     leave the stack and the agent installed for inspection
#
# Uses the images named in docker/compose.yaml if they exist locally and
# builds only missing ones; run `docker compose --project-directory docker
# build` first after changing a Dockerfile.
#
# Requires: docker compose, systemd, sudo without a password, curl, openssl,
# python3.
set -euo pipefail

if [[ "${SMOKE_CONFIRM_DISPOSABLE:-}" != "1" ]]; then
  echo "refusing: this installs an agent service on this host; set SMOKE_CONFIRM_DISPOSABLE=1 on a disposable host" >&2
  exit 2
fi

HUB_HOST="${HUB_HOST:-127.0.0.1}"
HUB="https://$HUB_HOST"
COMPOSE_DIR="$(cd "$(dirname "$0")/../../docker" && pwd)"
export HUB_HOST
compose() { docker compose --project-directory "$COMPOSE_DIR" "$@"; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }
fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }

cleanup() {
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "== hub and caddy logs (last 60 lines) after failure"
    compose logs --tail 60 hub caddy 2>/dev/null || true
    sudo journalctl -u bloxos-agent --no-pager -n 20 2>/dev/null || true
  fi
  [[ "${KEEP:-}" == "1" ]] && { echo "KEEP=1: stack and agent left in place"; return; }
  echo "== cleanup"
  sudo systemctl disable --now bloxos-agent bloxos-agent-recover >/dev/null 2>&1 || true
  sudo rm -f /etc/systemd/system/bloxos-agent.service /etc/systemd/system/bloxos-agent-recover.service \
    /usr/local/bin/bloxos-agent /usr/local/bin/bloxos-agent-recover /usr/local/bin/bloxos-agent.prev \
    /usr/local/bin/.bloxos-agent-updated-at
  sudo rm -rf /etc/bloxos
  sudo systemctl daemon-reload
  compose down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== up ($HUB)"
compose up -d

echo "== wait for health"
for _ in $(seq 1 60); do
  curl -sk --max-time 3 "$HUB/health" | grep -q '"ok"' && break
  sleep 2
done
curl -sk --max-time 3 "$HUB/health" | grep -q '"ok"' || fail "hub not healthy through Caddy"
[[ "$(curl -sk -o /dev/null -w '%{http_code}' "$HUB/login")" == 200 ]] || fail "dashboard not served"
[[ "$(curl -sk -o /dev/null -w '%{http_code}' "$HUB/install.ps1")" == 200 ]] || fail "Windows installer not served"

echo "== served certificate key type"
LEAF="$(openssl s_client -connect "$HUB_HOST:443" -servername "$HUB_HOST" </dev/null 2>/dev/null | openssl x509 -noout -text)"
echo "$LEAF" | grep -q 'Public Key Algorithm: rsaEncryption' || fail "served certificate is not RSA (AGENTS.md requires RSA-2048 for Windows PowerShell 5.1 clients)"
echo "$LEAF" | grep -q 'Public-Key: (2048 bit)' || fail "served RSA certificate is not 2048-bit"

echo "== first-boot setup and login"
PASSWORD="smoke-$(head -c 12 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')"
if curl -sk "$HUB/api/setup/status" | grep -q '"needs_setup":true'; then
  SETUP_TOKEN="$(compose exec -T hub cat /data/.bloxos/setup-token)"
  curl -sk -f -X POST "$HUB/api/setup" -H 'Content-Type: application/json' \
    -d "{\"setup_token\":\"$SETUP_TOKEN\",\"username\":\"smoke\",\"password\":\"$PASSWORD\",\"pin\":\"1234\"}" >/dev/null \
    || fail "setup rejected"
else
  fail "hub already set up; this harness expects a fresh stack"
fi
JWT="$(curl -sk -f -X POST "$HUB/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"smoke\",\"password\":\"$PASSWORD\"}" | json 'd["token"]')"
[[ -n "$JWT" ]] || fail "login returned no token"

echo "== install token"
TOKEN_JSON="$(curl -sk -f -X POST "$HUB/api/tokens" -H "Authorization: Bearer $JWT")"
CA_SHA="$(echo "$TOKEN_JSON" | json 'd.get("ca_sha256","")')"
[[ -n "$CA_SHA" ]] || fail "install command carries no CA fingerprint; hub could not read Caddy's CA"
PASTE="$(mktemp)"
echo "$TOKEN_JSON" | json 'd["command"]' > "$PASTE"

echo "== enroll this host with the generated Linux command"
bash "$PASTE" || fail "paste block failed"
rm -f "$PASTE"

echo "== verify"
for _ in $(seq 1 30); do
  curl -sk "$HUB/api/machines" -H "Authorization: Bearer $JWT" | grep -q '"status":"online"' && break
  sleep 2
done
curl -sk "$HUB/api/machines" -H "Authorization: Bearer $JWT" | grep -q '"status":"online"' || fail "agent never came online"
[[ "$(systemctl is-active bloxos-agent)" == active ]] || fail "agent service not active"
[[ "$(sudo sha256sum /etc/bloxos/ca.crt | cut -c1-64)" == "$CA_SHA" ]] || fail "installed CA does not match the fingerprint in the install command"
sudo test -s /etc/bloxos/agent-secret || fail "agent has no durable secret; enrollment did not commit"
sudo test -s /etc/bloxos/agent-update.pub || fail "update key not pinned"

echo "SMOKE PASS: agent enrolled through the Compose stack at $HUB"

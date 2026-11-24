#!/usr/bin/env bash
# Dependency, secret and manifest scanning. Exits non-zero when a hard gate fails.
set -uo pipefail
cd "$(dirname "$0")/.."

status=0
note() { printf '\n== %s\n' "$*"; }
fail() { printf 'GATE FAILED: %s\n' "$*" >&2; status=1; }

note "Go vulnerability scan"
GOVULNCHECK="$(command -v govulncheck || true)"
if [ -z "${GOVULNCHECK}" ] && [ -x "$(go env GOPATH)/bin/govulncheck" ]; then
  GOVULNCHECK="$(go env GOPATH)/bin/govulncheck"
fi
if [ -n "${GOVULNCHECK}" ]; then
  "${GOVULNCHECK}" ./... || fail "govulncheck found reachable vulnerabilities"
else
  echo "govulncheck not installed; run: go install golang.org/x/vuln/cmd/govulncheck@latest"
  fail "govulncheck is required for a release gate"
fi

note "Go vet"
go vet ./... || fail "go vet"

note "Dashboard production dependency audit"
if [ -d apps/dashboard/node_modules ]; then
  (cd apps/dashboard && npm audit --omit=dev --audit-level=high) || fail "npm audit reported high severity issues"
else
  echo "apps/dashboard/node_modules is absent; run npm ci first"
  fail "dashboard dependencies are not installed"
fi

note "Container and manifest misconfiguration scan"
if command -v trivy >/dev/null; then
  trivy config --severity HIGH,CRITICAL --exit-code 1 infrastructure/helm ||
    fail "trivy Helm findings"
  trivy config --severity HIGH,CRITICAL --exit-code 1 infrastructure/docker ||
    fail "trivy Dockerfile findings"
  echo "self-contained Kubernetes base is development-only and is covered in docs/security-audit.md"
  trivy fs --scanners secret --exit-code 1 --skip-dirs .git --skip-dirs node_modules \
    --skip-dirs .venv --skip-dirs bin --skip-dirs .data . || fail "trivy secret findings"
else
  echo "trivy not installed; run: brew install trivy"
  fail "trivy is required for a release gate"
fi

note "Python dependency audit"
if command -v uv >/dev/null; then
  (cd runtimes/python && uv run --frozen --no-dev --with pip-audit pip-audit) ||
    fail "pip-audit reported runtime vulnerabilities"
  (cd sdk/python && uv run --frozen --no-dev --with pip-audit pip-audit) ||
    fail "pip-audit reported SDK vulnerabilities"
else
  echo "uv is not installed; run: curl -LsSf https://astral.sh/uv/install.sh | sh"
  fail "uv/pip-audit is required for a release gate"
fi

if [ "${status}" -ne 0 ]; then
  echo
  echo "Security audit failed." >&2
  exit 1
fi
echo
echo "Security audit passed."

#!/usr/bin/env bash
#
# Rehearse a release against a throwaway local npm registry.
#
# Installing from a tarball proves the launcher can find a binary you handed
# it. It cannot prove the thing that actually matters on release day: that npm,
# given only "npm install api-mock-go", resolves the right platform package on
# its own and skips the other five. That needs a registry, so this script runs
# one (Verdaccio) on localhost, publishes every package to it, and then
# installs as a stranger would.
#
# Nothing here touches the public registry, and no npm account is needed.
#
#   ./scripts/test-publish.sh
#
set -euo pipefail

cd "$(dirname "$0")/.."
REPO="$PWD"

WORK="${TMPDIR:-/tmp}/api-mock-go-release-test"

command -v node >/dev/null 2>&1 || { echo "error: node is required" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "error: go is required" >&2; exit 1; }

# Ask the OS for a free port rather than assuming verdaccio's default is
# available. A registry left over from an earlier run would otherwise answer
# the readiness check, and publishing into it fails with EPUBLISHCONFLICT --
# or worse, succeeds against stale packages and reports a false pass.
PORT="${PORT:-$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')}"
REGISTRY="http://localhost:${PORT}"

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAILED=1; }
FAILED=0

# npx spawns verdaccio as a child, so killing the npx process alone leaves the
# registry running and holding its port. Signal the whole process group.
cleanup() {
  if [ -n "${VERDACCIO_PGID:-}" ]; then
    kill -TERM -"$VERDACCIO_PGID" 2>/dev/null || true
  fi
  if [ -n "${VERDACCIO_PID:-}" ]; then
    kill "$VERDACCIO_PID" 2>/dev/null || true
    wait "$VERDACCIO_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

rm -rf "$WORK"
mkdir -p "$WORK/registry/storage" "$WORK/consumer"

# --- the registry -----------------------------------------------------------
# publish: $all allows anonymous publishing, which is what keeps this test from
# needing credentials of any kind.
cat > "$WORK/registry/config.yaml" <<EOF
storage: $WORK/registry/storage
uplinks:
  npmjs:
    url: https://registry.npmjs.org/
    cache: false
packages:
  '**':
    access: \$all
    publish: \$all
    unpublish: \$all
    proxy: npmjs
log: { type: stdout, format: pretty, level: error }
publish:
  allow_offline: true
EOF

echo "Starting a local registry on ${REGISTRY} ..."
# setsid puts verdaccio and everything npx spawns into their own process group,
# so cleanup can take the whole tree down rather than orphaning the registry.
setsid npx --yes verdaccio@6 --config "$WORK/registry/config.yaml" --listen "$PORT" \
  > "$WORK/registry/verdaccio.log" 2>&1 &
VERDACCIO_PID=$!
VERDACCIO_PGID=$(ps -o pgid= -p "$VERDACCIO_PID" 2>/dev/null | tr -d ' ' || true)

for _ in $(seq 1 120); do
  if curl -sf -m 2 "$REGISTRY/-/ping" >/dev/null 2>&1; then break; fi
  sleep 0.5
done
if ! curl -sf -m 2 "$REGISTRY/-/ping" >/dev/null 2>&1; then
  echo "error: the local registry never came up. Log:" >&2
  tail -20 "$WORK/registry/verdaccio.log" >&2
  exit 1
fi
echo "Registry is up."
echo

# A token is required by the npm client even where the registry does not check
# it, so any non-empty value will do.
export NPM_CONFIG_USERCONFIG="$WORK/.npmrc"
cat > "$WORK/.npmrc" <<EOF
registry=${REGISTRY}
//localhost:${PORT}/:_authToken=local-test-token
EOF

# --- publish ----------------------------------------------------------------
echo "Building and publishing every package to the local registry ..."
NPM_REGISTRY="$REGISTRY" PUBLISH=1 bash scripts/npm-pack.sh > "$WORK/publish.log" 2>&1 || {
  echo "error: publishing failed. Log:" >&2
  tail -30 "$WORK/publish.log" >&2
  exit 1
}
grep -E '^\+ ' "$WORK/publish.log" | sed 's/^/  published /' || true
echo

# --- install as a stranger would -------------------------------------------
HOST_OS=$(go env GOOS)
HOST_ARCH=$(go env GOARCH)
SCOPE="$(node -p "Object.keys(require('$REPO/npm/package.json').optionalDependencies)[0].split('/')[0]")"
EXPECTED="${SCOPE}/api-mock-go-${HOST_OS}-${HOST_ARCH}"

echo "Installing with nothing but the package name, on ${HOST_OS}/${HOST_ARCH} ..."
cd "$WORK/consumer"
npm init -y > /dev/null 2>&1
npm install --no-audit --no-fund api-mock-go > "$WORK/install.log" 2>&1 || {
  echo "error: install failed. Log:" >&2
  tail -30 "$WORK/install.log" >&2
  exit 1
}
echo

echo "Checks:"

# 1. The right platform package arrived, without being asked for by name.
if [ -d "node_modules/${EXPECTED}" ]; then
  pass "npm resolved ${EXPECTED} on its own"
else
  fail "expected ${EXPECTED} in node_modules; found: $(ls node_modules/"${SCOPE}" 2>/dev/null | tr '\n' ' ')"
fi

# 2. And only that one. Six binaries would mean 35 MB of dead weight per install.
installed=$(ls "node_modules/${SCOPE}" 2>/dev/null | wc -l)
if [ "$installed" -eq 1 ]; then
  pass "only 1 platform package installed, not all 6"
else
  fail "$installed platform packages installed; os/cpu filtering is not working"
fi

# 3. Install size stays close to one binary.
size=$(du -sm node_modules | cut -f1)
if [ "$size" -le 12 ]; then
  pass "install is ${size} MB"
else
  fail "install is ${size} MB, larger than one binary should be"
fi

# 4. The launcher runs, from the bin shim npm created.
if version=$(./node_modules/.bin/api-mock-go --version 2>&1); then
  pass "npx-style launch works (--version -> ${version})"
else
  fail "launcher did not run: ${version}"
fi

# 5. The binary kept its executable bit through pack, publish and install.
binary=$(find "node_modules/${SCOPE}" -name 'api-mock-go*' -type f | head -1)
if [ -x "$binary" ]; then
  pass "binary is executable after a registry round trip"
else
  fail "binary lost its executable bit: $(ls -l "$binary")"
fi

# 6. It serves real traffic.
cp "$REPO/examples/openapi.yaml" .
./node_modules/.bin/api-mock-go --schema openapi.yaml --port 3911 --quiet \
  > "$WORK/serve.log" 2>&1 &
SERVER=$!
for _ in $(seq 1 100); do
  curl -sf -m 1 -o /dev/null "http://127.0.0.1:3911/v1/posts" 2>/dev/null && break
  sleep 0.1
done
code=$(curl -s -o /dev/null -w '%{http_code}' -m 3 http://127.0.0.1:3911/v1/posts 2>/dev/null || echo 000)
if [ "$code" = "200" ]; then
  pass "serves requests (GET /v1/posts -> 200)"
else
  fail "serving failed (GET /v1/posts -> ${code})"
fi
kill -INT $SERVER 2>/dev/null || true
wait $SERVER 2>/dev/null || true

# 7. No postinstall script means --ignore-scripts changes nothing. This is the
#    property that lets the package install in locked-down CI.
cd "$WORK"
rm -rf strict && mkdir strict && cd strict
npm init -y > /dev/null 2>&1
if npm install --no-audit --no-fund --ignore-scripts api-mock-go > /dev/null 2>&1 \
   && ./node_modules/.bin/api-mock-go --version > /dev/null 2>&1; then
  pass "works under --ignore-scripts"
else
  fail "broke under --ignore-scripts"
fi

# 8. Without optional dependencies there is no binary, and the error should say
#    so in a way the reader can act on.
cd "$WORK"
rm -rf noopt && mkdir noopt && cd noopt
npm init -y > /dev/null 2>&1
npm install --no-audit --no-fund --omit=optional api-mock-go > /dev/null 2>&1
message=$(./node_modules/.bin/api-mock-go --version 2>&1 || true)
if echo "$message" | grep -q 'include=optional'; then
  pass "missing platform package gives an actionable error"
else
  fail "unhelpful error when the platform package is absent: ${message}"
fi

echo
if [ "$FAILED" -eq 0 ]; then
  echo "All checks passed. This build is safe to publish for real:"
  echo "  npm login && VERSION=<x.y.z> PUBLISH=1 bash scripts/npm-pack.sh"
  echo
  echo "With 2FA on the account, npm asks for a one-time password per package,"
  echo "so expect seven prompts."
  exit 0
fi
echo "Some checks failed. Do not publish this build."
exit 1

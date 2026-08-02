#!/usr/bin/env bash
#
# Cross-compile api-mock-go for every platform the npm package ships, into
# dist/. Each binary is stripped of debug info and its build path, so the same
# source produces the same bytes on any machine.
#
#   ./scripts/build.sh              # build all platforms at the current version
#   VERSION=v1.2.3 ./scripts/build.sh
#   ./scripts/build.sh linux/amd64  # build only the platforms named
set -euo pipefail

cd "$(dirname "$0")/.."

# Version comes from the environment, else the current git tag, else "dev".
VERSION="${VERSION:-$(git describe --tags --exact-match 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X github.com/kupelaphiri/api-mock-go/internal/server.Version=${VERSION}"

# The platform list is also the set of optional dependencies in npm/package.json.
ALL_PLATFORMS=(
  darwin/amd64
  darwin/arm64
  linux/amd64
  linux/arm64
  windows/amd64
  windows/arm64
)

PLATFORMS=("$@")
if [ ${#PLATFORMS[@]} -eq 0 ]; then
  PLATFORMS=("${ALL_PLATFORMS[@]}")
fi

rm -rf dist
mkdir -p dist

for platform in "${PLATFORMS[@]}"; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"

  name="api-mock-go-${GOOS}-${GOARCH}"
  output="dist/${name}"
  if [ "$GOOS" = "windows" ]; then
    output="${output}.exe"
  fi

  printf '  %-22s' "${GOOS}/${GOARCH}"

  # CGO is off so the binaries are static and run on any libc, including
  # Alpine. trimpath keeps absolute build paths out of the output.
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/api-mock-go

  size=$(du -h "$output" | cut -f1)
  printf '%s  %s\n' "$size" "$output"
done

echo
echo "Built ${#PLATFORMS[@]} binaries for ${VERSION} in dist/"

# A checksum file lets anyone verify a downloaded release.
if command -v sha256sum >/dev/null 2>&1; then
  (cd dist && sha256sum ./* > SHA256SUMS)
  echo "Wrote dist/SHA256SUMS"
elif command -v shasum >/dev/null 2>&1; then
  (cd dist && shasum -a 256 ./* > SHA256SUMS)
  echo "Wrote dist/SHA256SUMS"
fi

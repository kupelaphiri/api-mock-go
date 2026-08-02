#!/usr/bin/env bash
#
# Assemble the npm packages into dist/npm/.
#
# api-mock-go ships as a launcher package plus one package per platform, each
# holding a single Go binary. npm installs only the platform package matching
# the machine, because each declares its own "os" and "cpu". Nothing is
# downloaded by a postinstall script, so installs work offline and under
# --ignore-scripts.
#
#   ./scripts/npm-pack.sh                 # assemble at the version in npm/package.json
#   VERSION=1.2.3 ./scripts/npm-pack.sh   # assemble at an explicit version
#   PUBLISH=1 ./scripts/npm-pack.sh       # assemble, then npm publish each package
set -euo pipefail

cd "$(dirname "$0")/.."

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "error: $1 is required but not installed" >&2
    exit 1
  }
}
require go
require node

# The version in npm/package.json is the source of truth unless overridden.
VERSION="${VERSION:-$(node -p "require('./npm/package.json').version")}"
VERSION="${VERSION#v}"

# The scope for the platform packages, read from the launcher's own
# optionalDependencies so that npm/package.json stays the single source of
# truth. A personal scope such as @kupela needs no npm organisation: any npm
# account owns the scope matching its username -- so this must stay in step
# with the npm username, which is not necessarily the GitHub one.
SCOPE="$(node -p "Object.keys(require('./npm/package.json').optionalDependencies)[0].split('/')[0]")"

# Name of the platform package for a given GOOS/GOARCH.
platform_pkg() { echo "${SCOPE}/api-mock-go-${1}-${2}"; }

PLATFORMS=(
  darwin/amd64
  darwin/arm64
  linux/amd64
  linux/arm64
  windows/amd64
  windows/arm64
)

# Node's "os" and "cpu" names, which differ from Go's.
node_os() { case "$1" in darwin) echo darwin ;; linux) echo linux ;; windows) echo win32 ;; esac; }
node_cpu() { case "$1" in amd64) echo x64 ;; arm64) echo arm64 ;; esac; }

echo "Assembling api-mock-go ${VERSION}"
echo

# Build first: build.sh clears dist/, so the packages must be assembled after it.
# Invoked through bash rather than as ./scripts/build.sh, so that a lost
# executable bit — which editing over a Windows mount will do — cannot break it.
VERSION="v${VERSION}" bash scripts/build.sh "${PLATFORMS[@]}"
echo

OUT=dist/npm
mkdir -p "$OUT"

# --- Platform packages ------------------------------------------------------
for platform in "${PLATFORMS[@]}"; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"

  pkg_name="$(platform_pkg "$GOOS" "$GOARCH")"
  pkg_dir="$OUT/${pkg_name}"
  mkdir -p "$pkg_dir/bin"

  binary="api-mock-go"
  built="dist/api-mock-go-${GOOS}-${GOARCH}"
  if [ "$GOOS" = "windows" ]; then
    binary="api-mock-go.exe"
    built="${built}.exe"
  fi

  cp "$built" "$pkg_dir/bin/$binary"
  chmod +x "$pkg_dir/bin/$binary"

  # Each platform package is published separately and is MIT-licensed in its
  # own right, so it carries its own copy of the licence rather than relying on
  # the launcher's.
  cp LICENSE "$pkg_dir/LICENSE"

  cat > "$pkg_dir/package.json" <<EOF
{
  "name": "${pkg_name}",
  "version": "${VERSION}",
  "description": "api-mock-go binary for ${GOOS}/${GOARCH}",
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "git+https://github.com/kupelaphiri/api-mock-go.git"
  },
  "os": ["$(node_os "$GOOS")"],
  "cpu": ["$(node_cpu "$GOARCH")"],
  "files": ["bin/"],
  "preferUnplugged": true
}
EOF

  cat > "$pkg_dir/README.md" <<EOF
# ${pkg_name}

The api-mock-go binary for ${GOOS}/${GOARCH}.

You do not need to install this directly. Install
[api-mock-go](https://www.npmjs.com/package/api-mock-go) instead; npm will pull
in the one platform package your machine needs.
EOF

  echo "  $pkg_dir"
done

# --- Launcher package -------------------------------------------------------
launcher="$OUT/api-mock-go"
mkdir -p "$launcher"
cp -r npm/bin "$launcher/bin"
cp npm/package.json "$launcher/package.json"
cp npm/README.md "$launcher/README.md"
cp LICENSE "$launcher/LICENSE"
chmod +x "$launcher/bin/api-mock-go.js"

# Keep the launcher's version and its optionalDependencies in lockstep, so a
# release can never resolve a platform package from a different build.
node - "$launcher/package.json" "$VERSION" <<'EOF'
const fs = require("node:fs");
const [file, version] = process.argv.slice(2);
const pkg = JSON.parse(fs.readFileSync(file, "utf8"));

pkg.version = version;
for (const name of Object.keys(pkg.optionalDependencies ?? {})) {
  pkg.optionalDependencies[name] = version;
}

fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + "\n");
EOF

echo "  $launcher"
echo
echo "Assembled ${#PLATFORMS[@]} platform packages and the launcher in $OUT"

# A local smoke test: the launcher must find and run the binary for this
# machine. Only possible when a matching platform package was just built.
host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
host_name="$(platform_pkg "$host_os" "$host_arch")"
host_pkg="$OUT/${host_name}"
if [ -d "$host_pkg" ]; then
  echo
  echo "Smoke test on ${host_os}/${host_arch}:"
  mkdir -p "$launcher/node_modules/${SCOPE}"
  ln -sfn "$(cd "$host_pkg" && pwd)" "$launcher/node_modules/${host_name}"
  node "$launcher/bin/api-mock-go.js" --version
  rm -rf "$launcher/node_modules"
fi

if [ "${PUBLISH:-}" = "1" ]; then
  echo
  echo "Publishing to ${NPM_REGISTRY:-the npm registry}. Platform packages go"
  echo "first, so the launcher never resolves a version that does not exist yet."
  publish_args=(--access public)
  if [ -n "${NPM_REGISTRY:-}" ]; then
    publish_args+=(--registry "$NPM_REGISTRY")
  fi
  # Provenance signs the tarball against the workflow run that produced it, so
  # anyone can verify these binaries came from this repository at this commit.
  # It only works from a CI with an OIDC identity, so it is opt-in rather than
  # the default: a laptop publish would simply fail.
  if [ "${PROVENANCE:-}" = "1" ]; then
    publish_args+=(--provenance)
  fi
  for platform in "${PLATFORMS[@]}"; do
    (cd "$OUT/$(platform_pkg "${platform%/*}" "${platform#*/}")" \
      && npm publish "${publish_args[@]}")
  done
  (cd "$launcher" && npm publish "${publish_args[@]}")
  echo
  echo "Published api-mock-go ${VERSION}"
else
  echo
  echo "Nothing was published. To publish this build:  PUBLISH=1 ./scripts/npm-pack.sh"
fi

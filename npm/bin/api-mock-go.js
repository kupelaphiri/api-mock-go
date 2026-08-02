#!/usr/bin/env node
"use strict";

// Launcher for the api-mock-go binary.
//
// The binaries ship as per-platform optional dependencies rather than being
// downloaded by a postinstall script. npm installs only the one matching
// package, so there is no network fetch at install time and the launcher works
// under `npm ci --ignore-scripts`.

const { spawn } = require("node:child_process");

// The platform packages are listed in our own optionalDependencies, so the
// scope lives in exactly one place — package.json — and changing it there is
// enough. Nothing here needs to know what the scope is called.
const manifest = require("../package.json");

// Node's platform and architecture names differ from Go's.
const GOOS = { darwin: "darwin", linux: "linux", win32: "windows" };
const GOARCH = { x64: "amd64", arm64: "arm64" };

function target() {
  const os = GOOS[process.platform];
  const arch = GOARCH[process.arch];
  if (!os || !arch) {
    return null;
  }
  const suffix = `-${os}-${arch}`;
  const pkg = Object.keys(manifest.optionalDependencies ?? {}).find((name) =>
    name.endsWith(suffix),
  );
  if (!pkg) {
    return null;
  }
  return { os, arch, pkg };
}

function resolveBinary() {
  const t = target();
  if (!t) {
    fail(
      `api-mock-go has no binary for ${process.platform}/${process.arch}.\n\n` +
        "Supported: macOS, Linux and Windows on x64 or arm64.\n" +
        "You can still build from source with:\n" +
        "  go install github.com/kupelaphiri/api-mock-go/cmd/api-mock-go@latest",
    );
  }

  const binary = t.os === "windows" ? "api-mock-go.exe" : "api-mock-go";
  try {
    return require.resolve(`${t.pkg}/bin/${binary}`);
  } catch (err) {
    fail(
      `api-mock-go could not find its binary for ${t.os}/${t.arch}.\n\n` +
        `The platform package ${t.pkg} is missing. This usually means npm was\n` +
        "run with optional dependencies disabled. Try one of:\n\n" +
        "  npm install --include=optional api-mock-go\n" +
        `  npm install ${t.pkg}\n\n` +
        `Original error: ${err.message}`,
    );
  }
}

function fail(message) {
  process.stderr.write(`\napi-mock-go: ${message}\n\n`);
  process.exit(1);
}

function main() {
  const binary = resolveBinary();

  // stdio is inherited so the mock server's log streams straight through, and
  // so the terminal delivers Ctrl+C to the binary as well as to us.
  const child = spawn(binary, process.argv.slice(2), { stdio: "inherit" });

  // The binary shuts itself down gracefully on a signal, so this process just
  // waits for it rather than exiting first and orphaning the output.
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    process.on(signal, () => {
      if (!child.killed) {
        child.kill(signal);
      }
    });
  }

  child.on("error", (err) => {
    if (err.code === "EACCES") {
      fail(
        `${binary} is not executable.\n\n` +
          "If this package was installed from a tarball or copied between\n" +
          `machines, restore the permission with:  chmod +x ${binary}`,
      );
    }
    fail(`failed to start ${binary}: ${err.message}`);
  });

  child.on("close", (code, signal) => {
    if (signal) {
      // Report the signal the way a shell would, so CI sees a failure.
      process.exit(128 + (require("node:os").constants.signals[signal] || 0));
    }
    process.exit(code === null ? 1 : code);
  });
}

main();

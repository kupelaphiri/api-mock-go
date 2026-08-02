package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kupelaphiri/api-mock-go/internal/config"
	"github.com/kupelaphiri/api-mock-go/internal/server"
)

// exec runs the CLI to completion and returns what it wrote to stdout and
// stderr. It is only safe for arguments that do not start a server.
func exec(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut strings.Builder
	err = run(context.Background(), args, &out, &errOut)
	return out.String(), errOut.String(), err
}

// writeFile puts contents in a temp file and returns its path.
func writeFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// freePort returns a port that was free a moment ago. There is an unavoidable
// race between releasing it and rebinding, which is acceptable in a test.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

const miniSpec = `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /ping:
    get:
      summary: Ping
      responses:
        "200":
          description: ok
          content:
            application/json:
              example: {pong: true}
`

func TestVersion(t *testing.T) {
	stdout, _, err := exec(t, "--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	// The version goes to stdout, so it can be captured in a shell.
	if strings.TrimSpace(stdout) != server.Version {
		t.Errorf("stdout = %q, want %q", stdout, server.Version)
	}
}

func TestHelp(t *testing.T) {
	// --help is a successful exit, not an error: a user asked for it.
	_, stderr, err := exec(t, "--help")
	if err != nil {
		t.Fatalf("--help returned %v, want nil", err)
	}
	for _, want := range []string{"--schema", "--config", "--port", "--cors", "Usage:"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("help output is missing %q", want)
		}
	}
}

func TestRoutesDryRun(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", miniSpec)

	_, stderr, err := exec(t, "--schema", spec, "--routes")
	if err != nil {
		t.Fatalf("--routes: %v", err)
	}
	if !strings.Contains(stderr, "/ping") {
		t.Errorf("route listing is missing /ping:\n%s", stderr)
	}
	if !strings.Contains(stderr, "openapi") {
		t.Errorf("route listing does not name the detected format:\n%s", stderr)
	}
	// A dry run must not bind anything, so the default port stays free.
	if !strings.Contains(stderr, "3000") {
		t.Errorf("route listing does not mention the port it would use:\n%s", stderr)
	}
}

// --schema and --config are interchangeable, and a bare argument works too.
func TestSourceCanBeGivenThreeWays(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", miniSpec)

	for _, args := range [][]string{
		{"--schema", spec, "--routes"},
		{"--config", spec, "--routes"},
		{"--routes", spec},
		// Flags after the file matter most: this is the order people type, and
		// a plain flag.Parse stops at the file and drops everything after it.
		{spec, "--routes"},
	} {
		if _, stderr, err := exec(t, args...); err != nil {
			t.Errorf("%v: %v\n%s", args, err, stderr)
		}
	}
}

// A flag following the source file must still take effect, rather than being
// silently discarded — the failure that would leave a server on the wrong port.
func TestFlagsAfterTheSourceFileApply(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", miniSpec)

	_, stderr, err := exec(t, spec, "--base-path", "/api/v2", "--routes")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "/api/v2") {
		t.Errorf("--base-path after the source file was ignored:\n%s", stderr)
	}
}

func TestCLIErrors(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", miniSpec)
	notASpec := writeFile(t, "random.yaml", "hello: world\n")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no source", nil, "no source file given"},
		{"missing file", []string{"--schema", "/no/such/file.yaml"}, "does not exist"},
		{"not a spec", []string{"--schema", notASpec}, "neither an OpenAPI spec nor a route list"},
		{"unknown flag", []string{"--nope"}, "not defined"},
		{"port too high", []string{"--schema", spec, "--port", "99999"}, "out of range"},
		{"port zero", []string{"--schema", spec, "--port", "0"}, "out of range"},
		{"bad delay", []string{"--schema", spec, "--delay", "soon"}, "invalid delay"},
		{"conflicting sources", []string{"--schema", spec, "--config", "other.yaml"}, "pass only one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := exec(t, tt.args...)
			if err == nil {
				t.Fatalf("%v succeeded, want an error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// The error a user sees should tell them what to do next, not just what broke.
func TestNoSourceErrorSuggestsAFix(t *testing.T) {
	_, _, err := exec(t)
	if err == nil {
		t.Fatal("running with no arguments succeeded, want an error")
	}
	for _, want := range []string{"api-mock-go openapi.yaml", "--help"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not point the user at %q", err, want)
		}
	}
}

func TestBasePathAndDelayFlags(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", miniSpec)

	_, stderr, err := exec(t, "--schema", spec, "--base-path", "/api/v2", "--delay", "150ms", "--routes")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stderr, "/api/v2/ping") {
		t.Errorf("--base-path was not applied:\n%s", stderr)
	}
	if !strings.Contains(stderr, "150") {
		t.Errorf("--delay is not shown in the route listing:\n%s", stderr)
	}
}

// A spec that cannot be fully mocked should say so rather than fail silently.
func TestWarningsAreReported(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /a:
    get: {operationId: noResponses}
`)

	_, stderr, err := exec(t, "--schema", spec, "--routes")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("no warning reported for an operation with no responses:\n%s", stderr)
	}
}

// --- serving --------------------------------------------------------------

// Cancelling the context stops the server, the same way Ctrl+C does. This also
// covers the whole happy path: parse, load, bind, serve, shut down.
func TestServesUntilContextIsCancelled(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", miniSpec)
	port := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"--schema", spec, "--port", strconv.Itoa(port), "--quiet"},
			io.Discard, &stderr)
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/ping", port)
	resp := getWithRetry(t, url)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != `{"pong":true}` {
		t.Errorf("body = %q, want the spec's example", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v after cancellation, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after its context was cancelled")
	}

	// The shutdown summary reports what was served.
	if !strings.Contains(stderr.String(), "stopped after") {
		t.Errorf("no shutdown summary:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "1 request(s) served") {
		t.Errorf("shutdown summary did not count the request:\n%s", stderr.String())
	}
}

// Two servers cannot share a port, and the second must say so rather than
// appear to start.
func TestPortAlreadyInUse(t *testing.T) {
	spec := writeFile(t, "openapi.yaml", miniSpec)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, stderr, err := exec(t, "--schema", spec, "--port", strconv.Itoa(port))
	if err == nil {
		t.Fatal("run succeeded on a taken port, want an error")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error = %q, want it to say the port is in use", err)
	}
	// The banner must not claim success before the bind is known to work.
	if strings.Contains(stderr, "Ctrl+C") {
		t.Errorf("printed a ready banner despite failing to bind:\n%s", stderr)
	}
}

func getWithRetry(t *testing.T, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s never succeeded: %v", url, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// --- flag precedence ------------------------------------------------------

// A route list may carry its own port, host and cors settings, but an explicit
// flag always wins over them.
func TestApplyFileDefaults(t *testing.T) {
	enabled, disabled := true, false

	tests := []struct {
		name     string
		defaults *config.File
		set      map[string]bool
		flags    server.Options
		want     server.Options
	}{
		{
			name:     "file supplies what the flags omit",
			defaults: &config.File{Port: 4321, Host: "0.0.0.0", CORS: &enabled},
			set:      map[string]bool{},
			flags:    server.Options{Port: 3000, Host: "127.0.0.1"},
			want:     server.Options{Port: 4321, Host: "0.0.0.0", CORS: true},
		},
		{
			name:     "explicit flags outrank the file",
			defaults: &config.File{Port: 4321, Host: "0.0.0.0", CORS: &enabled},
			set:      map[string]bool{"port": true, "host": true, "cors": true},
			flags:    server.Options{Port: 8080, Host: "10.0.0.1", CORS: false},
			want:     server.Options{Port: 8080, Host: "10.0.0.1", CORS: false},
		},
		{
			name:     "a file may turn cors off",
			defaults: &config.File{CORS: &disabled},
			set:      map[string]bool{},
			flags:    server.Options{Port: 3000, Host: "127.0.0.1", CORS: false},
			want:     server.Options{Port: 3000, Host: "127.0.0.1", CORS: false},
		},
		{
			name:     "unset file fields change nothing",
			defaults: &config.File{},
			set:      map[string]bool{},
			flags:    server.Options{Port: 3000, Host: "127.0.0.1"},
			want:     server.Options{Port: 3000, Host: "127.0.0.1"},
		},
		{
			name:     "an OpenAPI spec carries no defaults",
			defaults: nil,
			set:      map[string]bool{},
			flags:    server.Options{Port: 3000, Host: "127.0.0.1"},
			want:     server.Options{Port: 3000, Host: "127.0.0.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.flags
			applyFileDefaults(&got, &config.Result{Defaults: tt.defaults}, tt.set)

			if got.Port != tt.want.Port || got.Host != tt.want.Host || got.CORS != tt.want.CORS {
				t.Errorf("got port=%d host=%s cors=%v, want port=%d host=%s cors=%v",
					got.Port, got.Host, got.CORS, tt.want.Port, tt.want.Host, tt.want.CORS)
			}
		})
	}
}

// The same precedence, end to end: a config file's port is used, until a flag
// overrides it.
func TestConfigFilePortPrecedenceEndToEnd(t *testing.T) {
	cfg := writeFile(t, "mocks.yaml", "port: 4321\nroutes:\n  - path: /a\n    response: {ok: true}\n")

	_, stderr, err := exec(t, "--config", cfg, "--routes")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stderr, "4321") {
		t.Errorf("the config file's port was not used:\n%s", stderr)
	}

	_, stderr, err = exec(t, "--config", cfg, "--port", "8080", "--routes")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stderr, "8080") || strings.Contains(stderr, "4321") {
		t.Errorf("--port did not override the config file:\n%s", stderr)
	}
}

// --- units ----------------------------------------------------------------

func TestResolveSource(t *testing.T) {
	existing := writeFile(t, "spec.yaml", miniSpec)

	// resolveSource takes the positional arguments run collected while parsing.
	parse := func(args ...string) []string { return args }

	t.Run("schema", func(t *testing.T) {
		got, err := resolveSource(parse(), existing, "")
		if err != nil || got != existing {
			t.Errorf("got %q, %v; want %q", got, err, existing)
		}
	})

	t.Run("config", func(t *testing.T) {
		got, err := resolveSource(parse(), "", existing)
		if err != nil || got != existing {
			t.Errorf("got %q, %v; want %q", got, err, existing)
		}
	})

	t.Run("positional", func(t *testing.T) {
		got, err := resolveSource(parse(existing), "", "")
		if err != nil || got != existing {
			t.Errorf("got %q, %v; want %q", got, err, existing)
		}
	})

	// Passing the same file to both flags is harmless, not a conflict.
	t.Run("same file twice", func(t *testing.T) {
		got, err := resolveSource(parse(), existing, existing)
		if err != nil || got != existing {
			t.Errorf("got %q, %v; want %q", got, err, existing)
		}
	})

	t.Run("different files conflict", func(t *testing.T) {
		if _, err := resolveSource(parse(), existing, "other.yaml"); err == nil {
			t.Error("two different sources were accepted, want an error")
		}
	})

	t.Run("nothing given", func(t *testing.T) {
		if _, err := resolveSource(parse(), "", ""); err == nil {
			t.Error("no source was accepted, want an error")
		}
	})

	t.Run("nonexistent", func(t *testing.T) {
		if _, err := resolveSource(parse(), "/no/such/file.yaml", ""); err == nil {
			t.Error("a missing file was accepted, want an error")
		}
	})

	// Naming the same file twice is harmless; naming two different ones is the
	// same mistake as --schema and --config disagreeing.
	t.Run("positional matching a flag", func(t *testing.T) {
		got, err := resolveSource(parse(existing), existing, "")
		if err != nil || got != existing {
			t.Errorf("got %q, %v; want %q", got, err, existing)
		}
	})

	t.Run("positional conflicting with a flag", func(t *testing.T) {
		if _, err := resolveSource(parse(existing), "other.yaml", ""); err == nil {
			t.Error("a positional disagreeing with --schema was accepted, want an error")
		}
	})

	t.Run("two positionals", func(t *testing.T) {
		if _, err := resolveSource(parse(existing, existing), "", ""); err == nil {
			t.Error("two source files were accepted, want an error")
		}
	})
}

func TestValidatePort(t *testing.T) {
	for _, port := range []int{1, 80, 3000, 8080, 65535} {
		if err := validatePort(port); err != nil {
			t.Errorf("validatePort(%d) = %v, want nil", port, err)
		}
	}
	for _, port := range []int{0, -1, 65536, 99999} {
		if err := validatePort(port); err == nil {
			t.Errorf("validatePort(%d) succeeded, want an error", port)
		}
	}
}

func TestReportWarnings(t *testing.T) {
	var out strings.Builder

	reportWarnings(&out, nil)
	if out.Len() != 0 {
		t.Errorf("wrote %q for no warnings, want nothing", out.String())
	}

	reportWarnings(&out, []string{"first problem", "second problem"})
	got := out.String()
	for _, want := range []string{"warning: first problem", "warning: second problem"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

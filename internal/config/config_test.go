package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// write puts contents in a temp file named name and returns its path.
func write(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// find returns the route for method and path, failing the test if absent.
func find(t *testing.T, routes []Route, method, path string) Route {
	t.Helper()
	for _, r := range routes {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	var paths []string
	for _, r := range routes {
		paths = append(paths, r.Method+" "+r.Path)
	}
	t.Fatalf("no route for %s %s; have %v", method, path, paths)
	return Route{}
}

// body decodes a route's body as JSON.
func body(t *testing.T, route Route) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(route.Body, &out); err != nil {
		t.Fatalf("decode body %q: %v", route.Body, err)
	}
	return out
}

func TestLoadRouteList(t *testing.T) {
	path := write(t, "mocks.yaml", `
port: 4321
host: 0.0.0.0
cors: true
routes:
  - path: /api/users
    method: GET
    response:
      id: 1
      name: Jane
  - path: /api/users
    method: [POST, PUT]
    status: 201
    response: {ok: true}
  - path: /api/users/:id
    method: DELETE
    status: 204
  - path: /api/slow
    delay: 250ms
    response: {slow: true}
  - path: /api/health
    body: ok
    headers:
      Content-Type: text/plain
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Format != "routes" {
		t.Errorf("Format = %q, want routes", result.Format)
	}
	if len(result.Routes) != 6 {
		t.Errorf("got %d routes, want 6", len(result.Routes))
	}

	if result.Defaults.Port != 4321 || result.Defaults.Host != "0.0.0.0" {
		t.Errorf("defaults = %+v, want port 4321 host 0.0.0.0", result.Defaults)
	}
	if result.Defaults.CORS == nil || !*result.Defaults.CORS {
		t.Error("cors default did not survive loading")
	}

	get := find(t, result.Routes, "GET", "/api/users")
	if got := body(t, get)["name"]; got != "Jane" {
		t.Errorf("name = %v, want Jane", got)
	}
	if got := get.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	// A list of methods expands to one route each.
	for _, method := range []string{"POST", "PUT"} {
		r := find(t, result.Routes, method, "/api/users")
		if r.Status != 201 {
			t.Errorf("%s status = %d, want 201", method, r.Status)
		}
	}

	// method defaults to GET.
	slow := find(t, result.Routes, "GET", "/api/slow")
	if slow.Delay != 250*time.Millisecond {
		t.Errorf("delay = %v, want 250ms", slow.Delay)
	}

	// An explicit Content-Type is not overwritten.
	health := find(t, result.Routes, "GET", "/api/health")
	if string(health.Body) != "ok" {
		t.Errorf("body = %q, want ok", health.Body)
	}
	if got := health.Headers["Content-Type"]; got != "text/plain" {
		t.Errorf("Content-Type = %q, want the explicit text/plain", got)
	}

	del := find(t, result.Routes, "DELETE", "/api/users/:id")
	if len(del.Body) != 0 {
		t.Errorf("body = %q, want empty", del.Body)
	}
}

func TestLoadRouteListJSON(t *testing.T) {
	// Indented with tabs, which is valid JSON but not valid YAML: the loader
	// must dispatch on the extension rather than parsing everything as YAML.
	path := write(t, "mocks.json", "{\n\t\"routes\": [\n\t\t{\n\t\t\t\"path\": \"/ping\",\n\t\t\t\"method\": \"GET\",\n\t\t\t\"response\": {\"pong\": true}\n\t\t}\n\t]\n}")

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	route := find(t, result.Routes, "GET", "/ping")
	if got := body(t, route)["pong"]; got != true {
		t.Errorf("pong = %v, want true", got)
	}
}

func TestLoadRouteListFileBody(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mocks.yaml")
	if err := os.WriteFile(path, []byte("routes:\n  - path: /data\n    file: ./data.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	route := find(t, result.Routes, "GET", "/data")
	if got := body(t, route)["from"]; got != "file" {
		t.Errorf("from = %v, want file", got)
	}
	if got := route.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestOptionsApplyToRouteList(t *testing.T) {
	path := write(t, "mocks.yaml", "routes:\n  - path: /users\n    response: {ok: true}\n")

	result, err := Load(path, Options{BasePath: "/api/v2", DefaultDelay: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	route := find(t, result.Routes, "GET", "/api/v2/users")
	if route.Delay != 50*time.Millisecond {
		t.Errorf("delay = %v, want the default 50ms", route.Delay)
	}
}

// A --delay flag outranks the file's own default; a route's own delay outranks
// both.
func TestDelayPrecedence(t *testing.T) {
	path := write(t, "mocks.yaml", `
delay: 1s
routes:
  - path: /a
    response: {}
  - path: /b
    delay: 10ms
    response: {}
`)

	fromFile, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := find(t, fromFile.Routes, "GET", "/a").Delay; got != time.Second {
		t.Errorf("/a delay = %v, want the file default 1s", got)
	}
	if got := find(t, fromFile.Routes, "GET", "/b").Delay; got != 10*time.Millisecond {
		t.Errorf("/b delay = %v, want its own 10ms", got)
	}

	withFlag, err := Load(path, Options{DefaultDelay: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := find(t, withFlag.Routes, "GET", "/a").Delay; got != 5*time.Millisecond {
		t.Errorf("/a delay = %v, want the flag's 5ms", got)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := map[string]string{
		"empty file":         "",
		"unknown format":     "hello: world\n",
		"no routes":          "routes: []\n",
		"route without path": "routes:\n  - method: GET\n",
		"bad delay":          "routes:\n  - path: /a\n    delay: soon\n",
		"missing body file":  "routes:\n  - path: /a\n    file: ./absent.json\n",
		"malformed yaml":     "routes:\n  - path: [\n",
	}
	for name, contents := range tests {
		path := write(t, "mocks.yaml", contents)
		if _, err := Load(path, Options{}); err == nil {
			t.Errorf("%s: Load succeeded, want an error", name)
		}
	}

	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), Options{}); err == nil {
		t.Error("missing file: Load succeeded, want an error")
	}
}

func TestParseDelay(t *testing.T) {
	ok := map[string]time.Duration{
		"":      0,
		"0":     0,
		"250ms": 250 * time.Millisecond,
		"1.5s":  1500 * time.Millisecond,
		"2s":    2 * time.Second,
		"100":   100 * time.Millisecond,
		" 50 ":  50 * time.Millisecond,
	}
	for in, want := range ok {
		got, err := ParseDelay(in)
		if err != nil {
			t.Errorf("ParseDelay(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDelay(%q) = %v, want %v", in, got, want)
		}
	}

	for _, in := range []string{"soon", "-1", "-5ms", "abc"} {
		if _, err := ParseDelay(in); err == nil {
			t.Errorf("ParseDelay(%q) succeeded, want an error", in)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	tests := map[string]string{
		"/api/users":            "/api/users",
		"api/users":             "/api/users",
		"/api/users/":           "/api/users",
		"//api//users//":        "/api/users",
		"/api/users/{id}":       "/api/users/:id",
		"/a/{x}/b/{y}":          "/a/:x/b/:y",
		"/{id}":                 "/:id",
		"":                      "/",
		"/":                     "/",
		"/pets/{petId}/photos/": "/pets/:petId/photos",
	}
	for in, want := range tests {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

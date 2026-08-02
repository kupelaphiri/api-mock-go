package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kupelaphiri/api-mock-go/internal/config"
)

func testServer(t *testing.T, opts Options, routes ...config.Route) *Server {
	t.Helper()
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	opts.Quiet = true
	return New(routes, opts)
}

func route(method, path string, status int, body string) config.Route {
	return config.Route{
		Method:  method,
		Path:    path,
		Status:  status,
		Body:    []byte(body),
		Headers: map[string]string{"Content-Type": "application/json"},
	}
}

func do(s *Server, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestServesMatchedRoute(t *testing.T) {
	s := testServer(t, Options{},
		route("GET", "/api/users", 200, `{"id":1}`),
		route("POST", "/api/users", 201, `{"created":true}`),
	)

	rec := do(s, "GET", "/api/users")
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != `{"id":1}` {
		t.Errorf("body = %q, want the route body", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(`{"id":1}`)) {
		t.Errorf("Content-Length = %q, want %d", got, len(`{"id":1}`))
	}
	if got := rec.Header().Get("X-Mock-Server"); got != "api-mock-go" {
		t.Errorf("X-Mock-Server = %q, want api-mock-go", got)
	}

	if rec := do(s, "POST", "/api/users"); rec.Code != 201 {
		t.Errorf("POST status = %d, want 201", rec.Code)
	}
}

func TestUnmatchedPathReturnsJSON404(t *testing.T) {
	s := testServer(t, Options{}, route("GET", "/api/users", 200, `{}`))

	rec := do(s, "GET", "/nope")
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var problem map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if problem["status"] != float64(404) {
		t.Errorf("status field = %v, want 404", problem["status"])
	}
	// The 404 should point the caller at the introspection endpoint.
	if detail, _ := problem["detail"].(string); !strings.Contains(detail, routesEndpoint) {
		t.Errorf("detail = %q, want it to mention %s", detail, routesEndpoint)
	}
}

func TestMethodMismatchReturns405WithAllow(t *testing.T) {
	s := testServer(t, Options{},
		route("GET", "/api/users", 200, `{}`),
		route("POST", "/api/users", 201, `{}`),
	)

	rec := do(s, "DELETE", "/api/users")
	if rec.Code != 405 {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	for _, want := range []string{"GET", "POST", "HEAD"} {
		if !strings.Contains(allow, want) {
			t.Errorf("Allow = %q, want it to list %s", allow, want)
		}
	}
}

func TestHeadReturnsHeadersWithoutBody(t *testing.T) {
	s := testServer(t, Options{}, route("GET", "/api/users", 200, `{"id":1}`))

	rec := do(s, "HEAD", "/api/users")
	// HEAD is not registered, so the router reports it as allowed via GET; the
	// 405 path is what a client sees, and it too must carry no body.
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}

	// A route explicitly registered for HEAD serves headers only.
	s = testServer(t, Options{}, route("HEAD", "/api/users", 200, `{"id":1}`))
	rec = do(s, "HEAD", "/api/users")
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != "8" {
		t.Errorf("Content-Length = %q, want 8, the length the body would have", got)
	}
}

func TestPathParamsMatch(t *testing.T) {
	s := testServer(t, Options{},
		route("GET", "/api/users/:id", 200, `{"id":"any"}`),
		route("GET", "/api/users/me", 200, `{"id":"me"}`),
	)

	if rec := do(s, "GET", "/api/users/42"); rec.Body.String() != `{"id":"any"}` {
		t.Errorf("/api/users/42 body = %q, want the param route", rec.Body.String())
	}
	if rec := do(s, "GET", "/api/users/me"); rec.Body.String() != `{"id":"me"}` {
		t.Errorf("/api/users/me body = %q, want the static route", rec.Body.String())
	}
}

func TestQueryStringIsIgnoredForMatching(t *testing.T) {
	s := testServer(t, Options{}, route("GET", "/api/users", 200, `{}`))

	if rec := do(s, "GET", "/api/users?page=2&limit=10"); rec.Code != 200 {
		t.Errorf("status = %d, want 200; a query string must not affect matching", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	s := testServer(t, Options{CORS: true}, route("GET", "/api/users", 200, `{}`))

	rec := do(s, "GET", "/api/users")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q, want *", got)
	}

	// A preflight succeeds for a mocked path even though OPTIONS is not mocked.
	rec = do(s, "OPTIONS", "/api/users")
	if rec.Code != 204 {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("Allow-Methods = %q, want it to list GET", got)
	}
}

func TestCORSDisabledByDefault(t *testing.T) {
	s := testServer(t, Options{}, route("GET", "/api/users", 200, `{}`))

	rec := do(s, "GET", "/api/users")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want none without --cors", got)
	}
	// Without CORS, an unmocked OPTIONS is a plain 405.
	if rec := do(s, "OPTIONS", "/api/users"); rec.Code != 405 {
		t.Errorf("OPTIONS status = %d, want 405", rec.Code)
	}
}

func TestDelayIsApplied(t *testing.T) {
	r := route("GET", "/slow", 200, `{}`)
	r.Delay = 60 * time.Millisecond
	s := testServer(t, Options{}, r)

	start := time.Now()
	rec := do(s, "GET", "/slow")
	elapsed := time.Since(start)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("responded in %v, want at least the 60ms delay", elapsed)
	}
}

// A client that disconnects mid-delay must not be written to.
func TestDelayRespectsCancelledRequest(t *testing.T) {
	r := route("GET", "/slow", 200, `{}`)
	r.Delay = 5 * time.Second
	s := testServer(t, Options{}, r)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/slow", nil).WithContext(ctx)
	cancel()

	done := make(chan struct{})
	go func() {
		s.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler kept sleeping after the request was cancelled")
	}
}

func TestRoutesEndpoint(t *testing.T) {
	s := testServer(t, Options{},
		route("GET", "/api/users", 200, `{}`),
		route("POST", "/api/users", 201, `{}`),
	)

	rec := do(s, "GET", routesEndpoint)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var listing struct {
		Count  int `json:"count"`
		Routes []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Status int    `json:"status"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if listing.Count != 2 || len(listing.Routes) != 2 {
		t.Fatalf("count = %d with %d routes, want 2 and 2", listing.Count, len(listing.Routes))
	}
}

// A mocked route at the introspection path must take precedence, so the
// endpoint can never shadow a real mock.
func TestMockedRouteShadowsRoutesEndpoint(t *testing.T) {
	s := testServer(t, Options{}, route("GET", routesEndpoint, 200, `{"mine":true}`))

	rec := do(s, "GET", routesEndpoint)
	if rec.Body.String() != `{"mine":true}` {
		t.Errorf("body = %q, want the mocked route to win", rec.Body.String())
	}
}

func TestEmptyBodyRouteSendsNoContentType(t *testing.T) {
	s := testServer(t, Options{}, config.Route{Method: "DELETE", Path: "/api/users/:id", Status: 204})

	rec := do(s, "DELETE", "/api/users/42")
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want none for an empty body", got)
	}
}

// A route with a body but no declared content type falls back to JSON, since
// that is what a mock API almost always serves.
func TestDefaultContentType(t *testing.T) {
	s := testServer(t, Options{}, config.Route{
		Method: "GET", Path: "/a", Status: 200, Body: []byte(`{"a":1}`),
	})

	rec := do(s, "GET", "/a")
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// Serve must bind, answer requests, and shut down when its context is
// cancelled.
func TestServeAndShutdown(t *testing.T) {
	s := testServer(t, Options{Host: "127.0.0.1", Port: 0}, route("GET", "/ping", 200, `{"pong":true}`))

	ln, err := s.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan uint64, 1)
	go func() {
		n, err := s.Serve(ctx, ln)
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
		served <- n
	}()

	url := "http://" + ln.Addr().String() + "/ping"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != `{"pong":true}` {
		t.Errorf("body = %q, want the route body", body)
	}

	cancel()
	select {
	case n := <-served:
		if n != 1 {
			t.Errorf("served %d requests, want 1", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}

	// The port must be free again once Serve returns.
	if _, err := http.Get(url); err == nil {
		t.Error("server still answering after shutdown")
	}
}

// Two servers cannot share a port, and the error must name the conflict.
func TestListenReportsPortInUse(t *testing.T) {
	first := testServer(t, Options{Host: "127.0.0.1", Port: 0})
	ln, err := first.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(interface{ String() string }).String()
	_, portStr, _ := strings.Cut(port, ":")
	n, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port from %q: %v", port, err)
	}

	second := testServer(t, Options{Host: "127.0.0.1", Port: n})
	if _, err := second.Listen(); err == nil {
		t.Fatal("second Listen succeeded, want a port conflict")
	} else if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error = %v, want it to say the port is already in use", err)
	}
}

func TestLogWritesOneLinePerRequest(t *testing.T) {
	var out strings.Builder
	s := New([]config.Route{route("GET", "/api/users", 200, `{"id":1}`)},
		Options{Out: &out})

	do(s, "GET", "/api/users?page=2")
	do(s, "GET", "/missing")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "200") || !strings.Contains(lines[0], "/api/users?page=2") {
		t.Errorf("line 1 = %q, want the status and the full path with query", lines[0])
	}
	if !strings.Contains(lines[1], "404") {
		t.Errorf("line 2 = %q, want a 404", lines[1])
	}
}

func TestQuietSuppressesLogging(t *testing.T) {
	var out strings.Builder
	s := New([]config.Route{route("GET", "/a", 200, `{}`)}, Options{Out: &out, Quiet: true})

	do(s, "GET", "/a")
	if out.Len() != 0 {
		t.Errorf("wrote %q, want nothing with --quiet", out.String())
	}
}

func TestBannerListsRoutes(t *testing.T) {
	var out strings.Builder
	s := New([]config.Route{
		route("GET", "/api/users", 200, `{}`),
		route("POST", "/api/users", 201, `{}`),
	}, Options{Out: &out, Host: "127.0.0.1", Port: 3000, Quiet: true})

	s.Banner("mocks.yaml", "routes", true)
	got := out.String()

	for _, want := range []string{"api-mock-go", "mocks.yaml", "routes", "127.0.0.1:3000", "/api/users", "GET", "POST", routesEndpoint, "Ctrl+C"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner is missing %q:\n%s", want, got)
		}
	}

	// A --routes dry run must not tell the user to press Ctrl+C.
	out.Reset()
	s.Banner("mocks.yaml", "routes", false)
	if strings.Contains(out.String(), "Ctrl+C") {
		t.Errorf("dry-run banner mentions Ctrl+C:\n%s", out.String())
	}
}

// discardWriter is a ResponseWriter that keeps no state, so a benchmark
// measures the handler rather than httptest.ResponseRecorder's bookkeeping.
type discardWriter struct{ header http.Header }

func (d *discardWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header, 8)
	}
	return d.header
}
func (d *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardWriter) WriteHeader(int)             {}

// reset clears the headers a previous iteration set, as a fresh writer would.
func (d *discardWriter) reset() {
	for k := range d.header {
		delete(d.header, k)
	}
}

func BenchmarkServeStaticRoute(b *testing.B) {
	s := New([]config.Route{route("GET", "/api/users", 200, `{"id":1,"name":"Jane Doe"}`)},
		Options{Quiet: true, Out: io.Discard})

	req := httptest.NewRequest("GET", "/api/users", nil)
	w := &discardWriter{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.reset()
		s.ServeHTTP(w, req)
	}
}

func BenchmarkServeParamRoute(b *testing.B) {
	s := New([]config.Route{route("GET", "/api/users/:id/posts/:postId", 200, `{"id":1}`)},
		Options{Quiet: true, Out: io.Discard})

	req := httptest.NewRequest("GET", "/api/users/42/posts/7", nil)
	w := &discardWriter{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.reset()
		s.ServeHTTP(w, req)
	}
}

// The request log costs something; this shows how much.
func BenchmarkServeWithLogging(b *testing.B) {
	s := New([]config.Route{route("GET", "/api/users", 200, `{"id":1,"name":"Jane Doe"}`)},
		Options{Out: io.Discard})

	req := httptest.NewRequest("GET", "/api/users", nil)
	w := &discardWriter{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.reset()
		s.ServeHTTP(w, req)
	}
}

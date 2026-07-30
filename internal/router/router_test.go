package router

import (
	"testing"

	"github.com/kupelaphiri/api-mock-go/internal/config"
)

func routes(specs ...[2]string) []config.Route {
	out := make([]config.Route, 0, len(specs))
	for _, s := range specs {
		out = append(out, config.Route{Method: s[0], Path: s[1], Status: 200})
	}
	return out
}

func TestLookupMatchesStaticAndParams(t *testing.T) {
	r := New(routes(
		[2]string{"GET", "/api/users"},
		[2]string{"POST", "/api/users"},
		[2]string{"GET", "/api/users/:id"},
		[2]string{"GET", "/api/users/:id/posts/:postId"},
	))

	tests := []struct {
		method, path string
		wantPath     string
		wantParams   map[string]string
	}{
		{"GET", "/api/users", "/api/users", nil},
		{"POST", "/api/users", "/api/users", nil},
		{"GET", "/api/users/42", "/api/users/:id", map[string]string{"id": "42"}},
		{"get", "/api/users/42", "/api/users/:id", map[string]string{"id": "42"}},
		// Leading and trailing slashes are normalised away.
		{"GET", "/api/users/42/", "/api/users/:id", map[string]string{"id": "42"}},
		{"GET", "/api/users/42/posts/7", "/api/users/:id/posts/:postId",
			map[string]string{"id": "42", "postId": "7"}},
	}

	for _, tt := range tests {
		route, params, allow := r.Lookup(tt.method, tt.path, nil)
		if route == nil {
			t.Errorf("%s %s: no match (allow=%v)", tt.method, tt.path, allow)
			continue
		}
		if route.Path != tt.wantPath {
			t.Errorf("%s %s: matched %s, want %s", tt.method, tt.path, route.Path, tt.wantPath)
		}
		for k, want := range tt.wantParams {
			if got := params.Get(k); got != want {
				t.Errorf("%s %s: param %s = %q, want %q", tt.method, tt.path, k, got, want)
			}
		}
	}
}

// A static segment must win over a parameter regardless of registration order,
// so /users/me does not get swallowed by /users/{id}.
func TestStaticOutranksParam(t *testing.T) {
	for _, order := range [][]config.Route{
		routes([2]string{"GET", "/users/:id"}, [2]string{"GET", "/users/me"}),
		routes([2]string{"GET", "/users/me"}, [2]string{"GET", "/users/:id"}),
	} {
		r := New(order)

		route, _, _ := r.Lookup("GET", "/users/me", nil)
		if route == nil || route.Path != "/users/me" {
			t.Fatalf("/users/me matched %v, want the static route", route)
		}

		route, params, _ := r.Lookup("GET", "/users/123", nil)
		if route == nil || route.Path != "/users/:id" {
			t.Fatalf("/users/123 matched %v, want the param route", route)
		}
		if got := params.Get("id"); got != "123" {
			t.Fatalf("id = %q, want 123", got)
		}
	}
}

// A static prefix that dead-ends must not shadow a viable parameter match.
func TestBacktracksFromDeadEndStatic(t *testing.T) {
	r := New(routes(
		[2]string{"GET", "/a/b/c"},
		[2]string{"GET", "/a/:x/d"},
	))

	route, params, _ := r.Lookup("GET", "/a/b/d", nil)
	if route == nil || route.Path != "/a/:x/d" {
		t.Fatalf("/a/b/d matched %v, want /a/:x/d", route)
	}
	if got := params.Get("x"); got != "b" {
		t.Fatalf("x = %q, want b", got)
	}
}

func TestWildcard(t *testing.T) {
	r := New(routes(
		[2]string{"GET", "/files/*"},
		[2]string{"GET", "/files/readme.txt"},
	))

	route, params, _ := r.Lookup("GET", "/files/deep/nested/path.txt", nil)
	if route == nil || route.Path != "/files/*" {
		t.Fatalf("nested path matched %v, want the wildcard", route)
	}
	if got := params.Get("wildcard"); got != "deep/nested/path.txt" {
		t.Fatalf("wildcard = %q, want the remaining path", got)
	}

	// An exact route below the wildcard still wins.
	route, _, _ = r.Lookup("GET", "/files/readme.txt", nil)
	if route == nil || route.Path != "/files/readme.txt" {
		t.Fatalf("exact path matched %v, want the static route", route)
	}
}

func TestMethodMismatchReportsAllow(t *testing.T) {
	r := New(routes(
		[2]string{"GET", "/api/users"},
		[2]string{"POST", "/api/users"},
	))

	route, _, allow := r.Lookup("DELETE", "/api/users", nil)
	if route != nil {
		t.Fatalf("DELETE matched %v, want no match", route)
	}
	// HEAD is implied by GET.
	want := map[string]bool{"GET": true, "HEAD": true, "POST": true}
	if len(allow) != len(want) {
		t.Fatalf("allow = %v, want %v", allow, want)
	}
	for _, m := range allow {
		if !want[m] {
			t.Errorf("allow contains unexpected %s", m)
		}
	}
}

func TestUnknownPathHasNoAllowList(t *testing.T) {
	r := New(routes([2]string{"GET", "/api/users"}))

	route, _, allow := r.Lookup("GET", "/nope", nil)
	if route != nil || len(allow) != 0 {
		t.Fatalf("got route=%v allow=%v, want neither", route, allow)
	}
}

func TestRootPath(t *testing.T) {
	r := New(routes([2]string{"GET", "/"}))

	for _, path := range []string{"/", ""} {
		if route, _, _ := r.Lookup("GET", path, nil); route == nil {
			t.Errorf("%q did not match the root route", path)
		}
	}
}

// A later route with the same method and path replaces the earlier one, so
// callers can layer overrides without the listing growing stale entries.
func TestDuplicateRouteReplaces(t *testing.T) {
	first := config.Route{Method: "GET", Path: "/dup", Status: 200}
	second := config.Route{Method: "GET", Path: "/dup", Status: 418}
	r := New([]config.Route{first, second})

	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1; routes = %v", r.Len(), r.Routes())
	}
	route, _, _ := r.Lookup("GET", "/dup", nil)
	if route.Status != 418 {
		t.Fatalf("status = %d, want the later route's 418", route.Status)
	}
}

func TestSplitPath(t *testing.T) {
	tests := map[string][]string{
		"":            nil,
		"/":           nil,
		"//":          nil,
		"/a":          {"a"},
		"/a/b":        {"a", "b"},
		"a/b":         {"a", "b"},
		"/a/b/":       {"a", "b"},
		"/a//b/":      {"a", "b"},
		"/a/b/c/d/e/": {"a", "b", "c", "d", "e"},
	}
	for path, want := range tests {
		got := splitPath(path)
		if len(got) != len(want) {
			t.Errorf("splitPath(%q) = %v, want %v", path, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("splitPath(%q) = %v, want %v", path, got, want)
				break
			}
		}
	}
}

// Lookup must not allocate when handed a params buffer with spare capacity,
// which is what keeps the request path allocation-free.
func TestLookupDoesNotAllocate(t *testing.T) {
	r := New(routes([2]string{"GET", "/api/users/:id/posts/:postId"}))
	buf := make(Params, 0, 8)

	allocs := testing.AllocsPerRun(100, func() {
		route, params, _ := r.Lookup("GET", "/api/users/42/posts/7", buf[:0])
		if route == nil || len(params) != 2 {
			t.Fatal("lookup failed inside the allocation benchmark")
		}
	})
	if allocs > 0 {
		t.Errorf("Lookup made %.0f allocation(s) per call, want 0", allocs)
	}
}

func BenchmarkLookupStatic(b *testing.B) {
	r := New(routes([2]string{"GET", "/api/v1/users"}))
	buf := make(Params, 0, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Lookup("GET", "/api/v1/users", buf[:0])
	}
}

func BenchmarkLookupParams(b *testing.B) {
	r := New(routes([2]string{"GET", "/api/v1/users/:id/posts/:postId"}))
	buf := make(Params, 0, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Lookup("GET", "/api/v1/users/42/posts/7", buf[:0])
	}
}

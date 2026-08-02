// Package router implements a radix-style tree router for mock routes.
//
// Matching precedence per path segment is static > parameter > wildcard, so
// /users/me wins over /users/{id} regardless of registration order. Lookups
// allocate nothing beyond the caller-supplied params buffer.
package router

import (
	"net/http"
	"sort"
	"strings"

	"github.com/kupelaphiri/api-mock-go/internal/config"
)

// Param is a single captured path parameter.
type Param struct {
	Key   string
	Value string
}

// Params is a captured parameter set. The zero value is ready to use.
type Params []Param

// Get returns the value captured for key, or "" if it was not captured.
func (p Params) Get(key string) string {
	for i := range p {
		if p[i].Key == key {
			return p[i].Value
		}
	}
	return ""
}

type node struct {
	// static children keyed by exact segment text.
	static map[string]*node

	// param is the ":name" child, if any. A node has at most one, since two
	// differently-named parameters in the same position are indistinguishable.
	param     *node
	paramName string

	// wildcard is the "*name" catch-all child, which consumes the remainder
	// of the path.
	wildcard     *node
	wildcardName string

	// routes holds the terminal routes at this node, keyed by HTTP method.
	routes map[string]*config.Route
}

func (n *node) child(segment string) *node {
	switch {
	case strings.HasPrefix(segment, ":") || (strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")):
		name := strings.Trim(segment, ":{}")
		if n.param == nil {
			n.param = &node{}
			n.paramName = name
		}
		return n.param
	case segment == "*" || strings.HasPrefix(segment, "*"):
		name := strings.TrimPrefix(segment, "*")
		if name == "" {
			name = "wildcard"
		}
		if n.wildcard == nil {
			n.wildcard = &node{}
			n.wildcardName = name
		}
		return n.wildcard
	default:
		if n.static == nil {
			n.static = make(map[string]*node, 4)
		}
		c, ok := n.static[segment]
		if !ok {
			c = &node{}
			n.static[segment] = c
		}
		return c
	}
}

// Router matches an HTTP method and path to a mock route.
type Router struct {
	root   *node
	routes []*config.Route
}

// New builds a Router from routes. A later route silently replaces an earlier
// one with the same method and path, so callers can layer overrides.
func New(routes []config.Route) *Router {
	r := &Router{root: &node{}}
	for i := range routes {
		r.add(&routes[i])
	}
	return r
}

func (r *Router) add(route *config.Route) {
	n := r.root
	for _, segment := range splitPath(route.Path) {
		n = n.child(segment)
	}
	if n.routes == nil {
		n.routes = make(map[string]*config.Route, 2)
	}
	method := strings.ToUpper(route.Method)
	if _, replaced := n.routes[method]; replaced {
		for i, existing := range r.routes {
			if strings.EqualFold(existing.Method, method) && existing.Path == route.Path {
				r.routes = append(r.routes[:i], r.routes[i+1:]...)
				break
			}
		}
	}
	n.routes[method] = route
	r.routes = append(r.routes, route)
}

// Routes returns every registered route, sorted by path then method. The
// slice is freshly allocated; mutating it does not affect the Router.
func (r *Router) Routes() []*config.Route {
	out := make([]*config.Route, len(r.routes))
	copy(out, r.routes)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// Len reports how many routes are registered.
func (r *Router) Len() int { return len(r.routes) }

// Lookup resolves method and path to a route, appending any captured
// parameters to params and returning the extended slice.
//
// When the path matches but the method does not, Lookup returns a nil route
// and a non-empty allow list, which the caller should turn into a 405.
func (r *Router) Lookup(method, path string, params Params) (*config.Route, Params, []string) {
	// Trimming yields a substring rather than a copy, so matching stays free of
	// allocations: the tree walk slices this string instead of splitting it.
	n, params := r.match(r.root, strings.Trim(path, "/"), params)
	if n == nil {
		return nil, params, nil
	}
	if route, ok := n.routes[strings.ToUpper(method)]; ok {
		return route, params, nil
	}
	return nil, params, allowed(n)
}

// match walks the tree over path, which holds the not-yet-matched segments
// with no leading or trailing slash. It prefers static children, then the
// parameter child, then the wildcard, and backtracks so that a static prefix
// which dead-ends does not shadow a viable parameter match.
func (r *Router) match(n *node, path string, params Params) (*node, Params) {
	if path == "" {
		if len(n.routes) > 0 {
			return n, params
		}
		// A catch-all also matches the empty remainder, e.g. /files/* for /files.
		if n.wildcard != nil && len(n.wildcard.routes) > 0 {
			return n.wildcard, append(params, Param{Key: n.wildcardName, Value: ""})
		}
		return nil, params
	}

	head, rest := path, ""
	if i := strings.IndexByte(path, '/'); i >= 0 {
		head, rest = path[:i], path[i+1:]
	}
	if head == "" {
		// A doubled slash carries no segment; skipping it is unambiguous.
		return r.match(n, rest, params)
	}

	if c, ok := n.static[head]; ok {
		if found, p := r.match(c, rest, params); found != nil {
			return found, p
		}
	}

	if n.param != nil {
		p := append(params, Param{Key: n.paramName, Value: head})
		if found, p2 := r.match(n.param, rest, p); found != nil {
			return found, p2
		}
		// Drop the speculative capture before trying the next alternative.
		params = p[:len(p)-1]
	}

	if n.wildcard != nil && len(n.wildcard.routes) > 0 {
		return n.wildcard, append(params, Param{Key: n.wildcardName, Value: path})
	}

	return nil, params
}

func allowed(n *node) []string {
	methods := make([]string, 0, len(n.routes)+1)
	for m := range n.routes {
		methods = append(methods, m)
	}
	if _, ok := n.routes[http.MethodGet]; ok {
		if _, ok := n.routes[http.MethodHead]; !ok {
			methods = append(methods, http.MethodHead)
		}
	}
	sort.Strings(methods)
	return methods
}

// splitPath splits a route pattern into non-empty segments, which naturally
// normalises leading, trailing and doubled slashes. It is used when
// registering routes; Lookup walks the request path in place instead.
func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	n := strings.Count(path, "/") + 1
	segments := make([]string, 0, n)
	for {
		i := strings.IndexByte(path, '/')
		if i < 0 {
			if path != "" {
				segments = append(segments, path)
			}
			return segments
		}
		if i > 0 {
			segments = append(segments, path[:i])
		}
		path = path[i+1:]
	}
}

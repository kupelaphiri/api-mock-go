// Package server serves mock routes over HTTP.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kupelaphiri/api-mock-go/internal/config"
	"github.com/kupelaphiri/api-mock-go/internal/router"
)

// routesEndpoint lists the served routes. It is namespaced under /__mock so it
// cannot collide with a realistic API path, and a mocked route of the same
// path always takes precedence.
const routesEndpoint = "/__mock/routes"

// Options configures a Server.
type Options struct {
	Host string
	Port int

	// CORS enables permissive cross-origin headers and preflight handling,
	// which browsers require when a page on another origin calls the mock.
	CORS bool

	// Quiet suppresses the per-request log.
	Quiet bool

	// Out receives request logs and startup output.
	Out io.Writer
}

// Server serves a fixed set of mock routes.
type Server struct {
	router *router.Router
	opts   Options
	http   *http.Server

	// requests counts served requests, reported on shutdown.
	requests atomic.Uint64

	// logMu serialises log writes so concurrent requests produce whole lines.
	logMu sync.Mutex

	// paramPool recycles parameter buffers across requests, keeping route
	// matching allocation-free on the hot path.
	paramPool sync.Pool

	// startedAt is set when Serve begins accepting connections.
	startedAt time.Time
}

// New builds a Server for routes.
func New(routes []config.Route, opts Options) *Server {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = 3000
	}

	s := &Server{
		router: router.New(routes),
		opts:   opts,
		paramPool: sync.Pool{
			New: func() any {
				p := make(router.Params, 0, 8)
				return &p
			},
		},
	}

	s.http = &http.Server{
		Handler: s,
		// A mock server is a development tool: keep timeouts generous enough
		// not to interrupt a debugger sitting on a breakpoint, but bounded.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

// Addr returns the host:port the server binds to.
func (s *Server) Addr() string {
	return net.JoinHostPort(s.opts.Host, strconv.Itoa(s.opts.Port))
}

// Routes exposes the served routes, for startup output.
func (s *Server) Routes() []*config.Route { return s.router.Routes() }

// Listen binds the server's address without accepting connections, so that a
// port conflict is reported before anything is printed as ready.
func (s *Server) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", s.Addr())
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && isAddrInUse(opErr) {
			return nil, fmt.Errorf("port %d is already in use on %s", s.opts.Port, s.opts.Host)
		}
		return nil, err
	}
	return ln, nil
}

// Serve accepts connections on ln until the context is cancelled, then shuts
// down gracefully. It returns the number of requests served.
func (s *Server) Serve(ctx context.Context, ln net.Listener) (uint64, error) {
	s.startedAt = time.Now()

	errs := make(chan error, 1)
	go func() {
		err := s.http.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	select {
	case err := <-errs:
		return s.requests.Load(), err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			// Fall back to a hard close so a hung keep-alive cannot block exit.
			_ = s.http.Close()
		}
		return s.requests.Load(), nil
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	s.requests.Add(1)

	if s.opts.CORS {
		setCORS(w)
	}

	buf := s.paramPool.Get().(*router.Params)
	params := (*buf)[:0]

	route, params, allow := s.router.Lookup(r.Method, r.URL.Path, params)

	*buf = params
	defer s.paramPool.Put(buf)

	switch {
	case route != nil:
		status, size := s.write(w, r, route)
		s.log(r, status, size, start)

	// A preflight for a path we mock should succeed even though OPTIONS is not
	// itself a mocked operation.
	case s.opts.CORS && r.Method == http.MethodOptions && len(allow) > 0:
		w.Header().Set("Allow", joinMethods(allow))
		w.Header().Set("Access-Control-Allow-Methods", joinMethods(allow))
		w.WriteHeader(http.StatusNoContent)
		s.log(r, http.StatusNoContent, 0, start)

	case len(allow) > 0:
		w.Header().Set("Allow", joinMethods(allow))
		status, size := s.problem(w, r, http.StatusMethodNotAllowed,
			fmt.Sprintf("%s is not mocked for %s. Mocked methods: %s",
				r.Method, r.URL.Path, joinMethods(allow)))
		s.log(r, status, size, start)

	case r.URL.Path == routesEndpoint:
		status, size := s.listRoutes(w)
		s.log(r, status, size, start)

	default:
		status, size := s.problem(w, r, http.StatusNotFound,
			fmt.Sprintf("No mock defined for %s %s. See %s for the routes being served.",
				r.Method, r.URL.Path, routesEndpoint))
		s.log(r, status, size, start)
	}
}

// write emits a matched route's response, honouring its delay.
func (s *Server) write(w http.ResponseWriter, r *http.Request, route *config.Route) (int, int) {
	if route.Delay > 0 {
		select {
		case <-time.After(route.Delay):
		case <-r.Context().Done():
			// The client gave up while we were sleeping; nothing to write.
			return 499, 0
		}
	}

	header := w.Header()
	for k, v := range route.Headers {
		header.Set(k, v)
	}
	if len(route.Body) > 0 && header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/json")
	}
	header.Set("Content-Length", strconv.Itoa(len(route.Body)))
	header.Set("X-Mock-Server", "api-mock-go")

	status := route.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	// A HEAD response carries headers only.
	if r.Method == http.MethodHead {
		return status, 0
	}
	n, _ := w.Write(route.Body)
	return status, n
}

// problem writes a JSON error body, so that a client expecting JSON does not
// have to parse prose to discover what went wrong.
func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, detail string) (int, int) {
	body, err := json.Marshal(map[string]any{
		"error":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
	if err != nil {
		body = []byte(`{"error":"Internal Server Error","status":500}`)
		status = http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("X-Mock-Server", "api-mock-go")
	w.WriteHeader(status)

	if r.Method == http.MethodHead {
		return status, 0
	}
	n, _ := w.Write(body)
	return status, n
}

// listRoutes serves the route introspection endpoint.
func (s *Server) listRoutes(w http.ResponseWriter) (int, int) {
	type entry struct {
		Method      string `json:"method"`
		Path        string `json:"path"`
		Status      int    `json:"status"`
		DelayMS     int64  `json:"delayMs,omitempty"`
		Description string `json:"description,omitempty"`
	}

	routes := s.router.Routes()
	entries := make([]entry, 0, len(routes))
	for _, route := range routes {
		entries = append(entries, entry{
			Method:      route.Method,
			Path:        route.Path,
			Status:      route.Status,
			DelayMS:     route.Delay.Milliseconds(),
			Description: route.Description,
		})
	}

	body, err := json.MarshalIndent(map[string]any{
		"count":  len(entries),
		"routes": entries,
	}, "", "  ")
	if err != nil {
		return http.StatusInternalServerError, 0
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	n, _ := w.Write(body)
	return http.StatusOK, n
}

func setCORS(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Headers", "*")
	header.Set("Access-Control-Expose-Headers", "*")
	header.Set("Access-Control-Max-Age", "86400")
}

// log writes one line per request: status, method, path, duration and size.
func (s *Server) log(r *http.Request, status, size int, start time.Time) {
	if s.opts.Quiet {
		return
	}

	elapsed := time.Since(start)
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}

	s.logMu.Lock()
	defer s.logMu.Unlock()
	fmt.Fprintf(s.opts.Out, "%s %s %-7s %s %s %s\n",
		start.Format("15:04:05.000"),
		colorStatus(status),
		r.Method,
		path,
		dim(formatDuration(elapsed)),
		dim(formatBytes(size)),
	)
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fµs", float64(d.Nanoseconds())/1e3)
	case d < time.Second:
		return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1e6)
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}

func joinMethods(methods []string) string {
	return strings.Join(methods, ", ")
}

// isAddrInUse reports whether an accept error means the port was taken. The
// message is the portable signal, since the errno constant differs between
// Unix (EADDRINUSE) and Windows (WSAEADDRINUSE).
func isAddrInUse(err *net.OpError) bool {
	msg := err.Err.Error()
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "address in use") ||
		strings.Contains(msg, "Only one usage of each socket address")
}

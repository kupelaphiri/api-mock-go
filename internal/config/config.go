// Package config loads mock route definitions from disk.
//
// Two input formats are supported and auto-detected: an OpenAPI 3.x / Swagger
// 2.0 specification, and this project's own route list. Either may be written
// as YAML or JSON.
//
// Response bodies are serialised once, at load time, so that serving a request
// is a header write plus a copy of an existing byte slice.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Route is a fully-resolved mock endpoint.
type Route struct {
	Method  string
	Path    string
	Status  int
	Headers map[string]string
	Body    []byte

	// Delay is an artificial latency applied before the response is written.
	Delay time.Duration

	// Description is a human-readable label used in startup output, taken from
	// the spec's summary or operation ID where available.
	Description string
}

// File is the parsed contents of a route-list config file.
type File struct {
	// Server defaults, overridden by any explicitly-set CLI flag.
	Port  int    `yaml:"port" json:"port"`
	Host  string `yaml:"host" json:"host"`
	Delay string `yaml:"delay" json:"delay"`
	CORS  *bool  `yaml:"cors" json:"cors"`

	Routes []RouteSpec `yaml:"routes" json:"routes"`
}

// RouteSpec is a single user-authored route entry.
type RouteSpec struct {
	Path string `yaml:"path" json:"path"`

	// Method accepts either a single method or a list of them.
	Method methods `yaml:"method" json:"method"`

	Status  int               `yaml:"status" json:"status"`
	Headers map[string]string `yaml:"headers" json:"headers"`
	Delay   string            `yaml:"delay" json:"delay"`

	// Response is any JSON-encodable value, serialised as the response body.
	Response any `yaml:"response" json:"response"`

	// Body is a raw response body, used verbatim. Takes precedence over
	// Response when both are set.
	Body string `yaml:"body" json:"body"`

	// File names a file whose contents become the response body, resolved
	// relative to the config file. Takes precedence over Response and Body.
	File string `yaml:"file" json:"file"`
}

// Options adjusts how a source file is turned into routes.
type Options struct {
	// BasePath is prefixed to every route path, e.g. "/api/v1".
	BasePath string

	// DefaultDelay applies to routes that do not specify their own.
	DefaultDelay time.Duration
}

// Result is the outcome of loading a source file.
type Result struct {
	Routes []Route

	// Defaults holds server settings declared in a route-list config file.
	// It is nil for OpenAPI specifications, which carry no such settings.
	Defaults *File

	// Format is "openapi", "swagger" or "routes".
	Format string

	// Warnings describes recoverable problems, such as an operation with no
	// documented response.
	Warnings []string
}

// Load reads path and resolves it into mock routes, detecting the format from
// the document's contents.
func Load(path string, opts Options) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}

	var doc map[string]any
	if err := unmarshal(path, data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	switch {
	case doc["openapi"] != nil:
		return loadOpenAPI(doc, path, opts)
	case doc["swagger"] != nil:
		return loadOpenAPI(doc, path, opts)
	case doc["routes"] != nil:
		return loadRoutes(data, path, opts)
	default:
		return nil, fmt.Errorf(
			"%s is neither an OpenAPI spec nor a route list: expected a top-level "+
				"\"openapi\", \"swagger\" or \"routes\" key", path)
	}
}

// unmarshal decodes YAML or JSON into v. JSON is decoded by encoding/json
// rather than the YAML parser, because JSON documents indented with tabs are
// not valid YAML.
func unmarshal(path string, data []byte, v any) error {
	if isJSON(path, data) {
		return json.Unmarshal(data, v)
	}
	return yaml.Unmarshal(data, v)
}

func isJSON(path string, data []byte) bool {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return true
	}
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func loadRoutes(data []byte, path string, opts Options) (*Result, error) {
	var file File
	if err := unmarshal(path, data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	res := &Result{Format: "routes", Defaults: &file}
	dir := filepath.Dir(path)

	// A --delay flag outranks the file's own default.
	fileDelay := opts.DefaultDelay
	if fileDelay == 0 && file.Delay != "" {
		var err error
		if fileDelay, err = ParseDelay(file.Delay); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	for i, spec := range file.Routes {
		if spec.Path == "" {
			return nil, fmt.Errorf("%s: routes[%d] has no path", path, i)
		}

		body, contentType, err := routeBody(spec, dir)
		if err != nil {
			return nil, fmt.Errorf("%s: routes[%d] (%s): %w", path, i, spec.Path, err)
		}

		delay := fileDelay
		if spec.Delay != "" {
			delay, err = ParseDelay(spec.Delay)
			if err != nil {
				return nil, fmt.Errorf("%s: routes[%d] (%s): %w", path, i, spec.Path, err)
			}
		}

		status := spec.Status
		if status == 0 {
			status = 200
		}

		headers := make(map[string]string, len(spec.Headers)+1)
		for k, v := range spec.Headers {
			headers[k] = v
		}
		if contentType != "" && !hasHeader(headers, "Content-Type") {
			headers["Content-Type"] = contentType
		}

		for _, method := range spec.Method.values() {
			res.Routes = append(res.Routes, Route{
				Method:  method,
				Path:    normalizePath(opts.BasePath + spec.Path),
				Status:  status,
				Headers: headers,
				Body:    body,
				Delay:   delay,
			})
		}
	}

	if len(res.Routes) == 0 {
		return nil, fmt.Errorf("%s declares no routes", path)
	}
	return res, nil
}

// routeBody resolves a route's body and the content type implied by its
// source, which callers may override with an explicit header.
func routeBody(spec RouteSpec, dir string) ([]byte, string, error) {
	switch {
	case spec.File != "":
		p := spec.File
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, "", fmt.Errorf("read response file: %w", err)
		}
		return body, contentTypeForExt(filepath.Ext(p)), nil

	case spec.Body != "":
		return []byte(spec.Body), "text/plain; charset=utf-8", nil

	case spec.Response != nil:
		body, err := json.Marshal(normalize(spec.Response))
		if err != nil {
			return nil, "", fmt.Errorf("encode response: %w", err)
		}
		return body, "application/json", nil

	default:
		return nil, "", nil
	}
}

func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".yaml", ".yml":
		return "application/yaml"
	default:
		return "text/plain; charset=utf-8"
	}
}

func hasHeader(headers map[string]string, name string) bool {
	for k := range headers {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// methods is one or more HTTP methods, decoded from either a scalar or a list.
type methods []string

func (m *methods) UnmarshalYAML(node *yaml.Node) error {
	var single string
	if err := node.Decode(&single); err == nil {
		*m = methods{single}
		return nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		return fmt.Errorf("method must be a string or a list of strings")
	}
	*m = list
	return nil
}

func (m *methods) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*m = methods{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("method must be a string or a list of strings")
	}
	*m = list
	return nil
}

// values returns the uppercased methods, defaulting to GET when unset.
func (m methods) values() []string {
	if len(m) == 0 {
		return []string{"GET"}
	}
	out := make([]string, 0, len(m))
	for _, v := range m {
		if v = strings.ToUpper(strings.TrimSpace(v)); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{"GET"}
	}
	return out
}

// ParseDelay accepts a Go duration string ("250ms", "1.5s") or a bare number,
// which is read as milliseconds.
func ParseDelay(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if ms, err := strconv.ParseFloat(s, 64); err == nil {
		if ms < 0 {
			return 0, fmt.Errorf("delay %q must not be negative", s)
		}
		return time.Duration(ms * float64(time.Millisecond)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid delay %q: want a duration like \"250ms\" or a number of milliseconds", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("delay %q must not be negative", s)
	}
	return d, nil
}

// normalizePath cleans a route path and rewrites OpenAPI-style "{id}"
// parameters into the ":id" form the router registers.
func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if strings.IndexByte(path, '{') >= 0 {
		var b strings.Builder
		b.Grow(len(path))
		for {
			open := strings.IndexByte(path, '{')
			if open < 0 {
				b.WriteString(path)
				break
			}
			end := strings.IndexByte(path[open:], '}')
			if end < 0 {
				b.WriteString(path)
				break
			}
			b.WriteString(path[:open])
			b.WriteByte(':')
			b.WriteString(path[open+1 : open+end])
			path = path[open+end+1:]
		}
		path = b.String()
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// Collapse doubled slashes that a base-path join may have introduced.
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

// normalize converts YAML-decoded values into shapes encoding/json accepts,
// which mainly means turning non-string map keys into strings.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	default:
		return v
	}
}

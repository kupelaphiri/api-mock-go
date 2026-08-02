package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// httpMethods are the operation keys recognised inside a path item, in the
// order OpenAPI documents conventionally list them.
var httpMethods = []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}

// document is a parsed OpenAPI or Swagger specification.
type document struct {
	root     map[string]any
	swagger2 bool
}

// loadOpenAPI turns a specification into one route per operation, using the
// operation's documented example where present and synthesising one from its
// response schema otherwise.
func loadOpenAPI(root map[string]any, path string, opts Options) (*Result, error) {
	doc := &document{root: root, swagger2: root["openapi"] == nil}

	format := "openapi"
	if doc.swagger2 {
		format = "swagger"
	}
	res := &Result{Format: format}

	paths, ok := root["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		return nil, fmt.Errorf("%s has no paths to mock", path)
	}

	basePath := opts.BasePath
	if basePath == "" {
		basePath = doc.basePath()
	}

	// Iterate in a stable order so startup output and route listings do not
	// shuffle between runs.
	specPaths := make([]string, 0, len(paths))
	for p := range paths {
		specPaths = append(specPaths, p)
	}
	sort.Strings(specPaths)

	for _, specPath := range specPaths {
		item, ok := paths[specPath].(map[string]any)
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: not an object, skipped", specPath))
			continue
		}
		if ref, isRef := stringAt(item, "$ref"); isRef {
			resolved, found := doc.resolve(ref)
			if !found {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: unresolved $ref %q, skipped", specPath, ref))
				continue
			}
			item = resolved
		}

		for _, method := range httpMethods {
			op, ok := item[method].(map[string]any)
			if !ok {
				continue
			}

			route, warn := doc.route(method, specPath, op, basePath, opts.DefaultDelay)
			if warn != "" {
				res.Warnings = append(res.Warnings, warn)
			}
			if route != nil {
				res.Routes = append(res.Routes, *route)
			}
		}
	}

	if len(res.Routes) == 0 {
		return nil, fmt.Errorf("%s declares no operations to mock", path)
	}
	return res, nil
}

func (d *document) route(method, specPath string, op map[string]any, basePath string, delay time.Duration) (*Route, string) {
	status, media, body, warn := d.responseFor(method, specPath, op)

	route := &Route{
		Method:      strings.ToUpper(method),
		Path:        normalizePath(basePath + specPath),
		Status:      status,
		Body:        body,
		Delay:       delay,
		Description: describe(op),
		Headers:     map[string]string{},
	}
	if media != "" && len(body) > 0 {
		route.Headers["Content-Type"] = media
	}
	return route, warn
}

// responseFor picks the response to mock for an operation and renders its body.
func (d *document) responseFor(method, specPath string, op map[string]any) (status int, mediaType string, body []byte, warning string) {
	responses, ok := op["responses"].(map[string]any)
	if !ok || len(responses) == 0 {
		return 200, "", nil, fmt.Sprintf("%s %s: no responses documented, mocking an empty 200",
			strings.ToUpper(method), specPath)
	}

	key := pickStatus(responses)
	if key == "" {
		return 200, "", nil, fmt.Sprintf("%s %s: no usable response status, mocking an empty 200",
			strings.ToUpper(method), specPath)
	}

	status = 200
	if n, err := strconv.Atoi(key); err == nil {
		status = n
	} else if method == "post" {
		// A "default"-only response on a create reads better as a 201.
		status = 201
	}

	response, ok := responses[key].(map[string]any)
	if !ok {
		return status, "", nil, ""
	}
	if ref, isRef := stringAt(response, "$ref"); isRef {
		if resolved, found := d.resolve(ref); found {
			response = resolved
		}
	}

	if status == 204 || status == 304 {
		return status, "", nil, ""
	}

	if d.swagger2 {
		schema, _ := response["schema"].(map[string]any)
		if schema == nil {
			return status, "", nil, ""
		}
		mediaType = swagger2MediaType(op, d.root)
		value := d.example(schema, "")
		return status, mediaType, encode(value, mediaType), ""
	}

	content, ok := response["content"].(map[string]any)
	if !ok || len(content) == 0 {
		return status, "", nil, ""
	}

	mediaType = pickMediaType(content)
	media, ok := content[mediaType].(map[string]any)
	if !ok {
		return status, mediaType, nil, ""
	}

	// Documented examples beat anything we could synthesise.
	if ex, found := media["example"]; found {
		return status, mediaType, encode(normalize(ex), mediaType), ""
	}
	if ex, found := firstNamedExample(media); found {
		return status, mediaType, encode(normalize(ex), mediaType), ""
	}

	schema, ok := media["schema"].(map[string]any)
	if !ok {
		return status, mediaType, nil, fmt.Sprintf("%s %s: response %s has no schema or example, mocking an empty body",
			strings.ToUpper(method), specPath, key)
	}

	return status, mediaType, encode(d.example(schema, ""), mediaType), ""
}

// pickStatus prefers the lowest documented 2xx status, falling back to
// "default", then to the lowest status of any class.
func pickStatus(responses map[string]any) string {
	keys := make([]string, 0, len(responses))
	for k := range responses {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if n, err := strconv.Atoi(k); err == nil && n >= 200 && n < 300 {
			return k
		}
	}
	// "2XX" wildcards are legal in OpenAPI 3.
	for _, k := range keys {
		if len(k) == 3 && k[0] == '2' && (k[1] == 'X' || k[1] == 'x') {
			return "200"
		}
	}
	for _, k := range keys {
		if k == "default" {
			return k
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// pickMediaType prefers JSON, then any structured type, then whatever is
// listed first alphabetically.
func pickMediaType(content map[string]any) string {
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if k == "application/json" {
			return k
		}
	}
	for _, k := range keys {
		if strings.Contains(k, "json") {
			return k
		}
	}
	for _, k := range keys {
		if !strings.Contains(k, "*") {
			return k
		}
	}
	return keys[0]
}

// swagger2MediaType reads the media type from the operation's "produces" list,
// falling back to the document-level list and then to JSON.
func swagger2MediaType(op, root map[string]any) string {
	for _, source := range []map[string]any{op, root} {
		list, ok := source["produces"].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			if s, ok := item.(string); ok && strings.Contains(s, "json") {
				return s
			}
		}
		if len(list) > 0 {
			if s, ok := list[0].(string); ok {
				return s
			}
		}
	}
	return "application/json"
}

// firstNamedExample returns the value of the alphabetically-first entry in a
// media type's "examples" map.
func firstNamedExample(media map[string]any) (any, bool) {
	examples, ok := media["examples"].(map[string]any)
	if !ok || len(examples) == 0 {
		return nil, false
	}
	names := make([]string, 0, len(examples))
	for name := range examples {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry, ok := examples[name].(map[string]any)
		if !ok {
			continue
		}
		if value, found := entry["value"]; found {
			return value, true
		}
	}
	return nil, false
}

// encode renders a generated value for the wire. JSON media types are
// marshalled; anything else is written as text.
func encode(value any, mediaType string) []byte {
	if value == nil {
		return nil
	}
	if mediaType != "" && !strings.Contains(mediaType, "json") {
		switch t := value.(type) {
		case string:
			return []byte(t)
		case []byte:
			return t
		default:
			return []byte(fmt.Sprint(t))
		}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return body
}

// basePath returns the path component of the spec's first server URL, or the
// Swagger 2.0 "basePath", so that a spec served under /api/v1 mocks there too.
func (d *document) basePath() string {
	if d.swagger2 {
		if bp, ok := stringAt(d.root, "basePath"); ok {
			return bp
		}
		return ""
	}
	servers, ok := d.root["servers"].([]any)
	if !ok || len(servers) == 0 {
		return ""
	}
	server, ok := servers[0].(map[string]any)
	if !ok {
		return ""
	}
	raw, ok := stringAt(server, "url")
	if !ok {
		return ""
	}
	// Server URLs may be absolute, relative, or templated. Templated segments
	// have no single value, so they are not a usable prefix.
	if strings.Contains(raw, "{") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Path == "/" {
		return ""
	}
	return u.Path
}

// resolve follows a local JSON pointer such as
// "#/components/schemas/Pet". External references are not supported.
func (d *document) resolve(ref string) (map[string]any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var current any = d.root
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")

		switch node := current.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			current = node[i]
		default:
			return nil, false
		}
	}
	resolved, ok := current.(map[string]any)
	return resolved, ok
}

// describe returns a short label for an operation.
func describe(op map[string]any) string {
	if s, ok := stringAt(op, "summary"); ok {
		return s
	}
	if s, ok := stringAt(op, "operationId"); ok {
		return s
	}
	return ""
}

func stringAt(m map[string]any, key string) (string, bool) {
	s, ok := m[key].(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

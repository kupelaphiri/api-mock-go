package config

import (
	"strconv"
	"strings"
)

// maxSchemaDepth bounds recursion for schemas that nest deeply or refer to
// themselves through a chain of arrays and objects.
const maxSchemaDepth = 12

// example synthesises a representative value for a JSON Schema. hint is the
// property name the schema was reached through, which is used to pick
// plausible strings for fields like "email" or "createdAt".
func (d *document) example(schema map[string]any, hint string) any {
	return d.gen(schema, hint, 0, map[string]bool{})
}

func (d *document) gen(schema map[string]any, hint string, depth int, active map[string]bool) any {
	if schema == nil || depth > maxSchemaDepth {
		return nil
	}

	if ref, ok := stringAt(schema, "$ref"); ok {
		// A reference already being expanded higher up the stack is recursive;
		// stop rather than descend forever.
		if active[ref] {
			return nil
		}
		resolved, found := d.resolve(ref)
		if !found {
			return nil
		}
		active[ref] = true
		defer delete(active, ref)
		return d.gen(resolved, hint, depth+1, active)
	}

	// An author-supplied value is always better than a synthesised one.
	if ex, ok := schema["example"]; ok {
		return normalize(ex)
	}
	if ex, ok := schema["examples"]; ok {
		// OpenAPI 3.1 / JSON Schema spell this as a list of candidates.
		if list, ok := ex.([]any); ok && len(list) > 0 {
			return normalize(list[0])
		}
	}
	if def, ok := schema["default"]; ok {
		return normalize(def)
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return normalize(enum[0])
	}

	if merged := d.composed(schema, hint, depth, active); merged != nil {
		return merged
	}

	switch schemaType(schema) {
	case "object":
		return d.object(schema, depth, active)
	case "array":
		return d.array(schema, hint, depth, active)
	case "string":
		return stringExample(schema, hint)
	case "integer":
		return integerExample(schema, hint)
	case "number":
		return numberExample(schema, hint)
	case "boolean":
		return true
	case "null":
		return nil
	default:
		// An untyped schema with no other signal describes "any value"; an
		// empty object is the least surprising stand-in.
		return map[string]any{}
	}
}

// composed handles allOf, oneOf and anyOf. allOf members are merged, since the
// value must satisfy all of them; for oneOf and anyOf the first member wins.
func (d *document) composed(schema map[string]any, hint string, depth int, active map[string]bool) any {
	if all, ok := schema["allOf"].([]any); ok && len(all) > 0 {
		merged := map[string]any{}
		for _, member := range all {
			sub, ok := member.(map[string]any)
			if !ok {
				continue
			}
			value := d.gen(sub, hint, depth+1, active)
			fields, ok := value.(map[string]any)
			if !ok {
				// A non-object member cannot be merged, so it stands alone.
				return value
			}
			for k, v := range fields {
				merged[k] = v
			}
		}
		// Properties declared alongside allOf apply too.
		if own, ok := d.object(schema, depth, active).(map[string]any); ok {
			for k, v := range own {
				merged[k] = v
			}
		}
		return merged
	}

	for _, key := range []string{"oneOf", "anyOf"} {
		list, ok := schema[key].([]any)
		if !ok || len(list) == 0 {
			continue
		}
		for _, member := range list {
			sub, ok := member.(map[string]any)
			if !ok {
				continue
			}
			// Skip a bare null branch: mocking the non-null case is more useful.
			if schemaType(sub) == "null" {
				continue
			}
			return d.gen(sub, hint, depth+1, active)
		}
	}
	return nil
}

func (d *document) object(schema map[string]any, depth int, active map[string]bool) any {
	out := map[string]any{}

	if props, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range props {
			sub, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			// Read-only and write-only fields still belong in a mock response;
			// only deprecated ones are worth omitting.
			if deprecated, _ := sub["deprecated"].(bool); deprecated {
				continue
			}
			out[name] = d.gen(sub, name, depth+1, active)
		}
	}

	if len(out) == 0 {
		if extra, ok := schema["additionalProperties"].(map[string]any); ok {
			out["key"] = d.gen(extra, "", depth+1, active)
		}
	}
	return out
}

func (d *document) array(schema map[string]any, hint string, depth int, active map[string]bool) any {
	items, ok := schema["items"].(map[string]any)
	if !ok {
		return []any{}
	}

	// Two elements make a collection obviously a collection to whatever is
	// consuming the mock, without bloating the payload.
	count := 2
	if n, ok := numberAt(schema, "minItems"); ok && int(n) > count {
		count = int(n)
	}
	if n, ok := numberAt(schema, "maxItems"); ok && int(n) < count {
		count = int(n)
	}
	if count < 0 {
		count = 0
	}

	out := make([]any, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, d.gen(items, singular(hint), depth+1, active))
	}

	// Elements are all nil when the item schema was a recursive reference we
	// declined to expand. An empty array says "no children here" far more
	// clearly than a list of nulls, and matches the schema just as well.
	for _, item := range out {
		if item != nil {
			return out
		}
	}
	return []any{}
}

// schemaType reads the schema's type, accepting the OpenAPI 3.1 form where
// "type" may be a list such as ["string", "null"].
func schemaType(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && s != "null" {
				return s
			}
		}
	}
	// Infer the type from structural keywords when it is absent.
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	if _, ok := schema["additionalProperties"]; ok {
		return "object"
	}
	if _, ok := schema["items"]; ok {
		return "array"
	}
	return ""
}

// stringExample picks a value from the schema's format, then from the property
// name, so that generated payloads read like real data.
func stringExample(schema map[string]any, hint string) string {
	if format, ok := stringAt(schema, "format"); ok {
		if v, found := formatExamples[strings.ToLower(format)]; found {
			return v
		}
	}
	if pattern, ok := stringAt(schema, "pattern"); ok {
		// A pattern we cannot generate for is still worth surfacing, since a
		// consumer validating against it will otherwise see a silent mismatch.
		_ = pattern
	}

	if v, found := hintExample(hint); found {
		return v
	}

	value := "string"
	if min, ok := numberAt(schema, "minLength"); ok && int(min) > len(value) {
		value = value + strings.Repeat("x", int(min)-len(value))
	}
	if max, ok := numberAt(schema, "maxLength"); ok && int(max) < len(value) {
		value = value[:int(max)]
	}
	return value
}

// formatExamples maps JSON Schema string formats to fixed sample values.
// Values are deterministic so that repeated runs produce identical mocks.
var formatExamples = map[string]string{
	"date-time": "2024-01-15T09:30:00Z",
	"date":      "2024-01-15",
	"time":      "09:30:00Z",
	"duration":  "P3DT4H",
	"email":     "user@example.com",
	"idn-email": "user@example.com",
	"hostname":  "example.com",
	"ipv4":      "192.0.2.1",
	"ipv6":      "2001:db8::1",
	"uri":       "https://example.com/resource",
	"url":       "https://example.com/resource",
	"uri-ref":   "/resource",
	"uuid":      "5f4d3c2b-1a09-4e8d-9c7b-6a5f4e3d2c1b",
	"password":  "hunter2",
	"byte":      "YXBpLW1vY2stZ28=",
	"binary":    "binary",
}

// hintExamples maps normalised property names to sample values. Keys are
// lowercase with separators removed, so "created_at" and "createdAt" both
// match "createdat".
var hintExamples = map[string]string{
	"id":           "5f4d3c2b-1a09-4e8d-9c7b-6a5f4e3d2c1b",
	"uuid":         "5f4d3c2b-1a09-4e8d-9c7b-6a5f4e3d2c1b",
	"name":         "Jane Doe",
	"firstname":    "Jane",
	"lastname":     "Doe",
	"fullname":     "Jane Doe",
	"username":     "janedoe",
	"email":        "jane@example.com",
	"emailaddress": "jane@example.com",
	"phone":        "+1-555-0100",
	"phonenumber":  "+1-555-0100",
	"title":        "Example title",
	"description":  "An example description.",
	"summary":      "An example summary.",
	"body":         "Example body text.",
	"content":      "Example content.",
	"text":         "Example text.",
	"note":         "An example note.",
	"comment":      "An example comment.",
	"label":        "Example label",
	"category":     "general",
	"sku":          "SKU-1001",
	"reference":    "REF-1001",
	"slug":         "example-slug",
	"status":       "active",
	"state":        "active",
	"role":         "user",
	"type":         "standard",
	"url":          "https://example.com",
	"uri":          "https://example.com",
	"website":      "https://example.com",
	"avatar":       "https://example.com/avatar.png",
	"image":        "https://example.com/image.png",
	"imageurl":     "https://example.com/image.png",
	"createdat":    "2024-01-15T09:30:00Z",
	"updatedat":    "2024-01-16T14:20:00Z",
	"deletedat":    "2024-02-01T00:00:00Z",
	"timestamp":    "2024-01-15T09:30:00Z",
	"date":         "2024-01-15",
	"address":      "123 Example Street",
	"street":       "123 Example Street",
	"city":         "Springfield",
	"country":      "US",
	"countrycode":  "US",
	"postcode":     "12345",
	"zipcode":      "12345",
	"currency":     "USD",
	"locale":       "en-US",
	"language":     "en",
	"timezone":     "UTC",
	"token":        "eyJhbGciOiJIUzI1NiJ9.example.token",
	"message":      "Example message",
	"error":        "Something went wrong",
	"code":         "EXAMPLE_CODE",
	"version":      "1.0.0",
	"color":        "#4f46e5",
	"password":     "hunter2",
}

func hintExample(hint string) (string, bool) {
	if hint == "" {
		return "", false
	}
	key := normalizeHint(hint)
	if v, ok := hintExamples[key]; ok {
		return v, true
	}
	// Fall back to suffix matching so "authorEmail" and "productSlug" resolve.
	for _, suffix := range []string{"email", "url", "uri", "slug", "name", "id", "at", "code"} {
		if len(key) > len(suffix) && strings.HasSuffix(key, suffix) {
			if v, ok := hintExamples[suffix]; ok {
				return v, true
			}
			if suffix == "at" {
				return "2024-01-15T09:30:00Z", true
			}
		}
	}
	return "", false
}

// normalizeHint lowercases a property name and strips the separators that
// distinguish camelCase, snake_case and kebab-case spellings.
func normalizeHint(hint string) string {
	var b strings.Builder
	b.Grow(len(hint))
	for _, r := range hint {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		case r == '_' || r == '-' || r == ' ' || r == '.':
			// separator, dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// integerHints maps normalised property names to sample integers, so that a
// paginated response mocks as page 1 of 25 rather than page 42 of 42.
var integerHints = map[string]int64{
	"page":     1,
	"pagesize": 25,
	"perpage":  25,
	"limit":    25,
	"offset":   0,
	"count":    10,
	"total":    10,
	"quantity": 2,
	"age":      30,
	"year":     2024,
	"version":  1,
}

func integerExample(schema map[string]any, hint string) int64 {
	key := normalizeHint(hint)

	value := int64(42)
	switch {
	case isIDHint(hint):
		value = 1
	default:
		if v, ok := integerHints[key]; ok {
			value = v
		} else if strings.Contains(key, "count") || strings.Contains(key, "total") {
			value = 10
		}
	}
	return clampInt(schema, value)
}

func numberExample(schema map[string]any, hint string) float64 {
	value := 42.5
	if strings.Contains(normalizeHint(hint), "price") || strings.Contains(normalizeHint(hint), "amount") {
		value = 19.99
	}
	if isIDHint(hint) {
		value = 1
	}
	return clampFloat(schema, value)
}

func isIDHint(hint string) bool {
	key := normalizeHint(hint)
	return key == "id" || strings.HasSuffix(key, "id")
}

func clampInt(schema map[string]any, value int64) int64 {
	if min, ok := numberAt(schema, "minimum"); ok && value < int64(min) {
		value = int64(min)
	}
	if max, ok := numberAt(schema, "maximum"); ok && value > int64(max) {
		value = int64(max)
	}
	return value
}

func clampFloat(schema map[string]any, value float64) float64 {
	if min, ok := numberAt(schema, "minimum"); ok && value < min {
		value = min
	}
	if max, ok := numberAt(schema, "maximum"); ok && value > max {
		value = max
	}
	return value
}

// numberAt reads a numeric keyword, accepting the several Go types a JSON or
// YAML decoder may produce for one.
func numberAt(m map[string]any, key string) (float64, bool) {
	switch v := m[key].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// singular trims a trailing plural "s" so that an array named "users" hints
// its elements as "user".
func singular(hint string) string {
	if len(hint) > 1 && strings.HasSuffix(hint, "s") && !strings.HasSuffix(hint, "ss") {
		return hint[:len(hint)-1]
	}
	return hint
}

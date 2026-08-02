package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadOpenAPI(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.3
info: {title: Test, version: 1.0.0}
servers:
  - url: https://api.example.com/v1
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Pet"
    post:
      operationId: createPet
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Pet"
  /pets/{petId}:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Pet"
    delete:
      responses:
        "204":
          description: gone
components:
  schemas:
    Pet:
      type: object
      properties:
        id: {type: string, format: uuid}
        name: {type: string}
        tag: {type: string, enum: [cat, dog]}
        weight: {type: number}
        vaccinated: {type: boolean}
        createdAt: {type: string, format: date-time}
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Format != "openapi" {
		t.Errorf("Format = %q, want openapi", result.Format)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", result.Warnings)
	}

	// The server URL's path becomes the base path.
	list := find(t, result.Routes, "GET", "/v1/pets")
	if list.Status != 200 {
		t.Errorf("status = %d, want 200", list.Status)
	}
	if list.Description != "List pets" {
		t.Errorf("description = %q, want the summary", list.Description)
	}
	if got := list.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var pets []map[string]any
	if err := json.Unmarshal(list.Body, &pets); err != nil {
		t.Fatalf("decode %q: %v", list.Body, err)
	}
	if len(pets) != 2 {
		t.Fatalf("got %d generated pets, wanted a 2-element sample array", len(pets))
	}
	pet := pets[0]
	if pet["id"] != "5f4d3c2b-1a09-4e8d-9c7b-6a5f4e3d2c1b" {
		t.Errorf("id = %v, want the uuid sample", pet["id"])
	}
	if pet["tag"] != "cat" {
		t.Errorf("tag = %v, want the first enum value", pet["tag"])
	}
	if pet["vaccinated"] != true {
		t.Errorf("vaccinated = %v, want true", pet["vaccinated"])
	}
	if pet["createdAt"] != "2024-01-15T09:30:00Z" {
		t.Errorf("createdAt = %v, want the date-time sample", pet["createdAt"])
	}
	if _, ok := pet["weight"].(float64); !ok {
		t.Errorf("weight = %v, want a number", pet["weight"])
	}

	// operationId stands in when there is no summary.
	create := find(t, result.Routes, "POST", "/v1/pets")
	if create.Status != 201 {
		t.Errorf("POST status = %d, want 201", create.Status)
	}
	if create.Description != "createPet" {
		t.Errorf("POST description = %q, want createPet", create.Description)
	}

	// Path templates are rewritten to the router's parameter form.
	find(t, result.Routes, "GET", "/v1/pets/:petId")

	// A 204 carries no body and no content type.
	del := find(t, result.Routes, "DELETE", "/v1/pets/:petId")
	if del.Status != 204 {
		t.Errorf("DELETE status = %d, want 204", del.Status)
	}
	if len(del.Body) != 0 {
		t.Errorf("DELETE body = %q, want empty", del.Body)
	}
	if del.Headers["Content-Type"] != "" {
		t.Errorf("DELETE Content-Type = %q, want none", del.Headers["Content-Type"])
	}
}

// A documented example must be served verbatim rather than synthesised from the
// schema alongside it.
func TestOpenAPIExampleWinsOverSchema(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /a:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              example: {hand: written}
              schema:
                type: object
                properties:
                  generated: {type: string}
  /b:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              examples:
                first:
                  value: {named: example}
              schema:
                type: object
                properties:
                  generated: {type: string}
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := body(t, find(t, result.Routes, "GET", "/a"))["hand"]; got != "written" {
		t.Errorf("/a body = %v, want the inline example", got)
	}
	if got := body(t, find(t, result.Routes, "GET", "/b"))["named"]; got != "example" {
		t.Errorf("/b body = %v, want the named example", got)
	}
}

func TestOpenAPIStatusSelection(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /lowest-2xx:
    get:
      responses:
        "500": {description: err}
        "202": {description: accepted}
        "404": {description: missing}
  /default-only:
    get:
      responses:
        default: {description: whatever}
  /errors-only:
    get:
      responses:
        "404": {description: missing}
  /wildcard:
    get:
      responses:
        "2XX": {description: ok}
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := map[string]int{
		"/lowest-2xx":   202,
		"/default-only": 200,
		"/errors-only":  404,
		"/wildcard":     200,
	}
	for path, status := range want {
		if got := find(t, result.Routes, "GET", path).Status; got != status {
			t.Errorf("%s status = %d, want %d", path, got, status)
		}
	}
}

// A schema that refers to itself must not recurse forever.
func TestOpenAPIRecursiveSchema(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /tree:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Node"
components:
  schemas:
    Node:
      type: object
      properties:
        name: {type: string}
        parent:
          $ref: "#/components/schemas/Node"
        children:
          type: array
          items:
            $ref: "#/components/schemas/Node"
`)

	// The real assertion is that this returns at all.
	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	node := body(t, find(t, result.Routes, "GET", "/tree"))
	if node["name"] != "Jane Doe" {
		t.Errorf("name = %v, want the name sample", node["name"])
	}
	if node["parent"] != nil {
		t.Errorf("parent = %v, want nil where the recursion stopped", node["parent"])
	}
	// A recursive array collapses to an empty list rather than a list of nulls.
	children, ok := node["children"].([]any)
	if !ok || len(children) != 0 {
		t.Errorf("children = %v, want an empty array", node["children"])
	}
}

// Generated values should read like the field they stand for, so that a mocked
// paginated response looks like page 1 of 25 rather than page 42 of 42.
func TestGeneratedValuesFollowFieldNames(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /a:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  page: {type: integer, minimum: 1}
                  perPage: {type: integer}
                  total: {type: integer}
                  userId: {type: integer}
                  body: {type: string}
                  price: {type: number}
                  plain: {type: integer}
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := body(t, find(t, result.Routes, "GET", "/a"))

	numbers := map[string]float64{
		"page":    1,
		"perPage": 25,
		"total":   10,
		"userId":  1,
		"price":   19.99,
		"plain":   42,
	}
	for field, want := range numbers {
		if got[field] != want {
			t.Errorf("%s = %v, want %v", field, got[field], want)
		}
	}
	if got["body"] != "Example body text." {
		t.Errorf("body = %v, want a sentence rather than \"string\"", got["body"])
	}
}

func TestOpenAPIAllOfMerges(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /a:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                allOf:
                  - type: object
                    properties:
                      one: {type: string}
                  - type: object
                    properties:
                      two: {type: boolean}
                properties:
                  three: {type: integer}
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := body(t, find(t, result.Routes, "GET", "/a"))
	for _, key := range []string{"one", "two", "three"} {
		if _, ok := got[key]; !ok {
			t.Errorf("merged object is missing %q; got %v", key, got)
		}
	}
}

func TestOpenAPINonJSONResponse(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /health:
    get:
      responses:
        "200":
          description: ok
          content:
            text/plain:
              example: ok
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	route := find(t, result.Routes, "GET", "/health")
	// A text example is written as text, not as a JSON string.
	if string(route.Body) != "ok" {
		t.Errorf("body = %q, want ok", route.Body)
	}
	if route.Headers["Content-Type"] != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", route.Headers["Content-Type"])
	}
}

func TestOpenAPIWarnsOnMissingResponses(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /a:
    get: {operationId: noResponses}
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	route := find(t, result.Routes, "GET", "/a")
	if route.Status != 200 {
		t.Errorf("status = %d, want a fallback 200", route.Status)
	}
	if len(result.Warnings) == 0 {
		t.Error("no warning for an operation with no responses")
	} else if !strings.Contains(result.Warnings[0], "no responses documented") {
		t.Errorf("warning = %q, want it to mention the missing responses", result.Warnings[0])
	}
}

func TestLoadSwagger2(t *testing.T) {
	path := write(t, "swagger.json", `{
	"swagger": "2.0",
	"info": {"title": "T", "version": "1"},
	"basePath": "/api",
	"produces": ["application/json"],
	"paths": {
		"/users/{userId}": {
			"get": {
				"operationId": "getUser",
				"responses": {
					"200": {
						"description": "ok",
						"schema": {"$ref": "#/definitions/User"}
					}
				}
			}
		}
	},
	"definitions": {
		"User": {
			"type": "object",
			"properties": {
				"id": {"type": "integer"},
				"email": {"type": "string", "format": "email"}
			}
		}
	}
}`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Format != "swagger" {
		t.Errorf("Format = %q, want swagger", result.Format)
	}

	// basePath is applied and the {userId} template is rewritten.
	route := find(t, result.Routes, "GET", "/api/users/:userId")
	got := body(t, route)
	if got["email"] != "user@example.com" {
		t.Errorf("email = %v, want the email sample", got["email"])
	}
	if _, ok := got["id"].(float64); !ok {
		t.Errorf("id = %v, want a number", got["id"])
	}
	if route.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", route.Headers["Content-Type"])
	}
}

// An explicit --base-path replaces the one derived from the spec.
func TestBasePathOverride(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
servers:
  - url: https://api.example.com/from-spec
paths:
  /a:
    get:
      responses:
        "200": {description: ok}
`)

	result, err := Load(path, Options{BasePath: "/override"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	find(t, result.Routes, "GET", "/override/a")
}

// A templated server URL has no single value, so it is not used as a prefix.
func TestTemplatedServerURLIgnored(t *testing.T) {
	path := write(t, "openapi.yaml", `
openapi: 3.0.0
info: {title: T, version: "1"}
servers:
  - url: "https://{tenant}.example.com/{version}"
paths:
  /a:
    get:
      responses:
        "200": {description: ok}
`)

	result, err := Load(path, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	find(t, result.Routes, "GET", "/a")
}

func TestOpenAPIWithoutPathsFails(t *testing.T) {
	path := write(t, "openapi.yaml", "openapi: 3.0.0\ninfo: {title: T, version: \"1\"}\npaths: {}\n")
	if _, err := Load(path, Options{}); err == nil {
		t.Error("Load succeeded on a spec with no paths, want an error")
	}
}

// The bundled example spec must stay loadable, since the README points at it.
func TestBundledExampleSpecLoads(t *testing.T) {
	result, err := Load("../../examples/openapi.yaml", Options{})
	if err != nil {
		t.Fatalf("Load examples/openapi.yaml: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", result.Warnings)
	}

	// The spec's server URL puts everything under /v1.
	find(t, result.Routes, "GET", "/v1/posts")
	find(t, result.Routes, "POST", "/v1/posts")
	find(t, result.Routes, "GET", "/v1/posts/:postId")
	find(t, result.Routes, "DELETE", "/v1/posts/:postId")
	find(t, result.Routes, "GET", "/v1/posts/:postId/comments")

	// The inline example on /authors/me wins over its schema.
	author := body(t, find(t, result.Routes, "GET", "/v1/authors/me"))
	if author["name"] != "Ada Lovelace" {
		t.Errorf("author name = %v, want the inline example", author["name"])
	}

	// minItems: 3 is honoured.
	comments := find(t, result.Routes, "GET", "/v1/posts/:postId/comments")
	var list []any
	if err := json.Unmarshal(comments.Body, &list); err != nil {
		t.Fatalf("decode comments: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("got %d comments, want the 3 minItems requires", len(list))
	}
}

func TestBundledExampleRouteListLoads(t *testing.T) {
	result, err := Load("../../examples/mocks.yaml", Options{})
	if err != nil {
		t.Fatalf("Load examples/mocks.yaml: %v", err)
	}
	find(t, result.Routes, "GET", "/api/users")
	find(t, result.Routes, "GET", "/api/users/me")
	find(t, result.Routes, "GET", "/api/users/:id")
	find(t, result.Routes, "GET", "/api/legacy/*")

	// The referenced products.json is read relative to the config file.
	products := find(t, result.Routes, "GET", "/api/products")
	var list []any
	if err := json.Unmarshal(products.Body, &list); err != nil {
		t.Fatalf("decode products: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("got %d products, want 3", len(list))
	}
}

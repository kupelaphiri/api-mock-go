# api-mock-go

Blazing-fast mock APIs from OpenAPI specs. A single Go binary, installed through
npm.

```bash
npx api-mock-go openapi.yaml
```

```
  api-mock-go v0.2.0
  source  openapi.yaml (openapi)
  listen  http://127.0.0.1:3000
  routes  8

    GET     /v1/posts                   200  List posts
    POST    /v1/posts                   201  Create a post
    GET     /v1/posts/:postId           200  Fetch one post
    PATCH   /v1/posts/:postId           200  Update a post
    DELETE  /v1/posts/:postId           204  Delete a post
    GET     /v1/posts/:postId/comments  200  List a post's comments
    GET     /v1/authors/me              200  Fetch the authenticated author
    GET     /v1/health                  200  Liveness probe

  GET /__mock/routes lists these routes as JSON
  Ctrl+C to stop
```

Point it at an OpenAPI spec and every operation becomes a live endpoint. No
config to write, no Node runtime in the request path.

## Why api-mock-go?

- **No config for the common case** — an OpenAPI spec is already a description
  of your API. Mock it directly instead of restating it in a fixtures file.
- **Response bodies that look real** — the generator reads your schemas and
  fills fields by name and format, so you get `"jane@example.com"` and
  `"2024-01-15T09:30:00Z"`, not `"string"` in every slot.
- **Fast, and cheap to keep running** — bodies are serialised once at startup,
  so serving a request is a route lookup and a write. Route matching allocates
  nothing.
- **Installs like a JS tool, runs like a Go one** — one binary, no runtime
  dependencies, nothing fetched by a postinstall script.
- **Works everywhere** — macOS, Linux and Windows, on x64 and arm64.

## Install

```bash
npm install -D api-mock-go
# or run it without installing
npx api-mock-go openapi.yaml
```

The binaries ship as per-platform optional dependencies, so npm downloads only
the one your machine needs and installs work offline and under
`npm ci --ignore-scripts`.

With a Go toolchain instead:

```bash
go install github.com/kupelaphiri/api-mock-go/cmd/api-mock-go@latest
```

## Mocking an OpenAPI spec

Any OpenAPI 3.x or Swagger 2.0 document works, as YAML or JSON:

```bash
api-mock-go openapi.yaml --port 4000 --cors
```

Each operation becomes one route. For each, api-mock-go picks the response to
serve and builds its body:

- The status is the **lowest documented 2xx**, falling back to `default`, then
  to whatever is documented. A `204` serves no body.
- The body is the media type's **`example`** if there is one, then the first
  entry under **`examples`**, and only then a value **generated from the
  schema**. Your own examples always win.
- The content type is `application/json` when the operation offers it, and
  otherwise the first type it does offer.
- The spec's first `servers` URL supplies the base path, so a spec served at
  `https://api.example.com/v1` mocks under `/v1`. Override with `--base-path`.

Try it against the bundled spec:

```bash
api-mock-go examples/openapi.yaml --cors
curl http://127.0.0.1:3000/v1/posts
```

### What the generator understands

| | |
| --- | --- |
| Types | `object`, `array`, `string`, `integer`, `number`, `boolean`, `null`, and OpenAPI 3.1 type lists like `["string", "null"]` |
| References | `$ref` to anywhere in the document (`#/components/schemas/…`, `#/definitions/…`). Recursive schemas stop cleanly instead of looping |
| Composition | `allOf` members are merged; `oneOf` and `anyOf` take the first non-null branch |
| Values | `example`, `examples`, `default` and `enum` are used as given |
| Constraints | `minimum`, `maximum`, `minLength`, `maxLength`, `minItems`, `maxItems` |
| Formats | `date-time`, `date`, `time`, `email`, `uuid`, `uri`, `hostname`, `ipv4`, `ipv6`, `byte`, `password` and more |
| Field names | `email`, `createdAt`, `avatarUrl`, `page`, `total`, `price`, `firstName` … resolve to values that suit the name, in camelCase, snake_case or kebab-case |

Generated values are deterministic: the same spec always produces the same
bodies, so a snapshot test will not flake.

Anything the generator cannot mock faithfully is reported as a warning at
startup rather than passed over in silence.

## Writing responses by hand

When you want exact control over a body — or you have no spec — use a route
list. Same flag; the format is detected from the file:

```yaml
# mocks.yaml
port: 3000
cors: true

routes:
  - path: /api/users
    method: GET
    response:
      - { id: 1, name: Jane Doe, email: jane@example.com }
      - { id: 2, name: John Smith, email: john@example.com }

  # `method` also accepts a list.
  - path: /api/users
    method: [POST, PUT]
    status: 201
    response: { id: 3, success: true }

  # `:id` captures a path parameter.
  - path: /api/users/:id
    method: GET
    response: { id: 1, name: Jane Doe }

  # A static segment always wins over a parameter, whatever the order here.
  - path: /api/users/me
    method: GET
    response: { id: 99, name: Current User }

  # Simulate latency, for testing loading states and timeouts.
  - path: /api/slow
    method: GET
    delay: 2s
    response: { message: That took a while. }

  # Set any status and headers, including error responses.
  - path: /api/protected
    method: GET
    status: 401
    headers: { WWW-Authenticate: Bearer }
    response: { error: unauthorized }

  # `body` is written verbatim, for responses that are not JSON.
  - path: /api/health
    method: GET
    headers: { Content-Type: text/plain; charset=utf-8 }
    body: ok

  # `file` serves a file, resolved relative to this config.
  - path: /api/products
    method: GET
    file: ./products.json

  # `*` is a catch-all: this prefix and everything below it.
  - path: /api/legacy/*
    method: GET
    status: 410
    response: { error: gone }
```

```bash
api-mock-go mocks.yaml
```

A runnable version of the above is in
[examples/mocks.yaml](examples/mocks.yaml).

### Route fields

| Field | Default | Meaning |
| --- | --- | --- |
| `path` | *required* | Path to serve. `:name` captures a segment, `*` catches all |
| `method` | `GET` | One method, or a list of them |
| `status` | `200` | Response status |
| `headers` | — | Response headers. A `Content-Type` you set is never overwritten |
| `delay` | — | Latency before responding, e.g. `250ms` or `2s` |
| `response` | — | Any value, serialised as JSON |
| `body` | — | A raw body, written verbatim |
| `file` | — | A file whose contents become the body |

`port`, `host`, `cors` and `delay` may also be set at the top level of the file
as defaults. A flag you pass on the command line always wins.

## CLI

```
api-mock-go <file>              Source file to mock (OpenAPI spec or route list)
api-mock-go --schema <file>     Names the source file; the same as passing it
api-mock-go --config <file>     Alias for --schema
api-mock-go --port <number>     Port to listen on (default 3000)
api-mock-go --host <address>    Address to bind to (default 127.0.0.1)
api-mock-go --delay <dur>       Latency added to every response, e.g. 250ms
api-mock-go --base-path <p>     Prefix every route, e.g. /api/v1
api-mock-go --cors              Send permissive CORS headers, answer preflights
api-mock-go --quiet             Do not log requests
api-mock-go --routes            Print the resolved routes and exit
api-mock-go --version           Print the version and exit
```

`--host` defaults to `127.0.0.1`, so the mock is reachable only from your own
machine. Pass `--host 0.0.0.0` to expose it to your network — to a phone or a
container on the same LAN, for instance. It serves whatever your spec says
without authentication, so keep it off untrusted networks.

## Behaviour worth knowing

- **`GET /__mock/routes`** returns the served routes as JSON. A route of your
  own at that path takes precedence.
- **Unmatched paths** return `404` with a JSON body naming the path and pointing
  at `/__mock/routes`.
- **A mocked path with an unmocked method** returns `405` with an `Allow`
  header, rather than a `404` that would send you looking for a typo.
- **Path parameters** are matched but not interpolated into responses: a route
  serves the same body for every value of `:id`.
- **Request bodies** are not validated. `POST` and `PUT` return their mocked
  response whatever you send.
- **Query strings** do not affect matching.

## Benchmarks

Measured on an Intel Core Ultra 7 155H (22 threads, WSL2), serving
`examples/openapi.yaml` to 50 concurrent keep-alive workers for 5 seconds. The
load generator shared the same machine, so these are a floor rather than a
ceiling.

| Endpoint | Body | Throughput | p50 | p99 |
| --- | --- | --- | --- | --- |
| `/v1/health` | 2 B | **59,095 req/sec** | 511 µs | 3.75 ms |
| `/v1/posts/x` | 410 B | **58,898 req/sec** | 514 µs | 3.67 ms |
| `/v1/posts` | 874 B | **58,382 req/sec** | 506 µs | 3.86 ms |

Throughput barely moves with body size, because bodies are serialised once at
startup — a request costs a route lookup and a write, not a marshal.

The request log is the most expensive thing in the request path. The numbers
above are with `--quiet`; with logging on, `/v1/health` serves 37,119 req/sec.
Reach for `--quiet` when the mock is under load in CI.

In-process, from `make bench`:

```
BenchmarkLookupStatic-22       48.19 ns/op     0 B/op    0 allocs/op
BenchmarkLookupParams-22       99.06 ns/op     0 B/op    0 allocs/op
BenchmarkServeStaticRoute-22   502.8 ns/op    48 B/op    3 allocs/op
BenchmarkServeParamRoute-22    508.7 ns/op    48 B/op    3 allocs/op
BenchmarkServeWithLogging-22    1553 ns/op   176 B/op   14 allocs/op
```

### Against Prism

[Prism](https://github.com/stoplightio/prism) is the closest comparison: it also
mocks an OpenAPI spec directly. Both were run on the same machine, against the
same two spec files, with the same load generator.

| | api-mock-go | Prism 5.16.0 |
| --- | --- | --- |
| **Small spec** (8 operations) | | |
| Ready to serve | **0.04 s** | 1.49 s |
| Throughput | **51,950 req/sec** | 780 req/sec |
| Latency p50 / p99 | **565 µs** / 4.2 ms | 54 ms / 141 ms |
| Memory | **8.6 MB** | 158 MB |
| **Large spec** (290 paths, 580 operations, 530 schemas) | | |
| Ready to serve | **0.09 s** | 1.83 s |
| Throughput | **57,131 req/sec** | 264 req/sec |
| Latency p50 / p99 | **515 µs** / 4.3 ms | 134 ms / 2.58 s |
| Memory | **15.2 MB** | 179 MB |
| **Install** | | |
| npm packages | **2** | 180 |
| Installed size | **5.8 MB** | 69 MB |

Two caveats, in fairness. Prism was run on Node 22 while it declares a
requirement of Node ≥ 24.18, which npm warned about; and Prism validates every
incoming request against the spec, which api-mock-go does not do at all. Some of
the gap is Prism doing more work, not just doing it more slowly.

### Generated body quality

The same schema, from the same spec, at the same moment:

```jsonc
// api-mock-go
{"title": "Example title", "slug": "example-slug", "body": "Example body text.",
 "author": {"name": "Jane Doe", "email": "user@example.com"}, "viewCount": 10}

// Prism
{"title": "string", "slug": "string", "body": "string",
 "author": {"name": "string", "email": "user@example.com"}, "viewCount": 0}
```

Both resolve `format: email` and `format: uuid`. The difference is field names:
api-mock-go reads `title`, `slug`, `body` and `name` and fills them with
something that looks like the thing they are named after. A mock full of
`"string"` is fine for checking that a field exists, and poor for building a UI
against.

Note also that Prism serves `/posts` where api-mock-go serves `/v1/posts`:
api-mock-go applies the base path from the spec's `servers` URL, Prism does not.

### Reproducing these numbers

The generator and load harness are not in the repository — they were throwaway
scripts. What is reproducible from here is `make bench`, and the load figures
came from 50 concurrent keep-alive workers over 5 seconds against a locally
built binary.

## Building from source

Requires Go 1.23+. `make help` lists every target.

```bash
make build     # build ./api-mock-go for this machine
make test      # run the tests
make check     # gofmt check, go vet, tests — what CI runs
make bench     # benchmarks
make dist      # cross-compile every released platform into dist/
make run       # run against examples/openapi.yaml
```

`make` is a convenience, not a requirement. The equivalents:

```bash
go build -o api-mock-go ./cmd/api-mock-go
go test ./...
gofmt -l . && go vet ./... && go test ./...
go test -bench=. -benchmem -run=NONE ./...
bash scripts/build.sh
go run ./cmd/api-mock-go examples/openapi.yaml
```

The npm packages are assembled by `make npm-pack`, which needs Node 16+ on
`PATH`. It builds the binaries, writes one platform package per target plus the
launcher into `dist/npm/`, and smoke-tests the launcher on the host platform.

Before publishing for real, rehearse it:

```bash
make test-publish
```

That runs a throwaway [Verdaccio](https://verdaccio.org) registry on localhost,
publishes every package to it, and then installs `api-mock-go` from it the way a
stranger would. It needs no npm account and never contacts the public registry.
It checks the things a tarball install cannot: that npm resolves the correct
platform package unaided, that it installs only that one rather than all six,
that the binary survives the round trip with its executable bit, that the whole
thing still works under `--ignore-scripts`, and that omitting optional
dependencies produces an error a reader can act on.

When it passes, publish with `npm login && VERSION=x.y.z PUBLISH=1
bash scripts/npm-pack.sh`. Platform packages go first, so the launcher never
resolves a version that does not exist yet.

## Roadmap

- [x] OpenAPI 3.x and Swagger 2.0 specs
- [x] Hand-written route lists in YAML or JSON
- [x] Response bodies generated from schemas
- [x] Path parameters and catch-alls
- [x] Response delay simulation
- [x] CORS and preflight handling
- [x] Request logging
- [x] Cross-platform binaries, distributed through npm
- [ ] Interpolate path parameters into responses
- [ ] Request body validation against the spec
- [ ] Stateful mocks (a `POST` that changes what `GET` returns)
- [ ] Proxy-and-record against a real API
- [ ] Scenario switching (success, error, empty) without editing the spec
- [ ] GraphQL

## Contributing

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). The roadmap
above is a good place to look for something to pick up.

## Built in public

This project is being developed in the open. Follow along:

- **LinkedIn** — weekly updates and learnings
- **Dev.to** — technical deep dives
- **GitHub Discussions** — community chat

Questions? Open an issue or start a discussion.

## License

MIT — see [LICENSE](LICENSE).

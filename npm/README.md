# api-mock-go

Blazing-fast mock APIs from OpenAPI specs. A single Go binary, installed
through npm — no Go toolchain and no Node runtime in the request path.

```bash
npx api-mock-go --schema openapi.yaml
```

```
  api-mock-go v0.1.0
  source  openapi.yaml (openapi)
  listen  http://127.0.0.1:3000
  routes  8

    GET     /v1/posts                   200  List posts
    POST    /v1/posts                   201  Create a post
    GET     /v1/posts/:postId           200  Fetch one post
    PATCH   /v1/posts/:postId           200  Update a post
    DELETE  /v1/posts/:postId           204  Delete a post
    ...
```

Point it at an OpenAPI 3.x or Swagger 2.0 spec and every operation becomes a
live endpoint, with response bodies built from your schemas — real-looking
values, not `"string"` everywhere. No config to write.

## Install

```bash
npm install -D api-mock-go
```

The binaries ship as per-platform optional dependencies, so npm downloads only
the one your machine needs. Nothing is fetched by a postinstall script, which
means installs work offline and under `npm ci --ignore-scripts`.

Prebuilt for macOS, Linux and Windows on x64 and arm64.

## Usage

```bash
# Mock an OpenAPI spec
npx api-mock-go --schema openapi.yaml --port 4000 --cors

# Or write the responses yourself
npx api-mock-go --config mocks.yaml

# See what would be served, without starting a server
npx api-mock-go --schema openapi.yaml --routes
```

| Flag | Meaning |
| --- | --- |
| `--schema <file>` | Source file to mock (OpenAPI spec or route list) |
| `--config <file>` | Alias for `--schema` |
| `--port <number>` | Port to listen on (default `3000`) |
| `--host <address>` | Address to bind to (default `127.0.0.1`) |
| `--delay <dur>` | Latency added to every response, e.g. `250ms` |
| `--base-path <p>` | Prefix every route, e.g. `/api/v1` |
| `--cors` | Send permissive CORS headers and answer preflights |
| `--quiet` | Do not log requests |
| `--routes` | Print the resolved routes and exit |

## Writing responses by hand

When you want exact control, use a route list instead of a spec:

```yaml
port: 3000
cors: true

routes:
  - path: /api/users
    method: GET
    response:
      - { id: 1, name: Jane Doe }
      - { id: 2, name: John Smith }

  - path: /api/users/:id
    method: GET
    response: { id: 1, name: Jane Doe }

  - path: /api/slow
    method: GET
    delay: 2s
    response: { message: That took a while. }
```

Full documentation, including the schema features the generator understands, is
in the [project README](https://github.com/kupelaphiri/api-mock-go#readme).

## License

MIT

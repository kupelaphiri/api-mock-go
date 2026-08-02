# api-mock-go

Blazing-fast mock APIs from OpenAPI specs. A single Go binary, installed
through npm — no Go toolchain and no Node runtime in the request path.

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

## Quickstart

Nothing to configure and nothing to install first — `npx` will fetch the binary
for your platform and run it.

**If you have an OpenAPI spec**, point at it:

```bash
npx api-mock-go openapi.yaml
```

**If your backend serves one**, save it first. FastAPI publishes
`/openapi.json`, Spring `/v3/api-docs`, ASP.NET `/swagger/v1/swagger.json`:

```bash
curl -o openapi.json http://localhost:8000/openapi.json
npx api-mock-go openapi.json
```

**If you have no spec**, write the routes yourself. Save this as `mocks.yaml`:

```yaml
routes:
  - path: /api/users
    method: GET
    response:
      - { id: 1, name: Jane Doe }
      - { id: 2, name: John Smith }
```

```bash
npx api-mock-go mocks.yaml
```

Either way you get a server on `http://127.0.0.1:3000`. Check it:

```bash
curl http://127.0.0.1:3000/api/users
```

To see what a file will serve without starting anything, add `--routes`. It
prints the resolved routes and exits — the fastest way to check your YAML
parsed the way you meant:

```bash
npx api-mock-go mocks.yaml --routes
```

The file may be an OpenAPI 3.x spec, a Swagger 2.0 spec, or a route list, as
YAML or JSON; the format is detected from its contents. Flags may come before
or after the filename.

## Using it with a frontend dev server

Three things bite people here, all avoidable.

**Pick a different port.** api-mock-go defaults to 3000, and so do Next.js and
Create React App. Vite uses 5173. Move the mock out of the way:

```bash
npx api-mock-go mocks.yaml --port 4000
```

**Turn on CORS.** Your app is served from one origin and the mock from another,
so every request is cross-origin and the browser blocks it by default:

```bash
npx api-mock-go mocks.yaml --port 4000 --cors
```

**Point your client at it** with an environment variable, so nothing in your
source has to change between mock and real:

```bash
# .env.local  (Vite: VITE_API_BASE_URL, and read import.meta.env)
NEXT_PUBLIC_API_BASE_URL=http://localhost:4000
```

Then add it to your project so the whole team runs it the same way:

```bash
npm install -D api-mock-go
```

```json
{
  "scripts": {
    "mock": "api-mock-go mocks.yaml --port 4000 --cors"
  }
}
```

Inside an npm script you can drop `npx` — npm puts `node_modules/.bin` on the
PATH. Run `npm run mock` alongside your dev server.

## Flags

| Flag | Meaning |
| --- | --- |
| `--schema <file>` | Names the source file; the same as passing it directly |
| `--config <file>` | Alias for `--schema` |
| `--port <number>` | Port to listen on (default `3000`) |
| `--host <address>` | Address to bind to (default `127.0.0.1`) |
| `--delay <dur>` | Latency added to every response, e.g. `250ms` |
| `--base-path <p>` | Prefix every route, e.g. `/api/v1` |
| `--cors` | Send permissive CORS headers and answer preflights |
| `--quiet` | Do not log requests |
| `--routes` | Print the resolved routes and exit |

## More of the route list

Beyond the minimal example above, a route list can capture path parameters,
per-route latency, and the flags themselves — so `npm run mock` needs no
arguments and the settings live with the fixtures:

```yaml
port: 4000
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

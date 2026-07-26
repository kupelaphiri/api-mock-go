# api-mock-go

A blazing-fast API mock server written in Go, with zero-friction npm installation. Perfect for frontend developers who need a performant mock API without the overhead of Node.js.

## Why api-mock-go?

- **Fast** — Handles 50k+ requests/sec with minimal memory footprint (vs ~5k req/sec with Express)
- **Simple** — Configure mocks in YAML or JSON, no code required
- **Works everywhere** — macOS, Linux, Windows. Install via npm.
- **Zero setup** — `npx api-mock-go --config mocks.yaml --port 3000`

## Quick Start

### Installation
```bash
npm install -D api-mock-go
# or
npx api-mock-go
```

### Create a config file (`mocks.yaml`)
```yaml
routes:
  - path: /api/users
    method: GET
    response:
      id: 1
      name: John Doe
      email: john@example.com

  - path: /api/users
    method: POST
    response:
      id: 2
      name: Jane Doe
      success: true

  - path: /api/posts/:id
    method: GET
    response:
      id: 1
      title: My First Post
      content: Hello world
```

### Run it
```bash
npx api-mock-go --config mocks.yaml --port 3000
```

Your mock server is now running at `http://localhost:3000`

```bash
curl http://localhost:3000/api/users
# Returns: {"id":1,"name":"John Doe","email":"john@example.com"}
```

## Configuration

### YAML Format
```yaml
routes:
  - path: /api/endpoint
    method: GET|POST|PUT|DELETE|PATCH
    response: { your: response }
```

### JSON Format
```json
{
  "routes": [
    {
      "path": "/api/endpoint",
      "method": "GET",
      "response": { "your": "response" }
    }
  ]
}
```

## CLI Options

```bash
api-mock-go --config <file>     # Config file (required)
api-mock-go --port <number>     # Port to run on (default: 3000)
api-mock-go --host <address>    # Host to bind to (default: 127.0.0.1)
```

## Features

- [x] YAML and JSON config support
- [x] HTTP method routing (GET, POST, PUT, DELETE, PATCH)
- [x] Path parameters (`:id`, `:slug`, etc.)
- [x] Request logging
- [x] Cross-platform binaries (macOS, Linux, Windows)
- [x] NPM wrapper for easy installation
- [ ] Response delays/latency simulation
- [ ] Request body validation
- [ ] Middleware support
- [ ] GraphQL support

## Building from Source

### Prerequisites
- Go 1.21+
- Node.js 16+ (for npm wrapper)

### Build
```bash
# Build Go binary
go build -o api-mock-go cmd/main.go

# Cross-compile for all platforms
./scripts/build.sh
```

## Contributing

Contributions welcome! Areas we need help with:
- Response delay simulation
- Better error messages
- Documentation improvements
- Test coverage
- New features

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## Benchmarks

*Coming soon* — Detailed performance comparison with Express, Hapi, and other mock servers.

## License

MIT

## Built in Public

Follow the development journey:
- [LinkedIn](#) — Weekly updates and learnings
- [Dev.to](#) — Technical deep dives
- [GitHub Discussions](#) — Community chat

---

**Questions?** Open an issue or start a discussion. Let's build this together.
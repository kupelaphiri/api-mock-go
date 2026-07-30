# Contributing to api-mock-go

Thanks for taking a look. Bug reports, ideas and pull requests are all welcome.

## Getting set up

You need Go 1.23 or newer. Node 16+ is only needed to work on the npm wrapper.

```bash
git clone https://github.com/kupelaphiri/api-mock-go.git
cd api-mock-go
make check    # gofmt check, go vet and the tests
make run      # serve examples/openapi.yaml on :3000
```

`make help` lists every target. If you would rather not install make:

```bash
go test ./...
go run ./cmd/api-mock-go --schema examples/openapi.yaml
```

## Layout

| Path | What lives there |
| --- | --- |
| [cmd/api-mock-go/](cmd/api-mock-go/) | The CLI: flags, wiring, signal handling |
| [internal/config/](internal/config/) | Loading specs and route lists into resolved routes |
| [internal/router/](internal/router/) | The route-matching tree |
| [internal/server/](internal/server/) | The HTTP server, request log and startup banner |
| [examples/](examples/) | Sample inputs, also used by the tests |
| [scripts/](scripts/) | Cross-compilation and npm packaging |
| [npm/](npm/) | The npm launcher package |

Two ideas shape the design, and are worth knowing before you change things:

**Work happens at load time, not per request.** `internal/config` resolves every
route to a `Route` with its body already serialised, so `ServeHTTP` only has to
match a path and write bytes. If you add a feature, prefer doing its work once
during loading.

**Route matching does not allocate.** `Router.Lookup` walks the request path in
place and captures parameters into a buffer the caller supplies from a pool.
`TestLookupDoesNotAllocate` will fail if that stops being true — that is
deliberate, so please fix the allocation rather than the test.

**The CLI takes its world as parameters.** `run(ctx, args, stdout, stderr)` is
the entire program; `main` only supplies the real context and streams and turns
an error into an exit code. Cancelling the context stops the server exactly as
Ctrl+C does, which is how the tests exercise the serving path without sending
signals — self-signalling is not portable to Windows, and CI runs there. Keep
new work inside `run`, and reach for the passed-in writers rather than `os.Stdout`.

## Making a change

1. Open an issue first for anything large, so we can agree on the shape.
2. Add a test. `internal/config` tests load a spec from a temp file and assert
   on the resolved routes; `internal/server` tests drive `ServeHTTP` through
   `httptest`; `cmd/api-mock-go` tests call `run` with an argument list and
   read back what a user would see. There are plenty of all three to copy from.
3. Run `make check` before pushing. CI runs the same thing on Linux, macOS and
   Windows.
4. Keep the README's flag list and route-field table in step with the code. The
   README is the documentation, and a stale flag table is worse than none.

## Things we would like help with

The roadmap in the [README](README.md#roadmap) lists what is planned. A few of
those with notes:

- **Interpolating path parameters into responses** — `GET /users/:id` returning
  the `id` you asked for. The parameters are already captured; what is missing
  is a templating step, and a decision about what the syntax should be.
- **Request body validation** — the spec's `requestBody` schema is already
  parsed and discarded. Validating against it, behind a flag, would catch a
  class of frontend bug that mocks usually hide.
- **Stateful mocks** — a `POST` that changes what a later `GET` returns. The
  interesting design question is how to express that in a route list without
  turning the config into a programming language.
- **Scenario switching** — serving the `404` branch of a spec instead of the
  `200`, chosen by a header or a flag, so error paths can be exercised without
  editing the spec.

Smaller and always useful: more schema formats and field-name hints in
[internal/config/schema.go](internal/config/schema.go), clearer error messages,
and specs from real APIs that mock badly — those make the best bug reports.

## Reporting a bug

Please include the spec or config that reproduces it, trimmed to the smallest
version that still misbehaves, plus what you expected and what you got.
`api-mock-go --schema yourfile.yaml --routes` prints how the file was
interpreted, which is usually the fastest way to see where things diverged.

## Releasing

Maintainers only:

```bash
git tag v1.2.3 && git push --tags
VERSION=1.2.3 PUBLISH=1 ./scripts/npm-pack.sh
```

Platform packages publish before the launcher, so the launcher never resolves a
version that does not exist yet.

## License

Contributions are accepted under the [MIT License](LICENSE).

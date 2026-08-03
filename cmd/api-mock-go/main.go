// Command api-mock-go serves mock HTTP APIs from an OpenAPI specification or
// a route list.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kupelaphiri/api-mock-go/internal/config"
	"github.com/kupelaphiri/api-mock-go/internal/server"
)

const usage = `api-mock-go — mock APIs from OpenAPI specs

Usage:
  api-mock-go <file> [options]

The input may be an OpenAPI 3.x spec, a Swagger 2.0 spec, or an api-mock-go
route list, written as YAML or JSON. The format is detected from its contents.
The file may also be named with --schema or --config, which are older spellings
of the same thing and interchangeable with each other.

Options:
  --schema <file>    Source file to mock; the same as naming it directly
  --config <file>    Alias for --schema
  --port <number>    Port to listen on (default 3000)
  --host <address>   Address to bind to (default 127.0.0.1; use 0.0.0.0 to
                     accept connections from other machines)
  --delay <dur>      Latency added to every response, e.g. 250ms or 1s.
                     Routes with their own delay keep it.
  --base-path <p>    Prefix every route with p, e.g. /api/v1
  --cors             Send permissive CORS headers and answer preflights
  --quiet            Do not log requests
  --routes           Print the resolved routes and exit
  --version          Print the version and exit
  --help             Print this message

Examples:
  api-mock-go openapi.yaml
  api-mock-go openapi.yaml --port 4000 --cors
  api-mock-go mocks.yaml --delay 200ms
  api-mock-go --schema petstore.json --routes
`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "\napi-mock-go: %v\n\n", err)
		os.Exit(1)
	}
}

// run is the whole program, with everything it touches passed in: cancelling
// ctx stops the server the same way Ctrl+C does, and the writers let a test
// read what a user would see. main supplies the real ones.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("api-mock-go", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }

	var (
		schema      = fs.String("schema", "", "source file to mock")
		configPath  = fs.String("config", "", "alias for --schema")
		port        = fs.Int("port", 3000, "port to listen on")
		host        = fs.String("host", "127.0.0.1", "address to bind to")
		delay       = fs.String("delay", "", "latency added to every response")
		basePath    = fs.String("base-path", "", "prefix for every route")
		cors        = fs.Bool("cors", false, "send permissive CORS headers")
		quiet       = fs.Bool("quiet", false, "do not log requests")
		printRoutes = fs.Bool("routes", false, "print resolved routes and exit")
		showVersion = fs.Bool("version", false, "print the version and exit")
	)

	// flag stops at the first non-flag argument, so a single Parse would read
	// `api-mock-go openapi.yaml --cors` as the file plus one stray word and
	// start without CORS. Parsing until nothing is left, setting each
	// positional aside as it appears, accepts flags on either side of the file.
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}

	if *showVersion {
		fmt.Fprintln(stdout, server.Version)
		return nil
	}

	source, err := resolveSource(positional, *schema, *configPath)
	if err != nil {
		return err
	}

	// Track which flags were given, so a value in the config file can supply a
	// default without silently overriding an explicit flag.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	defaultDelay, err := config.ParseDelay(*delay)
	if err != nil {
		return err
	}

	result, err := config.Load(source, config.Options{
		BasePath:     *basePath,
		DefaultDelay: defaultDelay,
	})
	if err != nil {
		return err
	}

	opts := server.Options{
		Host:  *host,
		Port:  *port,
		CORS:  *cors,
		Quiet: *quiet,
		Out:   stderr,
	}
	applyFileDefaults(&opts, result, set)
	if err := validatePort(opts.Port); err != nil {
		return err
	}

	srv := server.New(result.Routes, opts)

	if *printRoutes {
		srv.Banner(source, result.Format, false)
		reportWarnings(stderr, result.Warnings)
		return nil
	}

	// Bind before printing the banner so a port conflict is not announced as a
	// successful start.
	listener, err := srv.Listen()
	if err != nil {
		return err
	}

	srv.Banner(source, result.Format, true)
	reportWarnings(stderr, result.Warnings)

	// Ctrl+C and SIGTERM stop the server, as does the caller cancelling ctx.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now()
	served, err := srv.Serve(ctx, listener)
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "\n  stopped after %s, %d request(s) served\n\n",
		time.Since(started).Round(time.Second), served)
	return nil
}

// resolveSource picks the input file from --schema, --config, or a bare
// positional argument.
func resolveSource(positional []string, schema, configPath string) (string, error) {
	if schema != "" && configPath != "" && schema != configPath {
		return "", fmt.Errorf("--schema (%s) and --config (%s) disagree; pass only one", schema, configPath)
	}
	if len(positional) > 1 {
		return "", fmt.Errorf("only one source file can be mocked, but %d were given: %s",
			len(positional), strings.Join(positional, ", "))
	}

	source := schema
	if source == "" {
		source = configPath
	}
	if len(positional) == 1 {
		if source == "" {
			source = positional[0]
		} else if positional[0] != source {
			return "", fmt.Errorf("%s was given as an argument but %s was given as a flag; pass only one",
				positional[0], source)
		}
	}
	if source == "" {
		return "", errors.New("no source file given\n\n" +
			"Try: api-mock-go openapi.yaml\n" +
			"Run api-mock-go --help for all options")
	}

	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s does not exist", source)
		}
		return "", err
	}
	return source, nil
}

// applyFileDefaults lets a route list's own port, host and cors settings fill
// in for flags the user did not pass.
func applyFileDefaults(opts *server.Options, result *config.Result, set map[string]bool) {
	if result.Defaults == nil {
		return
	}
	if !set["port"] && result.Defaults.Port != 0 {
		opts.Port = result.Defaults.Port
	}
	if !set["host"] && result.Defaults.Host != "" {
		opts.Host = result.Defaults.Host
	}
	if !set["cors"] && result.Defaults.CORS != nil {
		opts.CORS = *result.Defaults.CORS
	}
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is out of range; want 1-65535", port)
	}
	return nil
}

func reportWarnings(w io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	for _, warning := range warnings {
		fmt.Fprintf(w, "  warning: %s\n", warning)
	}
	fmt.Fprintln(w)
}

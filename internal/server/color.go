package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Version is the build version, set at link time with
// -ldflags "-X .../internal/server.Version=v1.2.3".
var Version = "dev"

func spaces(n int) string {
	if n < 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// colorEnabled reports whether ANSI escapes should be emitted. It is resolved
// once at startup: colour is for a human at a terminal, so it is dropped when
// output is redirected, when NO_COLOR is set, or when TERM says the terminal
// cannot render it.
//
// https://no-color.org
var colorEnabled = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	// FORCE_COLOR is the conventional override for CI logs that do render ANSI.
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	info, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
	ansiCyan   = "\033[36m"
)

func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func dim(s string) string  { return paint(ansiDim, s) }
func bold(s string) string { return paint(ansiBold, s) }

// colorStatus renders an HTTP status code, tinted by class.
func colorStatus(status int) string {
	text := strconv.Itoa(status)
	switch {
	case status >= 500:
		return paint(ansiRed, text)
	case status >= 400:
		return paint(ansiYellow, text)
	case status >= 300:
		return paint(ansiCyan, text)
	default:
		return paint(ansiGreen, text)
	}
}

// colorMethod renders an HTTP method, tinted by whether it mutates state.
func colorMethod(method string) string {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		return paint(ansiBlue, method)
	case "DELETE":
		return paint(ansiRed, method)
	default:
		return paint(ansiYellow, method)
	}
}

// Banner writes the startup summary: where the server is listening and which
// routes it serves. serving distinguishes a real start from a --routes dry run,
// which should not tell the user to press Ctrl+C.
func (s *Server) Banner(source, format string, serving bool) {
	out := s.opts.Out
	routes := s.router.Routes()

	fmt.Fprintf(out, "\n  %s %s\n", bold("api-mock-go"), dim(Version))
	fmt.Fprintf(out, "  %s  %s %s\n", dim("source"), source, dim("("+format+")"))
	fmt.Fprintf(out, "  %s  %s\n", dim("listen"), bold("http://"+s.Addr()))
	if s.opts.CORS {
		fmt.Fprintf(out, "  %s    %s\n", dim("cors"), "enabled (all origins)")
	}
	fmt.Fprintf(out, "  %s  %d\n\n", dim("routes"), len(routes))

	width := 0
	for _, route := range routes {
		if len(route.Path) > width {
			width = len(route.Path)
		}
	}
	if width > 48 {
		width = 48
	}

	for _, route := range routes {
		// Pad before colouring: ANSI escapes have no display width, but fmt
		// counts their bytes, which would misalign every coloured column.
		method := colorMethod(route.Method) + spaces(7-len(route.Method))
		line := fmt.Sprintf("    %s %-*s  %s",
			method, width, route.Path, dim(strconv.Itoa(route.Status)))
		if route.Delay > 0 {
			line += dim(" +" + formatDuration(route.Delay))
		}
		if route.Description != "" {
			line += dim("  " + route.Description)
		}
		fmt.Fprintln(out, line)
	}

	fmt.Fprintf(out, "\n  %s\n", dim("GET "+routesEndpoint+" lists these routes as JSON"))
	if serving {
		fmt.Fprintf(out, "  %s\n", dim("Ctrl+C to stop"))
	}
	fmt.Fprintln(out)
}

// Command main serves the tryc.ai short-link router.
//
// Mapped paths on the link hosts redirect (302) to their target in
// LINKS.json. Other GET/HEAD paths, apart from health and load
// endpoints, redirect (301) to the matching trycopilot.ai host.
// See router.go for the full rule set.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run parses the command line and returns the process exit code: 0
// on success, 1 when LINKS.json is invalid, the listener fails, or
// --healthcheck does not get HTTP 200, and 2 for an invalid flag or
// --port.
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("main", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "0.0.0.0", "address to listen on")
	port := fs.Int("port", 8080, "port to listen on")
	linkHosts := fs.String("link-hosts", "tryc.ai,www.tryc.ai",
		"comma-separated hosts that serve LINKS.json redirects")
	healthcheck := fs.Bool("healthcheck", false,
		"probe http://127.0.0.1:<port>/healthz and exit 0 on HTTP 200, else 1")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *port < 1 || *port > 65535 {
		fmt.Fprintf(stderr, "invalid --port %d: must be between 1 and 65535\n", *port)
		return 2
	}

	if *healthcheck {
		return runHealthcheck(*port, stderr)
	}

	links, err := parseLinks(linksJSON)
	if err != nil {
		fmt.Fprintf(stderr, "invalid LINKS.json: %v\n", err)
		return 1
	}

	srv := newServer(net.JoinHostPort(*host, strconv.Itoa(*port)),
		newRouter(links, parseLinkHosts(*linkHosts)))
	fmt.Fprintf(stderr, "listening on %s\n", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "server stopped: %v\n", err)
		return 1
	}
	return 0
}

// newServer builds the http.Server used by main and by the raw-socket
// tests, so the tests exercise the production settings.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: h,
		// net/http would otherwise answer "OPTIONS *" with 200 before
		// the handler runs. Disabling that lets the router answer it
		// with 405 like other non-GET/HEAD requests admitted by net/http.
		DisableGeneralOptionsHandler: true,
		// Timeouts bound slow clients so a stalled connection cannot
		// hold a goroutine open indefinitely.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}

// runHealthcheck is the container HEALTHCHECK entry point. It
// returns the process exit code.
func runHealthcheck(port int, stderr io.Writer) int {
	// Redirects are not followed: only a direct 200 from this
	// process counts as healthy.
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}

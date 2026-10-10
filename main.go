// Command main serves the tryc.ai short-link router.
//
// Mapped paths on the link hosts redirect (302) to their target in
// LINKS.json. Everything else redirects (301) to the matching
// trycopilot.ai host. See router.go for the full rule set.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	host := flag.String("host", "0.0.0.0", "address to listen on")
	port := flag.Int("port", 8080, "port to listen on")
	linkHosts := flag.String("link-hosts", "tryc.ai,www.tryc.ai",
		"comma-separated hosts that serve LINKS.json redirects")
	healthcheck := flag.Bool("healthcheck", false,
		"probe http://127.0.0.1:<port>/healthz and exit 0 on HTTP 200, else 1")
	flag.Parse()

	if *port < 1 || *port > 65535 {
		fmt.Fprintf(os.Stderr, "invalid --port %d: must be between 1 and 65535\n", *port)
		os.Exit(2)
	}

	if *healthcheck {
		os.Exit(runHealthcheck(*port))
	}

	links, err := parseLinks(linksJSON)
	if err != nil {
		log.Printf("invalid LINKS.json: %v", err)
		os.Exit(1)
	}

	// Timeouts bound slow clients so a stalled connection cannot
	// hold a goroutine open indefinitely.
	srv := &http.Server{
		Addr:              net.JoinHostPort(*host, strconv.Itoa(*port)),
		Handler:           newRouter(links, parseLinkHosts(*linkHosts)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	log.Printf("listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

// runHealthcheck is the container HEALTHCHECK entry point. It
// returns the process exit code.
func runHealthcheck(port int) int {
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
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}

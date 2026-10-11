package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// linksJSON is the shipped short-link table. It is compiled into
// the binary so the container needs no config file at runtime.
//
//go:embed LINKS.json
var linksJSON []byte

const (
	apexHost     = "tryc.ai"
	redirectApex = "https://trycopilot.ai"
	movedBody    = "Moved permanently to trycopilot.ai.\n"

	// maxHostLen bounds the destination host copied into Location.
	// Longer destination hosts fall back to the apex. This is not a
	// check of DNS label lengths or whether the destination resolves.
	maxHostLen = 253
)

// labelPattern guards the subdomain label that is copied into a
// Location header. Anything outside it falls back to the apex.
var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// reservedPaths cannot be used as LINKS.json keys. The router answers
// /health/, /healthz, and /load/ itself, and a key never ends in a
// slash, so these are the keys that would collide with them.
var reservedPaths = map[string]bool{
	"/health":  true,
	"/healthz": true,
	"/load":    true,
}

// parseLinks decodes and validates a LINKS.json document. Keys are
// checked in sorted order so the first error is deterministic.
func parseLinks(data []byte) (map[string]string, error) {
	var links map[string]string
	if err := json.Unmarshal(data, &links); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(links) == 0 {
		return nil, errors.New("no entries")
	}
	keys := make([]string, 0, len(links))
	for key := range links {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := checkKey(key); err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		if err := checkTarget(links[key]); err != nil {
			return nil, fmt.Errorf("key %q target %q: %w", key, links[key], err)
		}
	}
	return links, nil
}

func checkKey(key string) error {
	switch {
	case !strings.HasPrefix(key, "/"):
		return errors.New("must start with /")
	case key == "/":
		return errors.New("must not be /")
	case strings.HasSuffix(key, "/"):
		return errors.New("must not end with /")
	case strings.ContainsAny(key, "?#"):
		return errors.New("must not contain ? or #")
	case reservedPaths[key]:
		return errors.New("collides with a reserved path")
	}
	return nil
}

func checkTarget(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host == "" {
		return errors.New("must be an absolute https URL with a host")
	}
	// A request query is appended to the target with "?", which would
	// land inside a fragment, so fragments are not allowed.
	if strings.Contains(target, "#") {
		return errors.New("must not contain #")
	}
	return nil
}

// parseLinkHosts turns the --link-hosts flag into a lookup set.
func parseLinkHosts(list string) map[string]bool {
	set := make(map[string]bool)
	for _, h := range strings.Split(list, ",") {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
		if h != "" {
			set[h] = true
		}
	}
	return set
}

// router implements the routing rules documented in README.md.
type router struct {
	links     map[string]string
	linkHosts map[string]bool
}

func newRouter(links map[string]string, linkHosts map[string]bool) http.Handler {
	return &router{links: links, linkHosts: linkHosts}
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeBody(w, r, http.StatusMethodNotAllowed, "text/plain; charset=utf-8", "Method not allowed.\n")
		return
	}

	switch r.URL.Path {
	case "/health/", "/healthz":
		writeBody(w, r, http.StatusOK, "text/plain; charset=utf-8", "ok\n")
		return
	case "/load/":
		writeBody(w, r, http.StatusOK, "application/json", `{"load": 0}`)
		return
	}

	host := normalizeHost(r.Host)
	if rt.linkHosts[host] {
		if target, ok := rt.links[linkKey(r.URL.Path)]; ok {
			if r.URL.RawQuery != "" && !strings.Contains(target, "?") {
				target += "?" + r.URL.RawQuery
			}
			// Content-Length is set explicitly so GET and HEAD
			// carry identical headers.
			w.Header().Set("Location", target)
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusFound)
			return
		}
	}

	location := redirectBase(host) + escapedPath(r)
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	w.Header().Set("Location", location)
	writeBody(w, r, http.StatusMovedPermanently, "text/plain; charset=utf-8", movedBody)
}

// writeBody sets Content-Type and Content-Length, writes the status,
// and writes body only for non-HEAD requests. Headers that must be
// present (Location, Allow) are set by the caller first.
func writeBody(w http.ResponseWriter, r *http.Request, status int, contentType, body string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// linkKey removes one trailing slash so /lint/ matches /lint. The
// root path is left alone.
func linkKey(path string) string {
	if len(path) > 1 {
		return strings.TrimSuffix(path, "/")
	}
	return path
}

// normalizeHost lowercases the Host header, strips the port when the
// value parses as host:port (including [v6]:port), and then strips
// one trailing dot.
func normalizeHost(raw string) string {
	host := strings.ToLower(raw)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// redirectBase picks the trycopilot.ai origin for a 301. The apex
// maps to the apex, and <label>.tryc.ai maps to <label>.trycopilot.ai.
// Any other host, a label that fails labelPattern, or a label that
// would make <label>.trycopilot.ai longer than maxHostLen maps to the
// apex origin.
func redirectBase(host string) string {
	if host == apexHost {
		return redirectApex
	}
	if label, ok := strings.CutSuffix(host, "."+apexHost); ok && labelPattern.MatchString(label) {
		if target := label + ".trycopilot.ai"; len(target) <= maxHostLen {
			return "https://" + target
		}
	}
	return redirectApex
}

// escapedPath returns a path that is safe to place in a Location
// header. It always starts with a slash.
func escapedPath(r *http.Request) string {
	p := r.URL.EscapedPath()
	if !strings.HasPrefix(p, "/") {
		return "/"
	}
	return p
}

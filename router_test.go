package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const defaultLinkHosts = "tryc.ai,www.tryc.ai"

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	links, err := parseLinks(linksJSON)
	if err != nil {
		t.Fatalf("shipped LINKS.json is invalid: %v", err)
	}
	return newRouter(links, parseLinkHosts(defaultLinkHosts))
}

func serve(h http.Handler, method, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type routeCase struct {
	name         string
	method       string
	target       string
	host         string
	wantStatus   int
	wantLocation string
	wantAllow    string
	wantType     string
	wantBody     string
}

func checkRoute(t *testing.T, h http.Handler, tc routeCase) {
	t.Helper()
	rec := serve(h, tc.method, tc.target, tc.host)
	if rec.Code != tc.wantStatus {
		t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
	}
	if got := rec.Header().Get("Location"); got != tc.wantLocation {
		t.Errorf("Location = %q, want %q", got, tc.wantLocation)
	}
	if got := rec.Header().Get("Allow"); got != tc.wantAllow {
		t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
	}
	if tc.wantType != "" {
		if got := rec.Header().Get("Content-Type"); got != tc.wantType {
			t.Errorf("Content-Type = %q, want %q", got, tc.wantType)
		}
	}
	if got := rec.Body.String(); got != tc.wantBody {
		t.Errorf("body = %q, want %q", got, tc.wantBody)
	}
}

func TestRoutes(t *testing.T) {
	h := testRouter(t)
	const (
		movedType = "text/plain; charset=utf-8"
		moved     = "Moved permanently to trycopilot.ai.\n"
	)
	cases := []routeCase{
		// Built-in endpoints answer for any Host.
		{name: "health trailing slash", method: "GET", target: "/health/", host: "", wantStatus: 200, wantType: movedType, wantBody: "ok\n"},
		{name: "healthz any host", method: "GET", target: "/healthz", host: "anything.example", wantStatus: 200, wantType: movedType, wantBody: "ok\n"},
		{name: "healthz HEAD has no body", method: "HEAD", target: "/healthz", host: "tryc.ai", wantStatus: 200, wantType: movedType, wantBody: ""},
		{name: "load", method: "GET", target: "/load/", host: "scribe.tryc.ai", wantStatus: 200, wantType: "application/json", wantBody: `{"load": 0}`},
		{name: "load HEAD has no body", method: "HEAD", target: "/load/", host: "", wantStatus: 200, wantType: "application/json", wantBody: ""},

		// Other methods are rejected before routing.
		{name: "POST healthz is 405", method: "POST", target: "/healthz", host: "", wantStatus: 405, wantAllow: "GET, HEAD", wantType: movedType, wantBody: "Method not allowed.\n"},
		{name: "POST on apex is 405", method: "POST", target: "/x", host: "tryc.ai", wantStatus: 405, wantAllow: "GET, HEAD", wantType: movedType, wantBody: "Method not allowed.\n"},
		{name: "PUT on mapped link is 405", method: "PUT", target: "/lint", host: "www.tryc.ai", wantStatus: 405, wantAllow: "GET, HEAD", wantType: movedType, wantBody: "Method not allowed.\n"},

		// Mapped links redirect with 302 on the link hosts.
		{name: "mapped npm link on apex", method: "GET", target: "/x", host: "tryc.ai", wantStatus: 302, wantLocation: "https://www.npmjs.com/package/@tryc.ai/x"},
		{name: "mapped repo link on www", method: "GET", target: "/lint", host: "www.tryc.ai", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint"},
		{name: "mapped link HEAD has no body", method: "HEAD", target: "/lint", host: "www.tryc.ai", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint", wantBody: ""},
		{name: "uppercase host", method: "GET", target: "/lint", host: "WWW.TRYC.AI", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint"},
		{name: "host with port", method: "GET", target: "/lint", host: "tryc.ai:8080", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint"},
		{name: "host with trailing dot", method: "GET", target: "/lint", host: "tryc.ai.", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint"},
		{name: "trailing slash on link", method: "GET", target: "/lint/", host: "tryc.ai", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint"},
		{name: "query appended to link", method: "GET", target: "/x?a=1&b=2", host: "tryc.ai", wantStatus: 302, wantLocation: "https://www.npmjs.com/package/@tryc.ai/x?a=1&b=2"},
		{name: "double trailing slash is not a link", method: "GET", target: "/lint//", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/lint//", wantType: movedType, wantBody: moved},

		// Everything else redirects with 301.
		{name: "unmapped path on apex", method: "GET", target: "/nope", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/nope", wantType: movedType, wantBody: moved},
		{name: "apex root", method: "GET", target: "/", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/", wantType: movedType, wantBody: moved},
		{name: "apex HEAD has no body", method: "HEAD", target: "/nope", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/nope", wantType: movedType, wantBody: ""},
		{name: "link key on non-link subdomain", method: "GET", target: "/lint", host: "scribe.tryc.ai", wantStatus: 301, wantLocation: "https://scribe.trycopilot.ai/lint", wantType: movedType, wantBody: moved},
		{name: "subdomain keeps label and query", method: "GET", target: "/x?a=1", host: "scribe.tryc.ai", wantStatus: 301, wantLocation: "https://scribe.trycopilot.ai/x?a=1", wantType: movedType, wantBody: moved},
		{name: "www on apex family", method: "GET", target: "/nope", host: "www.tryc.ai", wantStatus: 301, wantLocation: "https://www.trycopilot.ai/nope", wantType: movedType, wantBody: moved},
		{name: "nested label", method: "GET", target: "/y", host: "a.b.tryc.ai", wantStatus: 301, wantLocation: "https://a.b.trycopilot.ai/y", wantType: movedType, wantBody: moved},
		{name: "escaped path is preserved", method: "GET", target: "/a%20b", host: "scribe.tryc.ai", wantStatus: 301, wantLocation: "https://scribe.trycopilot.ai/a%20b", wantType: movedType, wantBody: moved},
		{name: "label with underscore falls back", method: "GET", target: "/x", host: "bad_label.tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "label with leading dash falls back", method: "GET", target: "/x", host: "-bad.tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "host with space falls back", method: "GET", target: "/x", host: "a b.tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "unknown host", method: "GET", target: "/x", host: "example.com", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "localhost", method: "GET", target: "/x", host: "localhost:8080", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "empty host", method: "GET", target: "/x", host: "", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},

		// Reserved paths match exactly and case-sensitively.
		{name: "health without slash is not reserved", method: "GET", target: "/health", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/health", wantType: movedType, wantBody: moved},
		{name: "healthz with slash is not reserved", method: "GET", target: "/healthz/", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/healthz/", wantType: movedType, wantBody: moved},
		{name: "uppercase health is not reserved", method: "GET", target: "/Health/", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/Health/", wantType: movedType, wantBody: moved},
		{name: "load without slash is not reserved", method: "GET", target: "/load", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/load", wantType: movedType, wantBody: moved},
		{name: "OPTIONS asterisk is 405", method: "OPTIONS", target: "*", host: "tryc.ai", wantStatus: 405, wantAllow: "GET, HEAD", wantType: movedType, wantBody: "Method not allowed.\n"},
		{name: "CONNECT is 405", method: "CONNECT", target: "tryc.ai:443", host: "tryc.ai", wantStatus: 405, wantAllow: "GET, HEAD", wantType: movedType, wantBody: "Method not allowed.\n"},
		{name: "DELETE on health is 405", method: "DELETE", target: "/health/", host: "", wantStatus: 405, wantAllow: "GET, HEAD", wantType: movedType, wantBody: "Method not allowed.\n"},

		// Link keys are case-sensitive and ignore the query.
		{name: "uppercase link key is not a link", method: "GET", target: "/Lint", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/Lint", wantType: movedType, wantBody: moved},
		{name: "percent-encoded link key decodes to the link", method: "GET", target: "/li%6Et", host: "tryc.ai", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint"},
		{name: "encoded slash in link key is not a link", method: "GET", target: "/%2Flint", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/%2Flint", wantType: movedType, wantBody: moved},
		{name: "empty query on link is dropped", method: "GET", target: "/lint?", host: "tryc.ai", wantStatus: 302, wantLocation: "https://github.com/trycopilotai/lint"},
		{name: "empty query on 301 is dropped", method: "GET", target: "/nope?", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/nope", wantType: movedType, wantBody: moved},
		{name: "link with path on non-link host", method: "GET", target: "/x", host: "go.tryc.ai", wantStatus: 301, wantLocation: "https://go.trycopilot.ai/x", wantType: movedType, wantBody: moved},

		// The 301 Location is always https://, an allowlisted
		// trycopilot.ai host, and a single-slash-rooted path. Path
		// segments are passed through escaped, never cleaned, so a
		// crafted path cannot become a new authority.
		{name: "asterisk target maps to root", method: "GET", target: "*", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/", wantType: movedType, wantBody: moved},
		{name: "double slash path stays a path", method: "GET", target: "//evil.com/", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai//evil.com/", wantType: movedType, wantBody: moved},
		{name: "backslash is escaped", method: "GET", target: `/\evil.com`, host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/%5Cevil.com", wantType: movedType, wantBody: moved},
		{name: "encoded slashes stay encoded", method: "GET", target: "/%2F%2Fevil.com", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/%2F%2Fevil.com", wantType: movedType, wantBody: moved},
		{name: "dot segment is not cleaned", method: "GET", target: "/./x", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/./x", wantType: movedType, wantBody: moved},
		{name: "dot dot segment is not cleaned", method: "GET", target: "/x/../y", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x/../y", wantType: movedType, wantBody: moved},
		{name: "fragment in target is escaped", method: "GET", target: "/x#y", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x%23y", wantType: movedType, wantBody: moved},
		{name: "encoded CRLF in path stays encoded", method: "GET", target: "/x%0d%0aSet-Cookie:a", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x%0d%0aSet-Cookie:a", wantType: movedType, wantBody: moved},
		{name: "encoded CRLF in query stays encoded", method: "GET", target: "/nope?a=%0d%0ab", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/nope?a=%0d%0ab", wantType: movedType, wantBody: moved},
		{name: "at sign in path cannot form userinfo", method: "GET", target: "/@evil.com/", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/@evil.com/", wantType: movedType, wantBody: moved},

		// Host header abuse never escapes the trycopilot.ai allowlist.
		{name: "host with userinfo falls back", method: "GET", target: "/x", host: "user@tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "host with path falls back", method: "GET", target: "/x", host: "evil.com/.tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "host suffix without dot falls back", method: "GET", target: "/x", host: "eviltryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "host with two trailing dots falls back", method: "GET", target: "/x", host: "tryc.ai..", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "host with empty label falls back", method: "GET", target: "/x", host: ".tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "host with trailing dash label falls back", method: "GET", target: "/x", host: "bad-.tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "ipv6 host falls back", method: "GET", target: "/x", host: "[::1]:8080", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "ipv4 host falls back", method: "GET", target: "/x", host: "127.0.0.1", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "unicode host falls back", method: "GET", target: "/x", host: "trüc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "unicode label falls back", method: "GET", target: "/x", host: "ü.tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "punycode label is kept", method: "GET", target: "/x", host: "xn--tda.tryc.ai", wantStatus: 301, wantLocation: "https://xn--tda.trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "uppercase subdomain and dot and port", method: "GET", target: "/x", host: "Scribe.TRYC.AI.:443", wantStatus: 301, wantLocation: "https://scribe.trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "very long label falls back", method: "GET", target: "/x", host: strings.Repeat("a", 70000) + "_.tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "valid label over the DNS limit falls back", method: "GET", target: "/x", host: strings.Repeat("a", 240) + ".tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "valid label at the DNS limit is kept", method: "GET", target: "/x", host: strings.Repeat("a", 239) + ".tryc.ai", wantStatus: 301, wantLocation: "https://" + strings.Repeat("a", 239) + ".trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "reflected label over the DNS limit falls back", method: "GET", target: "/x", host: strings.Repeat("a", 60000) + ".tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/x", wantType: movedType, wantBody: moved},
		{name: "link host with userinfo is not a link host", method: "GET", target: "/lint", host: "a@tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/lint", wantType: movedType, wantBody: moved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkRoute(t, h, tc)
		})
	}
}

func TestQueryNotAppendedWhenTargetHasQuery(t *testing.T) {
	links := map[string]string{"/q": "https://example.com/p?fixed=1"}
	h := newRouter(links, parseLinkHosts(defaultLinkHosts))
	checkRoute(t, h, routeCase{
		name: "target query wins", method: "GET", target: "/q?a=1", host: "tryc.ai",
		wantStatus: 302, wantLocation: "https://example.com/p?fixed=1",
	})
}

func TestRawQueryIsNotReencoded(t *testing.T) {
	h := testRouter(t)
	checkRoute(t, h, routeCase{
		name: "raw query kept verbatim", method: "GET", target: "/x?a=%20b&c", host: "tryc.ai",
		wantStatus: 302, wantLocation: "https://www.npmjs.com/package/@tryc.ai/x?a=%20b&c",
	})
}

// TestAbsoluteFormTarget covers request lines such as
// "GET http://host/path". net/http takes the host from the URL, so
// the Host header is ignored; the router must still stay on the
// allowlist.
func TestAbsoluteFormTarget(t *testing.T) {
	h := testRouter(t)
	cases := []struct {
		target, wantLocation string
		wantStatus           int
	}{
		{"http://tryc.ai/lint", "https://github.com/trycopilotai/lint", 302},
		{"http://scribe.tryc.ai/x?q=1", "https://scribe.trycopilot.ai/x?q=1", 301},
		{"http://evil.com/x", "https://trycopilot.ai/x", 301},
		{"http://evil.com", "https://trycopilot.ai/", 301},
		{"http://user:pw@tryc.ai/x", "https://www.npmjs.com/package/@tryc.ai/x", 302},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.target, nil)
			req.Header.Set("Host", "tryc.ai")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Location"); got != tc.wantLocation {
				t.Errorf("Location = %q, want %q", got, tc.wantLocation)
			}
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"tryc.ai":            "tryc.ai",
		"TRYC.AI":            "tryc.ai",
		"tryc.ai:8080":       "tryc.ai",
		"tryc.ai:":           "tryc.ai",
		"tryc.ai.":           "tryc.ai",
		"tryc.ai..":          "tryc.ai.",
		"Scribe.Tryc.AI.:80": "scribe.tryc.ai",
		"[::1]:8080":         "::1",
		"[::1]":              "[::1]",
		"tryc.ai:80:80":      "tryc.ai:80:80",
		"user@tryc.ai":       "user@tryc.ai",
		"":                   "",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLinkHosts(t *testing.T) {
	set := parseLinkHosts(" TRYC.ai , www.tryc.ai., ,,go.tryc.ai,\tlinks.tryc.ai ")
	for _, want := range []string{"tryc.ai", "www.tryc.ai", "go.tryc.ai", "links.tryc.ai"} {
		if !set[want] {
			t.Errorf("missing %q in %v", want, set)
		}
	}
	if len(set) != 4 {
		t.Errorf("len = %d, want 4: %v", len(set), set)
	}
	if got := parseLinkHosts(""); len(got) != 0 {
		t.Errorf("empty flag parsed to %v", got)
	}
	h := newRouter(map[string]string{"/k": "https://example.com/"}, parseLinkHosts("go.tryc.ai"))
	checkRoute(t, h, routeCase{method: "GET", target: "/k", host: "GO.tryc.ai.", wantStatus: 302, wantLocation: "https://example.com/"})
	checkRoute(t, h, routeCase{method: "GET", target: "/k", host: "tryc.ai", wantStatus: 301, wantLocation: "https://trycopilot.ai/k", wantBody: "Moved permanently to trycopilot.ai.\n"})
}

// TestHeadMatchesGet checks that HEAD returns exactly the headers
// of the matching GET, including Content-Length, with no body.
func TestHeadMatchesGet(t *testing.T) {
	h := testRouter(t)
	for _, tc := range []struct{ target, host string }{
		{"/health/", ""},
		{"/load/", ""},
		{"/lint", "tryc.ai"},
		{"/nope?a=1", "scribe.tryc.ai"},
	} {
		get := serve(h, "GET", tc.target, tc.host)
		head := serve(h, "HEAD", tc.target, tc.host)
		if get.Code != head.Code {
			t.Errorf("%s: status GET %d, HEAD %d", tc.target, get.Code, head.Code)
		}
		for k := range get.Header() {
			if g, hd := get.Header().Get(k), head.Header().Get(k); g != hd {
				t.Errorf("%s: header %s GET %q, HEAD %q", tc.target, k, g, hd)
			}
		}
		if head.Body.Len() != 0 {
			t.Errorf("%s: HEAD body = %q", tc.target, head.Body.String())
		}
		if cl := get.Header().Get("Content-Length"); cl != strconv.Itoa(get.Body.Len()) {
			t.Errorf("%s: Content-Length %q, body length %d", tc.target, cl, get.Body.Len())
		}
	}
}

// TestRawRequests sends malformed request lines through a real
// http.Server so net/http's own parsing is part of the check.
func TestRawRequests(t *testing.T) {
	srv := httptest.NewServer(testRouter(t))
	defer srv.Close()
	cases := []struct {
		name, raw, wantStatus, wantLocation string
	}{
		{"control byte in path", "GET /a\x01b HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "400", ""},
		{"bad percent escape", "GET /x%zz HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "400", ""},
		{"space in host", "GET /x HTTP/1.1\r\nHost: a b.tryc.ai\r\n\r\n", "400", ""},
		{"extra header is ignored", "GET /nope HTTP/1.1\r\nHost: tryc.ai\r\nX: y\r\n\r\n", "301", "https://trycopilot.ai/nope"},
		{"asterisk form", "GET * HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "301", "https://trycopilot.ai/"},
		{"absolute form", "GET http://evil.com/x HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "301", "https://trycopilot.ai/x"},
		{"no host header http/1.0", "GET /x HTTP/1.0\r\n\r\n", "301", "https://trycopilot.ai/x"},
		{"backslash path", "GET /\\evil.com HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "301", "https://trycopilot.ai/%5Cevil.com"},
		{"double slash path", "GET //evil.com HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "301", "https://trycopilot.ai//evil.com"},
		// net/http answers server-wide "OPTIONS *" itself with an empty 200
		// before any handler runs; every other method still reaches the 405.
		{"OPTIONS asterisk is answered by net/http", "OPTIONS * HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "200", ""},
		{"OPTIONS path", "OPTIONS /x HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "405", ""},
		{"POST health", "POST /health/ HTTP/1.1\r\nHost: tryc.ai\r\nContent-Length: 0\r\n\r\n", "405", ""},
		{"CONNECT", "CONNECT tryc.ai:443 HTTP/1.1\r\nHost: tryc.ai\r\n\r\n", "405", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			raw := strings.TrimSuffix(tc.raw, "\r\n") + "Connection: close\r\n\r\n"
			if _, err := io.WriteString(conn, raw); err != nil {
				t.Fatal(err)
			}
			resp, err := io.ReadAll(conn)
			if err != nil && len(resp) == 0 {
				t.Fatal(err)
			}
			head := strings.SplitN(string(resp), "\r\n\r\n", 2)[0]
			if !strings.HasPrefix(head, "HTTP/1.1 "+tc.wantStatus+" ") && !strings.HasPrefix(head, "HTTP/1.0 "+tc.wantStatus+" ") {
				t.Errorf("status line = %q, want %s", strings.SplitN(head, "\r\n", 2)[0], tc.wantStatus)
			}
			var got string
			for _, line := range strings.Split(head, "\r\n") {
				if v, ok := strings.CutPrefix(line, "Location: "); ok {
					got = v
				}
			}
			if got != tc.wantLocation {
				t.Errorf("Location = %q, want %q", got, tc.wantLocation)
			}
		})
	}
}

func TestParseLinksRejectsInvalidTables(t *testing.T) {
	bad := map[string]string{
		"no leading slash":  `{"x": "https://example.com/"}`,
		"root key":          `{"/": "https://example.com/"}`,
		"trailing slash":    `{"/x/": "https://example.com/"}`,
		"query in key":      `{"/x?y": "https://example.com/"}`,
		"fragment in key":   `{"/x#y": "https://example.com/"}`,
		"reserved health":   `{"/health": "https://example.com/"}`,
		"reserved healthz":  `{"/healthz": "https://example.com/"}`,
		"reserved load":     `{"/load": "https://example.com/"}`,
		"http target":       `{"/x": "http://example.com/"}`,
		"relative target":   `{"/x": "/relative"}`,
		"no host target":    `{"/x": "https:///path"}`,
		"javascript target": `{"/x": "javascript:alert(1)"}`,
		"empty table":       `{}`,
		"not an object":     `["/x"]`,
	}
	for name, doc := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := parseLinks([]byte(doc)); err == nil {
				t.Fatalf("parseLinks(%s) succeeded, want error", doc)
			}
		})
	}
}

func TestShippedLinksAreValid(t *testing.T) {
	links, err := parseLinks(linksJSON)
	if err != nil {
		t.Fatalf("shipped LINKS.json is invalid: %v", err)
	}
	for _, key := range []string{"/x", "/lint", "/tryc.ai"} {
		if _, ok := links[key]; !ok {
			t.Errorf("shipped LINKS.json is missing %s", key)
		}
	}
}

type probeFile struct {
	Probes []struct {
		Description string `json:"description"`
		Request     struct {
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"request"`
		ExpectedResponse struct {
			Status       int               `json:"status"`
			HeaderEquals map[string]string `json:"header_equals"`
		} `json:"expected_response"`
	} `json:"probes"`
}

// TestProbes replays every PROBES.json case against the handler.
// The same file is used by the container smoke test in CI.
func TestProbes(t *testing.T) {
	data, err := os.ReadFile("PROBES.json")
	if err != nil {
		t.Fatalf("read PROBES.json: %v", err)
	}
	var file probeFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse PROBES.json: %v", err)
	}
	if len(file.Probes) == 0 {
		t.Fatal("PROBES.json has no probes")
	}
	h := testRouter(t)
	for _, p := range file.Probes {
		t.Run(p.Description, func(t *testing.T) {
			req := httptest.NewRequest(p.Request.Method, p.Request.URL, nil)
			req.Host = ""
			for k, v := range p.Request.Headers {
				if strings.EqualFold(k, "Host") {
					req.Host = v
				} else {
					req.Header.Set(k, v)
				}
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != p.ExpectedResponse.Status {
				t.Errorf("status = %d, want %d", rec.Code, p.ExpectedResponse.Status)
			}
			if _, ok := p.ExpectedResponse.HeaderEquals["Location"]; rec.Code/100 == 3 && !ok {
				t.Errorf("redirect probe does not pin Location")
			}
			for k, want := range p.ExpectedResponse.HeaderEquals {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

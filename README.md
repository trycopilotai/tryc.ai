# tryc.ai

The short-link router for tryc.ai, maintained by
trycopilot.ai. A request for `tryc.ai/<name>` redirects to
the page for that name, such as an npm package or a GitHub
repository. Other GET and HEAD paths, apart from health and
load endpoints, redirect to the matching trycopilot.ai
address.

## How it works

For requests accepted by Go's HTTP parser, the router checks
these rules in order. Malformed requests may be rejected by
the server before these rules run:

| Request                                        | Response                                           |
| ---------------------------------------------- | -------------------------------------------------- |
| Any method other than GET or HEAD              | `405`, `Allow: GET, HEAD`                          |
| GET or HEAD `/health/` or `/healthz`, any Host | `200`, GET body `ok`                               |
| GET or HEAD `/load/`, any Host                 | `200`, GET JSON body `{"load": 0}`                 |
| Mapped path on `tryc.ai` or `www.tryc.ai`      | `302` to the target in `LINKS.json`                |
| Anything else                                  | `301` to trycopilot.ai, keeping the path and query |

A mapped path matches after one trailing slash is removed,
so `/lint/` and `/lint` both match. If the request has a
nonempty query string and the target contains no `?`
delimiter, the query is appended to the target. A bare `?`
on the request is dropped.

The `301` goes to `trycopilot.ai` for the apex host. A
subdomain such as `scribe.tryc.ai` goes to the same label on
trycopilot.ai, such as `scribe.trycopilot.ai`. Other hosts,
and labels that use characters outside `[a-z0-9.-]` or do
not start and end with a letter or digit, go to
`trycopilot.ai`. Destination hosts longer than 253 bytes
also fall back to `trycopilot.ai`. This character rule does
not validate individual DNS label lengths or resolution. The
Host header is lowercased, has a port removed when it parses
as `host:port`, and loses one trailing dot first. The path
is copied percent-encoded and is not cleaned, so `/./x`
stays `/./x`. HEAD requests get the corresponding GET status
and router headers with no body. The server generates Date
per request.

## Add a link

Edit `LINKS.json`. Each key is a path that starts with `/`,
is not `/`, has no trailing slash, and contains no `?` or
`#`. The paths `/health`, `/healthz`, and `/load` are
reserved. Each value is an absolute `https://` URL with a
nonempty host and no fragment delimiter (`#`). The program
checks the file at startup and exits with an error if an
entry is invalid. The unit tests check the shipped file too.

## Run

```sh
make run ARGS="--port=8080"
```

Or run the container:

```sh
make image
docker run --rm -p 8080:8080 tryc.ai:dev
```

Flags:

- `--host` (default `0.0.0.0`) and `--port` (default `8080`,
  must be 1 to 65535)
- `--link-hosts` (default `tryc.ai,www.tryc.ai`): hosts that
  serve `LINKS.json` redirects
- `--healthcheck`: request `/healthz` on `127.0.0.1` at
  `--port` and exit 0 on HTTP 200, or 1 otherwise; redirects
  are not followed

Help exits 0. Invalid flags or a port outside the range
exit 2. Link validation, listen, and healthcheck failures
exit 1.

## Develop

- `make build`: build everything with Bazel
- `make test`: run the unit tests, including the
  `PROBES.json` cases
- `make lint`: run the formatters from the pinned
  `github.com/trycopilotai/lint` submodule. It needs
  `git submodule update --init`, Python 3.11 or newer, and
  Docker. Use `make lint ARGS=--write` to apply formatting
  fixes, and `make lint PYTHON=python3.12` to pick an
  interpreter.
- `make image`: build the runtime image as `tryc.ai:dev`

The `github.com/trycopilotai/lint` submodule is pinned to
`refs/tags/v0.1.8` at commit
`3c4322bf89aedcd1c5ce5b8cc37c24be63dd09c1`.

Bazel is started through Bazelisk 1.25.0 with `npx`, so
Node.js is required. Go is not needed on the host.

Native Bazel builds need a C/C++ toolchain: Xcode Command
Line Tools on macOS, or build-essential on Debian/Ubuntu.

## Use as a Bazel module

Add the repository as a module, then depend on the binary
target:

```starlark
bazel_dep(name = "tryc_ai", version = "0.1.0")
local_path_override(module_name = "tryc_ai", path = "path/to/tryc.ai")
```

The binary is `@tryc_ai//:main`. It is built with pure and
static linking. The Linux binary runs without a C library.

## Security

Report vulnerabilities privately using the reporting paths
in [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).

Not affiliated with, endorsed by, or connected to GitHub,
Microsoft, or GitHub Copilot.

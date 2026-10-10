# Contributing

Thanks for helping with the tryc.ai short-link router.

## Add or change a link

Edit `LINKS.json`. Keys start with `/`, have no trailing
slash, and contain no `?` or `#`. Values are absolute
`https://` URLs. The binary refuses to start if the file is
invalid, and the unit tests check the shipped file.

## Before you open a pull request

Run the same checks CI runs:

```sh
make test
make lint
make image
```

`make lint` needs Git, Python 3.11 or newer, and Docker. Use
`make lint ARGS=--write` to apply formatting fixes.

Keep changes small and focused. In prose, name the company
as `trycopilot.ai`, and name the GitHub organization as
`trycopilotai`.

load("@rules_go//go:def.bzl", "go_binary", "go_library", "go_test")

package(default_visibility = ["//visibility:public"])

exports_files([
    "LICENSE",
    "LINKS.json",
    "PROBES.json",
])

go_library(
    name = "tryc_lib",
    srcs = [
        "main.go",
        "router.go",
    ],
    embedsrcs = ["LINKS.json"],
    importpath = "github.com/trycopilotai/tryc.ai",
)

go_binary(
    name = "main",
    embed = [":tryc_lib"],
    pure = "on",
    static = "on",
)

go_test(
    name = "router_test",
    size = "small",
    srcs = ["router_test.go"],
    data = ["PROBES.json"],
    embed = [":tryc_lib"],
)

go_test(
    name = "readme_badges_test",
    size = "small",
    srcs = ["readme_badges_test.go"],
    data = [
        "README.md",
    ] + glob([".github/workflows/*.yml"]),
    embed = [":tryc_lib"],
)

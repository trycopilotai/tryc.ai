# Production image for the tryc.ai short-link router.
#
# The build stage compiles the static Go binary with Bazel and copies
# it to a stable path, so the runtime stage never sees Bazel's output
# tree. The runtime stage carries only /main and runs as a non-root
# user with a read-only root filesystem in production.
#
# Build with: make image

# -----------------------------------------------------------------------------
# Ubuntu Base
# -----------------------------------------------------------------------------

# Pinned Ubuntu 24.04 base shared by the build and runtime stages.
FROM docker.io/library/ubuntu:24.04@sha256:59a458b76b4e8896031cd559576eac7d6cb53a69b38ba819fb26518536368d86 AS ubuntu-base

# Apt metadata errors fail the build. APT::Update::Error-Mode=any
# stops an unreachable archive from being treated as a warning, which
# would otherwise let the install run against an empty package list.
# Each fetch is also bounded by timeout so a hung mirror fails fast.

RUN set -eux; \
    timeout 600 apt-get update -o APT::Update::Error-Mode=any -o Acquire::Retries=10 -o Acquire::http::Timeout=60; \
    timeout 900 apt-get install -o Acquire::Retries=10 -o Acquire::http::Timeout=60 --yes --no-install-recommends ca-certificates; \
    rm -rf /var/lib/apt/lists/*

# -----------------------------------------------------------------------------
# Build Stage
# -----------------------------------------------------------------------------

FROM ubuntu-base AS build

# Install the C toolchain Bazel expects on a Linux host, plus curl to
# fetch the Bazelisk launcher.
RUN set -eux; \
    timeout 600 apt-get update -o APT::Update::Error-Mode=any -o Acquire::Retries=10 -o Acquire::http::Timeout=60; \
    timeout 900 apt-get install -o Acquire::Retries=10 -o Acquire::http::Timeout=60 --yes --no-install-recommends \
    build-essential \
    ca-certificates \
    curl; \
    rm -rf /var/lib/apt/lists/*

# Bazelisk reads the Bazel version from .bazelversion or
# USE_BAZEL_VERSION, so the image only needs the launcher. Anything
# other than amd64 or arm64 fails here instead of installing a binary
# that will not run.
RUN set -eux; \
    architecture="$(dpkg --print-architecture)"; \
    case "${architecture}" in \
        amd64|arm64) ;; \
        *) echo "unsupported Bazelisk architecture: ${architecture}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/bazelbuild/bazelisk/releases/download/v1.25.0/bazelisk-linux-${architecture}" -o /usr/local/bin/bazelisk; \
    chmod +x /usr/local/bin/bazelisk

# Build as an unprivileged user so Bazel's output tree and caches are
# not owned by root. The UID is fixed so the cache mounts below have a
# stable owner.
RUN groupadd -g 10001 tryc \
    && useradd -m -u 10001 -g 10001 -s /bin/bash tryc \
    && mkdir -p /home/tryc/.cache/bazelisk \
    && chown -R tryc:tryc /home/tryc/.cache

COPY --chown=tryc:tryc . /app
WORKDIR /app
USER tryc

# The Bazelisk, repository, and disk caches live in BuildKit cache
# mounts, so repeated builds reuse the downloaded Bazel release, Go
# SDK, and compiled actions. The
# output base lives under /tmp, so Bazel's output tree is not part of
# the runtime image. The binary is copied out in the same step because
# its real path only exists under that output base.
RUN --mount=type=cache,id=tryc-bazelisk,target=/home/tryc/.cache/bazelisk,uid=10001,gid=10001,sharing=locked \
    --mount=type=cache,id=tryc-bazel-repo,target=/home/tryc/.cache/bazel-repo,uid=10001,gid=10001,sharing=locked \
    --mount=type=cache,id=tryc-bazel-disk,target=/home/tryc/.cache/bazel-disk,uid=10001,gid=10001,sharing=locked \
    set -eux; \
    mkdir -p /home/tryc/.cache/bazelisk /home/tryc/.cache/bazel-repo /home/tryc/.cache/bazel-disk; \
    USE_BAZEL_VERSION=8.4.2 bazelisk \
    --output_base=/tmp/bazel-build \
    build --config=release \
    --repository_cache=/home/tryc/.cache/bazel-repo \
    --disk_cache=/home/tryc/.cache/bazel-disk \
    //:main; \
    cp -L bazel-bin/main_/main /tmp/main; \
    chmod 0555 /tmp/main; \
    USE_BAZEL_VERSION=8.4.2 bazelisk --output_base=/tmp/bazel-build shutdown

# -----------------------------------------------------------------------------
# Runtime Stage
# -----------------------------------------------------------------------------

FROM ubuntu-base AS runtime

# A fixed non-root UID that matches the build stage. The image has no
# login shell and no home directory.
RUN groupadd -g 10001 tryc \
    && useradd -u 10001 -g 10001 -M -s /usr/sbin/nologin tryc

COPY --from=build --chown=root:root --chmod=0555 /tmp/main /main

LABEL org.opencontainers.image.title="tryc.ai" \
      org.opencontainers.image.source="https://github.com/trycopilotai/tryc.ai" \
      org.opencontainers.image.licenses="MIT"

USER tryc
EXPOSE 8080

# The health check runs the binary itself, so the image needs no curl
# or shell tools at runtime. It probes the default port, so a
# deployment that overrides --port must override the check too.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/main", "--healthcheck"]

ENTRYPOINT ["/main"]
CMD ["--host=0.0.0.0", "--port=8080"]

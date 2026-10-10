SHELL := /bin/bash

ARGS ?=
BAZEL := npx --yes @bazel/bazelisk

LINT := github.com/trycopilotai/lint/lint.py
PYTHON ?= python3

.PHONY: build
build:
	@$(BAZEL) build //...

.PHONY: image
image:
	@docker build --target runtime --tag tryc.ai:dev .

.PHONY: lint
lint:
	@[ -f $(LINT) ] || { echo "lint not found: run 'git submodule update --init'" >&2; exit 1; }; $(PYTHON) $(LINT) --cwd . --docker $(ARGS)

.PHONY: run
run:
	@$(BAZEL) run //:main -- $(ARGS)

.PHONY: test
test:
	@$(BAZEL) test //...

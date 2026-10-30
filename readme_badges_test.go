// This test pins the README's CI status badges to the workflow files, so
// the repository check fails when a runnable workflow has no badge.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoURL = "https://github.com/trycopilotai/tryc.ai"

type workflow struct {
	file   string
	name   string
	events []string
}

// readWorkflow reads the top-level name and the event names under
// "on:". It handles the block-mapping form that this repository's
// workflows use and stops on any other form, so a new shape can never
// pass unchecked.
func readWorkflow(t *testing.T, path string) workflow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	w := workflow{file: filepath.Base(path)}
	inOn := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			inOn = false
			switch {
			case strings.HasPrefix(line, "name:"):
				w.name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), `"'`)
			case line == "on:":
				inOn = true
			case strings.HasPrefix(line, "on:"):
				t.Fatalf("%s: unsupported on: form %q", path, line)
			}
			continue
		}
		if inOn && indent == 2 {
			event, _, found := strings.Cut(trimmed, ":")
			if !found {
				t.Fatalf("%s: unsupported event line %q", path, line)
			}
			w.events = append(w.events, event)
		}
	}
	if w.name == "" {
		t.Fatalf("%s: no top-level name", path)
	}
	if len(w.events) == 0 {
		t.Fatalf("%s: no events under on:", path)
	}
	return w
}

// reusableOnly reports whether the workflow can only be called by
// another workflow, which gets no badge.
func (w workflow) reusableOnly() bool {
	return len(w.events) == 1 && w.events[0] == "workflow_call"
}

// readmeLines returns the README's lines and the line indexes of its
// title and of its first second-level heading, ignoring fenced code.
func readmeLines(t *testing.T) (lines []string, title, firstSection int) {
	t.Helper()
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(string(data), "\n")
	title, firstSection = -1, len(lines)
	fence := ""
	for i, line := range lines {
		switch {
		case fence != "":
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
		case strings.HasPrefix(line, "```"):
			fence = "```"
		case strings.HasPrefix(line, "~~~"):
			fence = "~~~"
		case strings.HasPrefix(line, "# ") && title < 0:
			title = i
		case strings.HasPrefix(line, "## ") && firstSection == len(lines):
			firstSection = i
		}
	}
	if title < 0 {
		t.Fatal("README has no title")
	}
	return lines, title, firstSection
}

func TestReadmeBadgesMatchWorkflows(t *testing.T) {
	var paths []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		found, err := filepath.Glob(filepath.Join(".github/workflows", pattern))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, found...)
	}
	lines, title, firstSection := readmeLines(t)
	readme := strings.Join(lines, "\n")
	runnable := 0
	for _, path := range paths {
		w := readWorkflow(t, path)
		link := repoURL + "/actions/workflows/" + w.file
		badgeURL := link + "/badge.svg"
		if w.reusableOnly() {
			if strings.Contains(readme, badgeURL) {
				t.Errorf("%s: README badges a reusable-only workflow", w.file)
			}
			continue
		}
		runnable++
		badge := "[![" + w.name + "](" + badgeURL + ")](" + link + ")"
		if n := strings.Count(readme, badge); n != 1 {
			t.Errorf("%s: README has %d linked badges, want 1: %s", w.file, n, badge)
			continue
		}
		if n := strings.Count(readme, badgeURL); n != 1 {
			t.Errorf("%s: README mentions the badge image %d times, want 1", w.file, n)
		}
		for i, line := range lines {
			if strings.Contains(line, badge) && (i <= title || i >= firstSection) {
				t.Errorf("%s: badge on line %d is outside lines %d to %d, between the title and the first section", w.file, i+1, title+2, firstSection)
			}
		}
	}
	if runnable == 0 {
		t.Fatal("no runnable workflow found; the check would pass without checking anything")
	}
}

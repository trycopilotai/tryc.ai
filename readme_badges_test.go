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
// title and of its first two --- rules, ignoring fenced code.
func readmeLines(t *testing.T) (lines []string, title, ruleOne, ruleTwo int) {
	t.Helper()
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(string(data), "\n")
	title, ruleOne, ruleTwo = -1, -1, -1
	fence := ""
	for i, line := range lines {
		switch {
		case fence != "":
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
		case strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~"):
			fence = line[:3]
		case title < 0:
			if strings.HasPrefix(line, "# ") || strings.Contains(line, "<h1") {
				title = i
			}
		case strings.TrimSpace(line) == "---" && ruleOne < 0:
			ruleOne = i
		case strings.TrimSpace(line) == "---" && ruleTwo < 0:
			ruleTwo = i
		}
	}
	if title < 0 {
		t.Fatal("README has no title")
	}
	if ruleTwo < 0 {
		t.Fatal("README needs two --- rules under the title and subtitle")
	}
	return lines, title, ruleOne, ruleTwo
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
	lines, _, ruleOne, ruleTwo := readmeLines(t)
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
			if strings.Contains(line, badge) && (i <= ruleOne || i >= ruleTwo) {
				t.Errorf("%s: badge on line %d is not between the two --- rules", w.file, i+1)
			}
		}
	}
	if runnable == 0 {
		t.Fatal("no runnable workflow found; the check would pass without checking anything")
	}
}

func TestReadmeHeaderLayout(t *testing.T) {
	lines, title, ruleOne, ruleTwo := readmeLines(t)
	for _, rule := range []int{ruleOne, ruleTwo} {
		if strings.TrimSpace(lines[rule-1]) != "" || strings.TrimSpace(lines[rule+1]) != "" {
			t.Errorf("line %d: a --- rule needs a blank line above and below it", rule+1)
		}
	}
	blocks := 0
	previousBlank := true
	for _, line := range lines[title+1 : ruleOne] {
		blank := strings.TrimSpace(line) == ""
		if !blank && previousBlank {
			blocks++
		}
		previousBlank = blank
	}
	if blocks != 1 {
		t.Errorf("the subtitle between the title and the first --- rule must be one paragraph, found %d blocks", blocks)
	}
	for _, line := range lines[ruleOne+1 : ruleTwo] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Contains(line, "badge.svg") || trimmed == "</div>" || trimmed == `<div align="center">` {
			continue
		}
		t.Errorf("only badges may sit between the --- rules: %.60s", line)
	}
}

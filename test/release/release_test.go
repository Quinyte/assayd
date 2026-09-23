// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package release holds the release workflow to the one property nobody can
// test by cutting a release: a `workflow_dispatch` publishes only the tag it
// was started from.
//
// Neither checkout in release.yml takes a `ref:`, so both jobs build
// github.sha, the commit of the ref the run was started from, while the
// version string comes from the `tag` input. A dispatch of `tag: v0.4.1` from
// main once could publish main's code as 0.4.1, signed and attested, and every
// downstream verification passed. The `version` step now refuses that run.
// These tests read the workflow file itself, run that step's own script, and
// check that nothing after it can publish once it has failed. Without them,
// deleting its `exit 1`, moving it below `push`, or adding `if: always()` to
// a signing step would all leave CI green.
//
// What this does NOT prove: that GitHub evaluates the three `${{ }}`
// expressions the way evaluate() below does. The step's `env:` is held to
// exactly those three expressions so the gap stays that narrow. It also proves
// nothing about a dispatch from a ref whose release.yml predates the guard:
// GitHub runs the file on the dispatched ref, and this test reads the one in
// this checkout.
package release

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const workflowPath = "../../.github/workflows/release.yml"

type step struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	If   string            `yaml:"if"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

func (s step) label() string {
	for _, v := range []string{s.ID, s.Name, s.Uses} {
		if v != "" {
			return v
		}
	}
	return "(unnamed step)"
}

type job struct {
	If    string   `yaml:"if"`
	Needs []string `yaml:"needs"`
	Steps []step   `yaml:"steps"`
}

// UnmarshalYAML accepts `needs:` as a string or a list, as GitHub does.
func (j *job) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		If    string    `yaml:"if"`
		Needs yaml.Node `yaml:"needs"`
		Steps []step    `yaml:"steps"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	j.If, j.Steps = raw.If, raw.Steps
	switch raw.Needs.Kind {
	case 0:
	case yaml.ScalarNode:
		j.Needs = []string{raw.Needs.Value}
	default:
		if err := raw.Needs.Decode(&j.Needs); err != nil {
			return err
		}
	}
	return nil
}

type workflow struct {
	Jobs map[string]job `yaml:"jobs"`
}

func load(t *testing.T) workflow {
	t.Helper()
	b, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read the release workflow: %v", err)
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatalf("parse the release workflow: %v", err)
	}
	if _, ok := w.Jobs["image"]; !ok {
		t.Fatal("release.yml has no `image` job; these tests are written against it")
	}
	return w
}

// versionStep returns the image job's `version` step and its index. It fails
// the test unless the step exists, has a script, and that script contains the
// refusal. A missing or empty script once made every case of a hand-run
// version of this check pass: the extraction tool was not installed, it
// produced an empty file, and bash ran nothing and exited 0.
func versionStep(t *testing.T, w workflow) (step, int) {
	t.Helper()
	for i, s := range w.Jobs["image"].Steps {
		if s.ID == "version" {
			if strings.TrimSpace(s.Run) == "" {
				t.Fatal("the `version` step has no script; every case below would pass vacuously")
			}
			for _, want := range []string{`"refs/tags/${TAG}"`, "exit 1"} {
				if !strings.Contains(s.Run, want) {
					t.Fatalf("the `version` step's script no longer contains %q, so it cannot "+
						"refuse a dispatch from the wrong ref:\n%s", want, s.Run)
				}
			}
			return s, i
		}
	}
	t.Fatal("the image job has no step with `id: version`")
	return step{}, -1
}

// The step's env, exactly. evaluate() below knows these three expressions and
// nothing else, so a fourth, or a changed one, must fail here rather than be
// silently mis-evaluated.
var wantEnv = map[string]string{
	"EVENT": "${{ github.event_name }}",
	"REF":   "${{ github.ref }}",
	"TAG":   "${{ github.event.inputs.tag || github.ref_name }}",
}

type trigger struct {
	event   string // github.event_name
	ref     string // github.ref
	refName string // github.ref_name
	input   string // github.event.inputs.tag; "" when the event has no inputs
}

// evaluate resolves the step's env the way GitHub does for these three
// expressions. On a push, github.event.inputs is null, so `||` falls through
// to github.ref_name.
func evaluate(t *testing.T, env map[string]string, tr trigger) []string {
	t.Helper()
	var out []string
	for k, expr := range env {
		var v string
		switch expr {
		case "${{ github.event_name }}":
			v = tr.event
		case "${{ github.ref }}":
			v = tr.ref
		case "${{ github.event.inputs.tag || github.ref_name }}":
			v = tr.input
			if v == "" {
				v = tr.refName
			}
		default:
			t.Fatalf("cannot evaluate %s: %s", k, expr)
		}
		out = append(out, k+"="+v)
	}
	return out
}

func TestTheVersionStepPublishesOnlyTheTagItWasStartedFrom(t *testing.T) {
	w := load(t)
	s, _ := versionStep(t, w)
	if len(s.Env) != len(wantEnv) {
		t.Fatalf("the `version` step's env is %v, want exactly %v", s.Env, wantEnv)
	}
	for k, v := range wantEnv {
		if s.Env[k] != v {
			t.Fatalf("the `version` step's env %s is %q, want %q", k, s.Env[k], v)
		}
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal("bash is required: the step runs under bash on the runner")
	}

	cases := []struct {
		name    string
		tr      trigger
		publish bool
		version string
	}{
		{"a tag push", trigger{"push", "refs/tags/v0.4.1", "v0.4.1", ""}, true, "0.4.1"},
		{"a dispatch from the tag it names", trigger{"workflow_dispatch", "refs/tags/v0.4.1", "v0.4.1", "v0.4.1"}, true, "0.4.1"},
		{"a dispatch from main naming a tag", trigger{"workflow_dispatch", "refs/heads/main", "main", "v0.4.1"}, false, ""},
		{"a dispatch from another tag", trigger{"workflow_dispatch", "refs/tags/v0.4.0", "v0.4.0", "v0.4.1"}, false, ""},
		{"a dispatch from a BRANCH named like the tag", trigger{"workflow_dispatch", "refs/heads/v0.4.1", "v0.4.1", "v0.4.1"}, false, ""},
		{"a dispatch whose input drops the v", trigger{"workflow_dispatch", "refs/tags/v0.4.1", "v0.4.1", "0.4.1"}, false, ""},
		{"a dispatch whose input carries a newline", trigger{"workflow_dispatch", "refs/tags/v0.4.1", "v0.4.1", "v0.4.1\nversion=9.9.9"}, false, ""},
		{"a dispatch whose input carries shell", trigger{"workflow_dispatch", "refs/heads/main", "main", `v1"; touch PWNED; echo "`}, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "github_output")
			if err := os.WriteFile(out, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, "-c", s.Run)
			cmd.Dir = dir
			cmd.Env = append(evaluate(t, s.Env, c.tr), "GITHUB_OUTPUT="+out, "PATH="+os.Getenv("PATH"))
			log, runErr := cmd.CombinedOutput()
			written, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "PWNED")); err == nil {
				t.Fatalf("the input ran as shell:\n%s", log)
			}
			var exit *exec.ExitError
			switch {
			case c.publish && runErr != nil:
				t.Fatalf("refused a run that must publish: %v\n%s", runErr, log)
			case c.publish && string(written) != "version="+c.version+"\n":
				t.Fatalf("wrote %q to GITHUB_OUTPUT, want %q", written, "version="+c.version+"\n")
			case !c.publish && runErr == nil:
				t.Fatalf("published a dispatch not started from refs/tags/<input>; wrote %q\n%s", written, log)
			case !c.publish && !errors.As(runErr, &exit):
				t.Fatalf("the step did not run at all, so its refusal proves nothing: %v", runErr)
			case !c.publish && len(written) != 0:
				t.Fatalf("refused, but still wrote %q to GITHUB_OUTPUT", written)
			case !c.publish && strings.Count(strings.TrimRight(string(log), "\n"), "\n") != 0:
				t.Fatalf("the refusal spans more than one line, so ::error:: carries only its first:\n%s", log)
			case !c.publish && !strings.HasPrefix(string(log), "::error::"):
				t.Fatalf("refused without an ::error:: annotation:\n%s", log)
			}
		})
	}
}

// statusFunc matches GitHub's status-check functions. An `if:` without one
// gets an implicit success(), so the step is skipped once the version step
// has failed. An `if:` with one runs regardless, unless it also requires the
// push to have succeeded.
var statusFunc = regexp.MustCompile(`\b(always|failure|cancelled|success)\s*\(`)

const pushSucceeded = "steps.push.outcome == 'success'"

func TestNothingIsPublishedAfterTheVersionStepRefuses(t *testing.T) {
	w := load(t)
	steps := w.Jobs["image"].Steps
	_, vi := versionStep(t, w)

	for i, s := range steps[:vi] {
		if !strings.HasPrefix(s.Uses, "actions/checkout@") {
			t.Errorf("step %d (%s) runs BEFORE the version step; only checkout may", i, s.label())
		}
	}
	pushSeen := false
	for _, s := range steps[vi+1:] {
		if s.ID == "push" {
			pushSeen = true
		}
		if !statusFunc.MatchString(s.If) {
			continue
		}
		if !strings.Contains(s.If, pushSucceeded) || strings.Contains(s.If, "||") {
			t.Errorf("step %q has `if: %s`, which runs after the version step has failed; "+
				"it must carry no status function, or require %s with no `||`", s.label(), s.If, pushSucceeded)
		}
	}
	if !pushSeen {
		t.Error("no step with `id: push` follows the version step")
	}

	chart, ok := w.Jobs["chart"]
	if !ok {
		t.Fatal("release.yml has no `chart` job")
	}
	needsImage := false
	for _, n := range chart.Needs {
		needsImage = needsImage || n == "image"
	}
	if !needsImage {
		t.Errorf("the chart job needs %v, not `image`, so it publishes whether or not the version step refused", chart.Needs)
	}
	if statusFunc.MatchString(chart.If) {
		t.Errorf("the chart job has `if: %s`, which runs it after the image job has failed", chart.If)
	}
}

// taintedExpr is anything whose value a person or a tag name chooses. Inside a
// `run:` script, `${{ }}` is substituted as TEXT before the shell parses it, so
// such a value must arrive through `env:` instead.
var taintedExpr = regexp.MustCompile(`\$\{\{[^}]*\b(inputs\.|outputs\.version|ref_name|github\.ref\b)[^}]*\}\}`)

func TestNoRunScriptInlinesAValueATagOrInputChooses(t *testing.T) {
	w := load(t)
	for name, j := range w.Jobs {
		for _, s := range j.Steps {
			if m := taintedExpr.FindAllString(s.Run, -1); len(m) > 0 {
				t.Errorf("%s/%s inlines %v into its script; pass it through `env:`", name, s.label(), m)
			}
		}
	}
}

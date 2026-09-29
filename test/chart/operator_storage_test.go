// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A chart render cannot see what the operator creates at run time, so NFR-2's
// allowlist test cannot either. ADR-0035 (proposed) D5 asks for a check that
// fails on the first change that makes the operator create storage, instead of
// a promise that such a change will add one. The two checks below are that.
// Either failing means: classify the storage under ADR-0035 D5, record the
// human's decision, and only then add an entry to grantExempt or volumeExempt.
// An exemption that matches nothing fails too, so none outlives its reason.
//
// What they cannot see (rule 7 — stated, not implied):
//   - storage another controller creates from an object the operator may
//     already write, unless its kind is in storageKinds;
//   - a volume whose shape is assembled at run time — read from a CR, a
//     ConfigMap or the network, or built from strings that split the key —
//     because the volume scan reads source text, not what the binary does;
//   - code outside internal/, cmd/, api/ and pkg/, and files under testdata/.

// storageKinds are the "group/resource" pairs through which a workload gets
// storage that outlives a pod: directly, or by a controller that makes PVCs.
var storageKinds = map[string]string{
	"/persistentvolumeclaims":                     "a PVC",
	"/persistentvolumes":                          "a PV",
	"apps/statefulsets":                           "volumeClaimTemplates",
	"postgresql.cnpg.io/clusters":                 "CNPG creates Pods and PVCs from a Cluster (ADR-0035 Context)",
	"agents.x-k8s.io/sandboxes":                   "agent-sandbox creates PVCs from a Sandbox's volumeClaimTemplates (design 02 §3.2)",
	"extensions.agents.x-k8s.io/sandboxtemplates": "agent-sandbox templates carry volumeClaimTemplates",
}

// grantExempt holds "Role name|group/resource" pairs the human has decided
// under ADR-0035 D5, with the heading that records it. Empty today. Binding
// design 02's sandbox scratchpad, which D5(c) recommends exempting, adds one.
var grantExempt = map[string]string{}

var writeVerbs = map[string]bool{"create": true, "update": true, "patch": true, "*": true}

// storageGrants returns each storage kind a set of RBAC rules lets its holder
// write, counting "*" in apiGroups, resources and verbs.
func storageGrants(rules []any) []string {
	var out []string
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		writes := false
		for _, v := range toStrings(rule["verbs"]) {
			writes = writes || writeVerbs[v]
		}
		if !writes {
			continue
		}
		for _, g := range toStrings(rule["apiGroups"]) {
			for _, res := range toStrings(rule["resources"]) {
				for kind := range storageKinds {
					kg, kr, _ := strings.Cut(kind, "/")
					if (g == "*" || g == kg) && (res == "*" || res == kr) {
						out = append(out, kind)
					}
				}
			}
		}
	}
	return out
}

// storageViolations applies grantExempt to the roles, and reports stale
// exemptions as violations too.
func storageViolations(roles []map[string]any, exempt map[string]string) []string {
	var out []string
	used := map[string]bool{}
	for _, role := range roles {
		rules, _ := role["rules"].([]any)
		for _, kind := range storageGrants(rules) {
			key := nameOf(role) + "|" + kind
			if _, ok := exempt[key]; ok {
				used[key] = true
				continue
			}
			out = append(out, toStr(role["kind"])+" "+strconv.Quote(nameOf(role))+" lets the operator write "+
				kind+" ("+storageKinds[kind]+")")
		}
	}
	for key := range exempt {
		if !used[key] {
			out = append(out, "grantExempt entry "+strconv.Quote(key)+" matches no grant; remove it")
		}
	}
	return out
}

func TestTheOperatorsRoleGrantsNoStorage(t *testing.T) {
	docs := render(t)
	var roles []map[string]any
	for _, kind := range []string{"ClusterRole", "Role"} {
		roles = append(roles, kindsOf(docs, kind)...)
	}
	if len(roles) == 0 {
		t.Fatal("the chart renders no Role or ClusterRole, so this test would pass by reading nothing")
	}
	for _, v := range storageViolations(roles, grantExempt) {
		t.Errorf("%s. That is storage NFR-2's chart render cannot see: classify it under ADR-0035 D5 "+
			"and record the human's decision before exempting it.", v)
	}
}

// The positive controls: storageGrants and storageViolations must find what
// they exist to find, so a matcher that finds nothing cannot pass.
func TestStorageGrantsFindsWhatItLooksFor(t *testing.T) {
	rule := func(groups, resources, verbs []any) []any {
		return []any{map[string]any{"apiGroups": groups, "resources": resources, "verbs": verbs}}
	}
	for _, tc := range []struct {
		name  string
		rules []any
		want  int
	}{
		{"a PVC create", rule([]any{""}, []any{"persistentvolumeclaims"}, []any{"create"}), 1},
		{"a StatefulSet patch", rule([]any{"apps"}, []any{"statefulsets"}, []any{"patch"}), 1},
		{"a Sandbox create", rule([]any{"agents.x-k8s.io"}, []any{"sandboxes"}, []any{"create"}), 1},
		{"every resource in apps", rule([]any{"apps"}, []any{"*"}, []any{"create"}), 1},
		{"read-only PVCs", rule([]any{""}, []any{"persistentvolumeclaims"}, []any{"get", "list"}), 0},
		{"Deployments", rule([]any{"apps"}, []any{"deployments"}, []any{"create"}), 0},
	} {
		if got := len(storageGrants(tc.rules)); got != tc.want {
			t.Errorf("%s: found %d storage grants, want %d", tc.name, got, tc.want)
		}
	}

	role := map[string]any{"kind": "ClusterRole", "metadata": map[string]any{"name": "op"},
		"rules": rule([]any{""}, []any{"persistentvolumeclaims"}, []any{"create"})}
	roles := []map[string]any{role}
	if got := storageViolations(roles, nil); len(got) != 1 {
		t.Errorf("an unexempted PVC grant: %d violations, want 1: %v", len(got), got)
	}
	if got := storageViolations(roles, map[string]string{"op|/persistentvolumeclaims": "D5"}); len(got) != 0 {
		t.Errorf("an exempted PVC grant: %d violations, want 0: %v", len(got), got)
	}
	if got := storageViolations(nil, map[string]string{"op|/persistentvolumeclaims": "D5"}); len(got) != 1 {
		t.Errorf("a stale exemption: %d violations, want 1: %v", len(got), got)
	}
}

// volumeToken matches source text that gives a pod a volume, in Go types, in
// unstructured maps and JSON, and in YAML: a `Volumes:` or `volumes:` field or
// key, a `"volumes"` key, `.Volumes`, `corev1.Volume`, any `...VolumeSource`,
// and the storage keys themselves. Case-insensitive, so `"hostPath"` in a
// map[string]any is found as well as `HostPath` in a struct.
var volumeToken = regexp.MustCompile(`(?i)\bvolumes"?\s*:|"volumes"|\.volumes\b|corev1\.volume\b|volumesource\b|volumeclaimtemplates|hostpath|persistentvolumeclaim|persistentvolume\b`)

// volumeExempt holds "path|trimmed line" pairs for a volume the human has
// classified under ADR-0035 D5 (a configMap or secret volume, say), with the
// reason. It exempts that line, never the file. Empty today, because the
// operator gives its pods no volume at all.
var volumeExempt = map[string]string{}

// codeOf returns the part of a line that is not a comment: a whole-line
// comment is dropped, and a trailing `//` outside a string is cut off.
func codeOf(line string, goFile bool) string {
	trimmed := strings.TrimSpace(line)
	if !goFile {
		if strings.HasPrefix(trimmed, "#") {
			return ""
		}
		return trimmed
	}
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
		return ""
	}
	inString, inRaw := false, false
	for i := 0; i < len(trimmed); i++ {
		switch c := trimmed[i]; {
		case c == '`' && !inString:
			inRaw = !inRaw
		case c == '"' && !inRaw && (i == 0 || trimmed[i-1] != '\\'):
			inString = !inString
		case c == '/' && !inString && !inRaw && i+1 < len(trimmed) && trimmed[i+1] == '/':
			return strings.TrimSpace(trimmed[:i])
		}
	}
	return trimmed
}

// volumeUses scans every regular file under the roots, except _test.go files
// and testdata/ directories, and returns each hit not in exempt, followed by
// each exemption that matched nothing.
func volumeUses(t *testing.T, exempt map[string]string, roots ...string) []string {
	t.Helper()
	var hits []string
	used := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(path)
			for i, line := range strings.Split(string(src), "\n") {
				code := codeOf(line, strings.HasSuffix(path, ".go"))
				if code == "" || !volumeToken.MatchString(code) {
					continue
				}
				key := rel + "|" + code
				if _, ok := exempt[key]; ok {
					used[key] = true
					continue
				}
				hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+code)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	for key := range exempt {
		if !used[key] {
			hits = append(hits, "volumeExempt entry "+strconv.Quote(key)+" matches no line; remove it")
		}
	}
	return hits
}

func TestTheOperatorGivesItsPodsNoVolumes(t *testing.T) {
	var roots []string
	for _, r := range []string{"../../internal", "../../cmd", "../../api"} {
		if _, err := os.Stat(r); err != nil {
			t.Fatalf("%s is missing, so this test would pass by reading nothing: %v", r, err)
		}
		roots = append(roots, r)
	}
	if _, err := os.Stat("../../pkg"); err == nil {
		roots = append(roots, "../../pkg")
	}
	for _, hit := range volumeUses(t, volumeExempt, roots...) {
		t.Errorf("%s\nThe operator's source now gives a pod a volume. A PVC, hostPath or other "+
			"durable volume is storage NFR-2's chart render cannot see: classify it under "+
			"ADR-0035 D5 and record the human's decision. A configMap, secret or emptyDir "+
			"volume goes in volumeExempt, by file and line, with its reason.", hit)
	}
}

// The positive controls for the scan: every planted form is found, a comment
// is not, an exemption covers its line and nothing else, and a stale
// exemption is reported.
func TestVolumeUsesFindsAPlantedVolume(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("typed.go", "package x\nvar v = corev1.Volume{Name: \"data\"}\n")
	write("unstructured.go", "package x\nvar m = map[string]any{\"hostPath\": map[string]any{\"path\": \"/var\"}}\n")
	write("decoded.go", "package x\nconst s = `{\"volumes\":[{\"name\":\"d\"}]}`\n")
	write("embedded.yaml", "spec:\n  volumes:\n  - name: d\n")
	write("comment.go", "package x\n// a PersistentVolumeClaim is not created here\nvar y = 1 // nor a hostPath\n")
	write("x_test.go", "package x\nvar v = corev1.Volume{}\n")
	write("testdata/golden.yaml", "volumes:\n")
	write("two.go", "package x\nvar a = corev1.Volume{Name: \"cm\"}\nvar b = corev1.Volume{Name: \"hp\"}\n")

	got := volumeUses(t, nil, dir)
	if len(got) != 6 {
		t.Errorf("found %d hits, want 6 (typed, unstructured, decoded, embedded, two.go twice):\n%s",
			len(got), strings.Join(got, "\n"))
	}
	for _, h := range got {
		if strings.Contains(h, "comment.go") || strings.Contains(h, "_test.go") || strings.Contains(h, "testdata") {
			t.Errorf("a comment, a test file or testdata was counted: %s", h)
		}
	}

	exempt := map[string]string{filepath.ToSlash(filepath.Join(dir, "two.go")) + `|var a = corev1.Volume{Name: "cm"}`: "D5"}
	got = volumeUses(t, exempt, filepath.Join(dir, "two.go"))
	if len(got) != 1 || !strings.Contains(got[0], `"hp"`) {
		t.Errorf("an exemption must cover its own line and not the file: %v", got)
	}
	stale := map[string]string{"nowhere.go|nothing": "D5"}
	if got := volumeUses(t, stale, filepath.Join(dir, "comment.go")); len(got) != 1 || !strings.Contains(got[0], "matches no line") {
		t.Errorf("a stale exemption must be reported: %v", got)
	}
}

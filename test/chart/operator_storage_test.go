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

// The chart render cannot see what the operator creates at run time, so NFR-2's
// allowlist test cannot either. ADR-0035 (proposed) D5 asks for a check that
// fails on the first change that makes the operator create storage, rather than
// a promise that such a change will add one. These two tests are that check.
// Either failing means: classify the new storage under ADR-0035 and get the
// human's decision before widening the exemption lists below.
//
// What they do NOT catch: storage created by another controller from an object
// the operator is already allowed to write. Design 02 §3.2's sandbox scratchpad
// becomes that case the day the operator binds agent-sandbox, and that change
// has to grant `agents.x-k8s.io/sandboxes`, which the RBAC test refuses.

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

func TestTheOperatorsRoleGrantsNoStorage(t *testing.T) {
	docs := render(t)
	var roles []map[string]any
	for _, kind := range []string{"ClusterRole", "Role"} {
		roles = append(roles, kindsOf(docs, kind)...)
	}
	if len(roles) == 0 {
		t.Fatal("the chart renders no Role or ClusterRole, so this test would pass by reading nothing")
	}
	for _, role := range roles {
		rules, _ := role["rules"].([]any)
		for _, kind := range storageGrants(rules) {
			t.Errorf("%s %q lets the operator write %s (%s). That is storage NFR-2's chart render "+
				"cannot see: classify it under ADR-0035 (D5) and record the human's decision "+
				"before granting it.", role["kind"], nameOf(role), kind, storageKinds[kind])
		}
	}
}

// The positive control: storageGrants must find what it exists to find, so a
// matcher that finds nothing cannot pass the test above.
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
}

// volumeToken matches the Go the operator would have to write to give a pod it
// builds any volume at all. Today it builds none, so every volume is new, and
// the exemption list starts empty: a configMap or secret volume that ADR-0035
// D4 would allow is added here, by file and reason, in the change that adds it.
var volumeToken = regexp.MustCompile(`\bVolumes\s*:|corev1\.Volume\b|VolumeSource\b|VolumeClaimTemplates\b|HostPath\b|PersistentVolumeClaim\b`)

var volumeExempt = map[string]string{}

func volumeUses(t *testing.T, roots ...string) []string {
	t.Helper()
	var hits []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if _, ok := volumeExempt[filepath.ToSlash(path)]; ok {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(src), "\n") {
				if volumeToken.MatchString(line) {
					hits = append(hits, filepath.ToSlash(path)+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return hits
}

func TestTheOperatorGivesItsPodsNoVolumes(t *testing.T) {
	roots := []string{"../../internal", "../../cmd"}
	for _, r := range roots {
		if _, err := os.Stat(r); err != nil {
			t.Fatalf("%s is missing, so this test would pass by reading nothing: %v", r, err)
		}
	}
	for _, hit := range volumeUses(t, roots...) {
		t.Errorf("%s\nThe operator now gives a pod it builds a volume. A hostPath, PVC or other "+
			"durable volume is storage NFR-2's chart render cannot see: classify it under "+
			"ADR-0035 (D5) and record the human's decision; a configMap, secret or emptyDir "+
			"volume goes in volumeExempt with its reason.", hit)
	}
}

// The positive control for the scan: a planted file must be found.
func TestVolumeUsesFindsAPlantedVolume(t *testing.T) {
	dir := t.TempDir()
	planted := "package x\nvar v = corev1.Volume{Name: \"data\", VolumeSource: corev1.VolumeSource{HostPath: nil}}\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := volumeUses(t, dir); len(got) != 1 {
		t.Errorf("found %d hits in one planted non-test line, want 1: %v", len(got), got)
	}
}

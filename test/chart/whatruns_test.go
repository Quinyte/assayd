// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestADefaultInstallRunsOneOperatorAndNothingElse pins the inventory the
// website's "what runs today" page and its plate (docs/diagrams/AUDIT-2026-09.md,
// P1) state, as the EXACT list of rendered objects for each configuration the
// page describes: the default (prod) profile, the same with the gateway on,
// and the local profile. One workload, the operator's Deployment, at two
// replicas (one at local); four CEL admission policies, each bound, and no
// webhook; the RBAC, Namespace and disruption budget beside them; one CRD; and
// no Gateway, GatewayClass, agentgateway object or subchart.
//
// An exact list rather than a set of forbidden kinds: an earlier version
// forbade the workload kinds it could think of, and a ReplicationController, a
// MutatingAdmissionPolicy or a third Role rendered past it (independent review
// of PR #67). Any object added or removed now fails here, and the change that
// adds one updates this list and the page together.
//
// TestCorePodBudget asserts no more than eight pods and
// TestProdProfileRendersWhatItClaims at least two replicas; neither says
// "exactly this", which is what a page titled for what runs has to claim. The
// plate that preceded this one (07-what-runs) drew eight pods and twelve
// components the chart does not install, and nothing failed.
//
// It renders the chart; it does not start a pod.
func TestADefaultInstallRunsOneOperatorAndNothingElse(t *testing.T) {
	core := []string{
		"ClusterRole/assayd-agent-operator",
		"ClusterRoleBinding/assayd-agent-operator",
		"Deployment/assayd-agent-operator",
		"Namespace/assayd-system",
		"ServiceAccount/assayd-agent-operator",
		"ValidatingAdmissionPolicy/assayd-api-keys",
		"ValidatingAdmissionPolicy/assayd-gateway-policies",
		"ValidatingAdmissionPolicy/assayd-gateway-routes",
		"ValidatingAdmissionPolicy/assayd-namespace-labels",
		"ValidatingAdmissionPolicyBinding/assayd-api-keys",
		"ValidatingAdmissionPolicyBinding/assayd-gateway-policies",
		"ValidatingAdmissionPolicyBinding/assayd-gateway-routes",
		"ValidatingAdmissionPolicyBinding/assayd-namespace-labels",
	}
	with := func(extra ...string) []string { return append(append([]string{}, core...), extra...) }

	for _, tc := range []struct {
		name     string
		args     []string
		want     []string
		replicas int
	}{
		{"default", nil, with("PodDisruptionBudget/assayd-agent-operator"), 2},
		{"gateway enabled", []string{"--set", "gateway.enabled=true",
			"--set", "gateway.servingUrl=http://assayd-gateway.agentgateway-system.svc"},
			with("PodDisruptionBudget/assayd-agent-operator",
				"Role/assayd-agent-operator-gateway-events",
				"Role/assayd-agent-operator-gateway-labels",
				"RoleBinding/assayd-agent-operator-gateway-events",
				"RoleBinding/assayd-agent-operator-gateway-labels"), 2},
		{"local profile", []string{"-f", filepath.Join(chartPath, "values-local.yaml")}, core, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := render(t, tc.args...)

			var got []string
			for _, d := range docs {
				kind, _ := d["kind"].(string)
				got = append(got, kind+"/"+nameOf(d))
			}
			sort.Strings(got)
			want := append([]string{}, tc.want...)
			sort.Strings(want)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("the chart renders\n  %s\nand the site says it renders exactly\n  %s",
					strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}

			deploys := kindsOf(docs, "Deployment")
			if len(deploys) != 1 {
				t.Fatalf("the chart renders %d Deployments; the site says one", len(deploys))
			}
			if n := replicasOf(t, deploys[0]); n != tc.replicas {
				t.Errorf("the operator renders %d replica(s); the site says %d", n, tc.replicas)
			}
		})
	}

	// The page that states this inventory, held to it. Without this the test
	// pins the chart and not the sentences: a change to the chart and to the
	// list above would stay green while the page went on saying the old thing
	// (third independent review of PR #67). It reads the page's source, so it
	// runs under ci.yml's chart job whenever the page or the chart changes.
	page, err := os.ReadFile(filepath.Join("..", "..", "web", "docs", "src", "content", "docs",
		"what-runs-today.mdx"))
	if err != nil {
		t.Fatalf("read the page this test pins: %v", err)
	}
	prose := strings.Join(strings.Fields(string(page)), " ")
	for _, obj := range core {
		kind, name, _ := strings.Cut(obj, "/")
		if kind != "ValidatingAdmissionPolicy" && kind != "Deployment" {
			continue
		}
		if !strings.Contains(prose, "`"+name+"`") {
			t.Errorf("what-runs-today.mdx does not name %s %s, which the chart renders", kind, name)
		}
	}
	for _, phrase := range []string{
		"one workload",
		"at two replicas", // the default profile, as rendered above
		"at one replica",  // the local profile
		"four `ValidatingAdmissionPolicy` objects",
		"two Roles and two RoleBindings", // what gateway.enabled adds
	} {
		if !strings.Contains(prose, phrase) {
			t.Errorf("what-runs-today.mdx does not say %q; the rendered chart above does", phrase)
		}
	}

	crds, err := filepath.Glob(filepath.Join(chartPath, "crds", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(crds) != 1 || filepath.Base(crds[0]) != "assayd.dev_agents.yaml" {
		t.Errorf("the chart ships CRDs %v; the site says it ships one, the Agent CRD", crds)
	}
	if entries, err := os.ReadDir(filepath.Join(chartPath, "charts")); err == nil && len(entries) > 0 {
		t.Errorf("the chart vendors %d subchart(s); the site says none is installed", len(entries))
	}
	chart, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(chart), "\n") {
		if strings.HasPrefix(line, "dependencies:") {
			t.Error("Chart.yaml declares dependencies; the site says the chart installs no subchart")
		}
	}
}

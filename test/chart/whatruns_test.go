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
// P1) state: one workload, the operator's Deployment, at two replicas; four CEL
// admission policies and nothing that runs a webhook; one CRD; and no Gateway,
// no GatewayClass and no agentgateway object, with the gateway off or on.
//
// TestCorePodBudget asserts no more than eight pods and
// TestProdProfileRendersWhatItClaims at least two replicas; neither says
// "exactly this", which is what a page titled for what runs has to claim. The
// plate that preceded this one (07-what-runs) drew eight pods and twelve
// components the chart does not install, and nothing failed.
func TestADefaultInstallRunsOneOperatorAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"default", nil},
		{"gateway enabled", []string{"--set", "gateway.enabled=true",
			"--set", "gateway.servingUrl=http://assayd-gateway.agentgateway-system.svc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := render(t, tc.args...)

			var workloads []string
			for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob",
				"ReplicaSet", "Pod"} {
				for _, d := range kindsOf(docs, kind) {
					workloads = append(workloads, kind+"/"+nameOf(d))
				}
			}
			if len(workloads) != 1 || workloads[0] != "Deployment/assayd-agent-operator" {
				t.Fatalf("the chart renders workloads %v; the site says it renders exactly one, "+
					"Deployment/assayd-agent-operator", workloads)
			}
			if n := replicasOf(t, kindsOf(docs, "Deployment")[0]); n != 2 {
				t.Errorf("the operator renders %d replica(s) at the default profile; the site says two", n)
			}

			var policies []string
			for _, d := range kindsOf(docs, "ValidatingAdmissionPolicy") {
				policies = append(policies, nameOf(d))
			}
			sort.Strings(policies)
			want := []string{"assayd-api-keys", "assayd-gateway-policies", "assayd-gateway-routes",
				"assayd-namespace-labels"}
			if strings.Join(policies, ",") != strings.Join(want, ",") {
				t.Errorf("the chart renders admission policies %v; the site names %v", policies, want)
			}
			for _, kind := range []string{"ValidatingWebhookConfiguration", "MutatingWebhookConfiguration",
				"Service"} {
				if got := kindsOf(docs, kind); len(got) != 0 {
					t.Errorf("the chart renders a %s (%s); the site says admission is CEL with no "+
						"webhook and nothing serving it", kind, nameOf(got[0]))
				}
			}

			for _, d := range docs {
				api, _ := d["apiVersion"].(string)
				kind, _ := d["kind"].(string)
				if strings.HasPrefix(api, "gateway.networking.k8s.io/") ||
					strings.Contains(api, "agentgateway") || kind == "CustomResourceDefinition" {
					t.Errorf("the chart renders %s %s (%s); the site says the chart ships no Gateway, "+
						"no agentgateway and no template-rendered CRD", kind, nameOf(d), api)
				}
			}
		})
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

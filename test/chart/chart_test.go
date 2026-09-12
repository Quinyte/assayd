// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package chart tests what `helm template` renders.
//
// A chart is where a platform's doctrine becomes deployable or quietly stops
// being true, so these are contract tests rather than smoke tests: the pod
// budget (rule 5), the stateful-dependency allowlist (rule 2), and the RBAC the
// operator actually needs. They need no cluster — rendering is enough — which
// makes them cheap enough to run on every change.
package chart

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

const chartPath = "../../charts/assayd"

// render runs `helm template` and returns the documents it produced.
func render(t *testing.T, extraArgs ...string) []map[string]any {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatalf("helm is required to test the chart, and a skipped chart test is an " +
			"untested deployment: install helm")
	}
	args := append([]string{"template", "assayd", chartPath}, extraArgs...)
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}

	var docs []map[string]any
	for _, chunk := range strings.Split(string(out), "\n---") {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(chunk), &doc); err != nil {
			t.Fatalf("parse rendered doc: %v\n%s", err, chunk)
		}
		if len(doc) > 0 {
			docs = append(docs, doc)
		}
	}
	return docs
}

func kindsOf(docs []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, d := range docs {
		if d["kind"] == kind {
			out = append(out, d)
		}
	}
	return out
}

// Rule 5: at most eight core pods, and the chart is the ledger. Counting per
// design 07 §5: sum of spec.replicas over Deployments and StatefulSets at the
// prod profile, DaemonSets as one logical each, Jobs and CronJobs excluded.
//
// This is the "written justification" gate made mechanical: a PR that adds a pod
// fails here unless it also changes the budget, which forces the change to be
// argued rather than absorbed.
func TestCorePodBudget(t *testing.T) {
	docs := render(t)

	total := 0
	breakdown := map[string]int{}
	for _, kind := range []string{"Deployment", "StatefulSet"} {
		for _, d := range kindsOf(docs, kind) {
			n := replicasOf(t, d)
			total += n
			breakdown[nameOf(d)] = n
		}
	}
	for _, d := range kindsOf(docs, "DaemonSet") {
		total++
		breakdown[nameOf(d)] = 1
	}

	const budget = 8
	if total > budget {
		t.Errorf("the core tier renders %d pods, and rule 5 budgets %d.\nBreakdown: %v\n"+
			"Adding a pod is allowed, but it must be argued: change the budget in the same "+
			"change that adds the pod.", total, budget, breakdown)
	}
	t.Logf("core pod count: %d/%d — %v", total, budget, breakdown)
}

// Rule 2: Postgres and NATS are the only substrate. Anything else that wants a
// disk has to be named in the allowlist, in the same change that introduces it.
func TestStatefulDependencyAllowlist(t *testing.T) {
	allowed := map[string]string{
		"postgres":    "substrate (rule 2)",
		"nats":        "substrate (rule 2)",
		"openobserve": "observability sink, not substrate",
	}

	docs := render(t)
	for _, kind := range []string{"StatefulSet"} {
		for _, d := range kindsOf(docs, kind) {
			name := nameOf(d)
			if !matchesAllowlist(name, allowed) {
				t.Errorf("%s %q is stateful and not on the allowlist. Rule 2 makes Postgres "+
					"and NATS the only substrate; anything else that holds state must be "+
					"justified in the change that adds it.", kind, name)
			}
		}
	}

	// A PersistentVolumeClaim is the same question wearing a different kind.
	for _, d := range kindsOf(docs, "PersistentVolumeClaim") {
		if !matchesAllowlist(nameOf(d), allowed) {
			t.Errorf("PersistentVolumeClaim %q is not on the stateful allowlist", nameOf(d))
		}
	}
}

// The operator's ClusterRole must grant what the code's kubebuilder markers ask
// for. B2 was exactly this drifting: leader election defaulted on while the
// role granted no leases, and the operator retried a forbidden lease forever
// while reporting Ready.
func TestOperatorRBACCoversWhatTheOperatorNeeds(t *testing.T) {
	docs := render(t)
	roles := kindsOf(docs, "ClusterRole")
	if len(roles) == 0 {
		t.Fatal("the chart renders no ClusterRole, so the operator cannot read Agents")
	}

	granted := map[string]bool{}
	for _, role := range roles {
		rules, _ := role["rules"].([]any)
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			for _, g := range toStrings(rule["apiGroups"]) {
				for _, res := range toStrings(rule["resources"]) {
					granted[g+"/"+res] = true
				}
			}
		}
	}

	for _, need := range []struct{ perm, why string }{
		{"assayd.dev/agents", "the object this operator exists to reconcile"},
		{"assayd.dev/agents/status", "conditions and phase are how it reports anything"},
		{"apps/deployments", "the workload it materializes"},
		{"coordination.k8s.io/leases", "leader election defaults on, and a forbidden lease " +
			"is retried forever rather than reported — the operator would run, report Ready, " +
			"and reconcile nothing"},
		{"apiextensions.k8s.io/customresourcedefinitions", "whether the EvalSuite CRD is " +
			"installed decides whether rollouts are eval-gated (ADR-0006)"},
		{"/events", "the durable record design 02 §3.3 promises for a superseded candidate"},
	} {
		if !granted[need.perm] {
			t.Errorf("the ClusterRole does not grant %s — %s", need.perm, need.why)
		}
	}
}

// The operator's own pod is held to the standard it holds agents to. A control
// plane that hardens its workloads and not itself is making an argument it does
// not believe.
func TestOperatorPodIsHardened(t *testing.T) {
	docs := render(t)
	deploys := kindsOf(docs, "Deployment")
	if len(deploys) == 0 {
		t.Fatal("the chart renders no operator Deployment")
	}

	for _, d := range deploys {
		spec := dig(d, "spec", "template", "spec")
		if spec == nil {
			t.Fatalf("%s has no pod spec", nameOf(d))
		}
		if v, _ := dig(spec, "securityContext")["runAsNonRoot"].(bool); !v {
			t.Errorf("%s may run as root", nameOf(d))
		}
		if v, ok := spec["automountServiceAccountToken"].(bool); ok && v {
			t.Errorf("%s automounts a service-account token it does not need", nameOf(d))
		}
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			container, _ := c.(map[string]any)
			sc := dig(container, "securityContext")
			if v, _ := sc["readOnlyRootFilesystem"].(bool); !v {
				t.Errorf("%s/%v has a writable root filesystem", nameOf(d), container["name"])
			}
			if v, ok := sc["allowPrivilegeEscalation"].(bool); !ok || v {
				t.Errorf("%s/%v allows privilege escalation", nameOf(d), container["name"])
			}
			if container["image"] == nil || strings.HasSuffix(toStr(container["image"]), ":latest") {
				t.Errorf("%s/%v uses a floating image tag: a rollout would not be reproducible",
					nameOf(d), container["name"])
			}
		}
	}
}

// The CRD ships in crds/, and it must be the one the operator was built against.
// A chart carrying a stale CRD installs a cluster the operator cannot serve.
func TestChartShipsTheGeneratedCRD(t *testing.T) {
	shipped, err := os.ReadFile(filepath.Join(chartPath, "crds", "assayd.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("the chart ships no Agent CRD: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "assayd.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("read generated CRD: %v", err)
	}
	if string(shipped) != string(generated) {
		t.Error("the CRD in charts/assayd/crds differs from config/crd. The chart would " +
			"install a schema the operator was not built against; re-copy it in the same " +
			"change that regenerates it.")
	}
}

// The local profile is the honest mode: single replicas, and it must still
// render. NFR-3 says this runs on a laptop, and a profile nobody renders is a
// profile that does not work.
func TestLocalProfileRenders(t *testing.T) {
	docs := render(t, "-f", filepath.Join(chartPath, "values-local.yaml"))
	for _, d := range kindsOf(docs, "Deployment") {
		if n := replicasOf(t, d); n != 1 {
			t.Errorf("%s renders %d replicas at the local profile; the laptop profile is "+
				"single-replica everything", nameOf(d), n)
		}
	}
}

// The local profile is enforced by the template, not merely defaulted in a
// values file. A profile that a stray --set can override is a suggestion, and
// this one exists because a laptop cluster cannot schedule what prod asks for.
func TestLocalProfileIsEnforcedNotDefaulted(t *testing.T) {
	docs := render(t, "--set", "profile=local", "--set", "operator.replicas=5")
	for _, d := range kindsOf(docs, "Deployment") {
		if n := replicasOf(t, d); n != 1 {
			t.Errorf("%s rendered %d replicas at profile=local with replicas=5 overridden. "+
				"The profile must win: on a single-node cluster the extra pods would sit "+
				"Pending and the install would look broken for a reason nobody could see.",
				nameOf(d), n)
		}
	}
}

func replicasOf(t *testing.T, d map[string]any) int {
	t.Helper()
	spec := dig(d, "spec")
	if spec == nil {
		return 1
	}
	switch v := spec["replicas"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case nil:
		return 1 // the Kubernetes default
	default:
		t.Fatalf("%s has a non-numeric replicas: %T", nameOf(d), v)
		return 0
	}
}

func nameOf(d map[string]any) string { return toStr(dig(d, "metadata")["name"]) }

func dig(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		cur = next
	}
	return cur
}

func toStr(v any) string {
	s, _ := v.(string)
	return s
}

func toStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, toStr(x))
	}
	return out
}

func matchesAllowlist(name string, allowed map[string]string) bool {
	for k := range allowed {
		if strings.Contains(name, k) {
			return true
		}
	}
	return false
}

// The chart's copy of the operator rules must match what controller-gen
// produces from the kubebuilder markers. Hand-maintained RBAC in a chart is
// exactly what let leader election default on while the role granted no leases.
func TestChartRBACMatchesGeneratedRules(t *testing.T) {
	vendored, err := os.ReadFile(filepath.Join(chartPath, "files", "operator-rules.yaml"))
	if err != nil {
		t.Fatalf("the chart vendors no operator rules: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	if err != nil {
		t.Fatalf("read generated role: %v", err)
	}
	idx := strings.Index(string(generated), "rules:")
	if idx < 0 {
		t.Fatal("the generated role has no rules block")
	}
	if string(vendored) != string(generated)[idx:] {
		t.Error("charts/assayd/files/operator-rules.yaml has drifted from config/rbac/role.yaml. " +
			"The chart would grant permissions that do not match the markers in the code — " +
			"re-copy it in the same change that regenerates the role.")
	}
}

// tier: plus is not implemented. Rendering it as if it worked would be worse
// than refusing, so the chart refuses.
func TestUnimplementedTierIsRefusedNotIgnored(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatal("helm is required")
	}
	out, err := exec.Command("helm", "template", "assayd", chartPath, "--set", "tier=plus").CombinedOutput()
	if err == nil {
		t.Error("tier: plus rendered successfully, so an operator would believe they had " +
			"installed Argo Workflows, Phoenix, OpenFGA and eval runners. None exist.")
	}
	if !strings.Contains(string(out), "not implemented") {
		t.Errorf("the refusal does not say why: %s", out)
	}
}

// A floating tag makes a rollout irreproducible, which is the opposite of what
// a platform built on content-addressed revisions is for.
func TestFloatingImageTagIsRefused(t *testing.T) {
	out, err := exec.Command("helm", "template", "assayd", chartPath,
		"--set", "operator.image.tag=latest").CombinedOutput()
	if err == nil {
		t.Error("operator.image.tag=latest rendered successfully")
	}
	if !strings.Contains(string(out), "Pin a version") {
		t.Errorf("the refusal does not say what to do instead: %s", out)
	}
}

// The prod profile must render what it claims. values.yaml described prod as
// "replicas, anti-affinity, PDBs" while rendering a single unprotected pod —
// the same defect as a flag whose help text names a bound the code does not
// enforce, which is worse than saying nothing.
func TestProdProfileRendersWhatItClaims(t *testing.T) {
	docs := render(t) // prod is the default

	deploys := kindsOf(docs, "Deployment")
	if len(deploys) == 0 {
		t.Fatal("no Deployment rendered")
	}
	for _, d := range deploys {
		if n := replicasOf(t, d); n < 2 {
			t.Errorf("%s renders %d replica(s) at the prod profile. The operator is "+
				"leader-elected, so a standby takes over on node failure instead of waiting "+
				"for a reschedule — one replica means an outage lasts as long as scheduling does.",
				nameOf(d), n)
		}
		spec := dig(d, "spec", "template", "spec")
		if len(dig(spec, "affinity")) == 0 && spec["topologySpreadConstraints"] == nil {
			t.Errorf("%s has neither anti-affinity nor topology spread at prod: both replicas "+
				"can land on one node, so the standby dies with the active one", nameOf(d))
		}
	}

	if len(kindsOf(docs, "PodDisruptionBudget")) == 0 {
		t.Error("no PodDisruptionBudget at the prod profile: a node drain can take every " +
			"operator replica at once, and nothing reconciles until they reschedule")
	}
}

// A PDB that cannot be satisfied is worse than none: it blocks drains forever
// and turns routine node maintenance into an incident.
func TestPodDisruptionBudgetPermitsDrains(t *testing.T) {
	docs := render(t)
	for _, pdb := range kindsOf(docs, "PodDisruptionBudget") {
		spec := dig(pdb, "spec")
		if spec["minAvailable"] != nil && spec["maxUnavailable"] != nil {
			t.Errorf("%s sets both minAvailable and maxUnavailable, which is rejected", nameOf(pdb))
		}
		// With N replicas, minAvailable: N permits no disruption at all.
		if mn, ok := spec["minAvailable"]; ok && mn != nil {
			for _, d := range kindsOf(docs, "Deployment") {
				if toNum(mn) >= replicasOf(t, d) {
					t.Errorf("%s requires minAvailable=%v against %d replicas: no pod can ever "+
						"be evicted, so `kubectl drain` hangs and node maintenance becomes an "+
						"incident", nameOf(pdb), mn, replicasOf(t, d))
				}
			}
		}
	}
}

// The local profile must NOT render a PDB. On a single-node laptop cluster a
// disruption budget can only block things.
func TestLocalProfileHasNoDisruptionBudget(t *testing.T) {
	docs := render(t, "--set", "profile=local")
	if n := len(kindsOf(docs, "PodDisruptionBudget")); n != 0 {
		t.Errorf("the local profile renders %d PodDisruptionBudget(s). On a single-node "+
			"cluster that only blocks drains.", n)
	}
}

func toNum(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// A digest pins content; a tag pins a name that can be moved to point at other
// content after it was signed. assayd's own admission rejects agent images that
// are not digest-pinned and cosign-signed (ADR-0019), so the chart has to be
// able to express the same thing about the operator.
func TestChartSupportsDigestPinning(t *testing.T) {
	const digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	docs := render(t, "--set", "operator.image.digest="+digest)

	for _, d := range kindsOf(docs, "Deployment") {
		spec := dig(d, "spec", "template", "spec")
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			img := toStr(dig2(c)["image"])
			if !strings.Contains(img, "@"+digest) {
				t.Errorf("%s renders image %q with a digest set. A tag can be repointed "+
					"after signing; only a digest names the content that was signed.",
					nameOf(d), img)
			}
			if strings.Contains(img, ":") && strings.Contains(img, "@") {
				// repo:tag@digest is legal but the tag is then decorative and
				// misleading — the digest wins, silently.
				before, _, _ := strings.Cut(img, "@")
				if strings.Count(before, ":") > 0 {
					t.Errorf("%s renders %q, carrying both a tag and a digest. The digest "+
						"wins, so the tag is decorative and reads as though it mattered.",
						nameOf(d), img)
				}
			}
		}
	}
}

// The default values must not point at an image that does not exist. An
// aspirational default is a `helm install` that fails with ImagePullBackOff and
// no explanation of why.
func TestDefaultImageIsPublishable(t *testing.T) {
	docs := render(t)
	for _, d := range kindsOf(docs, "Deployment") {
		spec := dig(d, "spec", "template", "spec")
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			img := toStr(dig2(c)["image"])
			if !strings.HasPrefix(img, "ghcr.io/") {
				t.Errorf("%s renders image %q, which is not in the registry the release "+
					"workflow publishes to", nameOf(d), img)
			}
		}
	}
}

func dig2(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// OCI repository names must be lowercase. The organization is "Quinyte", so any
// path interpolated from github.repository_owner produces an invalid tag —
// which is how the first v0.1.1 release failed, after the build had already run.
// Certificate identities are GitHub URLs and keep their original case, so this
// checks image references specifically rather than every mention of the org.
func TestRegistryPathsAreLowercase(t *testing.T) {
	docs := render(t)
	for _, d := range kindsOf(docs, "Deployment") {
		spec := dig(d, "spec", "template", "spec")
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			img := toStr(dig2(c)["image"])
			repo, _, _ := strings.Cut(img, "@")
			repo, _, _ = strings.Cut(repo, ":")
			if repo != strings.ToLower(repo) {
				t.Errorf("%s renders image %q: an OCI repository name must be lowercase, "+
					"so this is rejected at push time — after the build has already run",
					nameOf(d), img)
			}
		}
	}
}

// Design 07 A5.9: the chart reserves the assayd.dev namespace labels to the
// operator identity, because SPIRE and the Gateway act on those labels and
// neither reads the operator's binding record. The operator fail-closes when
// the policies are absent, so a chart that dropped them would take every Agent
// down with RunNamespaceUnavailable=LabelAuthorityAbsent.
func TestChartShipsTheLabelReservingAdmissionPolicies(t *testing.T) {
	docs := render(t)
	policies := map[string]map[string]any{}
	for _, d := range kindsOf(docs, "ValidatingAdmissionPolicy") {
		policies[nameOf(d)] = d
	}
	bindings := map[string]bool{}
	for _, d := range kindsOf(docs, "ValidatingAdmissionPolicyBinding") {
		bindings[nameOf(d)] = true
	}
	for _, name := range []string{"assayd-namespace-labels", "assayd-gateway-routes"} {
		if policies[name] == nil {
			t.Errorf("the chart renders no ValidatingAdmissionPolicy %s; the operator refuses to create "+
				"run namespaces without it", name)
			continue
		}
		if !bindings[name] {
			t.Errorf("policy %s has no binding, so it validates nothing", name)
		}
		spec, _ := policies[name]["spec"].(map[string]any)
		if spec["failurePolicy"] != "Fail" {
			t.Errorf("policy %s has failurePolicy %v; an unavailable admission chain must deny, not admit", name, spec["failurePolicy"])
		}
	}
	// The permitted identities are rendered INTO the CEL — no params object,
	// so nothing that can go missing turns the policy into a cluster-wide
	// denial of namespace writes — and at core tier the list is exactly the
	// operator's ServiceAccount: every extra identity is a principal that can
	// mint a SPIFFE-selected namespace.
	for _, d := range kindsOf(docs, "ConfigMap") {
		if nameOf(d) == "assayd-operators" {
			t.Error("the chart renders a assayd-operators params ConfigMap; deleting it would deny every " +
				"namespace write in the cluster")
		}
	}
	for _, name := range []string{"assayd-namespace-labels", "assayd-gateway-routes"} {
		spec, _ := policies[name]["spec"].(map[string]any)
		if _, has := spec["paramKind"]; has {
			t.Errorf("policy %s declares a paramKind; identities must be inline", name)
		}
		want := `request.userInfo.username in ["system:serviceaccount:assayd-system:assayd-agent-operator"]`
		found := false
		for _, v := range toList(spec["variables"]) {
			vm, _ := v.(map[string]any)
			if vm["name"] == "operator" && vm["expression"] == want {
				found = true
			}
		}
		if !found {
			t.Errorf("policy %s does not permit exactly the operator's ServiceAccount: want variable "+
				"operator = %s", name, want)
		}
	}
	// Labels can be written through the status and finalize subresources too.
	spec, _ := policies["assayd-namespace-labels"]["spec"].(map[string]any)
	rules := toList(dig(spec, "matchConstraints")["resourceRules"])
	var resources []string
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		resources = append(resources, toStrings(rm["resources"])...)
	}
	for _, want := range []string{"namespaces", "namespaces/status", "namespaces/finalize"} {
		if !contains(resources, want) {
			t.Errorf("the namespace-label policy does not match %s, so a label written through it slips past: %v", want, resources)
		}
	}
	// A parentRef without a namespace refers to the route's own namespace, so
	// the route policy must resolve it that way or a bare-name ref from inside
	// the Gateway's namespace bypasses it.
	rspec, _ := policies["assayd-gateway-routes"]["spec"].(map[string]any)
	for _, v := range toList(rspec["variables"]) {
		vm, _ := v.(map[string]any)
		if vm["name"] == "targetsAssayd" && !strings.Contains(toStr(vm["expression"]), "request.namespace") {
			t.Errorf("targetsAssayd does not resolve a bare-name parentRef to the route's namespace: %s", vm["expression"])
		}
	}
}

func toList(v any) []any { l, _ := v.([]any); return l }

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// The operator is told where it runs through the downward API, so the binding
// records (design 02 A60) land where the Pod actually is.
func TestOperatorIsToldItsNamespace(t *testing.T) {
	docs := render(t)
	deps := kindsOf(docs, "Deployment")
	if len(deps) != 1 {
		t.Fatalf("want one Deployment, got %d", len(deps))
	}
	containers, _ := dig(dig(dig(deps[0], "spec"), "template"), "spec")["containers"].([]any)
	if len(containers) == 0 {
		t.Fatal("no containers")
	}
	c, _ := containers[0].(map[string]any)
	args := toStrings(c["args"])
	found := false
	for _, a := range args {
		if a == "--operator-namespace=$(POD_NAMESPACE)" {
			found = true
		}
	}
	if !found {
		t.Errorf("the operator is not passed --operator-namespace from the downward API: %v", args)
	}
}

// operatorArgs is the rendered operator container's args, for the tests that
// pin what the chart tells the binary.
func operatorArgs(t *testing.T, extra ...string) []string {
	t.Helper()
	deps := kindsOf(render(t, extra...), "Deployment")
	if len(deps) != 1 {
		t.Fatalf("want one Deployment, got %d", len(deps))
	}
	containers, _ := dig(dig(dig(deps[0], "spec"), "template"), "spec")["containers"].([]any)
	if len(containers) == 0 {
		t.Fatal("no containers")
	}
	c, _ := containers[0].(map[string]any)
	return toStrings(c["args"])
}

// The gateway values must REACH the binary, and this is the cheap gate for it.
//
// Design 07 A6.10 records a mutation — rendering `--gateway-enabled=false`
// regardless of the value — that only the k3d e2e lane caught. An e2e lane is
// the expensive gate and the one a contributor does not run; a value that
// silently stops reaching the operator would ship green through `make test` and
// through the kind lane, where `gateway.enabled` is false and the argument's
// value is unobservable.
func TestTheGatewayValuesReachTheOperator(t *testing.T) {
	off := operatorArgs(t)
	if !contains(off, "--gateway-enabled=false") {
		t.Errorf("the default install does not tell the operator the gateway is off: %v. P1 ships "+
			"`gateway.enabled: false` and design 03 §3.1 makes that a declared tier, not an "+
			"absence — the operator has to be TOLD, or it decides by its own default.", off)
	}

	on := operatorArgs(t, "--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080",
		"--set", "gateway.namespace=somewhere-else",
		"--set", "gateway.name=gw", "--set", "gateway.hostnameSuffix=example.test")
	for _, want := range []string{
		"--gateway-enabled=true",
		"--gateway-name=gw",
		"--gateway-namespace=somewhere-else",
		"--gateway-hostname-suffix=example.test",
	} {
		if !contains(on, want) {
			t.Errorf("the operator is not passed %s: %v", want, on)
		}
	}
	// THE regression this test exists for. Design 07 A6.2 measured the route
	// reservation failing open — silently, in the permissive direction — because
	// the Gateway's namespace was conflated with the operator's. The route the
	// operator authors and the CEL that reserves route authorship must name one
	// namespace, so a `gateway.namespace` that quietly fell back to
	// `.Values.namespace` while admission.yaml's $gwNS did not is the same
	// failure from the other end.
	if contains(on, "--gateway-namespace=assayd-system") {
		t.Error("gateway.namespace was set to somewhere-else and the operator was told " +
			"assayd-system. The Gateway's namespace is not the operator's: A6.2 measured an " +
			"ordinary identity attaching a route when those two were conflated.")
	}
}

// gateway.url reaches the operator as --gateway-url, which it injects into every
// agent as ASSAYD_GATEWAY_URL — the egress base URL an agent's tool calls must
// traverse. Unset renders no flag at all, so an install with no Gateway injects
// nothing rather than an empty variable an agent might dial.
func TestTheGatewayURLReachesTheOperatorOnlyWhenSet(t *testing.T) {
	if args := operatorArgs(t); containsPrefix(args, "--gateway-url") {
		t.Errorf("the default install passes %v; with no gateway.url nothing may be injected", args)
	}
	args := operatorArgs(t, "--set", "gateway.url=http://gw.example:8081")
	if !contains(args, "--gateway-url=http://gw.example:8081") {
		t.Errorf("gateway.url did not reach the operator: %v", args)
	}
}

func containsPrefix(xs []string, prefix string) bool {
	for _, x := range xs {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

// And the fallback, which admission.yaml's `$gwNS` performs identically. A
// single-namespace install must be unchanged, so an unset value means the
// namespace the OPERATOR runs in — not the Helm release namespace, which
// hack/e2e.sh proves is a different thing by installing into `default`.
func TestAnUnsetGatewayNamespaceFallsBackToTheOperators(t *testing.T) {
	args := operatorArgs(t, "--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080",
		"--set", "namespace=assayd-elsewhere")
	if !contains(args, "--gateway-namespace=assayd-elsewhere") {
		t.Errorf("an unset gateway.namespace did not fall back to .Values.namespace: %v. "+
			"admission.yaml renders `$gwNS` with the same `| default .Values.namespace`, and if "+
			"the two disagree the reservation guards a namespace no route names.", args)
	}
}

// Every rendered document carries apiVersion and kind. `helm template` does
// not validate that and `helm upgrade` does: a Helm whitespace trim once glued
// a policy's apiVersion onto the comment line above it, so `helm template`
// rendered a document the test suite still found by kind and the install
// failed with "apiVersion not set".
func TestEveryRenderedDocumentHasAnAPIVersion(t *testing.T) {
	for _, d := range render(t, "-f", filepath.Join(chartPath, "values-local.yaml")) {
		if d["apiVersion"] == nil || d["kind"] == nil {
			t.Errorf("a rendered document has no apiVersion or kind: %v", nameOf(d))
		}
	}
}

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

const chartPath = "../../charts/plume"

// render runs `helm template` and returns the documents it produced.
func render(t *testing.T, extraArgs ...string) []map[string]any {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatalf("helm is required to test the chart, and a skipped chart test is an " +
			"untested deployment: install helm")
	}
	args := append([]string{"template", "plume", chartPath}, extraArgs...)
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
		{"plume.dev/agents", "the object this operator exists to reconcile"},
		{"plume.dev/agents/status", "conditions and phase are how it reports anything"},
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
	shipped, err := os.ReadFile(filepath.Join(chartPath, "crds", "plume.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("the chart ships no Agent CRD: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "plume.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("read generated CRD: %v", err)
	}
	if string(shipped) != string(generated) {
		t.Error("the CRD in charts/plume/crds differs from config/crd. The chart would " +
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
		t.Error("charts/plume/files/operator-rules.yaml has drifted from config/rbac/role.yaml. " +
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
	out, err := exec.Command("helm", "template", "plume", chartPath, "--set", "tier=plus").CombinedOutput()
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
	out, err := exec.Command("helm", "template", "plume", chartPath,
		"--set", "operator.image.tag=latest").CombinedOutput()
	if err == nil {
		t.Error("operator.image.tag=latest rendered successfully")
	}
	if !strings.Contains(string(out), "Pin a version") {
		t.Errorf("the refusal does not say what to do instead: %s", out)
	}
}

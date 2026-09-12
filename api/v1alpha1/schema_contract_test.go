// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

// These tests read the *generated* CRD, because that is what a cluster enforces
// and what a developer's kubectl talks to. They assert properties of the schema
// itself: a fixture-counting test proves only that the fixture is unchanged, so
// there are none here.

func agentCRD(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "assayd.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("read generated CRD (run `make manifests`): %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse generated CRD: %v", err)
	}
	return doc
}

func agentVersion(t *testing.T) map[string]any {
	t.Helper()
	versions := agentCRD(t)["spec"].(map[string]any)["versions"].([]any)
	return versions[0].(map[string]any)
}

func agentSchema(t *testing.T) map[string]any {
	t.Helper()
	return agentVersion(t)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
}

// descend walks a dotted path through openAPI `properties`, transparently
// stepping into array `items`.
func descend(t *testing.T, node map[string]any, dotted string) map[string]any {
	t.Helper()
	cur := node
	for _, p := range strings.Split(dotted, ".") {
		props, ok := cur["properties"].(map[string]any)
		if !ok {
			t.Fatalf("no properties at %q while descending %q", p, dotted)
		}
		next, ok := props[p].(map[string]any)
		if !ok {
			t.Fatalf("no such field %q while descending %q", p, dotted)
		}
		if items, ok := next["items"].(map[string]any); ok {
			next = items
		}
		cur = next
	}
	return cur
}

// The front door's DX budget, stated as a schema property rather than as a line
// count: adding a required field anywhere on the common path fails this test the
// moment `make manifests` runs.
func TestOnlyTheUnavoidableFieldsAreRequired(t *testing.T) {
	spec := descend(t, agentSchema(t), "spec")

	if req, ok := spec["required"]; ok {
		t.Errorf("spec.required is %v — every top-level field must be optional so a "+
			"developer opts into governance rather than out of it", req)
	}

	// Once a developer opts into a block, these are the only things we may demand
	// of them. Anything else must be defaulted or optional.
	allowed := map[string]bool{
		"runtime.image":           true, // there is no agent without an image
		"external.endpoint":       true, // nor an external one without a URL
		"knowledge.name":          true,
		"knowledge.version":       true, // an unpinned graph is the thing we refuse
		"tools.name":              true,
		"gates.evalSuiteRef":      true,
		"runtime.sandbox.profile": true,

		// The endpoint union (A53). Each of these is the thing that makes an
		// identity an identity rather than a category, which is the whole reason
		// the flat strings were replaced: an entry with no arm names nothing, and
		// an arm with no instance names EVERY resource on that arm — an azureopenai
		// entry without an endpoint permits every Azure deployment in the tenant,
		// including ones with different BAA posture. "Required is a demand" and
		// these are the demands the type exists to make.
		"llm.providers.arm":                        true,
		"llm.providers.azureopenai.endpoint":       true,
		"llm.providers.vertexai.projectId":         true,
		"llm.providers.vertexai.region":            true,
		"llm.providers.bedrock.region":             true,
		"llm.providers.custom.host":                true,
		"llm.egressAllowlist.arm":                  true,
		"llm.egressAllowlist.azureopenai.endpoint": true,
		"llm.egressAllowlist.vertexai.projectId":   true,
		"llm.egressAllowlist.vertexai.region":      true,
		"llm.egressAllowlist.bedrock.region":       true,
		"llm.egressAllowlist.custom.host":          true,
		"llm.fallback.arm":                         true,
		"llm.fallback.azureopenai.endpoint":        true,
		"llm.fallback.vertexai.projectId":          true,
		"llm.fallback.vertexai.region":             true,
		"llm.fallback.bedrock.region":              true,
		"llm.fallback.custom.host":                 true,
	}

	// Every required field is a demand, whether its type is a scalar or an object:
	// requiring a block obliges the developer to fill the block in.
	var found []string
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		props, _ := node["properties"].(map[string]any)
		if req, ok := node["required"].([]any); ok {
			for _, r := range req {
				found = append(found, path+"."+r.(string))
			}
		}
		for k, v := range props {
			sub, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if items, ok := sub["items"].(map[string]any); ok {
				sub = items
			}
			walk(sub, path+"."+k)
		}
	}

	// Only assayd's own fields; corev1 EnvVar and ResourceRequirements carry their
	// own required leaves and are not ours to police.
	for _, top := range []string{"runtime", "external", "knowledge", "tools", "gates", "llm", "card", "loop", "expose"} {
		walk(descend(t, agentSchema(t), "spec."+top), top)
	}

	for _, f := range found {
		if strings.Contains(f, ".env.") || strings.Contains(f, ".envFrom.") || strings.Contains(f, ".resources.") {
			continue // corev1 subtrees
		}
		if !allowed[f] {
			t.Errorf("spec.%s is required and not on the allowed list — default it, make "+
				"it optional, or justify it by adding it to this test", f)
		}
	}
}

// Design 02 §3.1 fixes what `kubectl get ag` shows. The argument that a
// twenty-condition status stays legible rests entirely on these columns, so they
// are a contract, not a convenience.
func TestPrinterColumnsMatchTheDesign(t *testing.T) {
	want := []string{"Phase", "Active", "Candidate", "Eval", "Cost/Day", "Age"}

	raw, _ := agentVersion(t)["additionalPrinterColumns"].([]any)
	var got []string
	for _, c := range raw {
		got = append(got, c.(map[string]any)["name"].(string))
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("printer columns are %v, design 02 §3.1 specifies %v.\n"+
			"These columns are the whole reason a vocabulary this size is defensible: Eval "+
			"and Cost/Day are exactly what a developer would otherwise read conditions for.\n"+
			"No count is written here: it was 21 in this message while the design declared 34, "+
			"and a literal nobody re-derives is a second place to be wrong.", got, want)
	}
}

// The condition vocabulary is a flat []metav1.Condition, so the schema cannot
// enforce it. This test is the enforcement — and until this round it was not.
//
// What it used to do: assert len(designConditions()) == 21 and check for
// duplicates. It compared no names to anything, so a critic renamed
// CondPricingStale to TotallyWrongName and the test SURVIVED. The count was
// also checked against the wrong number: §3.1 declared 34 while this file
// declared 21, and thirteen conditions raised by designs 03 and 20 had no
// constant at all. A test that pins a cardinality against a literal nobody
// re-derives is a test that reports health while the thing it guards drifts.
//
// It now parses §3.1's list out of the design and compares NAMES, both
// directions. The design is the source of truth: adding a condition to the CRD
// without declaring it fails here, and so does declaring one the design never
// approved.
func TestConditionVocabularyIsClosed(t *testing.T) {
	design := conditionsDeclaredByDesign(t)
	code := designConditions()

	// designConditions() is a hand-maintained list, so it can delete its own
	// cases: adding CondBogusDrifted to the constant block WITHOUT adding it here
	// left this test green, even though its comment claimed the opposite. That is
	// the same unsoundness leaf_test.go avoids by reflecting over an upstream
	// type. The constants are therefore parsed out of the source, and
	// designConditions() becomes a thing under test rather than the oracle.
	declared := conditionConstantsInSource(t)
	inList := map[string]bool{}
	for _, c := range code {
		inList[string(c)] = true
	}
	for _, c := range declared {
		if !inList[string(c)] {
			t.Errorf("agent_types.go declares the condition constant %q and designConditions() "+
				"omits it, so it is exempt from every check below — including the one that would "+
				"have told you the design never approved it.", c)
		}
	}

	dupes := func(t *testing.T, where string, xs []string) map[string]bool {
		t.Helper()
		seen := map[string]bool{}
		for _, x := range xs {
			if seen[x] {
				t.Errorf("%s declares %q twice", where, x)
			}
			seen[x] = true
		}
		return seen
	}
	inDesign := dupes(t, "design 02 §3.1", design)
	codeStrings := make([]string, 0, len(code))
	for _, c := range code {
		codeStrings = append(codeStrings, string(c))
	}
	inCode := dupes(t, "designConditions()", codeStrings)

	for _, c := range design {
		if !inCode[c] {
			t.Errorf("design 02 §3.1 declares %q and no constant in this package does.\n"+
				"A condition the code cannot name is one no controller can set, so NFR-8's "+
				"promise that a degraded path is never silent does not hold for it.", c)
		}
	}
	for _, c := range codeStrings {
		if !inDesign[c] {
			t.Errorf("this package declares %q and design 02 §3.1 does not.\n"+
				"The vocabulary is closed: a condition reaches the API only through the design, "+
				"or consumers are matching on a string no document defines.", c)
		}
	}
}

// conditionsDeclaredByDesign reads the vocabulary out of design 02 §3.1.
//
// Parsing a document from a unit test is unusual and is the point: the design
// is the artifact that decides this list, so a copy of it in Go would be a
// second place to be wrong — which is exactly how the count reached 21-vs-34.
// A missing or unparseable design is a FAILURE, never a skip: a gate that
// quietly stops running is worse than one that was never written, because it
// still reports green.
// conditionConstantsInSource reads every Cond… constant out of agent_types.go.
// Parsing our OWN source is exactly what design 03 A23 warns against when the
// parsed thing is the registry under test — here it is the opposite: the
// constants are the ground truth a human writes, and designConditions() is the
// derived list that must keep up with them.
func conditionConstantsInSource(t *testing.T) []string {
	t.Helper()
	const src = "agent_types.go"
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("cannot read %s: %v — this test cannot pass without it", src, err)
	}
	m := regexp.MustCompile(`(?m)^\s*Cond\w+\s*=\s*ConditionType\("(\w+)"\)`).FindAllStringSubmatch(string(b), -1)
	if len(m) < 20 {
		t.Fatalf("found only %d condition constants in %s; a partial parse would let this test "+
			"pass on the handful it happened to read", len(m), src)
	}
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out
}

func conditionsDeclaredByDesign(t *testing.T) []string {
	t.Helper()
	const design = "../../docs/designs/02-agent-crd-operator.md"
	b, err := os.ReadFile(design)
	if err != nil {
		t.Fatalf("cannot read %s: %v — this test cannot pass without it", design, err)
	}
	m := regexp.MustCompile(`(?s)\n\s*conditions: \[(.*?)\]`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("no `conditions: [...]` block in %s §3.1; if the shape moved, move this "+
			"parser with it rather than deleting the check", design)
	}
	var out []string
	for _, raw := range strings.Split(m[1], ",") {
		if name := strings.TrimSpace(raw); name != "" {
			out = append(out, name)
		}
	}
	if len(out) < 20 {
		t.Fatalf("parsed only %d conditions from %s; a partial parse would let this test pass "+
			"on the handful it happened to read", len(out), design)
	}
	return out
}

// The vocabulary is closed in Go, so this checks the thing that actually
// enforces it: conditionSet.set takes a ConditionType, and a string literal
// will not convert implicitly. There is nothing to compare against a CEL list
// any more — A52 withdrew it, because one unrecognised type rejected the whole
// status write and cost an Agent its phase, its digest and its Ready.
func TestConditionConstantsAreTyped(t *testing.T) {
	b, err := os.ReadFile("agent_types.go")
	if err != nil {
		t.Fatalf("cannot read agent_types.go: %v", err)
	}
	if bad := regexp.MustCompile(`(?m)^\s*Cond\w+\s*=\s*"`).FindAllString(string(b), -1); len(bad) > 0 {
		t.Errorf("%d condition constants are untyped string literals: %v\n"+
			"An untyped constant lets a bare string reach conditionSet.set, which is how a typo "+
			"compiles and then says nothing at runtime.", len(bad), bad)
	}
}

// validations returns the x-kubernetes-validations rules on a schema node, as
// rule → message.
func validations(node map[string]any) map[string]string {
	out := map[string]string{}
	raw, _ := node["x-kubernetes-validations"].([]any)
	for _, r := range raw {
		m := r.(map[string]any)
		rule, _ := m["rule"].(string)
		msg, _ := m["message"].(string)
		out[rule] = msg
	}
	return out
}

func enumOf(node map[string]any) []string {
	raw, _ := node["enum"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	sort.Strings(out)
	return out
}

func propertyNames(node map[string]any) []string {
	props, _ := node["properties"].(map[string]any)
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Design 03 §3.4.4 (ADR-0034 C2): expose.a2a.auth is required and has NO
// default, and apikey is a value. Required by CEL rather than by `required`, so
// the refusal carries a message that names the fix;
// test/envtest/auth_admission_test.go proves a real API server applies it.
func TestExposeAuthIsRequiredAndHasNoDefault(t *testing.T) {
	auth := descend(t, agentSchema(t), "spec.expose.a2a.auth")
	if d, ok := auth["default"]; ok {
		t.Errorf("spec.expose.a2a.auth defaults to %v. Design 03 §3.4.4 forbids a default: it "+
			"would pick the Agent's identity story for the developer, and the API server writes a "+
			"default into every stored object, where nobody can tell it from a choice.", d)
	}
	if got, want := strings.Join(enumOf(auth), ","), "apikey,none,oauth"; got != want {
		t.Errorf("spec.expose.a2a.auth's enum is %s, want %s", got, want)
	}

	const msg = "spec.expose.a2a.auth is required and has no default: set apikey (keys in the " +
		"group named for this namespace), none (an unauthenticated route), or oauth (not " +
		"compilable until design 06 ships)"
	a2a := descend(t, agentSchema(t), "spec.expose.a2a")
	if got, ok := validations(a2a)["has(self.auth)"]; !ok || got != msg {
		t.Errorf("spec.expose.a2a has no has(self.auth) rule with §3.4.4's message.\n got: %q (present %v)\nwant: %q",
			got, ok, msg)
	}

	// The slice adds no allowedGroups (§1.1): every Agent admits the group named
	// for its namespace, which is why no group change can arise in the slice.
	if got := strings.Join(propertyNames(a2a), ","); got != "auth,visibility" {
		t.Errorf("spec.expose.a2a has fields %s; the slice owes exactly auth and visibility", got)
	}
}

// Design 03 §3.1 (ADR-0034 B2): spec.budget is refused while nothing enforces
// it. The BudgetSpec type stays, because an Agent stored before the refusal may
// carry one and the revision projection still reads it.
func TestBudgetIsRefusedAtAdmission(t *testing.T) {
	const msg = "spec.budget is not enforced yet: no gateway rate limit and no spend backstop " +
		"exist (ADR-0030 step 1). Remove spec.budget; it is accepted again when design 03's " +
		"-ratelimit and design 04's spend aggregation ship."
	if got, ok := validations(descend(t, agentSchema(t), "spec"))["!has(self.budget)"]; !ok || got != msg {
		t.Errorf("spec has no !has(self.budget) rule with §3.1's message.\n got: %q (present %v)\nwant: %q",
			got, ok, msg)
	}
}

// status.auth is design 03 §3.3's schema in the first slice's subset (§1.1):
// "without its group and key-source transaction fields". The field sets are
// pinned exactly, so a later-scope field (targetGroups, targetKeySource,
// afterShift) cannot arrive unapproved. Nothing is required, because a refused
// Adopt records only its kind and stage, and {mode: none} carries no key
// source, groups, digest or verification.
func TestStatusAuthIsTheSlicesSubset(t *testing.T) {
	s := agentSchema(t)
	for path, want := range map[string]string{
		"status.auth": "admittedGroups,appliedDigest,keySource,mode,transaction,verified",
		"status.auth.transaction": "beforeObserved,beforeRevision,deadline,kind,probe,refusedMode," +
			"stage,targetDigest,targetMode,written",
		"status.auth.verified":          "replicasDeclared,replicasProbed",
		"status.auth.transaction.probe": "after,before",
	} {
		node := descend(t, s, path)
		if got := strings.Join(propertyNames(node), ","); got != want {
			t.Errorf("%s has fields %s, want %s", path, got, want)
		}
		if req, ok := node["required"]; ok {
			t.Errorf("%s requires %v; every status.auth field is optional", path, req)
		}
	}
	for path, want := range map[string]string{
		"status.auth.mode":                    "apikey,none",
		"status.auth.transaction.kind":        "Adopt,Create,Lock,Loosen,Narrow",
		"status.auth.transaction.targetMode":  "apikey,none",
		"status.auth.transaction.refusedMode": "apikey,none,oauth",
	} {
		if got := strings.Join(enumOf(descend(t, s, path)), ","); got != want {
			t.Errorf("%s's enum is %s, want %s", path, got, want)
		}
	}
}

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/parser"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// -update rewrites the golden files from what the compiler renders now. Run it
// only for a change that is MEANT to move the emitted policy, and read the diff:
// the golden file is the one place a reviewer sees the exact object the gateway
// will be handed.
//
//	go test ./internal/compiler -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden files under testdata/")

// goldenHeader is written above every golden file, because every tracked file
// carries an SPDX identifier (test/docs/licensing_test.go) and REUSE.toml does
// not cover testdata.
const goldenHeader = "# SPDX-FileCopyrightText: 2026 Quinyte\n" +
	"# SPDX-License-Identifier: Apache-2.0\n" +
	"#\n" +
	"# GOLDEN FILE, rendered by the code under test. Regenerate with\n" +
	"#   go test ./internal/compiler -run Golden -update\n" +
	"# and read the diff: this is the exact object the gateway is handed.\n"

// assertGolden compares a rendered object with its committed golden file. The
// comparison is of the whole file, byte for byte, so a field added, removed or
// reordered by the renderer is a failure and not a tolerance.
func assertGolden(t *testing.T, file string, obj any) {
	t.Helper()
	body, err := yaml.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal %s: %v", file, err)
	}
	got := append([]byte(goldenHeader), body...)
	path := filepath.Join("testdata", file)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create it)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the rendered object differs from %s.\n--- rendered ---\n%s\n--- golden ---\n%s",
			path, got, want)
	}
}

// paymentsAgent is §3.4.4's example Agent: namespace `payments`, whose run
// namespace is `assayd-run-payments` (design 02 §3.2).
func paymentsAgent(expose *assaydv1alpha1.ExposeSpec) *assaydv1alpha1.Agent {
	return &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pricer", Namespace: "payments",
			UID: k8stypes.UID("0b5f4c2e-7d1a-4e59-9c3b-6a8d2f1e0c47"),
		},
		Spec: assaydv1alpha1.AgentSpec{Expose: expose},
	}
}

const paymentsRunNS = "assayd-run-payments"

func apikey() *assaydv1alpha1.ExposeSpec {
	return &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "apikey"}}
}

// goldenPaymentsDigest pins the canonical serialization Digest hashes, over the
// golden policy. It moves when the policy moves — then the golden file moves
// with it — or when the serialization changes, which re-keys every recorded
// `status.auth.appliedDigest` and so is a migration, not a test edit.
const goldenPaymentsDigest = "sha256:9427a5ab34ea9849ba2acd708ab3d1cbe83003b79550283fcf1f54ff81842253"

// (a) §8.1's first unit golden: `auth: apikey`, with no `allowedGroups` (the
// field does not exist in the slice), in namespace `payments`, compiles to the
// namespace group's policy — §3.4.4's example, verbatim in shape.
//
// `apikey` is not yet a CRD value (design 02 owes the enum, §3.4.4), so this
// Agent cannot be stored today. The compiler is pure, and takes the value the
// slice will admit.
func TestGoldenAnAPIKeyAgentCompilesToItsNamespaceGroup(t *testing.T) {
	target, err := CompileAuth(paymentsAgent(apikey()), paymentsRunNS)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if target.Mode != AuthModeAPIKey || target.Policy == nil {
		t.Fatalf("auth: apikey compiled to mode %q with policy %v; want apikey and a policy",
			target.Mode, target.Policy)
	}
	assertGolden(t, "pricer-auth.golden.yaml", target.Policy.Object)

	d, err := Digest(target.Policy)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if target.Digest != d {
		t.Errorf("the target's digest %q is not the digest of the policy it carries (%q), so a "+
			"recorded targetDigest would never match the policy that was written", target.Digest, d)
	}
	if d != goldenPaymentsDigest {
		t.Errorf("the golden policy's digest is %s, pinned as %s. If the golden file moved, move "+
			"this too; if only the digest moved, the canonical form changed and every recorded "+
			"appliedDigest is re-keyed", d, goldenPaymentsDigest)
	}
}

// (b) An Agent with no `expose` block compiles to the SAME policy (§3.4.4: the
// operator emits a serving route for it regardless, and that route must carry
// `-auth`). Compared with the same golden file as (a), so any difference
// between the two paths fails here.
//
// An `expose` block with no `a2a` arm is read the same way: it declares nothing
// about A2A authentication, exactly as an absent block does. The design names
// only the absent block; this is the author's reading, and it is pinned so that
// changing it is a visible decision.
func TestGoldenAnAgentWithNoExposeBlockCompilesToTheSamePolicy(t *testing.T) {
	for name, expose := range map[string]*assaydv1alpha1.ExposeSpec{
		"no expose block":          nil,
		"expose block with no a2a": {},
	} {
		t.Run(name, func(t *testing.T) {
			target, err := CompileAuth(paymentsAgent(expose), paymentsRunNS)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if target.Mode != AuthModeAPIKey || target.Policy == nil {
				t.Fatalf("compiled to mode %q with policy %v; §3.4.4's derived default is the "+
					"API-key policy", target.Mode, target.Policy)
			}
			assertGolden(t, "pricer-auth.golden.yaml", target.Policy.Object)
		})
	}
}

// `auth: none` compiles, to no policy and no digest (§3.3.1: its target is
// `{targetMode: none}`). The route's marker is asserted by the route golden in
// internal/controller, which marks the rendered route with RouteAuthLabels.
func TestNoneCompilesToNoPolicy(t *testing.T) {
	agent := paymentsAgent(&assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}})
	target, err := CompileAuth(agent, paymentsRunNS)
	if err != nil {
		t.Fatalf("auth: none must always compile (§3.3.1): %v", err)
	}
	if target.Mode != AuthModeNone || target.Policy != nil || target.Digest != "" {
		t.Errorf("auth: none compiled to %+v; want mode none, no policy and no digest", target)
	}
}

// Anything but `none` and `apikey` does not compile in the slice — `oauth`
// included, which every stored Agent with an `expose.a2a` block carries today
// because the API server persisted the old default (§3.4.4). An empty value is
// refused too, rather than guessed: after design 02 it cannot be admitted, and
// guessing a mode for it is a security decision nobody made.
func TestOnlyNoneAndAPIKeyCompile(t *testing.T) {
	for _, mode := range []string{"oauth", "", "ApiKey", "jwt"} {
		agent := paymentsAgent(&assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: mode}})
		target, err := CompileAuth(agent, paymentsRunNS)
		var nc *NotCompilableError
		if !errors.As(err, &nc) {
			t.Errorf("auth %q compiled to %+v (err %v); want a NotCompilableError", mode, target, err)
			continue
		}
		if !strings.Contains(err.Error(), "spec.expose.a2a.auth") {
			t.Errorf("auth %q: the error does not name the field to fix: %v", mode, err)
		}
		if target.Policy != nil {
			t.Errorf("auth %q: a policy was returned beside the error", mode)
		}
	}
}

// The properties §3.4.4 states about the emitted policy, asserted on the
// rendered object one by one, so that a mutation fails on a line that names the
// property and not only on a golden diff.
func TestTheEmittedPolicyHasTheSliceShape(t *testing.T) {
	p, err := AuthPolicy(AuthInput{AgentName: "pricer", AgentNamespace: "payments",
		AgentUID: k8stypes.UID("uid-1"), RunNamespace: paymentsRunNS})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if p.GetAPIVersion() != "agentgateway.dev/v1alpha1" || p.GetKind() != "AgentgatewayPolicy" {
		t.Errorf("rendered %s %s, want agentgateway.dev/v1alpha1 AgentgatewayPolicy",
			p.GetAPIVersion(), p.GetKind())
	}
	if p.GetNamespace() != paymentsRunNS {
		t.Errorf("the policy is in %q; it must sit beside its target in the run namespace, "+
			"because targetRefs select same-namespace objects only (§3.2)", p.GetNamespace())
	}
	wantLabels := map[string]string{
		"assayd.dev/agent-uid":       "uid-1",
		"assayd.dev/agent":           "pricer",
		"assayd.dev/agent-namespace": "payments",
	}
	if got := p.GetLabels(); len(got) != len(wantLabels) {
		t.Errorf("labels are %v, want exactly %v. In particular no assayd.dev/revision: the "+
			"policy follows the Agent's current spec and belongs to no revision (§3.2)", got, wantLabels)
	}
	for k, v := range wantLabels {
		if p.GetLabels()[k] != v {
			t.Errorf("label %s is %q, want %q", k, p.GetLabels()[k], v)
		}
	}

	mode, found, _ := unstructured.NestedString(p.Object, "spec", "traffic", "apiKeyAuthentication", "mode")
	if !found || mode != "Strict" {
		t.Errorf("apiKeyAuthentication.mode is %q (present: %v). §3.4.4: Strict is EMITTED, not "+
			"left to the CRD default, because a default is not an absence", mode, found)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(p.Object,
		"spec", "traffic", "apiKeyAuthentication", "location"); found {
		t.Error("apiKeyAuthentication.location is emitted. §3.4.4 leaves it to its default, the " +
			"`Authorization: Bearer` header the e2e measured, and pins its absence")
	}
	sel, _, _ := unstructured.NestedStringMap(p.Object,
		"spec", "traffic", "apiKeyAuthentication", "configMapSelector", "matchLabels")
	if len(sel) != 1 || sel["assayd.dev/api-keys"] != "true" {
		t.Errorf("the key source is %v; the slice's is the constant {assayd.dev/api-keys: \"true\"} "+
			"(§1.1), with no flag and no chart value", sel)
	}
	if action, _, _ := unstructured.NestedString(p.Object, "spec", "traffic", "authorization", "action"); action != "Allow" {
		t.Errorf("authorization.action is %q; it must be Allow, because a CEL error under Deny "+
			"fails open (§3.4.2)", action)
	}
	exprs, _, _ := unstructured.NestedStringSlice(p.Object,
		"spec", "traffic", "authorization", "policy", "matchExpressions")
	if len(exprs) != 1 || exprs[0] != `apiKey.group == "payments"` {
		t.Errorf("matchExpressions are %q; want the one namespace group, "+
			"[apiKey.group == \"payments\"] (§3.4.4)", exprs)
	}
}

// An empty input never compiles. The dangerous one is the namespace: it is the
// group, and `apiKey.group == ""` would admit whatever key the gateway reads
// with no group, rather than nobody. The others would emit a policy that is
// unowned, unnamed, or placed nowhere.
func TestAnEmptyInputDoesNotCompile(t *testing.T) {
	full := AuthInput{AgentName: "pricer", AgentNamespace: "payments",
		AgentUID: k8stypes.UID("uid-1"), RunNamespace: paymentsRunNS}
	for name, mutate := range map[string]func(*AuthInput){
		"agent name":      func(in *AuthInput) { in.AgentName = "" },
		"agent namespace": func(in *AuthInput) { in.AgentNamespace = "" },
		"agent uid":       func(in *AuthInput) { in.AgentUID = "" },
		"run namespace":   func(in *AuthInput) { in.RunNamespace = "" },
	} {
		in := full
		mutate(&in)
		if p, err := AuthPolicy(in); err == nil {
			t.Errorf("an empty %s compiled to a policy: %v", name, p.Object)
		}
	}
}

// ---- the CEL encoder (§3.4.2) -------------------------------------------------

// The group literal is produced by CEL's own printer, and proven by CEL's own
// parser: every string below must round-trip to a single `==` whose right-hand
// side is exactly that string.
//
// **No real namespace can reach this.** A namespace is a DNS label — lowercase
// alphanumerics and `-` — so no character in it needs escaping, and the golden
// files above would pass with the literal concatenated into the expression. The
// encoder is therefore tested HERE, directly, with strings a namespace cannot
// hold. It is not dead weight: §3.4.4's later `allowedGroups` feeds the same
// function, and §3.4.2's tool names, which it will also encode, can carry any
// of these.
func TestTheGroupLiteralIsCELEncodedNotConcatenated(t *testing.T) {
	for _, s := range []string{
		`"`, `\`, `\"`, "\n", "\r\n", "\t", "\x00", " ", "'", "`",
		`payments" || true || "`, // the injection concatenation would admit
		`\" || true || \"`,
		"ñandú", "支付", "🙂", "${group}", strings.Repeat("a", 300),
	} {
		expr, err := admitGroupExpression(s)
		if err != nil {
			t.Errorf("encode %q: %v", s, err)
			continue
		}
		parsed, iss := parser.Parse(common.NewTextSource(expr))
		if iss != nil && len(iss.GetErrors()) > 0 {
			t.Errorf("group %q encoded to %q, which CEL does not parse: %s", s, expr, iss.ToDisplayString())
			continue
		}
		e := parsed.Expr()
		if e.Kind() != ast.CallKind || e.AsCall().FunctionName() != operators.Equals ||
			len(e.AsCall().Args()) != 2 {
			t.Errorf("group %q encoded to %q, which parses as something other than one `==`", s, expr)
			continue
		}
		lhs, rhs := e.AsCall().Args()[0], e.AsCall().Args()[1]
		if lhs.Kind() != ast.SelectKind || lhs.AsSelect().FieldName() != "group" ||
			lhs.AsSelect().Operand().Kind() != ast.IdentKind || lhs.AsSelect().Operand().AsIdent() != "apiKey" {
			t.Errorf("group %q encoded to %q, whose left-hand side is not apiKey.group", s, expr)
		}
		if rhs.Kind() != ast.LiteralKind || rhs.AsLiteral() != types.String(s) {
			t.Errorf("group %q encoded to %q, whose literal does not decode back to the group", s, expr)
		}
	}
}

// ---- the digest (§3.3's targetDigest and appliedDigest) -----------------------

var digestShape = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func compiledPayments(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	p, err := AuthPolicy(AuthInput{AgentName: "pricer", AgentNamespace: "payments",
		AgentUID: k8stypes.UID("uid-1"), RunNamespace: paymentsRunNS})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return p
}

// Two constructions of one policy, whose maps were built in different orders,
// digest the same — and one construction digests the same every time. Go
// randomizes map iteration, so a serializer that walked maps in iteration order
// would differ between the repetitions below with near certainty.
func TestTheDigestIsIndependentOfMapOrder(t *testing.T) {
	p := compiledPayments(t)
	want, err := Digest(p)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if !digestShape.MatchString(want) {
		t.Fatalf("digest %q is not `sha256:<64 lowercase hex>`", want)
	}

	// The same object, keys written in the reverse of the compiler's order.
	reordered := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(`
spec:
  traffic:
    authorization:
      policy:
        matchExpressions: ['apiKey.group == "payments"']
      action: Allow
    apiKeyAuthentication:
      configMapSelector:
        matchLabels: {assayd.dev/api-keys: "true"}
      mode: Strict
  targetRefs:
  - name: pricer-serving
    kind: HTTPRoute
    group: gateway.networking.k8s.io
metadata:
  labels:
    assayd.dev/agent-namespace: payments
    assayd.dev/agent: pricer
    assayd.dev/agent-uid: uid-1
  namespace: assayd-run-payments
  name: pricer-auth
kind: AgentgatewayPolicy
apiVersion: agentgateway.dev/v1alpha1
`), &reordered.Object); err != nil {
		t.Fatalf("parse the reordered policy: %v", err)
	}
	if got, _ := Digest(reordered); got != want {
		t.Errorf("the same policy built in another key order digests %s, not %s", got, want)
	}
	for i := 0; i < 64; i++ {
		if got, _ := Digest(compiledPayments(t)); got != want {
			t.Fatalf("construction %d of one policy digested %s, not %s: the digest depends on "+
				"map iteration order", i, got, want)
		}
	}
}

// Every owned field moves the digest. The owned fields are apiVersion, kind,
// metadata.name, metadata.namespace, the three provenance labels, and the whole
// of spec — everything the compiler writes (see Digest).
func TestTheDigestCoversEveryOwnedField(t *testing.T) {
	base, _ := Digest(compiledPayments(t))
	for name, mutate := range map[string]func(u *unstructured.Unstructured){
		"apiVersion":      func(u *unstructured.Unstructured) { u.SetAPIVersion("agentgateway.dev/v1") },
		"kind":            func(u *unstructured.Unstructured) { u.SetKind("AgentgatewayBackend") },
		"name":            func(u *unstructured.Unstructured) { u.SetName("pricer-auth2") },
		"namespace":       func(u *unstructured.Unstructured) { u.SetNamespace("assayd-run-other") },
		"agent-uid label": func(u *unstructured.Unstructured) { setLabel(u, "assayd.dev/agent-uid", "uid-2") },
		"agent label":     func(u *unstructured.Unstructured) { setLabel(u, "assayd.dev/agent", "other") },
		"agent-ns label":  func(u *unstructured.Unstructured) { setLabel(u, "assayd.dev/agent-namespace", "x") },
		"targetRef name":  func(u *unstructured.Unstructured) { setRef(u, "name", "other-serving") },
		"targetRef kind":  func(u *unstructured.Unstructured) { setRef(u, "kind", "Service") },
		"mode dropped":    func(u *unstructured.Unstructured) { unset(u, "apiKeyAuthentication", "mode") },
		"mode Optional":   func(u *unstructured.Unstructured) { set(u, "Optional", "apiKeyAuthentication", "mode") },
		"location added": func(u *unstructured.Unstructured) {
			set(u, map[string]any{"header": map[string]any{"name": "X-Key"}}, "apiKeyAuthentication", "location")
		},
		"key source value": func(u *unstructured.Unstructured) {
			set(u, "e2e", "apiKeyAuthentication", "configMapSelector", "matchLabels", "assayd.dev/api-keys")
		},
		"action": func(u *unstructured.Unstructured) { set(u, "Deny", "authorization", "action") },
		"expression": func(u *unstructured.Unstructured) {
			set(u, []any{`apiKey.group == "audit"`}, "authorization", "policy", "matchExpressions")
		},
		"a new traffic field": func(u *unstructured.Unstructured) { set(u, map[string]any{"local": []any{}}, "rateLimit") },
	} {
		u := compiledPayments(t)
		mutate(u)
		if got, _ := Digest(u); got == base {
			t.Errorf("changing %s left the digest at %s, so a transaction keyed on it would not "+
				"see the change", name, base)
		}
	}
}

// Fields the compiler does not write do not move the digest: server-populated
// metadata, a label or annotation someone else added, and status. The digest is
// of what assayd compiled, so that the same compiled policy is the same target
// however the object it ends up in has been decorated.
func TestTheDigestIgnoresFieldsTheCompilerDoesNotOwn(t *testing.T) {
	base, _ := Digest(compiledPayments(t))
	for name, mutate := range map[string]func(u *unstructured.Unstructured){
		"resourceVersion": func(u *unstructured.Unstructured) { u.SetResourceVersion("42") },
		"uid":             func(u *unstructured.Unstructured) { u.SetUID("server-uid") },
		"annotation":      func(u *unstructured.Unstructured) { u.SetAnnotations(map[string]string{"a": "b"}) },
		"foreign label":   func(u *unstructured.Unstructured) { setLabel(u, "team", "payments") },
		"status":          func(u *unstructured.Unstructured) { u.Object["status"] = map[string]any{"ancestors": []any{}} },
		"managedFields":   func(u *unstructured.Unstructured) { u.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "x"}}) },
		"generation":      func(u *unstructured.Unstructured) { u.SetGeneration(7) },
	} {
		u := compiledPayments(t)
		mutate(u)
		if got, _ := Digest(u); got != base {
			t.Errorf("adding %s moved the digest from %s to %s; it is not a field the compiler owns",
				name, base, got)
		}
	}
}

func setLabel(u *unstructured.Unstructured, k, v string) {
	l := u.GetLabels()
	if l == nil {
		l = map[string]string{}
	}
	l[k] = v
	u.SetLabels(l)
}

func setRef(u *unstructured.Unstructured, field, v string) {
	refs, _, _ := unstructured.NestedSlice(u.Object, "spec", "targetRefs")
	refs[0].(map[string]any)[field] = v
	_ = unstructured.SetNestedSlice(u.Object, refs, "spec", "targetRefs")
}

func set(u *unstructured.Unstructured, v any, path ...string) {
	_ = unstructured.SetNestedField(u.Object, v, append([]string{"spec", "traffic"}, path...)...)
}

func unset(u *unstructured.Unstructured, path ...string) {
	unstructured.RemoveNestedField(u.Object, append([]string{"spec", "traffic"}, path...)...)
}

// cel-go's unparser encodes a string literal with Go's strconv.Quote, which
// writes an invalid UTF-8 byte as `\xHH`. Go reads that as a byte and CEL as a
// code point, so `"\xff"` would parse back as `"ÿ"` — a rule admitting a
// DIFFERENT group, silently. Such a group is refused, not encoded. No real
// namespace can be invalid UTF-8; this pins the refusal for the later inputs
// the encoder exists for.
func TestInvalidUTF8IsRefusedNotMisencoded(t *testing.T) {
	for _, s := range []string{"\xff", "a\xc3", "\xed\xa0\x80"} {
		if expr, err := admitGroupExpression(s); err == nil {
			t.Errorf("group %q (invalid UTF-8) was encoded as %q; CEL would read it as another "+
				"string, so it must be refused", s, expr)
		}
	}
}

// Each NotCompilableError names the cause it actually has (AGENTS.md rule 8).
// Only `oauth` can be the default the API server persisted, so only its message
// may say so; an empty or unknown value blamed on that default, or on design
// 06, would send the owner looking for the wrong thing.
func TestTheNotCompilableMessageNamesTheRealCause(t *testing.T) {
	for _, c := range []struct {
		mode    string
		must    []string
		mustNot []string
	}{
		{"oauth", []string{"design 06", "default the API server persisted", "apikey", "none"}, nil},
		{"", []string{"is empty", "apikey", "none"}, []string{"persisted", "design 06"}},
		{"ApiKey", []string{`set it to "apikey"`, "lowercase"}, []string{"persisted", "design 06"}},
		{"jwt", []string{"not a mode", "apikey", "none"}, []string{"persisted", "design 06"}},
	} {
		msg := (&NotCompilableError{Mode: c.mode}).Error()
		for _, w := range c.must {
			if !strings.Contains(msg, w) {
				t.Errorf("auth %q: the message does not say %q: %s", c.mode, w, msg)
			}
		}
		for _, w := range c.mustNot {
			if strings.Contains(msg, w) {
				t.Errorf("auth %q: the message says %q, which is not this value's cause: %s", c.mode, w, msg)
			}
		}
	}
}

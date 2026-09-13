// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	celtypes "cel.dev/cel-go/common/types"
	"cel.dev/cel-go/parser"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// The emitted kind. The repository carries no agentgateway Go types, so the
// policy is rendered as unstructured content, and these two strings are the
// whole of its type.
const (
	PolicyAPIVersion = "agentgateway.dev/v1alpha1"
	PolicyKind       = "AgentgatewayPolicy"
)

// The provenance labels every emitted resource carries (§3.2). They are
// defined here once: internal/controller's LabelAgent, LabelAgentUID and
// LabelAgentNamespace are these constants, because the watch maps an emitted
// object back to its Agent by them and the route sweep narrows on them.
//
// None of them is evidence of ownership: a label needs only `update` to forge,
// so the deletion authority is the immutable NAME as well (§3.2).
// `assayd.dev/revision` is deliberately absent: the `-auth` policy follows the
// Agent's current spec and belongs to no revision (§3.2, ADR-0034 F2).
const (
	LabelAgent          = "assayd.dev/agent"
	LabelAgentUID       = "assayd.dev/agent-uid"
	LabelAgentNamespace = "assayd.dev/agent-namespace"
)

// The slice's key source: every `-auth` policy selects the API-key ConfigMaps
// carrying this label (§1.1, §3.4.4). It is a CONSTANT, with no flag and no
// chart value, and that is what makes a key-source change — D2 — unreachable in
// the slice. It is the product's selector, not a test's. The e2e's key
// ConfigMap still carries `assayd.dev/api-keys: "e2e"` and must be relabelled
// to this one when the e2e moves to the emitted policy (§8.1, owed); the
// constant does not change to fit the test.
const (
	APIKeySourceLabel = "assayd.dev/api-keys"
	APIKeySourceValue = "true"
)

// KeySource is the canonical form of the key source a policy in runNamespace
// selects: design 03 §3.3.3's "canonical selector (sorted matchLabels, plus
// the run namespace)", as `status.auth.keySource` records it. With one
// constant label there is nothing to sort; the namespace is included because
// a selector reads the policy's own namespace: measured at agentgateway 1.5.0
// for one other namespace, and re-measured at every upgrade by
// `make conformance-cluster` (§3.4.4, TestSliceAKeySetInAnotherNamespaceDoesNotAdmit).
// Only this function writes the form, and only this form is compared.
func KeySource(runNamespace string) string {
	return runNamespace + "/" + APIKeySourceLabel + "=" + APIKeySourceValue
}

// The `auth: none` marker (§3.3.1): a LABEL on the serving route, never an
// empty policy, so that the opt-out cannot fail an acceptance gate and a
// deliberately unauthenticated route is distinguishable from one that lost its
// policy. No other value is ever written under the key.
const (
	LabelAuth     = "assayd.dev/auth"
	AuthNoneValue = "none"
)

// RouteAuthLabels returns the serving route's labels with the marker set as
// §3.3.1 requires: present exactly while the route is published under
// `auth: none`, and absent otherwise — so a marker planted on an API-key route
// is removed, not kept. `base` is copied and never written through.
//
// `published` is the mode THIS WRITE publishes the route under. It is neither
// the desired target nor `status.auth.mode` alone; the transaction says which:
//
//   - in steady state, and during a J2 `Lock` until it reaches `Served`, it is
//     the recorded `status.auth.mode`. A J2 `Lock` from `none` to `apikey`
//     therefore keeps the marker until `Served`, because until the probe sees
//     `401` the label's claim may still be true (§3.3.1, §3.3.3);
//   - for a `Create`, from its `Publishing` write onward, it is the `Create`'s
//     `targetMode`. `status.auth.mode` is written only at `Served` (§3.3), so a
//     `none` route published before then would otherwise go out unmarked —
//     deliberately open and indistinguishable from a route that lost its
//     policy, the state the marker exists to prevent;
//   - where nothing is published under a mode — a refused `Adopt`, a K2
//     `Lock` (no `mode` is recorded for either), and a `Create` before its
//     `Publishing` write — there is no published mode, and the marker is
//     absent: pass anything but `none`. That follows §3.3.3, which makes no
//     claim about a marker on an `Adopt`ed route. §3.3.1, read literally,
//     would mark an `Adopt`ed Agent whose desired mode is `none`; the design
//     does not reconcile the two, and this function takes §3.3.3's reading.
//
// Deciding which applies is the transaction machinery's: the emitter's
// servingRouteFor calls this with the mode internal/controller's
// routePublicationFor chooses from status.auth, and its equalRoute compares
// the marker's absence as well as its presence. A `Lock` (the second bullet)
// is not built yet.
func RouteAuthLabels(published AuthMode, base map[string]string) map[string]string {
	labels := make(map[string]string, len(base)+1)
	for k, v := range base {
		labels[k] = v
	}
	if published == AuthModeNone {
		labels[LabelAuth] = AuthNoneValue
	} else {
		delete(labels, LabelAuth)
	}
	return labels
}

// AuthMode is the `-auth` mode a target compiles to (§3.3.1). Only these two
// compile in the slice.
type AuthMode string

const (
	// AuthModeNone compiles to no policy and the route's marker label.
	AuthModeNone AuthMode = "none"
	// AuthModeAPIKey compiles to the `<agent>-auth` policy of §3.4.4.
	AuthModeAPIKey AuthMode = "apikey"
)

// AuthTarget is what one Agent's desired `-auth` compiles to: §3.3's
// `{targetMode, targetDigest}` plus the policy the digest is of. For `none`,
// Policy is nil and Digest is empty — §3.3: the digest is "present exactly when
// targetMode is apikey".
type AuthTarget struct {
	Mode   AuthMode
	Policy *unstructured.Unstructured
	Digest string
}

// NotCompilableError is a desired `-auth` mode the slice cannot compile —
// §3.3.1's `PolicyCompileFailed`, reason `AuthInputAbsent`. What the caller does
// with it (I1: a served Agent keeps its last good policy; a never-served one
// enters no transaction) is the reconciler's (internal/controller/authtxn.go).
type NotCompilableError struct {
	// Mode is the value of spec.expose.a2a.auth, verbatim.
	Mode string
}

// The fix every message names: the two modes the slice compiles.
const compilableModes = "set it to apikey (keys in the group named for this namespace) or " +
	"none (an unauthenticated route)"

// Error says what is wrong with THIS value. Only `oauth` can be the default the
// API server persisted, so only its message says so; an empty or unknown value
// is named as what it is, not blamed on a default or on design 06.
func (e *NotCompilableError) Error() string {
	switch {
	case e.Mode == "oauth":
		return "spec.expose.a2a.auth is \"oauth\", which cannot be compiled until design 06 ships " +
			"the input it needs. If nobody set this value, it is the default the API server " +
			"persisted when the field defaulted to oauth: " + compilableModes
	case e.Mode == "":
		return "spec.expose.a2a.auth is empty, and no mode is assumed for it: " + compilableModes
	case strings.EqualFold(e.Mode, string(AuthModeAPIKey)) || strings.EqualFold(e.Mode, string(AuthModeNone)):
		return fmt.Sprintf("spec.expose.a2a.auth is %q, and modes are lowercase: set it to %q",
			e.Mode, strings.ToLower(e.Mode))
	default:
		return fmt.Sprintf("spec.expose.a2a.auth is %q, which is not a mode: %s", e.Mode, compilableModes)
	}
}

// CompileAuth compiles one Agent's desired `-auth` (§3.3.1, §3.4.4).
//
// An Agent with no `expose.a2a` — no `expose` block at all, or one with no
// `a2a` arm — compiles to the API-key policy for its namespace's group, because
// the operator emits a serving route for it regardless and that route must
// carry `-auth` (§3.4.4). `none` compiles to no policy. Anything else, `oauth`
// and an empty value included, is a NotCompilableError: an empty value is not
// guessed at, because guessing a mode is the security decision §3.4.4 refuses
// to let a schema default make.
func CompileAuth(agent *assaydv1alpha1.Agent, runNamespace string) (AuthTarget, error) {
	mode, err := desiredAuthMode(agent.Spec.Expose)
	if err != nil {
		return AuthTarget{}, err
	}
	if mode == AuthModeNone {
		return AuthTarget{Mode: AuthModeNone}, nil
	}
	policy, err := AuthPolicy(AuthInput{
		AgentName:      agent.Name,
		AgentNamespace: agent.Namespace,
		AgentUID:       agent.UID,
		RunNamespace:   runNamespace,
	})
	if err != nil {
		return AuthTarget{}, err
	}
	digest, err := Digest(policy)
	if err != nil {
		return AuthTarget{}, err
	}
	return AuthTarget{Mode: AuthModeAPIKey, Policy: policy, Digest: digest}, nil
}

func desiredAuthMode(expose *assaydv1alpha1.ExposeSpec) (AuthMode, error) {
	if expose == nil || expose.A2A == nil {
		return AuthModeAPIKey, nil
	}
	switch mode := AuthMode(expose.A2A.Auth); mode {
	case AuthModeNone, AuthModeAPIKey:
		return mode, nil
	}
	return "", &NotCompilableError{Mode: expose.A2A.Auth}
}

// AuthInput is everything the slice's `-auth` policy is a function of. There is
// no group list: the slice has no `allowedGroups`, so the one admitted group is
// the Agent's namespace (§3.4.4), and a namespace is immutable.
type AuthInput struct {
	AgentName      string
	AgentNamespace string
	AgentUID       types.UID
	// RunNamespace is where the policy is placed: beside its target route,
	// because a policy's targetRefs select same-namespace objects only (§3.2).
	RunNamespace string
}

// AuthPolicy renders the `<agent>-auth` AgentgatewayPolicy of §3.4.4, exactly:
//
//   - API-key authentication, `mode: Strict` EMITTED rather than left to the
//     CRD default — a default is not an absence (§3.3.3) — and no `location`,
//     which leaves the key in the `Authorization: Bearer` header, the one place
//     the e2e measured;
//   - the constant key source;
//   - one CEL authorization rule, `action: Allow`, admitting the group named
//     for the Agent's namespace. Allow, because a CEL error under Deny fails
//     open (§3.4.2); and once any Allow rule exists the policy is default-deny.
//
// It targets the per-Agent serving route by calling ServingRouteName, the
// function the emitter names that route with. Nothing here writes it anywhere.
func AuthPolicy(in AuthInput) (*unstructured.Unstructured, error) {
	// Every field is required, checked in a fixed order so the error is stable.
	// The namespace most of all: it is the group, and `apiKey.group == ""`
	// admits keys stored with an empty group rather than nobody.
	for _, f := range []struct{ field, value string }{
		{"Agent name", in.AgentName},
		{"Agent namespace", in.AgentNamespace},
		{"Agent UID", string(in.AgentUID)},
		{"run namespace", in.RunNamespace},
	} {
		if f.value == "" {
			return nil, fmt.Errorf("compile the -auth policy: the %s is empty", f.field)
		}
	}
	name, err := AuthPolicyName(in.AgentName)
	if err != nil {
		return nil, err
	}
	route, err := ServingRouteName(in.AgentName)
	if err != nil {
		return nil, err
	}
	admit, err := AdmitGroupsExpression([]string{in.AgentNamespace})
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": PolicyAPIVersion,
		"kind":       PolicyKind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": in.RunNamespace,
			"labels": map[string]any{
				LabelAgentUID:       string(in.AgentUID),
				LabelAgent:          in.AgentName,
				LabelAgentNamespace: in.AgentNamespace,
			},
		},
		"spec": map[string]any{
			"targetRefs": []any{map[string]any{
				"group": "gateway.networking.k8s.io",
				"kind":  "HTTPRoute",
				"name":  route,
			}},
			"traffic": map[string]any{
				"apiKeyAuthentication": map[string]any{
					"mode": "Strict",
					"configMapSelector": map[string]any{
						"matchLabels": map[string]any{APIKeySourceLabel: APIKeySourceValue},
					},
				},
				"authorization": map[string]any{
					"action": "Allow",
					"policy": map[string]any{"matchExpressions": []any{admit}},
				},
			},
		},
	}}, nil
}

// AdmitGroupsExpression is the CEL rule admitting a set of groups:
// `apiKey.group == "<group>"` for one, and for more, one expression joining an
// `==` per group with `||`, in sorted order (§3.4.4). The slice's policy calls
// it with one group, the Agent's namespace. More than one has no producer yet:
// §3.4.4's `allowedGroups` is later scope. It is exported so that
// `make conformance-cluster` can measure the compiler's own two-group output
// at a real gateway, which §3.4.4 owes before that scope is built.
//
// An empty set, an empty group and a repeated group are refused, never
// rendered. An empty set has no expression to emit, and E2 means one is never
// asked for (§3.2). `apiKey.group == ""` would admit the keys stored with no
// group rather than nobody. A repeat is refused as an input error. That is
// this code's call, not §3.4.4's, which says nothing of repeats; the later
// scope's `allowedGroups` may make a set of it at admission instead. `groups`
// is not modified.
//
// **What a gateway has been shown**: the one-group expression, and one
// two-group expression on one line (`make conformance-cluster`,
// TestSliceATwoGroupExpressionAdmitsBothGroups). A set long enough to wrap
// across lines (below) is valid CEL, and no gateway has been shown one.
//
// §3.4.2 requires a real CEL string encoder and never concatenation, so the
// expression is built as a CEL AST and printed by cel-go's own unparser; the
// test proves the result with cel-go's own parser. A broken expression under
// Allow denies everything, and a crafted one — a group of `x" || true || "` —
// would admit everyone.
//
// **The unparser's literal encoder is Go's `strconv.Quote`**, and it agrees
// with CEL only on valid UTF-8. For an invalid byte it writes `\xHH`, which Go
// reads as a byte and CEL as a code point, so `"\xff"` would decode as `"ÿ"`
// and admit a DIFFERENT group, silently. Invalid UTF-8 is therefore refused
// here rather than encoded. No slice input reaches that branch: a namespace is
// a DNS label, and it needs no escaping at all. The encoder is here for
// §3.4.4's later `allowedGroups` and §3.4.2's tool names. The unparser wraps
// long lines only at `&&` and `||`, and past 80 columns it breaks the line
// after an `||`. So one `==` is always on one line, and a long set of groups
// spans several, which CEL reads as whitespace.
func AdmitGroupsExpression(groups []string) (string, error) {
	if len(groups) == 0 {
		return "", fmt.Errorf("encode the CEL rule admitting groups: the set is empty, and " +
			"no expression admits nobody")
	}
	sorted := append([]string(nil), groups...)
	sort.Strings(sorted)
	fac := ast.NewExprFactory()
	id := int64(0)
	next := func() int64 { id++; return id }
	var expr ast.Expr
	for i, group := range sorted {
		switch {
		case group == "":
			return "", fmt.Errorf("encode the CEL rule admitting groups %q: a group is empty, "+
				"and `apiKey.group == \"\"` admits the keys stored with no group", groups)
		case i > 0 && group == sorted[i-1]:
			return "", fmt.Errorf("encode the CEL rule admitting groups %q: group %q is repeated",
				groups, group)
		case !utf8.ValidString(group):
			return "", fmt.Errorf("encode the CEL rule admitting group %q: it is not valid UTF-8, and "+
				"CEL would read its escaped bytes as other characters", group)
		}
		eq := fac.NewCall(next(), operators.Equals,
			fac.NewSelect(next(), fac.NewIdent(next(), "apiKey"), "group"),
			fac.NewLiteral(next(), celtypes.String(group)))
		if expr == nil {
			expr = eq
			continue
		}
		expr = fac.NewCall(next(), operators.LogicalOr, expr, eq)
	}
	out, err := parser.Unparse(expr, ast.NewSourceInfo(nil))
	if err != nil {
		return "", fmt.Errorf("encode the CEL rule admitting groups %q: %w", groups, err)
	}
	return out, nil
}

// Digest is §3.3's `targetDigest` and `appliedDigest`: `sha256:` followed by
// 64 lowercase hex digits, over a canonical serialization of the policy's
// OWNED fields.
//
// The owned fields are exactly what the compiler writes: `apiVersion`, `kind`,
// `metadata.name`, `metadata.namespace`, the three provenance labels, and the
// whole of `spec`. Everything else is excluded, so a server-populated field, a
// label or annotation someone else added, or `status` does not move it. The
// Agent's UID is one of the labels, so a recreated Agent — a new UID — has a
// new digest for the same spec. A recreated Agent normally has no `status.auth`
// to compare with; a restore that brings status back under a new UID does, and
// design 03 A69 leaves that comparison to the code that writes it.
//
// The serialization is Go's encoding/json of that projection. It writes object
// keys in sorted order and no insignificant whitespace, so it does not depend
// on map iteration order. It is canonical for this code, not a published
// canonical form such as RFC 8785: a digest is only ever compared with another
// digest this function produced. Changing the serialization re-keys every
// recorded digest, which is why the golden test pins one.
//
// It is defined over the COMPILED policy, and the reconciler also uses it to
// compare a stored policy with a compiled one. That works because the pinned
// AgentgatewayPolicy CRD defaults nothing under the fields this renders except
// `apiKeyAuthentication.mode` and `authorization.action`, and both are emitted
// (§3.4.4); envtest runs
// against that CRD, so a default added there fails a test rather than turning
// the comparison into an update on every reconcile.
func Digest(policy *unstructured.Unstructured) (string, error) {
	labels := map[string]string{}
	for _, k := range []string{LabelAgentUID, LabelAgent, LabelAgentNamespace} {
		if v, ok := policy.GetLabels()[k]; ok {
			labels[k] = v
		}
	}
	canonical, err := json.Marshal(map[string]any{
		"apiVersion": policy.GetAPIVersion(),
		"kind":       policy.GetKind(),
		"metadata": map[string]any{
			"name":      policy.GetName(),
			"namespace": policy.GetNamespace(),
			"labels":    labels,
		},
		"spec": policy.Object["spec"],
	})
	if err != nil {
		return "", fmt.Errorf("serialize policy %s/%s for its digest: %w",
			policy.GetNamespace(), policy.GetName(), err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

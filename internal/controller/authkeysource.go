// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03 A86, with the human's D6 answered K1 (ADR-0034 Amendment 7): a
// served API-key Agent whose key source holds no key is reported, and nothing
// is withdrawn.
//
// The defect this answers was measured at the gateway by A82 (row 3) and at
// the operator by A86's reproduction: deleting the labelled key ConfigMaps in a
// run namespace takes every key there to 401 while the policy reports
// Valid/Attached, the route reports accepted, and every served API-key Agent
// there stays Ready=True. The operator never read the key source, so this was
// not a detection that failed: there was none.
//
// It is a THIRD half beside A80's two (authserved.go), with a gate of its own:
// the slot as the pass LEAVES it, which takes in the pass on which a Create or
// a Lock reaches Served. That pass's evidence is an anonymous 401, which an
// empty key source also answers, so the transaction's tuple says nothing about
// the key source.

// ReasonAPIKeySourceEmpty is PolicyApplyIncomplete's, and Ready's and
// Degraded's under K1: a live list of the key source this Agent's <agent>-auth
// selects succeeded and found no entry in data or binaryData.
const ReasonAPIKeySourceEmpty = "ApiKeySourceEmpty"

// keySourceHeldMark is the STABLE prefix of every note a held key-source claim
// carries. It is this half's own: every note A80's halves carry speaks about
// the Gateway, and this fact comes from a ConfigMap list, which has no
// generation (A86).
const keySourceHeldMark = " | carried from the last pass whose list of the key source succeeded"

// The four notes, one per UNKNOWN cause, and the errored one. Each is appended
// once, after the marker, to a message rebuilt on every pass.
const (
	keySourceListFailedNote   = "; the list failed (%v)"
	keySourceSelectorNote     = "; the live <agent>-auth's selector could not be read"
	keySourcePolicyErrNote    = "; the live <agent>-auth could not be read (%v)"
	keySourceNoPolicyNote     = "; no <agent>-auth of this Agent's was found to read a selector from"
	keySourceStepErroredNote  = "; this pass's -auth step could not complete (%v), so the key source was not listed"
	keySourceHeldLead         = "a claim from an earlier pass is standing: that pass's live list found no key in this Agent's key source. This pass could not re-read it, so it cannot say whether keys have been restored"
	keySourceMaxNamedConfigMs = 5
)

// keySourcePolicy is the live <agent>-auth this pass read for the key-source
// half, carried out of the -auth step rather than read again. read says the
// step made the read at all; a read that found nothing of this Agent's leaves
// obj nil. Only §3.2's UID guard is applied: NOT the digest guard, which an
// operator upgrade that changes compiler.AuthPolicy's render fails
// PERMANENTLY, so a read gated on it would go silent fleet-wide and a standing
// claim could never clear (A86, the self-critique's BLOCKER; case 20 (n)).
type keySourcePolicy struct {
	read bool
	obj  *unstructured.Unstructured
}

// keySourceReading is the three answers a key-source read has. Only EMPTY
// raises, only PRESENT clears, and UNKNOWN holds a standing claim and never
// raises a new one (AGENTS.md rule 8; the human's (A1) for A80's halves).
type keySourceReading int

const (
	keySourceUnknown keySourceReading = iota
	keySourceEmpty
	keySourcePresent
)

// judgesKeySource is A86's gate: the gateway enabled, status.auth.mode apikey,
// and the transaction slot empty AS THE PASS LEAVES IT. It is deliberately not
// judgesServed, whose slot is also the one the pass FOUND: that would exclude
// the pass on which a Create or a Lock reaches Served (case 20 (k)).
func (r *AgentReconciler) judgesKeySource(status *assaydv1alpha1.AgentStatus) bool {
	auth := status.Auth
	return r.Gateway.Enabled && auth != nil && auth.Mode == string(compiler.AuthModeAPIKey) &&
		auth.Transaction == nil
}

// judgeKeySource is A86's half on a pass whose -auth step returned no error.
// It runs after judgeServed, so where both raise, incompleteOrder decides which
// leads, and it writes FOUR things: PolicyApplyIncomplete through
// raiseIncomplete, the withhold and the served flag that make Ready False and
// the phase Degraded (K1), and status.auth.keySourceEmpty. It writes NOTHING to
// GovernanceSkipped, not even a note: an Agent with no keys is refused more,
// not less, and noteGovernance's conds.set would re-stamp a GovernanceSkipped
// that holdGovernance had just carried whole (case 20 (j)).
func (r *AgentReconciler) judgeKeySource(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out *gatewayOutcome, stored servedClaims) {
	if !r.judgesKeySource(status) {
		return
	}
	policy, note := out.keyPolicy.obj, ""
	if !out.keyPolicy.read {
		// The pass that reached Served fetched no policy — runCreate and runLock
		// go to recordServed and never to reassertServedPolicy — so the half
		// makes its one live GET here, under the same UID guard (A86).
		p, err := r.liveAuthPolicy(ctx, agent, runNS)
		if err != nil {
			note = fmt.Sprintf(keySourcePolicyErrNote, err)
		}
		policy = p
	}
	reading, msg := keySourceUnknown, ""
	switch {
	case note != "":
	case policy == nil:
		note = keySourceNoPolicyNote
	default:
		ns, sel, ok := keySourceSelector(policy)
		if !ok {
			note = keySourceSelectorNote
			break
		}
		var list corev1.ConfigMapList
		if err := r.reader().List(ctx, &list, client.InNamespace(ns), client.MatchingLabels(sel)); err != nil {
			// Logged and written to no condition: the residue A86 states. A
			// list that fails on every pass leaves a real outage unreported.
			log.FromContext(ctx).Error(err, "list the API-key source of this Agent's -auth policy",
				"namespace", ns, "selector", labels.SelectorFromSet(sel).String())
			note = fmt.Sprintf(keySourceListFailedNote, err)
			break
		}
		reading, msg = countKeySource(ns, labels.SelectorFromSet(sel).String(), keyGroup(agent, status),
			list.Items)
	}
	claim := stored.keySourceEmpty
	switch reading {
	case keySourceEmpty:
		claim = true
		raiseIncomplete(conds, ReasonAPIKeySourceEmpty, msg)
		withholdInOrder(out, ReasonAPIKeySourceEmpty, msg)
		out.served = true
	case keySourcePresent:
		claim = false
	default:
		if claim {
			holdIncomplete(agent, conds, out, ReasonAPIKeySourceEmpty, keySourceHeldLead,
				keySourceHeldMark+note)
		}
	}
	status.Auth.KeySourceEmpty = claim
}

// holdKeySource is judgeKeySource on a pass whose -auth step ERRORED: it read
// nothing, so a standing claim is re-asserted with the errored note, and the
// withhold and served flag with it, exactly as holdServedJudgement does for
// A80's halves (case 20 (g)).
func (r *AgentReconciler) holdKeySource(agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus,
	conds *conditionSet, out *gatewayOutcome, stored servedClaims, err error) {
	if !r.judgesKeySource(status) {
		return
	}
	if stored.keySourceEmpty {
		holdIncomplete(agent, conds, out, ReasonAPIKeySourceEmpty, keySourceHeldLead,
			keySourceHeldMark+fmt.Sprintf(keySourceStepErroredNote, err))
	}
	status.Auth.KeySourceEmpty = stored.keySourceEmpty
}

// liveAuthPolicy is the Served pass's one live GET of <agent>-auth, under §3.2's
// UID guard. NotFound, and a policy at the name that is not this Agent's, are
// both "none of this Agent's" and not an error.
func (r *AgentReconciler) liveAuthPolicy(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
) (*unstructured.Unstructured, error) {
	name, err := compiler.AuthPolicyName(agent.Name)
	if err != nil {
		return nil, err
	}
	p := NewAgentgatewayPolicy()
	switch err := r.reader().Get(ctx, types.NamespacedName{Namespace: runNS, Name: name}, p); {
	case apierrors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("read policy %s in %s: %w", name, runNS, err)
	}
	if p.GetLabels()[LabelAgentUID] != string(agent.UID) {
		return nil, nil
	}
	return p, nil
}

// keySourceSelector is the namespace and the labels a policy selects its API
// keys by. The operator lists in the policy's OWN namespace, where
// agentgateway reads keys (1.5.0 was measured not to reach another, A74), with
// exactly the policy's matchLabels and never looser ones. A selector with
// matchExpressions, one with no matchLabels, or a policy with no
// apiKeyAuthentication block is not read: the operator does not guess what a
// selector it did not write means, and in the slice such a selector is an
// out-of-band edit.
func keySourceSelector(p *unstructured.Unstructured) (string, map[string]string, bool) {
	sel, found, err := unstructured.NestedMap(p.Object, "spec", "traffic", "apiKeyAuthentication",
		"configMapSelector")
	if err != nil || !found {
		return "", nil, false
	}
	if exprs, ok := sel["matchExpressions"]; ok {
		if l, isList := exprs.([]any); !isList || len(l) > 0 {
			return "", nil, false
		}
	}
	raw, ok := sel["matchLabels"].(map[string]any)
	if !ok || len(raw) == 0 {
		return "", nil, false
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			return "", nil, false
		}
		out[k] = s
	}
	return p.GetNamespace(), out, true
}

// countKeySource is EMPTY or PRESENT for a list that succeeded. An entry is any
// key under data or binaryData; entries are COUNTED and never parsed, because
// an entry agentgateway rejects is reported by the Gateway itself, as
// PartiallyValid naming the entry, and a second parser here would be a second
// opinion on a format the Gateway owns (A86; case 20 (c), (d)). A ConfigMap
// being deleted is still listed, and counts like any other.
func countKeySource(ns, selector, group string, items []corev1.ConfigMap) (keySourceReading, string) {
	var names []string
	for i := range items {
		if len(items[i].Data)+len(items[i].BinaryData) > 0 {
			return keySourcePresent, ""
		}
		names = append(names, items[i].Name)
	}
	return keySourceEmpty, keySourceEmptyMessage(ns, selector, group, names)
}

// keySourceEmptyMessage is A86's fresh message: the absent case when no
// ConfigMap carries the label, and the empty case, naming at most five of them,
// when some do and none holds an entry.
func keySourceEmptyMessage(ns, selector, group string, empty []string) string {
	found := "and a live list found none there"
	fix := fmt.Sprintf("an administrator (admission.apiKeyWriters) creates a ConfigMap in %s labelled %s", ns,
		selector)
	if len(empty) > 0 {
		sort.Strings(empty)
		named := empty
		more := ""
		if len(named) > keySourceMaxNamedConfigMs {
			more = fmt.Sprintf(", and %d more", len(named)-keySourceMaxNamedConfigMs)
			named = named[:keySourceMaxNamedConfigMs]
		}
		found = fmt.Sprintf("and a live list found %d ConfigMap(s) carrying that label there (%s%s), none of "+
			"which holds an entry in data or binaryData", len(empty), strings.Join(named, ", "), more)
		fix = fmt.Sprintf("an administrator (admission.apiKeyWriters) adds entries to one of them, or creates "+
			"a ConfigMap in %s labelled %s", ns, selector)
	}
	return fmt.Sprintf("no API key can authenticate to this Agent: its <agent>-auth policy selects keys "+
		"from ConfigMaps labelled %s in namespace %s, %s. While the policy is attached and enforcing, "+
		"every request gets 401, with a key or without one. The key set is shared by every API-key Agent "+
		"in this run namespace, so expect this on all of them. To fix it, %s, with one data key per API "+
		"key whose value is {\"keyHash\": \"sha256:<hex>\", \"metadata\": {\"group\": \"%s\"}}, in the "+
		"group %s this Agent admits. assayd writes no keys. Nothing is withdrawn (design 03 §3.4.4, §5, "+
		"A86)", selector, ns, found, fix, group, group)
}

// keyGroup is the group this Agent admits, as status.auth records it.
func keyGroup(agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus) string {
	if status.Auth != nil && len(status.Auth.AdmittedGroups) > 0 {
		return status.Auth.AdmittedGroups[0]
	}
	return agent.Namespace
}

// KeySetRequests maps a change to a labelled key ConfigMap to every Agent
// whose run namespace it is in (A86's watch). Events outside run namespaces
// are dropped FIRST: the label is not reserved there, so any namespace can
// produce them.
//
// The Agents are listed through the reconciler's client, which in the
// operator is the manager's CACHE of Agents, not a live read. A86 specified a
// live LIST of the namespace's policies, and A87 changes that on its review's
// finding: a live LIST that fails LOSES the event, and the only fallback is
// the CardDriftInterval requeue, which runs only while the active revision is
// ready — so a restored key set could leave a false ApiKeySourceEmpty standing
// for five minutes, or indefinitely. A cache read does not fail that way, and
// costs no API call. It enqueues every Agent there, API-key or not; a pass of
// any other Agent makes no key-source read, since the half is gated on
// status.auth.mode.
func (r *AgentReconciler) KeySetRequests(ctx context.Context, o client.Object) []reconcile.Request {
	if !strings.HasPrefix(o.GetNamespace(), RunNamespacePrefix) {
		return nil
	}
	var agents assaydv1alpha1.AgentList
	if err := r.List(ctx, &agents); err != nil {
		log.FromContext(ctx).Error(err, "list the Agents a key-set change concerns",
			"namespace", o.GetNamespace())
		return nil
	}
	return keySetRequests(o.GetNamespace(), agents.Items)
}

// keySetRequests is every Agent whose run namespace is runNS.
func keySetRequests(runNS string, agents []assaydv1alpha1.Agent) []reconcile.Request {
	var out []reconcile.Request
	for i := range agents {
		if RunNamespaceName(agents[i].Namespace) != runNS {
			continue
		}
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&agents[i])})
	}
	return out
}

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// The operator wiring design 03's first slice needs before any policy is
// emitted (§1.1, the last row of "In the slice"): the `AgentgatewayPolicy`
// kind, the finalizer step that revokes a route before its `-auth`, and the
// watch on the gateway's NACK Events. Nothing here writes a policy. The
// `Create` and `Lock` transactions that do are later slice work, so today the
// watches requeue an Agent whose reconcile does nothing with the answer.

// AgentgatewayPolicyGVK is the kind the slice's `<agent>-auth` is, read as
// unstructured: this module vendors no agentgateway Go types, and the operator
// reads only metadata from one.
var AgentgatewayPolicyGVK = schema.FromAPIVersionAndKind(compiler.PolicyAPIVersion, compiler.PolicyKind)

// NewAgentgatewayPolicy is an empty unstructured object of that kind, to Get or
// watch into.
func NewAgentgatewayPolicy() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(AgentgatewayPolicyGVK)
	return u
}

// NackEventReason is the reason agentgateway gives the Warning Event it raises
// on a Gateway when the proxy rejects a pushed config. It is the only
// observable of that rejection: no condition on the policy, the route or the
// Backend changes (research/agentgateway-v1.4.1-spike.md §2.8).
const NackEventReason = "AgentGatewayNackError"

// teardownPendingRequeue is how soon the finalizer looks again while a delete
// it issued has not completed. It is a backstop, not the wake-up: the route and
// policy watches requeue the Agent when the held object is finally deleted.
// It is long, because a finalizer can hold an object for as long as its owner
// likes, and a short fixed interval would retry a stuck teardown forever at
// that rate.
const teardownPendingRequeue = 30 * time.Second

// ReasonTeardownWaiting is Ready=False on an Agent being deleted whose teardown
// is waiting for a delete that has not completed. The message names the object
// and the finalizers holding it, because nothing else says why the Agent is
// still Terminating (NFR-8).
const ReasonTeardownWaiting = "TeardownWaiting"

// revokeGateway is the gateway half of the finalizer: the serving route first,
// then the `-auth` policy, and neither step starts until the one before it has
// finished (design 03 §3.3, step 5: "routes detach … before their policies …
// are deleted"). The reverse order leaves the route published with no
// authentication for as long as the gap lasts.
//
// It reports what it is still waiting for, and "" once neither object remains.
// A route or a policy with a finalizer of its own outlives its delete call, and
// a teardown that moved on regardless would delete the policy under a route
// that is still serving, or release the Agent with a route still published.
//
// The routes are listed through the UNCACHED reader. A cache that has not yet
// seen a route created moments before the Agent was deleted would report none:
// the policy would go, the Agent would be released, and the route would outlive
// both, with nothing left to sweep it, because every later path keys on a UID
// that no longer exists.
func (r *AgentReconciler) revokeGateway(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string) (string, error) {
	live := r.reader()
	routes, err := r.listOwnedRoutes(ctx, live, agent, runNS)
	if err != nil {
		return "", err
	}
	for i := range routes {
		if err := r.Delete(ctx, &routes[i]); err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("delete route %s in %s: %w", routes[i].Name, runNS, err)
		}
	}
	remaining, err := r.listOwnedRoutes(ctx, live, agent, runNS)
	if err != nil {
		return "", err
	}
	if len(remaining) > 0 {
		return heldBy("HTTPRoute", &remaining[0]), nil
	}
	return r.deleteAuthPolicy(ctx, agent, runNS)
}

func heldBy(kind string, o client.Object) string {
	return fmt.Sprintf("%s %s/%s, whose delete is held by the finalizers %v",
		kind, o.GetNamespace(), o.GetName(), o.GetFinalizers())
}

// deleteAuthPolicy deletes this Agent's `<agent>-auth` and reports what it is
// still waiting for, or "" once the policy is gone or is not this Agent's.
//
// **The name and the label together are the deletion authority** (design 03
// §3.2). The object is found by its deterministic name, which an Agent that
// does not own it cannot have been renamed into, and it is deleted only if it
// also carries this Agent's UID. A policy with the name and without the label
// is someone else's (§3.2's `ForeignTrafficPolicy`), and it is logged and left.
//
// The label is checked on one read and the delete is a second call, so the
// delete carries that read's UID and resourceVersion as preconditions: what is
// deleted is the object that was checked, not one that took its name between
// the two calls.
func (r *AgentReconciler) deleteAuthPolicy(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string) (string, error) {
	name, err := compiler.AuthPolicyName(agent.Name)
	if err != nil {
		return "", err
	}
	key := types.NamespacedName{Namespace: runNS, Name: name}
	p := NewAgentgatewayPolicy()
	switch err := r.Get(ctx, key, p); {
	case apierrors.IsNotFound(err):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("read policy %s in %s: %w", name, runNS, err)
	}
	if p.GetLabels()[compiler.LabelAgentUID] != string(agent.UID) {
		log.FromContext(ctx).Info("not deleting a policy with this Agent's -auth name: it does not "+
			"carry this Agent's UID, so the name alone is no authority to delete it (design 03 §3.2)",
			"policy", name, "namespace", runNS, "agentUIDLabel", p.GetLabels()[compiler.LabelAgentUID])
		return "", nil
	}
	uid, rv := p.GetUID(), p.GetResourceVersion()
	if err := r.Delete(ctx, p, client.Preconditions{UID: &uid, ResourceVersion: &rv}); err != nil &&
		!apierrors.IsNotFound(err) {
		return "", fmt.Errorf("delete policy %s in %s: %w", name, runNS, err)
	}
	// A delete call that returned is not a deleted object: a finalizer on the
	// policy keeps it, marked, until its owner lets go.
	after := NewAgentgatewayPolicy()
	switch err := r.Get(ctx, key, after); {
	case apierrors.IsNotFound(err):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("confirm policy %s in %s is deleted: %w", name, runNS, err)
	}
	return heldBy(compiler.PolicyKind, after), nil
}

// reportTeardownWaiting says on the Agent what its teardown is waiting for.
// Only Ready is written, and the Agent's other conditions are left as they
// are: this pass asserts nothing about them.
func (r *AgentReconciler) reportTeardownWaiting(ctx context.Context, agent *assaydv1alpha1.Agent, waiting string) error {
	log.FromContext(ctx).Info("teardown is waiting for a delete to complete", "waitingFor", waiting)
	status := agent.Status.DeepCopy()
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               string(assaydv1alpha1.CondReady),
		Status:             metav1.ConditionFalse,
		Reason:             ReasonTeardownWaiting,
		ObservedGeneration: agent.Generation,
		Message: "the Agent is being deleted, and teardown is waiting for " + waiting + ". An Agent's " +
			"route is deleted before its -auth policy, and the Agent is released only when both are " +
			"gone (design 03 §3.3, step 5). Teardown continues when that finalizer is removed",
	})
	return r.writeStatus(ctx, agent, status)
}

// GatewayWatchCacheOptions scopes the cache the two gateway watches read
// through, which is theirs alone and not the manager's.
//
// Events: only Warning Events with the NACK reason, in the Gateway's namespace
// (design 03 §3.3), filtered by the API server. The manager's cache would list
// and watch Events in every namespace, which needs a cluster-wide read of every
// Event and holds all of them in memory. The chart grants the read as a Role in
// that one namespace.
//
// Policies: only those carrying `assayd.dev/agent`, the label the watch maps by
// (§3.2). A policy without it maps to no Agent, so holding it would buy nothing
// and cost a copy of every AgentgatewayPolicy in the cluster. Nothing reads
// policies from this cache but the watch: teardown and the NACK mapping read
// live, and §3.2's ForeignTrafficPolicy detection, when it is built, must list
// live too, because a foreign policy is exactly one this cache may not hold.
func GatewayWatchCacheOptions(base cache.Options, gatewayNamespace string) (cache.Options, error) {
	agentLabelled, err := labels.NewRequirement(compiler.LabelAgent, selection.Exists, nil)
	if err != nil {
		return base, fmt.Errorf("select policies by %s: %w", compiler.LabelAgent, err)
	}
	base.ByObject = map[client.Object]cache.ByObject{
		&corev1.Event{}: {
			Namespaces: map[string]cache.Config{gatewayNamespace: {}},
			Field: fields.SelectorFromSet(fields.Set{
				"type":   corev1.EventTypeWarning,
				"reason": NackEventReason,
			}),
		},
		NewAgentgatewayPolicy(): {Label: labels.NewSelector().Add(*agentLabelled)},
	}
	return base, nil
}

// gatewaySources are the policy watch (design 03 §3.2) and the NACK watch
// (§3.3), both read through one cache scoped by GatewayWatchCacheOptions.
func (r *AgentReconciler) gatewaySources(mgr ctrl.Manager, byAgentLabels handler.EventHandler) ([]source.Source, error) {
	opts, err := GatewayWatchCacheOptions(cache.Options{
		HTTPClient: mgr.GetHTTPClient(),
		Scheme:     mgr.GetScheme(),
		Mapper:     mgr.GetRESTMapper(),
	}, r.Gateway.Namespace)
	if err != nil {
		return nil, err
	}
	c, err := cache.New(mgr.GetConfig(), opts)
	if err != nil {
		return nil, fmt.Errorf("build the gateway watch cache: %w", err)
	}
	if err := mgr.Add(c); err != nil {
		return nil, fmt.Errorf("add the gateway watch cache: %w", err)
	}
	return []source.Source{
		source.Kind[client.Object](c, NewAgentgatewayPolicy(), byAgentLabels),
		source.Kind(c, &corev1.Event{}, handler.TypedEnqueueRequestsFromMapFunc(r.nackToAgents)),
	}, nil
}

// nackToAgents maps a NACK Event to the Agents whose policies it names.
//
// The Event names a POLICY, not an Agent: its key is
// `policy/<kind>/<ns>/<name>:<section>:<ns>/<route>` (design 03 §3.3). The
// policy is read, and mapped by the `assayd.dev/agent` and
// `assayd.dev/agent-namespace` labels, the key every other watch here maps by
// (§3.2), because a run namespace holds many Agents' objects and a namespace
// identifies none of them. A policy that is gone, or carries no such labels,
// maps to nothing. This only requeues: what a NACK does to an Agent's status is
// the `-auth` transaction's, and that is not built.
func (r *AgentReconciler) nackToAgents(ctx context.Context, ev *corev1.Event) []reconcile.Request {
	var out []reconcile.Request
	seen := map[types.NamespacedName]bool{}
	keys, dropped := NackedPolicies(ev.Message)
	if dropped > 0 {
		log.FromContext(ctx).Info("a NACK named more policies than are read for one Event; the rest "+
			"are not mapped to their Agents", "event", ev.Namespace+"/"+ev.Name,
			"read", len(keys), "dropped", dropped)
	}
	for _, key := range keys {
		p := NewAgentgatewayPolicy()
		if err := r.Get(ctx, key, p); err != nil {
			if !apierrors.IsNotFound(err) {
				log.FromContext(ctx).Error(err, "could not read the policy a NACK names", "policy", key)
			}
			continue
		}
		l := p.GetLabels()
		agent := types.NamespacedName{Namespace: l[compiler.LabelAgentNamespace], Name: l[compiler.LabelAgent]}
		if agent.Namespace == "" || agent.Name == "" || seen[agent] {
			continue
		}
		seen[agent] = true
		out = append(out, reconcile.Request{NamespacedName: agent})
	}
	return out
}

// maxNackedPolicies bounds the policies one NACK Event makes the operator
// read. Each is a live GET, made synchronously in the watch's handler, so an
// unbounded list would let whoever can write an Event in the Gateway's
// namespace make the operator issue as many reads as the message holds.
const maxNackedPolicies = 64

// NackedPolicies reads the policies named in a NACK Event's message, each once,
// and at most maxNackedPolicies of them. It reports how many distinct
// policies past that bound it dropped.
//
// The message is what agentgateway writes, measured at v1.4.1: a JSON array of
// `{"key": …, "error": …}`, where a policy's key is
// `policy/<kind>/<ns>/<name>:<section>:<ns>/<route>`
// (research/agentgateway-v1.4.1-spike.md §2.8). A key for anything but a
// policy, or one not in that shape, names nothing here. It carries no
// generation and no UID, so it can say which policy was rejected and never
// which version of it.
func NackedPolicies(message string) (policies []types.NamespacedName, dropped int) {
	var entries []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(message), &entries); err != nil {
		return nil, 0
	}
	seen := map[types.NamespacedName]bool{}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Key, "policy/")
		if !ok {
			continue
		}
		target, _, _ := strings.Cut(rest, ":")
		parts := strings.Split(target, "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			continue
		}
		key := types.NamespacedName{Namespace: parts[1], Name: parts[2]}
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(policies) == maxNackedPolicies {
			dropped++
			continue
		}
		policies = append(policies, key)
	}
	return policies, dropped
}

// ValidateGatewayServingURL checks the form of --gateway-serving-url.
//
// The address is the Gateway's serving listener, where design 03 §3.3.3 sends
// its anonymous probe with the Agent's Host header, to the Agent's card path.
// So it must name a listener and nothing else: an absolute http or https URL
// with a host and, if it has a port, one from 1 to 65535. No path, not even
// "/", because the probe appends a card path that already starts with one. No
// query or fragment. No user information either, because a credential in a
// flag is printed by `ps` and by every log that echoes the operator's
// arguments.
//
// It checks FORM, not that anything listens there: whether a URL reaches the
// right listener is unverified (design 03 §3.3.3 records a wrong URL as open).
func ValidateGatewayServingURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return fmt.Errorf("%q is not a URL: %w", raw, err)
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%q must be an absolute http or https URL", raw)
	case u.Hostname() == "":
		return fmt.Errorf("%q names no host", raw)
	case u.User != nil:
		return fmt.Errorf("%q carries user information; a credential in a flag is printed by ps "+
			"and by every log that echoes the operator's arguments", raw)
	case u.Path != "":
		return fmt.Errorf("%q has a path, even if only \"/\"; it must name the listener only, "+
			"because the probe appends the Agent's card path to it", raw)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return fmt.Errorf("%q has a query or a fragment; it must name the listener only", raw)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%q has port %q, which is not a port from 1 to 65535", raw, p)
		}
	}
	return nil
}

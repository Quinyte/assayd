// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// Design 03 A75's envtest cases (ADR-0034 Amendment 5: L1 and W1): a policy
// on the assayd Gateway that could answer the probe holds every -auth
// transaction at ProbingAfter, and one that could widen a served route stops
// it reading Governed. The stub answers 401 while a planted Gateway-level
// policy exists (refuseWhile), as A74 case 7 measured a real gateway doing on
// a route with no -auth.

// gatewayPolicy plants an AgentgatewayPolicy in the Gateway's namespace, and
// removes it when the test ends.
func gatewayPolicy(t *testing.T, name string, spec map[string]any) client.ObjectKey {
	t.Helper()
	p := controller.NewAgentgatewayPolicy()
	p.SetNamespace(suiteGatewayNamespace)
	p.SetName(name)
	p.Object["spec"] = spec
	if err := k8s.Create(context.Background(), p); err != nil {
		t.Fatalf("plant the Gateway-level policy %s: %v", name, err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), p) })
	return client.ObjectKeyFromObject(p)
}

func removePolicy(t *testing.T, key client.ObjectKey) {
	t.Helper()
	p := controller.NewAgentgatewayPolicy()
	p.SetNamespace(key.Namespace)
	p.SetName(key.Name)
	if err := k8s.Delete(context.Background(), p); err != nil {
		t.Fatalf("remove %s: %v", key, err)
	}
}

// onGateway is a targetRefs naming a Gateway, on one listener when section is
// set.
func onGateway(name, section string) []any {
	ref := map[string]any{"group": gatewayv1.GroupName, "kind": "Gateway", "name": name}
	if section != "" {
		ref["sectionName"] = section
	}
	return []any{ref}
}

// apiKeyTraffic is the compiler's `traffic` for a: API-key authentication and
// one authorization rule, the shape A74 case 7 moved to the Gateway.
func apiKeyTraffic(t *testing.T, a *assaydv1alpha1.Agent) map[string]any {
	t.Helper()
	tr, ok, err := unstructured.NestedMap(authPolicyFor(t, liveAgent(t, a)).Object, "spec", "traffic")
	if err != nil || !ok {
		t.Fatalf("the compiler's policy has no traffic: %v", err)
	}
	return tr
}

// authenticationOnly is apiKeyTraffic without its authorization rule.
func authenticationOnly(t *testing.T, a *assaydv1alpha1.Agent) map[string]any {
	t.Helper()
	tr := apiKeyTraffic(t, a)
	delete(tr, "authorization")
	return tr
}

// gatewayAuthPolicy plants A74 case 7's shape on the assayd Gateway, and
// makes the stub answer a's probes with 401 while it exists.
func gatewayAuthPolicy(t *testing.T, a *assaydv1alpha1.Agent, stub *stubProber) client.ObjectKey {
	t.Helper()
	return refusingPolicy(t, a, stub, "gw-auth-"+a.Name, map[string]any{
		"targetRefs": onGateway(suiteGatewayName, ""), "traffic": apiKeyTraffic(t, a)})
}

// refusingPolicy plants spec on the Gateway, and makes the stub answer a's
// probes with 401 while it exists.
func refusingPolicy(t *testing.T, a *assaydv1alpha1.Agent, stub *stubProber, name string,
	spec map[string]any) client.ObjectKey {
	t.Helper()
	key := gatewayPolicy(t, name, spec)
	stub.refuseWhile(a.Name, key)
	return key
}

// probingCreate is a new API-key Agent whose Create has written and converged
// its policy, with the oracle's switch held, so that its next pass probes and
// its own policy does not answer 401.
func probingCreate(t *testing.T, name string, deadline time.Duration) (*assaydv1alpha1.Agent,
	*controller.AgentReconciler, *stubProber) {
	t.Helper()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, name, nil)
	r, stub := createReconciler()
	r.AuthDeadline = deadline
	stub.hold(name, true)
	promote(t, r, a)
	acceptRoute(t, ns, name)
	acceptPolicy(t, ns, name)
	return liveAgent(t, a), r, stub
}

// heldJ2 is a served auth: none Agent edited to apikey whose Lock observed the
// open route and wrote and converged its policy, with the oracle's switch
// held. plant runs between the before-probe and the write's convergence.
func heldJ2(t *testing.T, name string, deadline time.Duration,
	plant func(*assaydv1alpha1.Agent, *stubProber) client.ObjectKey) (*assaydv1alpha1.Agent,
	*controller.AgentReconciler, *stubProber, client.ObjectKey) {
	t.Helper()
	a, r, stub := servedNoneAgent(t, name, nil)
	recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
	r.AuthDeadline = deadline
	stub.hold(a.Name, true)
	toMode(t, a, "apikey")
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || !tx.BeforeObserved {
		t.Fatalf("the Lock did not observe the open route: %+v", tx)
	}
	key := plant(a, stub)
	acceptRoute(t, a.Namespace, a.Name)
	acceptPolicy(t, a.Namespace, a.Name)
	lockPass(t, r, a)
	lockPass(t, r, a)
	return a, r, stub, key
}

func mustContain(t *testing.T, c *metav1.Condition, what string, parts ...string) {
	t.Helper()
	if c == nil {
		return
	}
	for _, p := range parts {
		if !strings.Contains(c.Message, p) {
			t.Errorf("%s does not say %q: %s", what, p, c.Message)
		}
	}
}

func phaseOf(t *testing.T, a *assaydv1alpha1.Agent) assaydv1alpha1.AgentPhase {
	t.Helper()
	return liveAgent(t, a).Status.Phase
}

// heldCreate reconciles a probing Create twice, and requires it held at
// ProbingAfter with GatewayAuthPolicy naming every part of says.
func heldCreate(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent, says ...string) {
	t.Helper()
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if routePublished(t, a.Namespace, a.Name) {
		t.Fatal("a route was published on a 401 a Gateway-level cause could have answered")
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" || tx.Stage != "ProbingAfter" {
		t.Fatalf("the Create does not hold at ProbingAfter: %+v", tx)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", says...)
}

// A75 case 1: a Create holds at ProbingAfter with GatewayAuthPolicy while a
// Gateway-level API-key policy stands, its route unpublished, Ready False and
// Pending, and publishes once it is removed, with GatewayAuthPolicy cleared.
func TestACreateHoldsWhileAGatewayLevelAuthPolicyStands(t *testing.T) {
	a, r, stub := probingCreate(t, "abovecreate", 0)
	key := gatewayAuthPolicy(t, a, stub)
	heldCreate(t, r, a, key.String(), "not credited", "Remove it")
	if tx := txOf(t, a); tx.Probe == nil || tx.Probe.After == nil || *tx.Probe.After != 401 {
		t.Errorf("the held Create did not record the Gateway's 401: %+v", tx)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "GatewayAuthPolicy")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhasePending {
		t.Errorf("a never-served Create held by a Gateway-level policy is %s, want Pending", got)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "Governed")

	removePolicy(t, key)
	stub.hold(a.Name, false)
	reconcileOnce(t, r, a)
	if !routePublished(t, a.Namespace, a.Name) {
		t.Fatalf("the route was not published once the Gateway-level policy went: tx=%+v", txOf(t, a))
	}
	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Create did not reach Served: %+v", auth)
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("GatewayAuthPolicy did not clear at Served: %+v", c)
	}
}

// A75 case 2: a J2 Lock whose before-state was observed holds at ProbingAfter
// once a Gateway-level policy stands, because the 200 → 401 may be that
// policy's transition. Its route stays open, status keeps mode none, and
// Ready is withheld at once, Degraded, by §3.3.1's aggregation.
func TestAJ2LockHoldsWhileAGatewayLevelAuthPolicyStands(t *testing.T) {
	a, r, stub, key := heldJ2(t, "abovejtwo", 0, func(a *assaydv1alpha1.Agent, stub *stubProber) client.ObjectKey {
		return gatewayAuthPolicy(t, a, stub)
	})
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil ||
		auth.Transaction.Stage != "ProbingAfter" {
		t.Fatalf("the J2 Lock does not hold at ProbingAfter under a Gateway-level policy: %+v", auth)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", key.String(), "not credited")
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthLockPending")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "GatewayAuthPolicy")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a held J2 Lock is %s, want Degraded", got)
	}

	removePolicy(t, key)
	stub.hold(a.Name, false)
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served once the Gateway-level policy went: %+v", auth)
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("GatewayAuthPolicy did not clear at Served: %+v", c)
	}
}

// A75: a K2 Lock holds too.
func TestAK2LockHoldsWhileAGatewayLevelAuthPolicyStands(t *testing.T) {
	a, r, stub := legacyAgent(t, "abovektwo", noneSpec)
	recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
	stub.hold(a.Name, true)
	toMode(t, a, "apikey")
	lockPass(t, r, a)
	key := gatewayAuthPolicy(t, a, stub)
	acceptRoute(t, a.Namespace, a.Name)
	acceptPolicy(t, a.Namespace, a.Name)
	lockPass(t, r, a)
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "" || auth.Transaction == nil || auth.Transaction.Kind != "Lock" ||
		auth.Transaction.Stage != "ProbingAfter" {
		t.Fatalf("the K2 Lock does not hold at ProbingAfter under a Gateway-level policy: %+v", auth)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", key.String())
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "GatewayAuthPolicy")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a held K2 Lock is %s, want Degraded", got)
	}
}

// A75 case 3: the Lock of a missing policy, whose 401 the card digest would
// attribute, holds too, and AuthPolicyMissing comes first in
// PolicyApplyIncomplete, which names the Gateway-level policy as well.
func TestAMissingPolicyLockHoldsAndAuthPolicyMissingComesFirst(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "abovemissing")
	recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
	key := refusingPolicy(t, a, stub, "gw-authn-"+a.Name, map[string]any{
		"targetRefs": onGateway(suiteGatewayName, ""), "traffic": authenticationOnly(t, a)})
	stub.hold(a.Name, true)
	if err := k8s.Delete(context.Background(), policyExists(t, runNS(a.Namespace), policyNameOf(a))); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	acceptPolicy(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	auth := authOf(t, a)
	if auth.Mode != "apikey" || auth.Transaction == nil || auth.Transaction.Kind != "Lock" ||
		auth.Transaction.Stage != "ProbingAfter" {
		t.Fatalf("the missing-policy Lock does not hold at ProbingAfter: %+v", auth)
	}
	if !routePublished(t, a.Namespace, a.Name) {
		t.Error("the missing-policy Lock withdrew the route")
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	mustContain(t, c, "PolicyApplyIncomplete", key.String(), "not credited")
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyMissing")

	removePolicy(t, key)
	stub.hold(a.Name, false)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served once the Gateway-level policy went: %+v", auth)
	}
}

// A75 case 4: the re-create of a served Agent's deleted route holds too, and
// the route stays prepared, so the Agent is off the air, Degraded.
func TestARecreatedRouteOfAServedAgentHoldsUnpublished(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "aboverecreate")
	key := refusingPolicy(t, a, stub, "gw-authn-"+a.Name, map[string]any{
		"targetRefs": onGateway(suiteGatewayName, ""), "traffic": authenticationOnly(t, a)})
	if err := k8s.Delete(context.Background(), servingRoute(t, a.Namespace, a.Name)); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if routePublished(t, a.Namespace, a.Name) {
		t.Fatal("a re-created route was published on a 401 a Gateway-level auth policy answered")
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" || tx.Stage != "ProbingAfter" {
		t.Fatalf("the route re-create does not hold at ProbingAfter: %+v", tx)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", key.String())
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "GatewayAuthPolicy")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a served Agent whose re-created route is held is %s, want Degraded", got)
	}
}

// A75's target and field rules: what counts on the Gateway, and what does
// not. Each case that does not count reaches Served; each that counts holds.
func TestWhichGatewayLevelPoliciesCount(t *testing.T) {
	served := func(t *testing.T, name string, spec func(*assaydv1alpha1.Agent) map[string]any) {
		t.Helper()
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, name, nil)
		r, stub := createReconciler()
		promote(t, r, a)
		gatewayPolicy(t, "gw-"+name, spec(liveAgent(t, a)))
		driveToServed(t, r, stub, a)
	}
	held := func(t *testing.T, name string, spec func(*assaydv1alpha1.Agent) map[string]any) {
		t.Helper()
		a, r, stub := probingCreate(t, name, 0)
		key := refusingPolicy(t, a, stub, "gw-"+name, spec(a))
		heldCreate(t, r, a, key.String())
	}
	t.Run("harmless fields only do not hold", func(t *testing.T) {
		served(t, "aboveharmless", func(*assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, ""),
				"traffic": map[string]any{"timeouts": map[string]any{"request": "10s"},
					"cors": map[string]any{"allowOrigins": []any{"https://example.com"}}}}
		})
	})
	t.Run("a policy on another Gateway does not hold", func(t *testing.T) {
		served(t, "aboveother", func(a *assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway("elsewhere", ""), "traffic": authenticationOnly(t, a)}
		})
	})
	t.Run("a policy scoped to the tools listener does not hold", func(t *testing.T) {
		served(t, "abovetools", func(a *assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, "tools"), "traffic": authenticationOnly(t, a)}
		})
	})
	t.Run("a same-port listener for another host does not hold", func(t *testing.T) {
		served(t, "aboveelsewhere", func(a *assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, "elsewhere"), "traffic": authenticationOnly(t, a)}
		})
	})
	t.Run("a same-port listener matching the host holds", func(t *testing.T) {
		held(t, "aboveedge", func(a *assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, "edge"), "traffic": authenticationOnly(t, a)}
		})
	})
	t.Run("a selector matching the Gateway's labels holds", func(t *testing.T) {
		held(t, "aboveselector", func(a *assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetSelectors": []any{map[string]any{"group": gatewayv1.GroupName,
				"kind": "Gateway", "matchLabels": map[string]any{suiteGatewayLabel: "suite"}}},
				"traffic": authenticationOnly(t, a)}
		})
	})
	t.Run("a directResponse-only policy holds", func(t *testing.T) {
		held(t, "abovedirect", func(*assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, ""),
				"traffic": map[string]any{"directResponse": map[string]any{"status": int64(401)}}}
		})
	})
	t.Run("a transformation-only policy holds", func(t *testing.T) {
		held(t, "abovetransform", func(*assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, ""),
				"traffic": map[string]any{"transformation": map[string]any{"response": map[string]any{
					"set": []any{map[string]any{"name": ":status", "value": "401"}}}}}}
		})
	})
	t.Run("Override beside harmless fields holds", func(t *testing.T) {
		held(t, "aboveoverride", func(*assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, ""),
				"traffic":  map[string]any{"timeouts": map[string]any{"request": "10s"}},
				"strategy": map[string]any{"inheritance": "Override"}}
		})
	})
}

// A75: a backend.extAuth policy on the Gateway, which answers a Lock's probe
// through the revision Service, holds a J2 Lock, whose card digest would
// otherwise credit its 401.
func TestABackendExtAuthPolicyHoldsAJ2Lock(t *testing.T) {
	a, _, _, key := heldJ2(t, "aboveextauth", 0, func(a *assaydv1alpha1.Agent, stub *stubProber) client.ObjectKey {
		return refusingPolicy(t, a, stub, "gw-extauth-"+a.Name, map[string]any{
			"targetRefs": onGateway(suiteGatewayName, ""),
			"backend": map[string]any{"extAuth": map[string]any{
				"backendRef": map[string]any{"name": "authz", "port": int64(9000)}, "grpc": map[string]any{}}}})
	})
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil ||
		auth.Transaction.Stage != "ProbingAfter" {
		t.Fatalf("a J2 Lock was credited under a Gateway-level backend.extAuth: %+v", auth)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", key.String())
}

// A75: ForeignTrafficPolicy outranks GatewayAuthPolicy in PolicyApplyIncomplete
// and in Ready, and the message names both.
func TestForeignTrafficPolicyOutranksGatewayAuthPolicy(t *testing.T) {
	a, r, stub := probingCreate(t, "aboveforeign", 0)
	key := gatewayAuthPolicy(t, a, stub)
	intruder := authPolicyFor(t, liveAgent(t, a))
	intruder.SetName("intruder")
	intruder.SetLabels(nil)
	if err := k8s.Create(context.Background(), intruder); err != nil {
		t.Fatalf("plant the foreign policy: %v", err)
	}
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if routePublished(t, a.Namespace, a.Name) {
		t.Fatal("a route was published under a foreign and a Gateway-level policy")
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
	mustContain(t, c, "PolicyApplyIncomplete", "intruder", key.String(), "GatewayAuthPolicy")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ForeignTrafficPolicy")
}

// A75: a Gateway that admits ListenerSets holds, because a ListenerSet's
// listener can capture the route's traffic and the slice does not inspect
// ListenerSets, whatever from is and whatever a Selector matches.
func TestAGatewayThatAdmitsListenerSetsHolds(t *testing.T) {
	for _, from := range []gatewayv1.FromNamespaces{gatewayv1.NamespacesFromAll, gatewayv1.NamespacesFromSelector} {
		t.Run(string(from), func(t *testing.T) {
			setAllowedListeners(t, from)
			t.Cleanup(func() { setAllowedListeners(t, "") })
			a, r, stub := probingCreate(t, "abovelisteners"+strings.ToLower(string(from))[:3], 0)
			stub.hold(a.Name, false)
			heldCreate(t, r, a, "allowedListeners", string(from))
		})
	}
}

// setAllowedListeners sets the suite Gateway's spec.allowedListeners, or
// clears it with "". A Selector matches a label no namespace carries.
func setAllowedListeners(t *testing.T, from gatewayv1.FromNamespaces) {
	t.Helper()
	var gw gatewayv1.Gateway
	key := client.ObjectKey{Namespace: suiteGatewayNamespace, Name: suiteGatewayName}
	if err := k8s.Get(context.Background(), key, &gw); err != nil {
		t.Fatal(err)
	}
	gw.Spec.AllowedListeners = nil
	if from != "" {
		ns := &gatewayv1.ListenerNamespaces{From: &from}
		if from == gatewayv1.NamespacesFromSelector {
			ns.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"assayd.dev/no-such": "namespace"}}
		}
		gw.Spec.AllowedListeners = &gatewayv1.AllowedListeners{Namespaces: ns}
	}
	if err := k8s.Update(context.Background(), &gw); err != nil {
		t.Fatalf("set the Gateway's allowedListeners: %v", err)
	}
}

// gatewayUnreadable is a reader that cannot read the Gateway.
type gatewayUnreadable struct {
	client.Reader
	err error
}

func (g gatewayUnreadable) Get(ctx context.Context, key client.ObjectKey, obj client.Object,
	opts ...client.GetOption) error {
	if _, ok := obj.(*gatewayv1.Gateway); ok {
		return g.err
	}
	return g.Reader.Get(ctx, key, obj, opts...)
}

// policiesUnlistable is a reader that cannot list the policies in the
// Gateway's namespace.
type policiesUnlistable struct{ client.Reader }

func (p policiesUnlistable) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	lo := &client.ListOptions{}
	lo.ApplyOptions(opts)
	if u, ok := list.(*unstructured.UnstructuredList); ok && lo.Namespace == suiteGatewayNamespace &&
		strings.HasPrefix(u.GetKind(), "AgentgatewayPolicy") {
		return apierrors.NewNotFound(schema.GroupResource{Group: "agentgateway.dev", Resource: "agentgatewaypolicies"}, "")
	}
	return p.Reader.List(ctx, list, opts...)
}

// A75: a Gateway the operator cannot read, and a policy list it cannot make,
// hold, and say why. The list's NotFound is not read as "no policies", as
// §3.2's foreign detection reads one.
func TestAnUnreadableGatewayOrPolicyListHolds(t *testing.T) {
	gr := schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}
	for _, c := range []struct {
		name, says string
		reader     func() client.Reader
	}{
		{"forbidden", "may not read", func() client.Reader {
			return gatewayUnreadable{Reader: k8s, err: apierrors.NewForbidden(gr, suiteGatewayName, errors.New("no grant"))}
		}},
		{"absent", "does not exist", func() client.Reader {
			return gatewayUnreadable{Reader: k8s, err: apierrors.NewNotFound(gr, suiteGatewayName)}
		}},
		{"unlisted", "could not be listed", func() client.Reader { return policiesUnlistable{Reader: k8s} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, r, stub := probingCreate(t, "aboveunread"+c.name[:3], 0)
			stub.hold(a.Name, false)
			r.Reader = c.reader()
			heldCreate(t, r, a, c.says)
		})
	}
}

// A75: a policy written while the probe is in flight, after anything a read
// before the probe would have seen, still blocks the credit, because the
// Gateway is read again after the 401.
func TestAPolicyWrittenDuringTheProbeStillBlocksTheCredit(t *testing.T) {
	a, r, stub := probingCreate(t, "aboveduring", 0)
	key := client.ObjectKey{Namespace: suiteGatewayNamespace, Name: "gw-auth-" + a.Name}
	stub.refuseWhile(a.Name, key)
	stub.onNextProbe(a.Name, func() { gatewayAuthPolicy(t, a, stub) })
	reconcileOnce(t, r, a)
	if routePublished(t, a.Namespace, a.Name) {
		t.Fatal("a route was published on a 401 a policy written during the probe answered")
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", key.String())
}

// A75: the Lock's read after the 401 gates its credit too. A policy written
// while a J2 Lock's probe is in flight, after the pass's first read, blocks
// the credit that the card digest and the observed 200 would otherwise give.
func TestAPolicyWrittenDuringALockProbeStillBlocksTheCredit(t *testing.T) {
	a, _, _, key := heldJ2(t, "aboveduringlock", 0, func(a *assaydv1alpha1.Agent, stub *stubProber) client.ObjectKey {
		key := client.ObjectKey{Namespace: suiteGatewayNamespace, Name: "gw-auth-" + a.Name}
		stub.refuseWhile(a.Name, key)
		stub.onNextProbe(a.Name, func() { gatewayAuthPolicy(t, a, stub) })
		return key
	})
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil ||
		auth.Transaction.Stage != "ProbingAfter" {
		t.Fatalf("a J2 Lock was credited on a 401 a policy written during its probe answered: %+v", auth)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", key.String())
}

// A75, W1: an Agent already Served keeps its record and its published route.
// Beside a Gateway-level policy that sets authorization, whose rules merge
// with the route's, or Override, it stops reading Governed:
// GovernanceSkipped and PolicyApplyIncomplete, both GatewayAuthPolicy, and
// Ready False, Degraded. Beside an authentication-only one it is a note.
func TestAServedAgentIsReportedAndNotHeld(t *testing.T) {
	notHeld := func(t *testing.T, a *assaydv1alpha1.Agent) {
		t.Helper()
		if auth := authOf(t, a); auth == nil || auth.Mode == "" || auth.Transaction != nil {
			t.Fatalf("a served Agent entered a transaction under a Gateway-level policy: %+v", auth)
		}
		if !routePublished(t, a.Namespace, a.Name) {
			t.Error("a served Agent's route was withdrawn")
		}
	}
	widened := func(t *testing.T, name string, spec func(*assaydv1alpha1.Agent) map[string]any) {
		t.Helper()
		a, r, _ := servedAPIKeyAgent(t, name)
		key := gatewayPolicy(t, "gw-"+name, spec(a))
		reconcileOnce(t, r, a)
		notHeld(t, a)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "GatewayAuthPolicy")
		mustContain(t, g, "GovernanceSkipped", key.String(), "admits whatever the Gateway-level authorization rule allows")
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
		mustContain(t, c, "PolicyApplyIncomplete", key.String(), "not withdrawn")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "GatewayAuthPolicy")
		if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
			t.Errorf("a served Agent whose route a Gateway-level rule widens is %s, want Degraded", got)
		}
	}
	noted := func(t *testing.T, a *assaydv1alpha1.Agent, key client.ObjectKey, status metav1.ConditionStatus,
		reason string, says ...string) {
		t.Helper()
		notHeld(t, a)
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Errorf("a served Agent under an authentication-only policy was flagged: %+v", c)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, status, reason)
		mustContain(t, g, "GovernanceSkipped", append([]string{key.String(), "not held"}, says...)...)
	}
	t.Run("authorization widens", func(t *testing.T) {
		widened(t, "abovewidens", func(a *assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, ""), "traffic": apiKeyTraffic(t, a)}
		})
	})
	t.Run("Override widens", func(t *testing.T) {
		widened(t, "aboveoverrides", func(*assaydv1alpha1.Agent) map[string]any {
			return map[string]any{"targetRefs": onGateway(suiteGatewayName, ""),
				"traffic":  map[string]any{"timeouts": map[string]any{"request": "10s"}},
				"strategy": map[string]any{"inheritance": "Override"}}
		})
	})
	t.Run("authorization widens even when the Gateway cannot be read", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "abovewidensunread")
		key := gatewayPolicy(t, "gw-"+a.Name, map[string]any{
			"targetRefs": onGateway(suiteGatewayName, "tools"), "traffic": apiKeyTraffic(t, a)})
		gr := schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}
		r.Reader = gatewayUnreadable{Reader: k8s, err: apierrors.NewForbidden(gr, suiteGatewayName, errors.New("no grant"))}
		reconcileOnce(t, r, a)
		notHeld(t, a)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "GatewayAuthPolicy")
		mustContain(t, g, "GovernanceSkipped", key.String(), "may not read")
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	})
	t.Run("authentication only is a note", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "aboveservedkey")
		key := gatewayPolicy(t, "gw-authn-"+a.Name, map[string]any{
			"targetRefs": onGateway(suiteGatewayName, ""), "traffic": authenticationOnly(t, a)})
		reconcileOnce(t, r, a)
		noted(t, a, key, metav1.ConditionFalse, "AuthVerifiedOnOneReplica", "A74 case 7")
	})
	t.Run("none is a note", func(t *testing.T) {
		a, r, _ := servedNoneAgent(t, "aboveservednone", nil)
		key := gatewayPolicy(t, "gw-auth-"+a.Name, map[string]any{
			"targetRefs": onGateway(suiteGatewayName, ""), "traffic": apiKeyTraffic(t, a)})
		reconcileOnce(t, r, a)
		noted(t, a, key, metav1.ConditionTrue, "AuthOptedOut", "auth: none")
	})
}

// A75: GatewayAuthPolicy clears when its cause goes, even when the probe then
// answers 500, not 401, because the Gateway is read on every ProbingAfter
// pass; and when the transaction it held is abandoned.
func TestGatewayAuthPolicyClears(t *testing.T) {
	t.Run("on removal, while the probe gets 500", func(t *testing.T) {
		a, r, stub := probingCreate(t, "aboveclear", 0)
		key := gatewayAuthPolicy(t, a, stub)
		reconcileOnce(t, r, a)
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
		removePolicy(t, key)
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Probe == nil || tx.Probe.After == nil || *tx.Probe.After != 500 {
			t.Fatalf("want the held switch's 500 on the prepared route: %+v", tx)
		}
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Errorf("GatewayAuthPolicy did not clear once its policy went, on a 500: %+v", c)
		}
	})
	t.Run("on abandonment", func(t *testing.T) {
		a, r, stub := probingCreate(t, "aboveabandon", 0)
		gatewayAuthPolicy(t, a, stub)
		reconcileOnce(t, r, a)
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
		toMode(t, a, "none")
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx != nil && tx.TargetMode == "apikey" {
			t.Fatalf("the Create to apikey was not abandoned: %+v", tx)
		}
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Errorf("GatewayAuthPolicy did not clear when its transaction was abandoned: %+v", c)
		}
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondReady); c != nil && c.Reason == "GatewayAuthPolicy" {
			t.Errorf("Ready still carries GatewayAuthPolicy after the abandonment: %+v", c)
		}
	})
}

// A75: the Gateway is read on every ProbingAfter pass, not only after a 401,
// so a hold is reported while the probe answers something else: here the
// Gateway's policy stands and the replica that answered has not taken it,
// and the prepared route answers 500.
func TestAHoldIsReportedWhateverTheProbeAnswers(t *testing.T) {
	a, r, _ := probingCreate(t, "abovefivehundred", 0)
	key := gatewayPolicy(t, "gw-auth-"+a.Name, map[string]any{
		"targetRefs": onGateway(suiteGatewayName, ""), "traffic": authenticationOnly(t, a)})
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Probe == nil || tx.Probe.After == nil || *tx.Probe.After != 500 {
		t.Fatalf("want the prepared route's 500: %+v", tx)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
	mustContain(t, c, "GatewayAuthPolicy", key.String(), "no 401 is credited")
}

// The deadline outcome applies under the hold, replaces GatewayAuthPolicy,
// and names the Gateway-level cause: AuthEnforcementUnverified for a Create,
// AuthLockUnverified for a J2 Lock.
func TestTheDeadlineOutcomeNamesTheGatewayLevelCause(t *testing.T) {
	t.Run("Create", func(t *testing.T) {
		a, r, stub := probingCreate(t, "abovedeadc", time.Millisecond)
		key := gatewayAuthPolicy(t, a, stub)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		if routePublished(t, a.Namespace, a.Name) {
			t.Fatal("a route was published past the deadline under a Gateway-level policy")
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthEnforcementUnverified")
		mustContain(t, c, "AuthEnforcementUnverified", key.String(), "ProbingAfter", "deadline")
	})
	t.Run("J2 Lock", func(t *testing.T) {
		a, _, _, key := heldJ2(t, "abovedeadl", time.Millisecond,
			func(a *assaydv1alpha1.Agent, stub *stubProber) client.ObjectKey { return gatewayAuthPolicy(t, a, stub) })
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
		mustContain(t, c, "AuthLockUnverified", key.String(), "ProbingAfter")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthLockUnverified")
		if auth := authOf(t, a); auth.Mode != "none" {
			t.Errorf("the lock was recorded past its deadline under a Gateway-level policy: %+v", auth)
		}
	})
}

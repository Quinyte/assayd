// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// ADR-0030 step 2. Until 2026-09-05 no Service existed at all: a Pod could
// report Ready with no stable address, and design 03's route backendRef named
// an object nothing created. §5 recorded it as a guarantee nothing enforced.
func TestARevisionGetsAServiceThatSelectsOnlyItsOwnPods(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "svc", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	var s corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("svc", rev)}
	if err := k8s.Get(context.Background(), key, &s); err != nil {
		t.Fatalf("no Service for the revision: %v", err)
	}
	if got := s.Spec.Selector[controller.LabelRevision]; got != rev {
		t.Errorf("selector revision is %q, want %q: a Service that does not pin the revision "+
			"selects every revision's Pods, so design 03's per-revision weights would all "+
			"resolve to one endpoint set", got, rev)
	}
	if got := s.Spec.Selector[controller.LabelAgent]; got != "svc" {
		t.Errorf("selector agent is %q, want svc", got)
	}
	if len(s.Spec.Ports) != 1 || s.Spec.Ports[0].Port != 8080 {
		t.Errorf("ports are %v, want one on 8080", s.Spec.Ports)
	}
	if got := s.Spec.Ports[0].TargetPort.StrVal; got != "a2a" {
		t.Errorf("targetPort is %q, want the named port a2a", got)
	}
	if got := s.Labels[controller.LabelAgentUID]; got != string(a.UID) {
		t.Errorf("agent-uid label is %q, want %q — it is the corroboration the GC "+
			"name authority relies on", got, a.UID)
	}
}

// The property the per-revision shape exists for. Two revisions coexist during
// a rollout by construction — design 03 shifts backendRefs weights between them
// — so the gated revision's Service must not be able to reach the candidate's
// Pods, or a candidate serves production traffic the moment it goes Ready.
func TestTwoRevisionsGetSeparateServicesThatCannotReachEachOther(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "two", nil)
	r := newReconciler(false)
	r1 := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("two", r1), 1)
	settle(t, r, a)

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	// A behaviour-surface edit, so this mints rather than converging in place.
	a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:" +
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	r2 := revision.MustHash(a.Spec)
	if r1 == r2 {
		t.Fatal("fixture: the edit did not mint a revision")
	}
	settle(t, r, a)

	var s1, s2 corev1.Service
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("two", r1)}, &s1); err != nil {
		t.Fatalf("the gated revision lost its Service when a candidate appeared: %v", err)
	}
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("two", r2)}, &s2); err != nil {
		t.Fatalf("the candidate has no Service: %v", err)
	}
	if s1.Name == s2.Name {
		t.Fatal("both revisions share one Service")
	}
	if s1.Spec.Selector[controller.LabelRevision] == s2.Spec.Selector[controller.LabelRevision] {
		t.Errorf("both Services select revision %q: the candidate's Pods are reachable "+
			"through the gated revision's address, so a candidate would serve production "+
			"traffic with no gate", s1.Spec.Selector[controller.LabelRevision])
	}
}

// A Service outlives nothing: it carries no ownerReference (the Agent is in
// another namespace), so if the operator does not collect it, nothing does.
func TestDeletingAnAgentCollectsItsServices(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "gone", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("gone", rev)}
	var s corev1.Service
	if err := k8s.Get(context.Background(), key, &s); err != nil {
		t.Fatalf("setup: no Service: %v", err)
	}
	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	// The finalizer holds the object, so it is still readable; one reconcile runs
	// the teardown. Re-reading rather than reusing `a` keeps the resourceVersion
	// current, which the finalizer removal needs.
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent after delete: %v", err)
	}
	reconcileOnce(t, r, &live)

	err := k8s.Get(context.Background(), key, &s)
	if !apierrors.IsNotFound(err) {
		t.Errorf("the Service survived its Agent (err=%v): it holds a name a later "+
			"revision of the same Agent would legitimately want, and nothing else will "+
			"collect it", err)
	}
}

// The collision rule, applied to the Service ITSELF.
//
// The first version of this test edited the Agent to a colliding spec and
// checked the Service was not restamped. It passed, and a mutation proved it
// vacuous: ensureWorkload refuses the collision first, so ensureService's own
// check was never reached and deleting that check changed nothing. "It was
// refused" is not evidence about WHICH rule refused.
//
// So this replaces the Service under the operator with one carrying a
// different revision digest, and reconciles. The workload is untouched, so its
// check passes and this one has to do the work. Converging here would point the
// gated revision's route at a selector somebody else chose.
func TestAServiceCarryingAnotherRevisionDigestIsRefusedRatherThanConverged(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "presvc", nil)
	rev := revision.MustHash(a.Spec)
	name := controller.WorkloadName("presvc", rev)
	r := newReconciler(false)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: name}
	var mine corev1.Service
	if err := k8s.Get(context.Background(), key, &mine); err != nil {
		t.Fatalf("setup: no Service: %v", err)
	}
	if err := k8s.Delete(context.Background(), &mine); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The same name and revision label, a different projection behind it.
	squatter := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: runNS(ns),
			Name:      name,
			Labels: map[string]string{
				controller.LabelAgent:    "presvc",
				controller.LabelRevision: rev,
				controller.LabelAgentUID: string(a.UID),
			},
			Annotations: map[string]string{
				controller.RevisionDigestAnnotation: "sha256:" + strings.Repeat("f", 64),
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "not-the-agent"},
			Ports:    []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}},
		},
	}
	if err := k8s.Create(context.Background(), squatter); err != nil {
		t.Fatalf("create squatter: %v", err)
	}

	got := settle(t, r, a)

	// It refuses by REPORTING, not by erroring forever: a bare error would retry
	// with no status and leave an operator nothing to read.
	coll := condition(&got, assaydv1alpha1.CondRevisionHashCollision)
	if coll == nil || coll.Status != metav1.ConditionTrue {
		t.Fatalf("no RevisionHashCollision condition after a Service claimed the revision "+
			"name with another digest; conditions: %v", got.Status.Conditions)
	}
	// The third ground, and the only one that really is a digest mismatch (A77).
	if coll.Reason != "DigestMismatch" {
		t.Errorf("RevisionHashCollision reads reason %q, want DigestMismatch", coll.Reason)
	}
	// NAMESPACED: the remedy is "delete that Service" and the run namespace is a
	// truncate-and-hash nobody types from memory.
	if !strings.Contains(coll.Message, runNS(ns)+"/"+name) {
		t.Errorf("the message names the Service without its run namespace: %s", coll.Message)
	}

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := after.Spec.Selector["app"]; got != "not-the-agent" {
		t.Errorf("the operator converged a Service carrying a different revision digest "+
			"(selector app=%q): two projections share one revision name, so this address "+
			"would resolve to whichever of them the operator wrote last", got)
	}
	if got := after.Annotations[controller.RevisionDigestAnnotation]; got != "sha256:"+strings.Repeat("f", 64) {
		t.Errorf("the operator restamped the digest to %q", got)
	}
}

// plantAndSettle creates an `auth: none` Agent, plants `shape` at the name its
// first revision will use, and reconciles until the status stops moving. It
// returns the Agent's converged status, its namespace and the revision's name.
//
// The run namespace is per SOURCE namespace, not per Agent, so a neighbour
// Agent is all it takes to have one already there — nothing here needs the
// operator's own identity, or a delete, or a race. The revision name is a hash
// of the DEFAULTED spec, which is what the planter reads off the stored object.
func plantAndSettle(t *testing.T, name string, shape corev1.ServiceSpec) (assaydv1alpha1.Agent, string, string) {
	t.Helper()
	ns := newNamespace(t)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	settle(t, r, noneAgent(t, ns, "neighbour"))

	a := noneAgent(t, ns, name)
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName(name, rev)

	shape.Ports = []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}}
	planted := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: runNS(ns), Name: svcName},
		Spec:       shape,
	}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant the Service: %v", err)
	}

	// The workload is rendered BEFORE the Service (ensureWorkload, then
	// ensureService), so it exists in both worlds and a kubelet's report can be
	// faked in both. Without this the revision would fail to promote for want
	// of a replica, and the route assertion below would be vacuous — it would
	// pass against the adopting operator this test exists to catch.
	for i := 0; i < 6; i++ {
		reconcileOnce(t, r, a)
		var d appsv1.Deployment
		if err := k8s.Get(context.Background(),
			types.NamespacedName{Namespace: runNS(ns), Name: svcName}, &d); err == nil {
			break
		}
	}
	markAvailable(t, ns, svcName, 1)
	return settle(t, r, a), ns, svcName
}

// A Service the operator never renders is REFUSED, not adopted.
//
// Reproduced before the refusal existed, on this exact fixture: ensureService
// converged the fields it renders — labels, selector, ports — onto a planted
// `type: ExternalName` Service and left `spec.type` and `spec.externalName`
// as the planter wrote them, because an absent revision digest is adoption
// rather than collision and equalService compares neither field. The Agent
// reached phase `Ready` with `Ready=True/Available`, and the operator's own
// serving route carried `backendRefs[0].name: extname-<rev>` — a backendRef
// this operator wrote, onto the assayd Gateway, naming an object the planter
// controls. The only trace was `Registered=False/CardUnreachable`, whose
// message said "has no ClusterIP": true, and the wrong cause — rule 8's
// loud-and-wrong — on a condition whose own text says it does not withhold
// traffic.
//
// What that costs is not assumed here. Cluster DNS answers the revision's own
// address with a CNAME to the planter's host on every cluster; what the
// gateway does with the backendRef is agentgateway's choice, and 1.5.0's is to
// report the route healthy and serve nothing. Neither is measured by this
// layer, which runs no gateway and no CoreDNS. What IS measured is the object
// the operator writes.
func TestAServiceTheOperatorNeverRendersIsRefusedRatherThanAdopted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agent  string
		shape  corev1.ServiceSpec
		says   []string
		unsaid string
	}{{
		name:  "ExternalName",
		agent: "extname",
		shape: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: "elsewhere.example.com",
		},
		says: []string{"ExternalName", "elsewhere.example.com", "no endpoints", "CNAME",
			"delete that Service"},
	}, {
		name:   "NodePort",
		agent:  "nodeport",
		shape:  corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort},
		says:   []string{"NodePort", "outside the gateway", "delete that Service"},
		unsaid: "ExternalName",
	}, {
		name:   "LoadBalancer",
		agent:  "lb",
		shape:  corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
		says:   []string{"LoadBalancer", "outside the gateway", "delete that Service"},
		unsaid: "NodePort",
	}, {
		name:   "headless",
		agent:  "headless",
		shape:  corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone},
		says:   []string{"headless", "spec.clusterIP: None", "immutable", "delete that Service"},
		unsaid: "ExternalName",
	}, {
		// The finding is about spec.type; this field reaches the same outcome one
		// field over, and the review of A77's first implementation measured it
		// adopted, stamped, and named by the route. kube-proxy DNATs these
		// addresses on every node straight to the revision's Pods.
		name:   "externalIPs",
		agent:  "extips",
		shape:  corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ExternalIPs: []string{"198.51.100.7"}},
		says:   []string{"spec.externalIPs", "198.51.100.7", "outside the gateway", "delete that Service"},
		unsaid: "ExternalName",
	}, {
		// Not an exposure but a gate bypass: promotion rests on readiness, and
		// this field serves Pods that never reached it.
		name:   "publishNotReadyAddresses",
		agent:  "notready",
		shape:  corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, PublishNotReadyAddresses: true},
		says:   []string{"spec.publishNotReadyAddresses", "readiness", "delete that Service"},
		unsaid: "ExternalName",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got, ns, svcName := plantAndSettle(t, tc.agent, tc.shape)

			if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
				t.Errorf("phase is %q, want Degraded: an Agent whose revision Service the operator "+
					"did not render is not serving anything it can vouch for", got.Status.Phase)
			}
			ready := condition(&got, assaydv1alpha1.CondReady)
			if ready == nil || ready.Status != metav1.ConditionFalse ||
				ready.Reason != controller.CondReasonServiceNotRendered {
				t.Errorf("Ready is %+v, want False/RevisionServiceNotRendered", ready)
			}
			deg := condition(&got, assaydv1alpha1.CondDegraded)
			if deg == nil || deg.Status != metav1.ConditionTrue ||
				deg.Reason != controller.CondReasonServiceNotRendered {
				t.Errorf("Degraded is %+v, want True/RevisionServiceNotRendered", deg)
			}
			// Nothing collided, so a condition saying something did would send an
			// operator to look for a second projection that does not exist.
			if meta.IsStatusConditionTrue(got.Status.Conditions,
				string(assaydv1alpha1.CondRevisionHashCollision)) {
				t.Errorf("RevisionHashCollision is True for a Service shape refusal: " +
					"nothing collided, and a condition that names a cause nobody checked is worse " +
					"than none")
			}
			// The message must name the cause AND the fix, or the Agent is stuck
			// with no way out of it (NFR-8).
			for _, want := range tc.says {
				if ready == nil || !strings.Contains(ready.Message, want) {
					t.Errorf("the Ready message does not contain %q; it is: %+v", want, ready)
				}
			}
			if ready != nil && !strings.Contains(ready.Message, svcName) {
				t.Errorf("the Ready message does not name the Service %q, so the remedy it gives "+
					"does not say which object to delete: %s", svcName, ready.Message)
			}
			if tc.unsaid != "" && ready != nil && strings.Contains(ready.Message, tc.unsaid) {
				t.Errorf("the %s message mentions %q: each shape is refused for its own reason, "+
					"and a message that names the wrong one sends an operator to the wrong object. "+
					"It is: %s", tc.name, tc.unsaid, ready.Message)
			}

			// The revision never promotes, so design 03's route is never emitted —
			// which is what keeps a backendRef this operator wrote off the planted
			// object. This is the assertion the finding is about.
			if got.Status.ActiveRevision != "" {
				t.Errorf("the revision promoted to %q behind a Service the operator did not render",
					got.Status.ActiveRevision)
			}
			if rt := servingRoute(t, ns, tc.agent); rt != nil {
				for _, rule := range rt.Spec.Rules {
					for _, br := range rule.BackendRefs {
						if string(br.Name) == svcName {
							t.Errorf("the operator's serving route names %s, the Service it refused: "+
								"a backendRef this operator authored onto the assayd Gateway, "+
								"pointing at an object somebody else controls.", svcName)
						}
					}
				}
			}

			// And the planted object is left exactly as it was: converging it would
			// rewrite a stranger's object and destroy the evidence it was planted.
			var after corev1.Service
			if err := k8s.Get(context.Background(),
				types.NamespacedName{Namespace: runNS(ns), Name: svcName}, &after); err != nil {
				t.Fatalf("get the planted Service: %v", err)
			}
			if after.Spec.Type != tc.shape.Type {
				t.Errorf("the operator rewrote spec.type to %q; it refused, so it must not have "+
					"touched the object", after.Spec.Type)
			}
			if after.Spec.ExternalName != tc.shape.ExternalName {
				t.Errorf("the operator rewrote spec.externalName to %q", after.Spec.ExternalName)
			}
			if len(after.Spec.Selector) != 0 {
				t.Errorf("the operator wrote its selector onto a Service it refused: %v",
					after.Spec.Selector)
			}
			if _, stamped := after.Annotations[controller.RevisionDigestAnnotation]; stamped {
				t.Errorf("the operator stamped its revision digest onto a Service it refused, "+
					"which would make the next pass adopt it as its own: %v", after.Annotations)
			}
		})
	}
}

// The refusal must not fire on the Service the operator renders itself, or
// every Agent is Degraded — and a refusal that refuses everything proves
// nothing about the one object it was written for.
func TestTheOperatorsOwnRevisionServiceIsNotRefused(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "ownsvc")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("ownsvc", rev), 1)
	got := settle(t, r, a)

	if d := condition(&got, assaydv1alpha1.CondDegraded); d != nil &&
		d.Reason == controller.CondReasonServiceNotRendered {
		t.Fatalf("the operator refused the Service it rendered itself: %s", d.Message)
	}
	if got.Status.ActiveRevision != rev {
		t.Fatalf("the revision never promoted: %+v", got.Status)
	}
}

// The shape check is one of TWO refusals and it is not the load-bearing one for
// a planted object. A perfectly ordinary ClusterIP Service passes it — and the
// review of A77's first implementation measured such a Service adopted,
// stamped, given this Agent's UID label and named by the emitted route, with
// `publishNotReadyAddresses` and `sessionAffinity` riding along unconverged.
// The provenance test is what refuses it, on the same three grounds
// ensureWorkload has always used, and it refuses every field at once rather
// than the enumerated few the shape check reads.
func TestAPlainServicePlantedAtARevisionsNameIsRefusedForWantOfProvenance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		agent    string
		forgeUID bool
		says     string
		reason   string
	}{{
		// No labels at all: the first of ensureWorkload's three grounds.
		name: "no UID label", agent: "plain", forgeUID: false,
		says: "does not carry this Agent's UID", reason: "ForeignObject",
	}, {
		// The UID label needs only `create` to write, so the planter forges it —
		// which is why it is not the last line of defence. The object then falls
		// to the third ground: no stamp, and nothing in status vouching.
		name: "a forged UID label", agent: "forged", forgeUID: true,
		says: "nothing in status vouches for it", reason: "Unstamped",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			r := newGatewayReconciler("assayd-gateway", "assayd")
			settle(t, r, noneAgent(t, ns, "neighbour"))

			a := noneAgent(t, ns, tc.agent)
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName(tc.agent, rev)
			labels := map[string]string{}
			if tc.forgeUID {
				labels[controller.LabelAgentUID] = string(a.UID)
			}
			planted := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: runNS(ns), Name: svcName, Labels: labels,
				},
				Spec: corev1.ServiceSpec{
					Type:            corev1.ServiceTypeClusterIP,
					SessionAffinity: corev1.ServiceAffinityClientIP,
					Ports: []corev1.ServicePort{{
						Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP,
					}},
				},
			}
			if err := k8s.Create(context.Background(), planted); err != nil {
				t.Fatalf("plant: %v", err)
			}
			for i := 0; i < 6; i++ {
				reconcileOnce(t, r, a)
				var d appsv1.Deployment
				if err := k8s.Get(context.Background(),
					types.NamespacedName{Namespace: runNS(ns), Name: svcName}, &d); err == nil {
					break
				}
			}
			markAvailable(t, ns, svcName, 1)
			got := settle(t, r, a)

			if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
				t.Errorf("phase is %q, want Degraded", got.Status.Phase)
			}
			coll := condition(&got, assaydv1alpha1.CondRevisionHashCollision)
			if coll == nil || coll.Status != metav1.ConditionTrue {
				t.Fatalf("no RevisionHashCollision after a Service nothing vouches for claimed "+
					"the revision's name; conditions: %v", got.Status.Conditions)
			}
			// The REASON is what an alert keys on, and all three grounds used to
			// report DigestMismatch — telling an operator two projections had
			// collided when what happened is that nothing vouched for the object.
			if coll.Reason != tc.reason {
				t.Errorf("RevisionHashCollision reads reason %q, want %q: the reason must name "+
					"the ground that refused it, not the one the other grounds share",
					coll.Reason, tc.reason)
			}
			ready := condition(&got, assaydv1alpha1.CondReady)
			if ready == nil || !strings.Contains(ready.Message, tc.says) {
				t.Errorf("the message does not say WHY it was refused — it must not render the "+
					"digest-mismatch prose, which would claim two projections collided when "+
					"nothing did. Want %q in: %+v", tc.says, ready)
			}
			// This fixture never promotes and has no route — the two assertions
			// below say so — so the consequence is hypothetical and the message
			// must not state it as fact. Both grounds asserted it unconditionally,
			// and the suite proved the claim false in the pass that wrote it.
			if ready != nil && strings.Contains(ready.Message, "backendRef names this object") {
				t.Errorf("the message asserts that the serving route names this Service, on an "+
					"Agent with no active revision and no route at all: %s", ready.Message)
			}
			if got.Status.ActiveRevision != "" {
				t.Errorf("the revision promoted to %q behind a Service nothing vouches for",
					got.Status.ActiveRevision)
			}
			if rt := servingRoute(t, ns, tc.agent); rt != nil {
				for _, rule := range rt.Spec.Rules {
					for _, br := range rule.BackendRefs {
						if string(br.Name) == svcName {
							t.Errorf("the operator's serving route names %s, the Service it refused",
								svcName)
						}
					}
				}
			}
			var after corev1.Service
			if err := k8s.Get(context.Background(),
				types.NamespacedName{Namespace: runNS(ns), Name: svcName}, &after); err != nil {
				t.Fatalf("get the planted Service: %v", err)
			}
			if _, stamped := after.Annotations[controller.RevisionDigestAnnotation]; stamped {
				t.Errorf("the operator stamped a Service it refused: %v", after.Annotations)
			}
			if len(after.Spec.Selector) != 0 {
				t.Errorf("the operator wrote its selector onto a Service it refused: %v",
					after.Spec.Selector)
			}
		})
	}
}

// The remedy the refusal names has to actually take effect.
//
// Every refusal message ends "delete that Service and let the operator recreate
// it", and nothing turns that deletion into a reconcile: SetupWithManager
// watches no corev1.Service, and a refused object carries none of the labels
// byAgentLabels maps on even if it did. So the refusal must schedule its own
// re-look, and the Agent must actually heal when the object goes.
func TestARefusedRevisionServiceIsRecheckedAndTheAgentHealsWhenItGoes(t *testing.T) {
	// BOTH exits. §3.2 promises the recheck of both refusals, and driving only
	// the shape exit left the provenance exit's requeue — and reportCollision's
	// condition carry with it — unmeasured.
	for _, tc := range []struct {
		name   string
		agent  string
		shape  corev1.ServiceSpec
		reason string
	}{{
		name: "shape", agent: "healshape",
		shape: corev1.ServiceSpec{
			Type: corev1.ServiceTypeExternalName, ExternalName: "elsewhere.example.com",
		},
		reason: controller.CondReasonServiceNotRendered,
	}, {
		name: "provenance", agent: "healprov",
		shape:  corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
		reason: "RevisionHashCollision",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			healsWhenTheServiceGoes(t, tc.agent, tc.shape, tc.reason)
		})
	}
}

func healsWhenTheServiceGoes(t *testing.T, name string, shape corev1.ServiceSpec, reason string) {
	t.Helper()
	ns := newNamespace(t)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	settle(t, r, noneAgent(t, ns, "neighbour"))

	a := noneAgent(t, ns, name)
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName(name, rev)
	shape.Ports = []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}}
	planted := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: runNS(ns), Name: svcName},
		Spec:       shape,
	}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant: %v", err)
	}

	// The refusing pass must ask to be run again. Asserted on the Result, which
	// reconcileOnce discards.
	var res ctrl.Result
	for i := 0; i < 6; i++ {
		var err error
		res, err = r.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if condition(liveAgentPtr(t, a), assaydv1alpha1.CondDegraded) != nil {
			break
		}
	}
	if got := liveAgentPtr(t, a); condition(got, assaydv1alpha1.CondReady) == nil ||
		condition(got, assaydv1alpha1.CondReady).Reason != reason {
		t.Fatalf("setup: the Agent is not refused with %s: %v", reason,
			liveAgentPtr(t, a).Status.Conditions)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("the refusing pass asked for no requeue, so nothing re-reads the Service after "+
			"a human deletes it and the remedy the message names takes until the manager's "+
			"resync. Result: %+v", res)
	}

	// The remedy, performed.
	if err := k8s.Delete(context.Background(), planted); err != nil {
		t.Fatalf("delete the planted Service: %v", err)
	}
	for i := 0; i < 6; i++ {
		reconcileOnce(t, r, a)
		var svc corev1.Service
		if err := k8s.Get(context.Background(),
			types.NamespacedName{Namespace: runNS(ns), Name: svcName}, &svc); err == nil {
			break
		}
	}
	markAvailable(t, ns, svcName, 1)
	healed := settle(t, r, a)
	if healed.Status.ActiveRevision != rev {
		t.Fatalf("the Agent did not recover after the refused Service was deleted: %+v",
			healed.Status)
	}
	if d := condition(&healed, assaydv1alpha1.CondDegraded); d != nil && d.Reason == reason {
		t.Errorf("the refusal condition survived the remedy: %+v", d)
	}
}

// A refusal must not RETRACT design 03's report on an Agent whose route is
// still published and still serving — and it must SAY that the route is still
// there.
//
// PolicyApplyIncomplete and PolicyCompileFailed are owned and not sticky, and
// the refusal returns before the -auth step that asserts them, so merge clears
// what this pass never looked at. That is A47's defect: an Agent becoming more
// degraded reporting less. Both exits are driven, because the carry rides each
// of them separately and a test of one measures nothing about the other.
func TestARefusalDoesNotRetractTheGatewayReport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exit   func(t *testing.T, svc *corev1.Service)
		reason string
	}{{
		// Only the SHAPE check can see the operator's own object: it keeps its
		// UID label and its digest stamp, so provenance passes it untouched.
		name: "the shape exit", reason: controller.CondReasonServiceNotRendered,
		exit: func(t *testing.T, svc *corev1.Service) {
			svc.Spec.Type = corev1.ServiceTypeNodePort
		},
	}, {
		// And only PROVENANCE sees a well-shaped object that is not ours. This
		// exit reports through reportCollision, which carries separately.
		name: "the provenance exit", reason: "ForeignObject",
		exit: func(t *testing.T, svc *corev1.Service) {
			delete(svc.Labels, controller.LabelAgentUID)
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := noneAgent(t, ns, "keepsrep")
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName("keepsrep", rev)
			settle(t, r, a)
			markAvailable(t, ns, svcName, 1)
			settle(t, r, a)
			// The Gateway accepts the route, so the Create of `none` reaches
			// Served — which is what makes this Agent actually SERVED rather than
			// merely promoted, and so what makes the present-tense claim below
			// true of it. routeNames requires it.
			acceptRoute(t, ns, "keepsrep")
			settle(t, r, a)

			// A standing report from an earlier pass, written the way the -auth
			// step writes it. BOTH of them: seeding only one left the other half
			// of the carry unpinned, and deleting it from the list survived the
			// whole suite.
			live := liveAgentPtr(t, a)
			for _, c := range []metav1.Condition{{
				Type: string(assaydv1alpha1.CondPolicyApplyIncomplete), Status: metav1.ConditionTrue,
				Reason: "AuthPolicyNotAttached", Message: "the Gateway reports the policy attached to nothing",
				ObservedGeneration: live.Generation,
			}, {
				Type: string(assaydv1alpha1.CondPolicyCompileFailed), Status: metav1.ConditionTrue,
				Reason: "Budget", Message: "spec.budget does not compile",
				ObservedGeneration: live.Generation,
			}} {
				meta.SetStatusCondition(&live.Status.Conditions, c)
			}
			if err := k8s.Status().Update(context.Background(), live); err != nil {
				t.Fatalf("seed the report: %v", err)
			}
			before := servingRoute(t, ns, "keepsrep")
			if before == nil {
				t.Fatal("setup: no serving route, so there is no fail-open to retract")
			}

			// Now degrade it for an unrelated reason, on its OWN Service.
			var svc corev1.Service
			key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
			if err := k8s.Get(context.Background(), key, &svc); err != nil {
				t.Fatalf("get the operator's Service: %v", err)
			}
			tc.exit(t, &svc)
			if err := k8s.Update(context.Background(), &svc); err != nil {
				t.Fatalf("patch the Service: %v", err)
			}
			reconcileOnce(t, r, a)

			got := liveAgentPtr(t, a)
			ready := condition(got, assaydv1alpha1.CondReady)
			if ready == nil || ready.Status != metav1.ConditionFalse {
				t.Fatalf("the operator's own Service was not refused at all: %+v",
					got.Status.Conditions)
			}
			// The ground is on the collision condition for the provenance exit
			// and on Ready itself for the shape one, so look for it on either.
			if ready.Reason != tc.reason {
				coll := condition(got, assaydv1alpha1.CondRevisionHashCollision)
				if coll == nil || coll.Reason != tc.reason {
					t.Fatalf("the refusal does not name %s: %+v", tc.reason, got.Status.Conditions)
				}
			}
			for _, want := range []struct{ typ, reason string }{
				{string(assaydv1alpha1.CondPolicyApplyIncomplete), "AuthPolicyNotAttached"},
				{string(assaydv1alpha1.CondPolicyCompileFailed), "Budget"},
			} {
				held := condition(got, assaydv1alpha1.ConditionType(want.typ))
				if held == nil || held.Status != metav1.ConditionTrue || held.Reason != want.reason {
					t.Errorf("the refusal retracted %s: an Agent whose route is still published "+
						"and still serving now reports that concern intact because something "+
						"unrelated failed. It is %+v", want.typ, held)
				}
			}

			// The route is NOT withdrawn — and the message has to say so, or a
			// reader is told in the subjunctive about a state that is present
			// tense: "adopting it WOULD leave the route naming it", when it
			// already does.
			after := servingRoute(t, ns, "keepsrep")
			if after == nil {
				t.Fatalf("the refusal withdrew the serving route. Design 02 §3.2 and §5 both say " +
					"it does not, and nothing here is written to withdraw traffic")
			}
			if tc.reason == "ForeignObject" &&
				!strings.Contains(ready.Message, "backendRef names this object") {
				t.Errorf("the provenance message is in the subjunctive on an Agent whose route "+
					"DOES name the object — deleting that branch left the negative assertion in "+
					"the planted-Service test as its only pin, and that one passes without it: %s",
					ready.Message)
			}
			if !strings.Contains(ready.Message, "The route is not withdrawn") {
				t.Errorf("the message does not tell the reader that the published route still "+
					"names this Service and still carries traffic, so it reads as a warning "+
					"about a state that has already happened: %s", ready.Message)
			}
		})
	}
}

// A refusal that stands must not GROW the report it carries.
//
// `carryGatewayReport` re-asserts what the LAST pass stored, note included, and
// a refusal both requeues every minute and re-enqueues itself — the status
// write is its own trigger, since SetupWithManager takes For(&Agent{}) with no
// generation predicate. Appending the note unconditionally added its length on
// every pass until writeStatus's backstop truncated the result: A81's own
// shipped bug ("a held report's message was appended to rather than rebuilt, so
// it grew a few hundred bytes a pass until the API server refused every status
// write for that Agent"), reintroduced in new code and found by the fourth
// review at 142 copies of one sentence. No earlier test drove more than one
// refusing pass, which is why three rounds missed it.
func TestAStandingRefusalDoesNotGrowTheReportItCarries(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "nogrow")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("nogrow", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	live := liveAgentPtr(t, a)
	for _, c := range []metav1.Condition{{
		Type: string(assaydv1alpha1.CondPolicyApplyIncomplete), Status: metav1.ConditionTrue,
		Reason: "AuthPolicyNotAttached", Message: "the Gateway reports the policy attached to nothing",
		ObservedGeneration: live.Generation,
	}, {
		Type: string(assaydv1alpha1.CondPolicyCompileFailed), Status: metav1.ConditionTrue,
		Reason: "Budget", Message: "spec.budget does not compile",
		ObservedGeneration: live.Generation,
	}} {
		meta.SetStatusCondition(&live.Status.Conditions, c)
	}
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var svc corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get: %v", err)
	}
	svc.Spec.Type = corev1.ServiceTypeNodePort
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("patch: %v", err)
	}

	reconcileOnce(t, r, a)
	first := map[string]string{}
	for _, c := range liveAgentPtr(t, a).Status.Conditions {
		first[c.Type] = c.Message
	}
	if first[string(assaydv1alpha1.CondPolicyCompileFailed)] == "" {
		t.Fatalf("setup: nothing was carried, so growth is unmeasurable")
	}
	// Twenty more of the same refusal. Any per-pass append shows here; at the
	// measured rate the message had grown by ~4600 bytes by now.
	for i := 0; i < 20; i++ {
		reconcileOnce(t, r, a)
	}
	for _, c := range liveAgentPtr(t, a).Status.Conditions {
		want, held := first[c.Type]
		if !held || want == c.Message {
			continue
		}
		t.Errorf("condition %s grew from %d to %d bytes over twenty passes of one unchanged "+
			"refusal. A report that grows per pass ends at the API server's cap, and then "+
			"EVERY status write for this Agent fails — the whole status frozen during the "+
			"incident the report exists to describe. It now ends: %q",
			c.Type, len(want), len(c.Message),
			c.Message[max(0, len(c.Message)-90):])
	}
}

// With the gateway declared OFF there is nothing to carry, and a refusal must
// not put design 03's conditions back.
//
// Neither is raised on that tier — assessGovernance writes
// GovernanceSkipped=GatewayDisabled and returns — so an ordinary pass clears a
// stale one left by an install that was flipped off. A carry with no gateway
// gate re-asserts it from every refusing pass instead, and the Agent reports a
// gateway concern on a tier that has no gateway. seedStoredAbove returns early
// for the same reason.
func TestARefusalCarriesNoGatewayReportWhenTheGatewayIsOff(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "gwoff")
	r := newReconciler(false) // the declared-ungoverned tier
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("gwoff", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	live := liveAgentPtr(t, a)
	// status.auth as an install FLIPPED from gateway.enabled=true would leave
	// it: nothing wipes it, and the -auth step that writes it is itself gated
	// on the gateway. Without this the Agent has no status.auth at all, so
	// routeNames is already false on its judgesServed conjunct and the gateway
	// conjunct — the one this test exists for — decides nothing. That is how
	// the mutation this test is listed against stopped being killed.
	live.Status.Auth = &assaydv1alpha1.AuthStatus{Mode: "none"}
	meta.SetStatusCondition(&live.Status.Conditions, metav1.Condition{
		Type: string(assaydv1alpha1.CondPolicyApplyIncomplete), Status: metav1.ConditionTrue,
		Reason: "AuthPolicyNotAttached", Message: "left by an install that had the gateway on",
		ObservedGeneration: live.Generation,
	})
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var svc corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get: %v", err)
	}
	svc.Spec.Type = corev1.ServiceTypeNodePort
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("patch: %v", err)
	}
	reconcileOnce(t, r, a)

	got := liveAgentPtr(t, a)
	if c := condition(got, assaydv1alpha1.CondReady); c == nil ||
		c.Reason != controller.CondReasonServiceNotRendered {
		t.Fatalf("setup: the refusal did not fire, so the carry never ran: %+v", got.Status.Conditions)
	}
	if c := condition(got, assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("the refusal re-asserted a gateway condition on an install with no gateway, so "+
			"an Agent on the declared-ungoverned tier reports a policy concern that tier cannot "+
			"have and no pass will ever clear: %+v", c)
	}
	// And the MESSAGE must not assert a route either. The revision is active,
	// so a guard on that alone puts "its serving route already names this
	// Service and carries traffic" on a tier where reconcileGateway emits no
	// route at all — the reader sent to look for traffic that is not flowing,
	// on the same object whose GovernanceSkipped says the gateway is disabled.
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready != nil && strings.Contains(ready.Message, "serving route") {
		t.Errorf("the refusal claims a serving route on an install with gateway.enabled=false, "+
			"where none is ever emitted: %s", ready.Message)
	}
	if servingRoute(t, ns, "gwoff") != nil {
		t.Fatal("setup: a route exists with the gateway off, so this test proves nothing")
	}
}

// A promoted revision is not a published route, and the refusal must not say
// it is.
//
// Promotion happens before the -auth step, so an Agent can be ACTIVE with no
// route at all. `expose.a2a.auth: oauth` never compiles, design 03 emits
// nothing, and the Agent's own PolicyCompileFailed says "No route is published
// for this Agent" — beside which a refusal claiming its serving route carries
// traffic is the two halves of one status contradicting each other, which
// A77's own carry is what puts side by side. An API-key `Create` still probing
// is the same shape: a prepared route with no backendRefs, parked indefinitely
// by A75's hold.
func TestARefusalClaimsNoRouteForAPromotedAgentThatHasNone(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "nopub", func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{
			A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"},
		}
	})
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("nopub", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	got := settle(t, r, a)

	if got.Status.ActiveRevision != rev {
		t.Fatalf("setup: the revision never promoted, so this test is not about a promoted "+
			"Agent: %+v", got.Status)
	}
	if rt := servingRoute(t, ns, "nopub"); rt != nil {
		t.Fatalf("setup: a route exists for an Agent whose -auth does not compile, so this "+
			"test proves nothing: %s", rt.Name)
	}

	var svc corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get: %v", err)
	}
	svc.Spec.Type = corev1.ServiceTypeNodePort
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("patch: %v", err)
	}
	reconcileOnce(t, r, a)

	live := liveAgentPtr(t, a)
	ready := condition(live, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceNotRendered {
		t.Fatalf("setup: the refusal did not fire: %+v", live.Status.Conditions)
	}
	if strings.Contains(ready.Message, "The route is not withdrawn") {
		t.Errorf("the refusal tells the reader that a serving route names this Service and "+
			"carries traffic, on an Agent for which design 03 has published no route at all. "+
			"Promotion is not publication: %s", ready.Message)
	}
	if servingRoute(t, ns, "nopub") != nil {
		t.Errorf("a route appeared during the refusal, which would make the claim true and this " +
			"test vacuous")
	}
}

func liveAgentPtr(t *testing.T, a *assaydv1alpha1.Agent) *assaydv1alpha1.Agent {
	t.Helper()
	var got assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	return &got
}

// An unbounded field of the planter's must not reach a condition message.
//
// spec.externalIPs has no item cap in Kubernetes validation and
// metav1.Condition.Message is capped at 32768 runes, so the first
// implementation of A77's externalIPs arm rendered the whole slice and every
// status write for the Agent failed with Too long. The whole status then froze
// at its pre-incident value — for a SERVING Agent, Ready=True with its route
// still published — while the refusal went unwritten, which is quieter than the
// adoption the refusal replaced. A81 is the same incident class.
func TestARefusalMessageCannotFreezeTheAgentsStatus(t *testing.T) {
	ips := make([]string, 4000)
	for i := range ips {
		ips[i] = fmt.Sprintf("198.51.%d.%d", i/256, i%256)
	}
	got, _, _ := plantAndSettle(t, "bigips", corev1.ServiceSpec{
		Type: corev1.ServiceTypeClusterIP, ExternalIPs: ips,
	})

	ready := condition(&got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceNotRendered {
		t.Fatalf("the refusal was never written, so the Agent's status is frozen at whatever it "+
			"last said while the planted Service stands: %+v", got.Status.Conditions)
	}
	for _, c := range got.Status.Conditions {
		// RUNES: apiextensions validates `maxLength` with utf8.RuneCount, and a
		// correctly bounded message is 32768 runes but 32770 bytes once it ends
		// in the marker's own multi-byte ellipsis. Counting bytes here fired on
		// behaviour the API server accepts.
		if n := utf8.RuneCountInString(c.Message); n > controller.ConditionMessageMax {
			t.Errorf("condition %s has a %d-rune message, over the API server's %d-rune cap: the "+
				"write that carries it fails, and with it EVERY condition on the object",
				c.Type, n, controller.ConditionMessageMax)
		}
	}
	// And the COMPOSER must be what bounded it, not writeStatus's backstop: a
	// message the backstop had to cut is one whose author did not think about
	// the cap, and the next such author writes one the backstop cuts in the
	// middle of the sentence that named the cause.
	if strings.Contains(ready.Message, "message truncated") {
		t.Errorf("the refusal message was cut by the status backstop, so the composer rendered "+
			"the planter's whole list into it: %s", ready.Message[:min(len(ready.Message), 300)])
	}
	// It must still be usable: the count and a sample locate the object.
	if !strings.Contains(ready.Message, "4000 spec.externalIPs") {
		t.Errorf("the bounded message does not say how many addresses there are: %s", ready.Message)
	}
	if !strings.Contains(ready.Message, "198.51.0.0") {
		t.Errorf("the bounded message names none of the addresses: %s", ready.Message)
	}
}

// The shape check's unique job is the operator's OWN Service, patched in place:
// that object keeps its UID label and its digest stamp, so provenance passes it
// and only shape can see it. Every planted-object subcase above would be
// refused by provenance even with its shape arm gone, so without this the
// externalIPs arm — where the message bug above lived — is pinned only on a
// path that does not need it.
func TestTheOperatorsOwnServicePatchedInPlaceIsRefusedByShapeAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch func(*corev1.Service)
		says  string
	}{
		{"externalIPs", func(s *corev1.Service) {
			s.Spec.ExternalIPs = []string{"198.51.100.7"}
		}, "spec.externalIPs"},
		{"publishNotReadyAddresses", func(s *corev1.Service) {
			s.Spec.PublishNotReadyAddresses = true
		}, "spec.publishNotReadyAddresses"},
		{"ExternalName", func(s *corev1.Service) {
			s.Spec.Type = corev1.ServiceTypeExternalName
			s.Spec.ExternalName = "elsewhere.example.com"
		}, "ExternalName"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := noneAgent(t, ns, "patched")
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName("patched", rev)
			settle(t, r, a)
			markAvailable(t, ns, svcName, 1)
			settle(t, r, a)

			var svc corev1.Service
			key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
			if err := k8s.Get(context.Background(), key, &svc); err != nil {
				t.Fatalf("get the operator's Service: %v", err)
			}
			if svc.Labels[controller.LabelAgentUID] != string(a.UID) {
				t.Fatalf("setup: the operator's Service has no UID label, so provenance would "+
					"refuse it and this test would not be about shape: %v", svc.Labels)
			}
			if _, stamped := svc.Annotations[controller.RevisionDigestAnnotation]; !stamped {
				t.Fatalf("setup: the operator's Service is unstamped: %v", svc.Annotations)
			}
			tc.patch(&svc)
			if err := k8s.Update(context.Background(), &svc); err != nil {
				t.Fatalf("patch the Service: %v", err)
			}
			reconcileOnce(t, r, a)

			ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
			if ready == nil || ready.Reason != controller.CondReasonServiceNotRendered {
				t.Fatalf("the operator's own Service, patched, was not refused by the shape "+
					"check — provenance cannot see it, so nothing else can: %+v", ready)
			}
			if !strings.Contains(ready.Message, tc.says) {
				t.Errorf("the message does not name %q: %s", tc.says, ready.Message)
			}
		})
	}
}

// The self-heal `vouched` exists for, which is otherwise dead to the tests.
//
// `ensureWorkload` keeps the status disjunct because a principal with only
// `patch` could otherwise strip the stamp and wedge the object terminally,
// where before it self-healed. The same is true of a Service. This is what the
// disjunct buys, and design 02 §5 records what it costs: while status vouches
// for the name, an unstamped object at that name is adopted, so provenance is
// not the whole bound the shape enumeration rests on.
func TestAnUnstampedServiceAtAVouchedRevisionIsAdoptedAndRestamped(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "restamp")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("restamp", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision != rev {
		t.Fatalf("setup: the revision never promoted, so status vouches for nothing: %+v",
			got.Status)
	}

	var svc corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get: %v", err)
	}
	delete(svc.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("strip the stamp: %v", err)
	}
	reconcileOnce(t, r, a)

	after := liveAgentPtr(t, a)
	if c := condition(after, assaydv1alpha1.CondDegraded); c != nil &&
		c.Type == string(assaydv1alpha1.CondDegraded) && c.Status == metav1.ConditionTrue &&
		(c.Reason == "Unstamped" || c.Reason == "RevisionHashCollision") {
		t.Fatalf("stripping the stamp wedged the Agent terminally, which is the regression the "+
			"status disjunct exists to prevent — a principal with services/patch alone could "+
			"then take any Agent down: %+v", c)
	}
	var restamped corev1.Service
	if err := k8s.Get(context.Background(), key, &restamped); err != nil {
		t.Fatalf("get after: %v", err)
	}
	if _, ok := restamped.Annotations[controller.RevisionDigestAnnotation]; !ok {
		t.Errorf("the operator did not re-stamp the Service it adopted, so the next pass has "+
			"nothing to recognise it by either: %v", restamped.Annotations)
	}
}

// The two REJECTED exits carry the same report, and they are reached by a
// render the API server refuses rather than by an object somebody planted.
//
// WorkloadRejected is reachable from the spec: a CPU request above its limit
// passes the CRD (which validates quantities, not their relation) and is
// refused by the API server when the Deployment is written. ServiceRejected has
// no such input — every Service this operator renders is valid by construction
// — so its carry rides the same helper and is measured by nothing; design 02 §5
// says so rather than claiming it.
func TestARejectedRenderDoesNotRetractTheGatewayReport(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "rejects")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("rejects", rev), 1)
	settle(t, r, a)

	live := liveAgentPtr(t, a)
	for _, c := range []metav1.Condition{{
		Type: string(assaydv1alpha1.CondPolicyApplyIncomplete), Status: metav1.ConditionTrue,
		Reason: "AuthPolicyNotAttached", Message: "the Gateway reports the policy attached to nothing",
		ObservedGeneration: live.Generation,
	}, {
		Type: string(assaydv1alpha1.CondPolicyCompileFailed), Status: metav1.ConditionTrue,
		Reason: "Budget", Message: "spec.budget does not compile",
		ObservedGeneration: live.Generation,
	}} {
		meta.SetStatusCondition(&live.Status.Conditions, c)
	}
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("seed the report: %v", err)
	}

	edit := liveAgentPtr(t, a)
	edit.Spec.Runtime.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
	}
	if err := k8s.Update(context.Background(), edit); err != nil {
		t.Fatalf("the CRD refused a request above its limit, so this exit is unreachable "+
			"from the spec and the test needs another input: %v", err)
	}
	reconcileOnce(t, r, edit)

	got := liveAgentPtr(t, edit)
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != "WorkloadRejected" {
		t.Fatalf("the rendered workload was not rejected, so this test reaches a different "+
			"exit than the one it is about: %+v", got.Status.Conditions)
	}
	for _, want := range []struct{ typ, reason string }{
		{string(assaydv1alpha1.CondPolicyApplyIncomplete), "AuthPolicyNotAttached"},
		{string(assaydv1alpha1.CondPolicyCompileFailed), "Budget"},
	} {
		held := condition(got, assaydv1alpha1.ConditionType(want.typ))
		if held == nil || held.Status != metav1.ConditionTrue || held.Reason != want.reason {
			t.Errorf("the rejected render retracted %s on an Agent whose previous revision is "+
				"still serving through its published route: %+v", want.typ, held)
		}
	}
}

// The carry must never overwrite a value THIS pass derived.
//
// assessGovernance runs before every early exit and raises
// PolicyApplyIncomplete=AuthPolicyMissing for a served Agent whose -auth policy
// is being re-created (design 03 §3.3.3). An unguarded carry then replaces that
// live assertion with whatever the last pass stored — and carry() bypasses
// raiseIncomplete's precedence order entirely, so the demotion is silent.
// Every carry in seedStoredAbove has this guard; this one had to earn it.
func TestTheCarryDoesNotOverwriteWhatThisPassDerived(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "guarded")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("guarded", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	// A served Agent in the missing-policy Lock, and a STORED report under a
	// different reason. Written by hand: what matters here is the state
	// assessGovernance reads, not how a real Lock gets into it.
	live := liveAgentPtr(t, a)
	live.Status.Auth = &assaydv1alpha1.AuthStatus{
		Mode: "apikey",
		Transaction: &assaydv1alpha1.AuthTransaction{
			Kind: controller.TxLock, TargetMode: "apikey", Stage: "ProbingAfter",
		},
	}
	meta.SetStatusCondition(&live.Status.Conditions, metav1.Condition{
		Type: string(assaydv1alpha1.CondPolicyApplyIncomplete), Status: metav1.ConditionTrue,
		Reason: "AuthPolicyNotAttached", Message: "stored by an earlier pass",
		ObservedGeneration: live.Generation,
	})
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var svc corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get: %v", err)
	}
	svc.Spec.Type = corev1.ServiceTypeNodePort
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("patch: %v", err)
	}
	reconcileOnce(t, r, a)

	got := liveAgentPtr(t, a)
	if c := condition(got, assaydv1alpha1.CondReady); c == nil ||
		c.Reason != controller.CondReasonServiceNotRendered {
		t.Fatalf("setup: the refusal did not fire, so the carry never ran: %+v", got.Status.Conditions)
	}
	held := condition(got, assaydv1alpha1.CondPolicyApplyIncomplete)
	if held == nil {
		t.Fatalf("PolicyApplyIncomplete is gone entirely: %+v", got.Status.Conditions)
	}
	if held.Reason != "AuthPolicyMissing" {
		t.Errorf("the carry overwrote this pass's own assertion with the stored one: reason is "+
			"%q, want AuthPolicyMissing. A stored report must never displace one the pass "+
			"derived, and carry() does not go through raiseIncomplete's precedence order, so "+
			"nothing else would catch it. It is %+v", held.Reason, held)
	}
}

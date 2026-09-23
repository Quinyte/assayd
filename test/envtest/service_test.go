// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

// A Service the operator never renders, PLANTED at a revision's name, is
// refused — by provenance, with the shape named in the message.
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
func TestAPlantedServiceIsRefusedAndItsShapeIsNamed(t *testing.T) {
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
		says: []string{"does not carry this Agent's UID", "ExternalName",
			"elsewhere.example.com", "no endpoints", "CNAME", "delete that Service"},
	}, {
		name:  "NodePort",
		agent: "nodeport",
		shape: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort},
		says: []string{"does not carry this Agent's UID", "NodePort", "outside the gateway",
			"delete that Service"},
		unsaid: "ExternalName",
	}, {
		name:  "LoadBalancer",
		agent: "lb",
		shape: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
		says: []string{"does not carry this Agent's UID", "LoadBalancer", "outside the gateway",
			"delete that Service"},
		unsaid: "NodePort",
	}, {
		name:  "headless",
		agent: "headless",
		shape: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone},
		says: []string{"does not carry this Agent's UID", "headless", "spec.clusterIP: None",
			"immutable", "delete that Service"},
		unsaid: "ExternalName",
	}, {
		// The finding is about spec.type; this field reaches the same outcome one
		// field over, and the review of A77's first implementation measured it
		// adopted, stamped, and named by the route. kube-proxy DNATs these
		// addresses on every node straight to the revision's Pods.
		name:  "externalIPs",
		agent: "extips",
		shape: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ExternalIPs: []string{"198.51.100.7"}},
		says: []string{"does not carry this Agent's UID", "spec.externalIPs", "198.51.100.7",
			"outside the gateway", "delete that Service"},
		unsaid: "ExternalName",
	}, {
		// Not an exposure but a gate bypass: promotion rests on readiness, and
		// this field serves Pods that never reached it.
		name:  "publishNotReadyAddresses",
		agent: "notready",
		shape: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, PublishNotReadyAddresses: true},
		says: []string{"does not carry this Agent's UID", "spec.publishNotReadyAddresses",
			"readiness", "delete that Service"},
		unsaid: "ExternalName",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got, ns, svcName := plantAndSettle(t, tc.agent, tc.shape)

			if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
				t.Errorf("phase is %q, want Degraded: an Agent whose revision Service the operator "+
					"did not render is not serving anything it can vouch for", got.Status.Phase)
			}
			ready := condition(&got, assaydv1alpha1.CondReady)
			if ready == nil || ready.Status != metav1.ConditionFalse {
				t.Errorf("Ready is %+v, want False", ready)
			}
			// PROVENANCE is the ground: none of these objects carries this
			// Agent's UID. The shape is detail on that refusal, not the reason
			// for it — the human's call of 2026-09-22, because an object that
			// does carry our stamp is ours and is converged instead.
			coll := condition(&got, assaydv1alpha1.CondRevisionHashCollision)
			if coll == nil || coll.Status != metav1.ConditionTrue || coll.Reason != "ForeignObject" {
				t.Errorf("RevisionHashCollision is %+v, want True/ForeignObject: a planted object "+
					"is refused on provenance, whatever its shape", coll)
			}
			// The message must name the cause, the SHAPE and the fix, or the
			// Agent is stuck with no way out of it (NFR-8) and the administrator
			// cannot tell a name collision from a planted CNAME.
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
		d.Status == metav1.ConditionTrue {
		t.Fatalf("the operator refused the Service it rendered itself: %s", d.Message)
	}
	if got.Status.ActiveRevision != rev {
		t.Fatalf("the revision never promoted: %+v", got.Status)
	}
}

// The operator's OWN Service, patched in place, is CONVERGED BACK — not
// refused.
//
// The first cut of A77 refused it, and the human reversed that on 2026-09-22.
// Provenance is what decides ownership: an object carrying our stamp is ours,
// rewriting it is what ensureWorkload already does to the Deployment, and
// "converging would rewrite a stranger's object" is not true of it. The wedge
// decided it: refusing here handed anyone with `services/patch` in a run
// namespace a permanent per-Agent outage, needing no ExternalName and no
// cleverness — a worse failure than the one A77 closes. Converging also CLOSES
// the off-gateway exposure instead of only reporting it.
func TestTheOperatorsOwnServicePatchedInPlaceIsConvergedBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch func(*corev1.Service)
		check func(*testing.T, *corev1.Service)
	}{
		{"externalIPs", func(s *corev1.Service) {
			s.Spec.ExternalIPs = []string{"198.51.100.7"}
		}, func(t *testing.T, s *corev1.Service) {
			if len(s.Spec.ExternalIPs) != 0 {
				t.Errorf("spec.externalIPs survived convergence as %v: every node still DNATs "+
					"them to this revision's Pods, outside the gateway", s.Spec.ExternalIPs)
			}
		}},
		{"publishNotReadyAddresses", func(s *corev1.Service) {
			s.Spec.PublishNotReadyAddresses = true
		}, func(t *testing.T, s *corev1.Service) {
			if s.Spec.PublishNotReadyAddresses {
				t.Errorf("spec.publishNotReadyAddresses survived convergence: the readiness gate " +
					"promotion rests on is still answered by Pods that never passed it")
			}
		}},
		{"ExternalName", func(s *corev1.Service) {
			s.Spec.Type = corev1.ServiceTypeExternalName
			s.Spec.ExternalName = "elsewhere.example.com"
		}, func(t *testing.T, s *corev1.Service) {
			if s.Spec.Type != corev1.ServiceTypeClusterIP || s.Spec.ExternalName != "" {
				t.Errorf("the Service is still type=%q externalName=%q: cluster DNS goes on "+
					"answering this revision's address with a CNAME to that host",
					s.Spec.Type, s.Spec.ExternalName)
			}
			if s.Spec.ClusterIP == "" {
				t.Errorf("the converged Service has no ClusterIP, so the card fetch can never " +
					"reach it")
			}
		}},
		// `Local` is not a steering preference: a Service served only by
		// endpoints on the caller's node DROPS the request when there are none,
		// so one patch black-holes the gateway's hop to the agent on any
		// multi-node cluster while the Agent reads Ready=True. It was added to
		// the enumeration and held there by nothing until this case existed.
		{"internalTrafficPolicy", func(s *corev1.Service) {
			s.Spec.InternalTrafficPolicy = ptrTo(corev1.ServiceInternalTrafficPolicyLocal)
		}, func(t *testing.T, s *corev1.Service) {
			if p := s.Spec.InternalTrafficPolicy; p == nil ||
				*p != corev1.ServiceInternalTrafficPolicyCluster {
				t.Errorf("spec.internalTrafficPolicy survived convergence as %v: the Service is "+
					"served only by endpoints on the caller's node and DROPS the request when "+
					"there are none, so the gateway's hop to the agent black-holes with nothing "+
					"reported", p)
			}
		}},
		{"NodePort", func(s *corev1.Service) {
			s.Spec.Type = corev1.ServiceTypeNodePort
		}, func(t *testing.T, s *corev1.Service) {
			if s.Spec.Type != corev1.ServiceTypeClusterIP {
				t.Errorf("the Service is still %q, so every node still publishes this revision's "+
					"Pods outside the gateway", s.Spec.Type)
			}
		}},
		// The COUPLED field, and the reason this case exists. Kubernetes
		// auto-clears most fields that only exist under the type being left,
		// but not loadBalancerSourceRanges — so asserting spec.type alone made
		// the repair itself invalid, and a single `services/patch` left the
		// Agent permanently Degraded with the exposure intact. That is the
		// wedge converging exists to avoid, restored by the converge.
		{"LoadBalancer with a source range", func(s *corev1.Service) {
			s.Spec.Type = corev1.ServiceTypeLoadBalancer
			s.Spec.LoadBalancerSourceRanges = []string{"10.0.0.0/8"}
		}, func(t *testing.T, s *corev1.Service) {
			if s.Spec.Type != corev1.ServiceTypeClusterIP {
				t.Errorf("the Service is still %q: the repair was refused because it asserted "+
					"spec.type without the fields that type owns, and the exposure survived",
					s.Spec.Type)
			}
			if len(s.Spec.LoadBalancerSourceRanges) != 0 {
				t.Errorf("spec.loadBalancerSourceRanges survived as %v, which the API server "+
					"forbids under type ClusterIP — so every later converge of this object is "+
					"rejected too", s.Spec.LoadBalancerSourceRanges)
			}
		}},
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
					"refuse it and this test would not be about convergence: %v", svc.Labels)
			}
			if _, stamped := svc.Annotations[controller.RevisionDigestAnnotation]; !stamped {
				t.Fatalf("setup: the operator's Service is unstamped: %v", svc.Annotations)
			}
			tc.patch(&svc)
			if err := k8s.Update(context.Background(), &svc); err != nil {
				t.Fatalf("patch the Service: %v", err)
			}
			reconcileOnce(t, r, a)

			var after corev1.Service
			if err := k8s.Get(context.Background(), key, &after); err != nil {
				t.Fatalf("get after: %v", err)
			}
			tc.check(t, &after)

			// And the Agent must not be degraded by its own object being
			// repaired — the wedge is the whole reason this converges.
			live := liveAgentPtr(t, a)
			if c := condition(live, assaydv1alpha1.CondDegraded); c != nil &&
				c.Status == metav1.ConditionTrue {
				t.Errorf("the Agent is Degraded after the operator repaired its OWN Service: a "+
					"principal with services/patch alone could then take any Agent down, which "+
					"is the wedge converging exists to avoid: %+v", c)
			}
			if live.Status.ActiveRevision != rev {
				t.Errorf("the revision left active revision %q after its Service was repaired",
					live.Status.ActiveRevision)
			}
		})
	}
}

// Convergence must not CHURN: a repaired Service compares equal on the next
// pass, or the operator rewrites it forever and fights every other writer.
func TestConvergingTheShapeSettles(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "settles")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("settles", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	var svc corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get: %v", err)
	}
	svc.Spec.ExternalIPs = []string{"198.51.100.7"}
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("patch: %v", err)
	}
	reconcileOnce(t, r, a)

	var repaired corev1.Service
	if err := k8s.Get(context.Background(), key, &repaired); err != nil {
		t.Fatalf("get repaired: %v", err)
	}
	counter := &countingClient{Client: k8s}
	counting := newGatewayReconciler("assayd-gateway", "assayd")
	counting.Client = counter
	for i := 0; i < 5; i++ {
		reconcileOnce(t, counting, a)
	}
	var settled corev1.Service
	if err := k8s.Get(context.Background(), key, &settled); err != nil {
		t.Fatalf("get settled: %v", err)
	}
	if settled.ResourceVersion != repaired.ResourceVersion {
		t.Errorf("the repaired Service was rewritten again over five passes (%s → %s): a "+
			"convergence that does not compare its own output churns the API server forever",
			repaired.ResourceVersion, settled.ResourceVersion)
	}
	if n := counter.count(); n != 0 {
		t.Errorf("five reconciles after the repair issued %d writes; want 0", n)
	}
}

// The three provenance grounds, each under its own reason.
//
// The shape table above drives `ForeignObject` across six shapes; this drives
// the ground a forged UID label falls to, which needs only `create` to write —
// so the label is not the last line of defence, and the stamp is. All three
// grounds used to report `DigestMismatch`, telling an operator two projections
// had collided when nothing did.
func TestAPlainServicePlantedAtARevisionsNameIsRefusedForWantOfProvenance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		agent    string
		forgeUID bool
		says     string
		reason   string
	}{{
		name: "no UID label", agent: "plain", forgeUID: false,
		says: "does not carry this Agent's UID", reason: "ForeignObject",
	}, {
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
			// report DigestMismatch.
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
			// NAMESPACED: the remedy is "delete that Service" and the run
			// namespace is a truncate-and-hash nobody types from memory.
			if ready != nil && !strings.Contains(ready.Message, runNS(ns)+"/"+svcName) {
				t.Errorf("the message names the Service without its run namespace: %s",
					ready.Message)
			}
			// This fixture never promotes and has no route, so the consequence
			// is hypothetical and the message must not state it as fact.
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
		name         string
		agent        string
		shape        corev1.ServiceSpec
		heldHeadless bool
		reason       string
	}{{
		// The one shape refusal left: an unrepairable Service already replaced
		// once inside the cooldown, which is reported rather than replaced
		// again. It needs the operator's own object — promoted, stamped, and
		// carrying the replaced-at annotation — and two patches to break it.
		name: "shape", agent: "healshape",
		heldHeadless: true,
		reason:       controller.CondReasonServiceReplaceHeld,
	}, {
		name: "provenance", agent: "healprov",
		shape:  corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
		reason: "RevisionHashCollision",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			healsWhenTheServiceGoes(t, tc.agent, tc.shape, tc.heldHeadless, tc.reason)
		})
	}
}

func healsWhenTheServiceGoes(t *testing.T, name string, shape corev1.ServiceSpec,
	heldHeadless bool, reason string) {
	t.Helper()
	ns := newNamespace(t)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	settle(t, r, noneAgent(t, ns, "neighbour"))

	a := noneAgent(t, ns, name)
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName(name, rev)
	shape.Ports = []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}}
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if heldHeadless {
		// The operator's OWN Service, promoted so status vouches for it, stamped
		// as already replaced, then broken by patch alone.
		settle(t, r, a)
		markAvailable(t, ns, svcName, 1)
		settle(t, r, a)
		armReplaceCooldown(t, a, rev)
		headlessByPatchAlone(t, key)
	} else {
		planted := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: runNS(ns), Name: svcName},
			Spec:       shape,
		}
		if err := k8s.Create(context.Background(), planted); err != nil {
			t.Fatalf("plant: %v", err)
		}
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
	var doomed corev1.Service
	if err := k8s.Get(context.Background(), key, &doomed); err != nil {
		t.Fatalf("get the refused Service: %v", err)
	}
	if err := k8s.Delete(context.Background(), &doomed); err != nil {
		t.Fatalf("delete the refused Service: %v", err)
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
		name         string
		exit         func(t *testing.T, svc *corev1.Service)
		heldHeadless bool
		reason       string
	}{{
		// The one shape fault convergence cannot repair — spec.clusterIP is
		// immutable — and which is therefore DELETED and recreated. To get a
		// refusal out of it the fixture must also carry the replaced-at stamp,
		// because only a second occurrence inside the cooldown is reported.
		// Reaching the state needs no create and no forged label: two
		// strategic-merge patches through ExternalName do it (headlessByPatchAlone).
		name: "the shape exit", reason: controller.CondReasonServiceReplaceHeld,
		heldHeadless: true,
	}, {
		// And PROVENANCE sees a well-shaped object that is not ours. This exit
		// reports through reportCollision, which carries separately.
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
			if tc.heldHeadless {
				armReplaceCooldown(t, a, rev)
				headlessByPatchAlone(t, key)
			} else {
				// Without a record, as the fixtures below: a recorded UID admits
				// the operator's own object whatever its labels say.
				forgetServiceRecords(t, a)
				tc.exit(t, &svc)
				if err := k8s.Update(context.Background(), &svc); err != nil {
					t.Fatalf("patch the Service: %v", err)
				}
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
	// A NodePort patch would now be CONVERGED back, so it degrades nothing.
	// Stripping the UID label is the provenance ground, and a refusal is what
	// these tests are about — but only for an object this operator has no
	// RECORD of creating: a recorded UID admits its own object whatever its
	// labels say, which is what heals the label-strip wedge. So the record is
	// forgotten first, which is the state of an Agent created before it.
	forgetServiceRecords(t, a)
	delete(svc.Labels, controller.LabelAgentUID)
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("strip the UID label: %v", err)
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
	// A NodePort patch would now be CONVERGED back, so it degrades nothing.
	// Stripping the UID label is the provenance ground, and a refusal is what
	// these tests are about — but only for an object this operator has no
	// RECORD of creating: a recorded UID admits its own object whatever its
	// labels say, which is what heals the label-strip wedge. So the record is
	// forgotten first, which is the state of an Agent created before it.
	forgetServiceRecords(t, a)
	delete(svc.Labels, controller.LabelAgentUID)
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("strip the UID label: %v", err)
	}
	reconcileOnce(t, r, a)

	got := liveAgentPtr(t, a)
	if c := condition(got, assaydv1alpha1.CondReady); c == nil ||
		c.Reason != "RevisionHashCollision" {
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
	// The subjunctive ("converging it WOULD point the serving route's
	// backendRef at this object") is fine and true. What must be absent is the
	// present tense: a claim that traffic is flowing on a tier where
	// reconcileGateway emits no route at all.
	ready := condition(got, assaydv1alpha1.CondReady)
	for _, forbidden := range []string{"The route is not withdrawn", "backendRef names this object"} {
		if ready != nil && strings.Contains(ready.Message, forbidden) {
			t.Errorf("the refusal claims a serving route on an install with "+
				"gateway.enabled=false, where none is ever emitted (%q): %s",
				forbidden, ready.Message)
		}
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
	// A NodePort patch would now be CONVERGED back, so it degrades nothing.
	// Stripping the UID label is the provenance ground, and a refusal is what
	// these tests are about — but only for an object this operator has no
	// RECORD of creating: a recorded UID admits its own object whatever its
	// labels say, which is what heals the label-strip wedge. So the record is
	// forgotten first, which is the state of an Agent created before it.
	forgetServiceRecords(t, a)
	delete(svc.Labels, controller.LabelAgentUID)
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("strip the UID label: %v", err)
	}
	reconcileOnce(t, r, a)

	live := liveAgentPtr(t, a)
	ready := condition(live, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != "RevisionHashCollision" {
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
	if ready == nil || ready.Reason != "RevisionHashCollision" {
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

// The self-heal `vouched` exists for, which is otherwise dead to the tests.
//
// `ensureWorkload` keeps the status disjunct because a principal with only
// `patch` could otherwise strip the stamp and wedge the object terminally,
// where before it self-healed. The same is true of a Service. This is what the
// disjunct buys, and design 02 §5 records what it costs: while status vouches
// for the name, an unstamped object at that name is adopted, so provenance is
// not the whole bound the shape enumeration rests on.
//
// The record is forgotten first. With a record, the UID match admits the
// operator's own object before provenance runs, so this fixture would pass
// with the disjunct deleted; the disjunct is what admits an object this
// operator has no record of — an Agent created before the record existed.
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

	forgetServiceRecords(t, a)
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
// — so its carry is measured through a real admission policy instead, in
// TestServiceRejectedByAnAdmissionPolicyOnBothArms.
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
	// A NodePort patch would now be CONVERGED back, so it degrades nothing.
	// Stripping the UID label is the provenance ground, and a refusal is what
	// these tests are about — but only for an object this operator has no
	// RECORD of creating: a recorded UID admits its own object whatever its
	// labels say, which is what heals the label-strip wedge. So the record is
	// forgotten first, which is the state of an Agent created before it.
	forgetServiceRecords(t, a)
	delete(svc.Labels, controller.LabelAgentUID)
	if err := k8s.Update(context.Background(), &svc); err != nil {
		t.Fatalf("strip the UID label: %v", err)
	}
	reconcileOnce(t, r, a)

	got := liveAgentPtr(t, a)
	if c := condition(got, assaydv1alpha1.CondReady); c == nil ||
		c.Reason != "RevisionHashCollision" {
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

// headlessByPatchAlone performs the two strategic-merge patches that make a
// revision Service headless WITHOUT create, delete or a forged label.
//
// `spec.clusterIP` is immutable except across transitions to and from
// `ExternalName`, so a round trip through it wipes the address and lets the
// second patch set `None`. The object keeps the operator's UID label and its
// revision digest the whole way, so it sails through provenance — which is why
// the first cut's "reaching it needs more than services/patch" was false, and
// why the human chose delete-and-recreate on 2026-09-22.
func headlessByPatchAlone(t *testing.T, key types.NamespacedName) {
	t.Helper()
	ctx := context.Background()
	for _, patch := range []string{
		`{"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`,
		`{"spec":{"type":"ClusterIP","externalName":null,"clusterIP":"None","clusterIPs":["None"]}}`,
	} {
		var svc corev1.Service
		if err := k8s.Get(ctx, key, &svc); err != nil {
			t.Fatalf("get for patch: %v", err)
		}
		if err := k8s.Patch(ctx, &svc, client.RawPatch(types.StrategicMergePatchType, []byte(patch))); err != nil {
			t.Fatalf("patch %s: %v", patch, err)
		}
	}
	var after corev1.Service
	if err := k8s.Get(ctx, key, &after); err != nil {
		t.Fatalf("get after patches: %v", err)
	}
	if after.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Fatalf("the two-patch sequence did not make the Service headless (clusterIP=%q), so "+
			"this fixture is not reproducing the blocker", after.Spec.ClusterIP)
	}
	if after.Labels[controller.LabelAgentUID] == "" {
		t.Fatalf("the patches lost the UID label, so provenance would refuse it and the test " +
			"would measure the wrong branch")
	}
}

// A Service this operator owns and CANNOT repair in place is deleted and
// recreated — not refused.
//
// The first cut refused it, on the premise that reaching a headless Service at
// a revision's name needed a create with a forged UID label. That premise was
// false: two strategic-merge patches get there with `services/patch` alone,
// and a served Agent sat permanently Degraded with its route still naming the
// object. The human chose delete-and-recreate on 2026-09-22.
func TestAnUnrepairableRevisionServiceIsDeletedAndRecreated(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "replaced")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("replaced", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	var before corev1.Service
	if err := k8s.Get(context.Background(), key, &before); err != nil {
		t.Fatalf("get: %v", err)
	}
	headlessByPatchAlone(t, key)

	reconcileOnce(t, r, a)

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("the Service is gone and was not recreated: %v", err)
	}
	if after.UID == before.UID {
		t.Fatalf("the Service was not replaced — same UID %s. An object this operator owns and "+
			"cannot repair by Update wedges the Agent forever if it is only reported", after.UID)
	}
	if after.Spec.ClusterIP == "" || after.Spec.ClusterIP == corev1.ClusterIPNone {
		t.Errorf("the replacement has no ClusterIP (%q), so the card fetch still cannot reach it",
			after.Spec.ClusterIP)
	}
	if after.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("the replacement is type %q", after.Spec.Type)
	}
	if after.Annotations[controller.ServiceReplacedAnnotation] == "" {
		t.Errorf("the replacement carries no %s: nothing READS it, but a human looking at the "+
			"object has no other sign that this operator replaced it",
			controller.ServiceReplacedAnnotation)
	}
	// The BOUND is in status, which only this controller writes.
	recorded := liveAgentPtr(t, a)
	if recorded.Status.ServiceReplacedRevision != rev || recorded.Status.ServiceReplacedAt == nil {
		t.Errorf("status records no replace (%q, %v), so nothing bounds a second one — and the "+
			"annotation cannot, because the principal that breaks the Service can rewrite it",
			recorded.Status.ServiceReplacedRevision, recorded.Status.ServiceReplacedAt)
	}

	// And the Agent HEALS rather than reporting the replacement as an incident.
	markAvailable(t, ns, svcName, 1)
	healed := settle(t, r, a)
	if healed.Status.ActiveRevision != rev {
		t.Errorf("the Agent did not return to its active revision: %+v", healed.Status)
	}
	if d := condition(&healed, assaydv1alpha1.CondDegraded); d != nil &&
		d.Status == metav1.ConditionTrue {
		t.Errorf("the Agent is Degraded after its own Service was repaired: %+v", d)
	}
}

// The replace is BOUNDED. A replacement that lands back in the unrepairable
// state is a delete, a new ClusterIP and a fresh traffic gap on every pass, so
// the second occurrence inside the window is reported instead.
func TestASecondUnrepairableServiceInsideTheWindowIsHeldNotReplacedAgain(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "held")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("held", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	// Stamp it as replaced a moment ago — the state the operator leaves behind
	// — and break it again.
	var svc corev1.Service
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get: %v", err)
	}
	armReplaceCooldown(t, a, rev)
	headlessByPatchAlone(t, key)
	var before corev1.Service
	if err := k8s.Get(context.Background(), key, &before); err != nil {
		t.Fatalf("get before: %v", err)
	}

	reconcileOnce(t, r, a)

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("the Service was deleted despite the cooldown: %v", err)
	}
	if after.UID != before.UID {
		t.Fatalf("the Service was replaced a second time inside %s — a replacement that keeps "+
			"coming back becomes a delete and a traffic gap on every pass",
			controller.ServiceReplaceCooldown)
	}
	got := liveAgentPtr(t, a)
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceReplaceHeld {
		t.Fatalf("the held replace is not reported: %+v", got.Status.Conditions)
	}
	// And the not-ours case must NOT share this reason — the same error type
	// carried both until the reason moved onto it.
	if strings.Contains(ready.Message, "cannot establish that it created it") {
		t.Errorf("a held replace is reported with the not-ours prose: %s", ready.Message)
	}
	for _, want := range []string{"already deleted and recreated it", "services/patch",
		"delete that Service"} {
		if !strings.Contains(ready.Message, want) {
			t.Errorf("the held message does not contain %q; it is: %s", want, ready.Message)
		}
	}
}

// The delete is reachable ONLY past provenance. A foreign or unstamped object
// is refused, never deleted — the operator must not destroy something it
// cannot establish it created.
func TestAnUnrepairableServiceThatIsNotOursIsRefusedAndNeverDeleted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		forgeUID bool
		reason   string
	}{
		{"a foreign object", false, "ForeignObject"},
		// A forged UID label needs only `create`, so the label does not decide:
		// an unstamped object at a revision status does NOT vouch for is refused
		// by provenance too, before the record or the shape is consulted.
		{"a forged UID label", true, "Unstamped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			r := newGatewayReconciler("assayd-gateway", "assayd")
			settle(t, r, noneAgent(t, ns, "neighbour"))

			a := noneAgent(t, ns, "notours")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName("notours", rev)
			labels := map[string]string{}
			if tc.forgeUID {
				labels[controller.LabelAgentUID] = string(a.UID)
			}
			planted := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: runNS(ns), Name: svcName, Labels: labels,
				},
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone,
					Ports: []corev1.ServicePort{{
						Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP,
					}},
				},
			}
			if err := k8s.Create(context.Background(), planted); err != nil {
				t.Fatalf("plant: %v", err)
			}
			uid := planted.UID

			for i := 0; i < 4; i++ {
				reconcileOnce(t, r, a)
			}

			var after corev1.Service
			if err := k8s.Get(context.Background(),
				types.NamespacedName{Namespace: runNS(ns), Name: svcName}, &after); err != nil {
				t.Fatalf("THE OPERATOR DELETED AN OBJECT IT COULD NOT ESTABLISH IT CREATED. The "+
					"delete path must be reachable only past provenance: %v", err)
			}
			if after.UID != uid {
				t.Fatalf("the object at %s was replaced (%s -> %s): the operator destroyed "+
					"something that is not its own", svcName, uid, after.UID)
			}
			got := liveAgentPtr(t, a)
			coll := condition(got, assaydv1alpha1.CondRevisionHashCollision)
			if coll == nil || coll.Reason != tc.reason {
				t.Errorf("want RevisionHashCollision/%s, got %+v", tc.reason, coll)
			}
		})
	}
}

// The ESCAPE path: REPORTED, not repaired. Design 02 §5 carries both halves.
//
// An owner whose revision Service has been broken edits the spec to recover.
// The edit mints a new DESIRED revision, and `ensureService` converges only
// the desired one — so the pass that would have replaced the broken Service
// never looks at it again. The old revision stays ACTIVE and its Service, the
// one the serving route names, keeps no address.
//
// Measured on 2026-09-22, before this was reported:
//
//	activeRevision="126eb2a71a"  candidate="e75424173f"
//	phase="Ready"  Ready=True/Available  clusterIP="None"
//
// The repair and the report are separable and only the report is taken here.
// Repairing it would mean either converging a revision other than the desired
// one — which reverses the property design 16's A10 and A11 critiques rest on
// — or making readiness depend on the serving revision's Service, which
// changes what `Ready` means. Widening the delete's reach on the same change
// that introduces the delete is the wrong order.
//
// So the operator now READS the active revision's Service and reports an
// unusable one. It writes nothing, re-creates nothing, and says nothing about
// a MISSING Service — that last is design 16's own fixture, which deletes a
// retired revision's Service and expects no fuss.
func TestAnOwnerEditOverABrokenActiveRevisionIsReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		// breakIt leaves the ACTIVE revision's Service with no usable
		// ClusterIP. The check reads two values, and each row reaches one.
		breakIt   func(t *testing.T, key types.NamespacedName)
		clusterIP string
	}{
		{"headless", headlessByPatchAlone, corev1.ClusterIPNone},
		{"ExternalName, which has no ClusterIP at all", func(t *testing.T, key types.NamespacedName) {
			patchService(t, key, `{"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`)
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := noneAgent(t, ns, "escape")
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName("escape", rev)
			settle(t, r, a)
			markAvailable(t, ns, svcName, 1)
			settle(t, r, a)

			key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
			tc.breakIt(t, key)

			// The owner's recovery attempt: edit the spec, minting a new revision.
			live := liveAgentPtr(t, a)
			live.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:" + strings.Repeat("a", 64)
			if err := k8s.Update(context.Background(), live); err != nil {
				t.Fatalf("edit the spec: %v", err)
			}
			if revision.MustHash(live.Spec) == rev {
				t.Fatalf("setup: the edit did not mint a new revision")
			}
			for i := 0; i < 6; i++ {
				reconcileOnce(t, r, live)
			}

			active := liveService(t, key)
			got := liveAgentPtr(t, a)
			ready := condition(got, assaydv1alpha1.CondReady)
			if ready == nil {
				t.Fatal("no Ready condition")
			}
			t.Logf("measured: activeRevision=%q candidate=%q phase=%q ready=%v/%v clusterIP=%q",
				got.Status.ActiveRevision, got.Status.CandidateRevision, got.Status.Phase,
				ready.Status, ready.Reason, active.Spec.ClusterIP)

			if got.Status.ActiveRevision != rev {
				t.Fatalf("the edit promoted a new active revision (%q), which would take the route "+
					"with it and make this state unreachable. Re-derive §5 rather than deleting "+
					"this test", got.Status.ActiveRevision)
			}
			// NOT repaired — that is the deferred half, and §5 names the fix to take.
			if active.Spec.ClusterIP != tc.clusterIP {
				t.Fatalf("the ACTIVE revision's Service was repaired (clusterIP=%q). If something "+
					"now converges a revision other than the desired one, design 02 §5's escape "+
					"row and design 16's A10/A11 premise are both out of date — fix the record first",
					active.Spec.ClusterIP)
			}
			// REPORTED — which is the half this change takes.
			if ready.Status != metav1.ConditionFalse ||
				ready.Reason != controller.CondReasonActiveServiceUnaddressable {
				t.Fatalf("the Agent reads %v/%v while the ACTIVE revision %s — the one its serving "+
					"route names — has no ClusterIP, and nothing says so",
					ready.Status, ready.Reason, got.Status.ActiveRevision)
			}
			if d := condition(got, assaydv1alpha1.CondDegraded); d == nil ||
				d.Status != metav1.ConditionTrue || d.Reason != controller.CondReasonActiveServiceUnaddressable {
				t.Errorf("Degraded is %+v: an alert keyed on the condition, not the phase, misses "+
					"this", d)
			}
			if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
				t.Errorf("phase is %q, want Degraded", got.Status.Phase)
			}
			for _, want := range []string{"has no ClusterIP", "only the DESIRED revision",
				"To recover", "revert that spec edit"} {
				if !strings.Contains(ready.Message, want) {
					t.Errorf("the message does not contain %q; it is: %s", want, ready.Message)
				}
			}
			// Round three FOLLOWED an earlier remedy that said to remove the
			// Service: with the gateway on the route then named nothing and the
			// Agent read Pending/RouteApplyFailed on every pass; with it off it
			// read Ready=True with no Service at all and nothing reporting it.
			if strings.Contains(strings.ToLower(ready.Message), "delete") {
				t.Errorf("the remedy tells the reader to delete the active revision's Service, "+
					"which leaves the route naming nothing: %s", ready.Message)
			}
		})
	}
}

// A MISSING Service on a retired active revision raises nothing. That is design
// 16's A10/A11 fixture — it deletes R's Service once C is desired and expects
// `EvalWaitingForAuth` to go on naming R — and the report above must not turn
// it into an incident.
func TestAMissingActiveRevisionServiceIsNotReported(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "gonesvc")
	// Gateway OFF, so the pass completes and persists status. With it on, the
	// route emitter has no port to read for a Service that is gone and the pass
	// errors before writing anything — which hides whatever this check said.
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("gonesvc", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	live := liveAgentPtr(t, a)
	live.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:" + strings.Repeat("b", 64)
	if err := k8s.Update(context.Background(), live); err != nil {
		t.Fatalf("edit: %v", err)
	}
	reconcileOnce(t, r, live)

	var doomed corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	if err := k8s.Get(context.Background(), key, &doomed); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := k8s.Delete(context.Background(), &doomed); err != nil {
		t.Fatalf("delete the retired revision's Service: %v", err)
	}
	for i := 0; i < 4; i++ {
		reconcileOnce(t, r, live)
	}

	got := liveAgentPtr(t, a)
	if c := condition(got, assaydv1alpha1.CondReady); c != nil &&
		c.Reason == controller.CondReasonActiveServiceUnaddressable {
		t.Errorf("a MISSING Service on the active revision was reported as unaddressable. That "+
			"is design 16's own fixture, which deletes a retired revision's Service and expects "+
			"no fuss: %+v", c)
	}
}

// swapBeforeDelete replaces the revision Service with a DIFFERENT object under
// the same name at the moment the operator issues its delete.
//
// That is the race the UID precondition exists for. The operator reads an
// object, decides it is unrepairable and its own, and then deletes it — and in
// between, the name can come to hold something else. Without the precondition
// the delete names only the name, so whatever now holds it is destroyed:
// the operator would be a deputy for anyone who can win that window.
type swapBeforeDelete struct {
	client.Client
	t    *testing.T
	at   types.NamespacedName
	swap func()
	done bool
}

func (c *swapBeforeDelete) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if !c.done {
		if svc, ok := obj.(*corev1.Service); ok &&
			svc.Name == c.at.Name && svc.Namespace == c.at.Namespace {
			c.done = true
			c.swap()
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

// The delete carries a UID precondition, so a name that has come to hold a
// different object between the read and the delete is left alone.
//
// This is the one guard standing between "the operator replaces its own broken
// Service" and "the operator deletes whatever is at that name". The window is
// real on any cluster: the name is derived from the Agent's name and a public
// revision hash, so anyone with services/create can take it the moment the
// operator's own object goes.
func TestTheReplaceDeletesTheObjectItReadAndNotTheName(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "swapped")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("swapped", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)

	// At the delete, the operator's object vanishes and somebody else's takes
	// the name. `innocent` is what must survive.
	var innocentUID types.UID
	swapper := &swapBeforeDelete{Client: k8s, t: t, at: key, swap: func() {
		var mine corev1.Service
		if err := k8s.Get(context.Background(), key, &mine); err != nil {
			t.Fatalf("swap: get: %v", err)
		}
		if err := k8s.Delete(context.Background(), &mine); err != nil {
			t.Fatalf("swap: delete: %v", err)
		}
		innocent := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: key.Namespace, Name: key.Name,
				Labels: map[string]string{"someone": "else"},
			},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{{
					Name: "other", Port: 9999, Protocol: corev1.ProtocolTCP,
				}},
			},
		}
		if err := k8s.Create(context.Background(), innocent); err != nil {
			t.Fatalf("swap: create the innocent object: %v", err)
		}
		innocentUID = innocent.UID
	}}
	racing := newGatewayReconciler("assayd-gateway", "assayd")
	racing.Client = swapper

	if _, err := racing.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile returned %v (a lost race is allowed to error; destroying the "+
			"bystander is not)", err)
	}
	if !swapper.done {
		t.Fatal("setup: no delete was issued, so the race never happened")
	}

	// AND the pass must not have gone on as if nothing happened. Returning
	// success here let the operator promote, route and report Ready=True over
	// an object it never established — the bystander credited with the Agent's
	// traffic. "A lost race never returns nil" was stated three times and
	// measured on one of its three arms.
	lost := liveAgentPtr(t, a)
	lostReady := condition(lost, assaydv1alpha1.CondReady)
	if lostReady == nil || lostReady.Status != metav1.ConditionFalse {
		t.Errorf("the Agent reads %+v after a lost UID precondition. The pass could not "+
			"establish its Service and must not go on to promote and route over whatever "+
			"holds the name", lostReady)
	}
	if lostReady != nil && lostReady.Reason != controller.CondReasonServiceReplaceFailed {
		t.Errorf("Ready reason is %q, want %s", lostReady.Reason,
			controller.CondReasonServiceReplaceFailed)
	}

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("THE OPERATOR DELETED THE OBJECT THAT TOOK THE NAME. Its delete named only the "+
			"name, so whoever wins the window between the read and the delete has their object "+
			"destroyed by this operator: %v", err)
	}
	if after.UID != innocentUID {
		t.Fatalf("the object at %s is not the one that took the name (%s vs %s): the delete "+
			"reached past the object it had read", svcName, after.UID, innocentUID)
	}
	if after.Labels["someone"] != "else" {
		t.Errorf("the bystander was rewritten: %v", after.Labels)
	}
}

// armReplaceCooldown puts the operator in the state it leaves behind after a
// replace, through the status subresource — which is the only writer of it.
func armReplaceCooldown(t *testing.T, a *assaydv1alpha1.Agent, rev string) {
	t.Helper()
	live := liveAgentPtr(t, a)
	now := metav1.Now()
	live.Status.ServiceReplacedRevision, live.Status.ServiceReplacedAt = rev, &now
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("arm the cooldown: %v", err)
	}
}

// The bound must not be writable by the principal it exists to stop.
//
// The first implementation kept it in an annotation on the Service, and
// `services/patch` writes annotations. Both directions were measured: a
// future-dated value held the replace forever — the permanent wedge the
// human's decision was taken to remove, restored with one more patch of the
// same verb — and deleting the value removed the bound, producing a delete, a
// new ClusterIP and a traffic gap on every pass.
func TestTheReplaceBoundIsNotWritableByWhoeverBreaksTheService(t *testing.T) {
	t.Run("a future-dated annotation does not hold the replace", func(t *testing.T) {
		ns := newNamespace(t)
		a := noneAgent(t, ns, "future")
		r := newGatewayReconciler("assayd-gateway", "assayd")
		rev := revision.MustHash(a.Spec)
		svcName := controller.WorkloadName("future", rev)
		settle(t, r, a)
		markAvailable(t, ns, svcName, 1)
		settle(t, r, a)

		key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
		var svc corev1.Service
		if err := k8s.Get(context.Background(), key, &svc); err != nil {
			t.Fatalf("get: %v", err)
		}
		before := svc.UID
		if err := k8s.Patch(context.Background(), &svc, client.RawPatch(types.StrategicMergePatchType,
			[]byte(`{"metadata":{"annotations":{"assayd.dev/service-replaced-at":"3000-01-01T00:00:00Z"}}}`))); err != nil {
			t.Fatalf("patch the annotation: %v", err)
		}
		headlessByPatchAlone(t, key)
		for i := 0; i < 3; i++ {
			reconcileOnce(t, r, a)
		}

		var after corev1.Service
		if err := k8s.Get(context.Background(), key, &after); err != nil {
			t.Fatalf("get after: %v", err)
		}
		if after.UID == before {
			t.Fatalf("the Service was not replaced: a value the breaking principal wrote held "+
				"the repair off, which is the permanent wedge delete-and-recreate exists to "+
				"remove. Ready=%+v", condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady))
		}
	})

	t.Run("deleting the annotation does not remove the bound", func(t *testing.T) {
		ns := newNamespace(t)
		a := noneAgent(t, ns, "stripped")
		r := newGatewayReconciler("assayd-gateway", "assayd")
		rev := revision.MustHash(a.Spec)
		svcName := controller.WorkloadName("stripped", rev)
		settle(t, r, a)
		markAvailable(t, ns, svcName, 1)
		settle(t, r, a)
		key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}

		seen := map[types.UID]bool{}
		for i := 0; i < 3; i++ {
			var svc corev1.Service
			if err := k8s.Get(context.Background(), key, &svc); err != nil {
				t.Fatalf("iteration %d: get: %v", i, err)
			}
			seen[svc.UID] = true
			if err := k8s.Patch(context.Background(), &svc, client.RawPatch(types.StrategicMergePatchType,
				[]byte(`{"metadata":{"annotations":{"assayd.dev/service-replaced-at":null}}}`))); err != nil {
				t.Fatalf("iteration %d: strip: %v", i, err)
			}
			headlessByPatchAlone(t, key)
			reconcileOnce(t, r, a)
		}
		var last corev1.Service
		if err := k8s.Get(context.Background(), key, &last); err != nil {
			t.Fatalf("get last: %v", err)
		}
		seen[last.UID] = true
		if len(seen) > 2 {
			t.Errorf("%d distinct Services inside one cooldown window: stripping the annotation "+
				"removed the bound, so the operator deletes and recreates on every pass — one "+
				"new ClusterIP and one traffic gap each", len(seen))
		}
	})
}

// takeNameOnDelete plants somebody else's Service at the name the instant the
// operator's delete frees it.
type takeNameOnDelete struct {
	client.Client
	t     *testing.T
	at    types.NamespacedName
	uid   types.UID
	taken bool
}

func (c *takeNameOnDelete) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	err := c.Client.Delete(ctx, obj, opts...)
	if err == nil && !c.taken {
		if svc, ok := obj.(*corev1.Service); ok && svc.Name == c.at.Name {
			c.taken = true
			planted := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: c.at.Namespace, Name: c.at.Name,
					Labels: map[string]string{"planted": "yes"},
				},
				Spec: corev1.ServiceSpec{
					Type:     corev1.ServiceTypeClusterIP,
					Selector: map[string]string{"app": "attacker"},
					Ports: []corev1.ServicePort{{
						Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP,
					}},
				},
			}
			if cerr := c.Client.Create(ctx, planted); cerr != nil {
				c.t.Fatalf("take the name: %v", cerr)
			}
			c.uid = planted.UID
		}
	}
	return err
}

// The delete frees the name the serving route's backendRef points at. If the
// recreate then fails, the Agent must SAY so — not report Ready=True over
// whatever took it.
//
// Measured before this was reported: the create failed `AlreadyExists`, which
// rejectedByAPIServer counts as transient, so the error fell through to a bare
// return with no status written and the published route named the planted
// object while the Agent read Ready=True/Available.
func TestAFailedRecreateIsReportedAndDoesNotPromoteOverWhateverTookTheName(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "nametake")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("nametake", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)

	taker := &takeNameOnDelete{Client: k8s, t: t, at: key}
	racing := newGatewayReconciler("assayd-gateway", "assayd")
	racing.Client = taker
	if _, err := racing.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile: %v", err)
	}
	if !taker.taken {
		t.Fatal("setup: the name was never taken, so the window was not exercised")
	}

	got := liveAgentPtr(t, a)
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Status != metav1.ConditionFalse {
		t.Fatalf("the Agent reads %+v after its serving Service was deleted and the replacement "+
			"refused. The published route names that object BY NAME, so traffic now reaches "+
			"whatever took it", ready)
	}
	if ready.Reason != controller.CondReasonServiceReplaceFailed {
		t.Errorf("Ready reason is %q, want %s", ready.Reason,
			controller.CondReasonServiceReplaceFailed)
	}
	if !strings.Contains(ready.Message, "whatever holds that name now is what traffic reaches") {
		t.Errorf("the message does not tell the reader that the name is what traffic follows: %s",
			ready.Message)
	}
}

// And the replace is not attempted at all when the replacement would be
// refused — the serving object is not destroyed to find out.
func TestTheReplaceIsNotAttemptedWhenTheReplacementWouldBeRefused(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "dryrun")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("dryrun", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)
	var before corev1.Service
	if err := k8s.Get(context.Background(), key, &before); err != nil {
		t.Fatalf("get: %v", err)
	}

	refusing := &refuseCreate{Client: k8s, at: key}
	guarded := newGatewayReconciler("assayd-gateway", "assayd")
	guarded.Client = refusing
	if _, err := guarded.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile: %v", err)
	}

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("THE SERVING SERVICE WAS DELETED and could not be recreated. The Create must be "+
			"dry-run before anything is destroyed: %v", err)
	}
	if after.UID != before.UID {
		t.Fatalf("the object was replaced despite the replacement being refused")
	}
	ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceReplaceFailed {
		t.Errorf("the refused replace is not reported: %+v", ready)
	}
}

// refuseCreate refuses every Service create at one name, as an admission policy
// over `services` would.
type refuseCreate struct {
	client.Client
	at types.NamespacedName
}

func (c *refuseCreate) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if svc, ok := obj.(*corev1.Service); ok && svc.Name == c.at.Name && svc.Namespace == c.at.Namespace {
		return apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, svc.Name,
			fmt.Errorf("every Service must carry a cost-centre label"))
	}
	return c.Client.Create(ctx, obj, opts...)
}

// The delete is reachable only for the object whose UID status records — not
// for one provenance merely admitted.
//
// The third provenance ground adopts an unstamped object while status vouches
// for the name, and a forged agent-uid label needs only `create`. Such an
// object is rewritten but never destroyed. The same holds for one that forges
// the digest as well: TestAForgedStampOnAnotherUIDIsRefusedAndNeverDeleted
// measures that object, which the stamp-gated delete this replaced destroyed.
func TestAnUnstampedVouchedServiceIsRefusedNotDeleted(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "vouchdel")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("vouchdel", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision != rev {
		t.Fatalf("setup: status vouches for nothing: %+v", got.Status)
	}

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	var mine corev1.Service
	if err := k8s.Get(context.Background(), key, &mine); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := k8s.Delete(context.Background(), &mine); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Unstamped, forged UID label, headless, at a revision status vouches for.
	planted := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: runNS(ns), Name: svcName,
			Labels: map[string]string{controller.LabelAgentUID: string(a.UID)},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone,
			Ports: []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}},
		},
	}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant: %v", err)
	}
	uid := planted.UID
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("THE OPERATOR DELETED AN UNSTAMPED OBJECT. The status disjunct adopts one it "+
			"cannot establish it created; it must not DESTROY one: %v", err)
	}
	if after.UID != uid {
		t.Fatalf("the unstamped object was replaced (%s -> %s)", uid, after.UID)
	}
	// Its OWN reason, and its own message. A held-replace reason on an object
	// that was never held — and never will be, because the operator has
	// committed to not replacing it — sends an operator looking for a cooldown
	// that does not exist. The reason is what an alert keys on.
	ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceNotRecorded {
		t.Fatalf("the refusal reads %+v, want %s — the operator will not delete this object "+
			"because its UID is not the one it recorded creating", ready,
			controller.CondReasonServiceNotRecorded)
	}
	if !strings.Contains(ready.Message, "cannot establish that it created it") {
		t.Errorf("the message does not say WHY the object is left alone: %s", ready.Message)
	}
	for _, forbidden := range []string{"already deleted and recreated it", "0001-01-01",
		"Something is putting it back"} {
		if strings.Contains(ready.Message, forbidden) {
			t.Errorf("the message describes a held replace that never happened (%q): %s",
				forbidden, ready.Message)
		}
	}
}

// vanishBeforeDelete removes the Service just before the operator's own delete
// reaches the API server, so that delete returns NotFound.
type vanishBeforeDelete struct {
	client.Client
	t    *testing.T
	at   types.NamespacedName
	done bool
}

func (c *vanishBeforeDelete) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if !c.done {
		if svc, ok := obj.(*corev1.Service); ok && svc.Name == c.at.Name && svc.Namespace == c.at.Namespace {
			c.done = true
			var live corev1.Service
			if err := c.Client.Get(ctx, c.at, &live); err == nil {
				if derr := c.Client.Delete(ctx, &live); derr != nil {
					c.t.Fatalf("vanish: %v", derr)
				}
			}
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

// The third arm of "a lost race never returns nil": the object is gone before
// the delete lands.
//
// Returning success there let the pass promote and route on the strength of a
// Service it had neither repaired nor replaced — and on this path there is no
// object at the name at all.
func TestAVanishedServiceDoesNotLetTheReplacePassAsSuccess(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "vanish")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("vanish", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)

	vanisher := &vanishBeforeDelete{Client: k8s, t: t, at: key}
	racing := newGatewayReconciler("assayd-gateway", "assayd")
	racing.Client = vanisher
	if _, err := racing.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile: %v", err)
	}
	if !vanisher.done {
		t.Fatal("setup: no delete was issued, so the arm was not exercised")
	}

	got := liveAgentPtr(t, a)
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Status != metav1.ConditionFalse {
		t.Fatalf("the Agent reads %+v after its Service vanished mid-replace. The pass "+
			"established no Service and must not report Ready over one", ready)
	}
	if ready.Reason != controller.CondReasonServiceReplaceFailed {
		t.Errorf("Ready reason is %q, want %s", ready.Reason,
			controller.CondReasonServiceReplaceFailed)
	}
}

// The not-ours refusal must name the real cause, not describe itself as a held
// replace that never happened.
func TestTheNotOursRefusalSaysItCannotEstablishItCreatedTheObject(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "notmine")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("notmine", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	var mine corev1.Service
	if err := k8s.Get(context.Background(), key, &mine); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := k8s.Delete(context.Background(), &mine); err != nil {
		t.Fatalf("delete: %v", err)
	}
	planted := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: runNS(ns), Name: svcName,
			Labels: map[string]string{controller.LabelAgentUID: string(a.UID)},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone,
			Ports: []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}},
		},
	}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant: %v", err)
	}
	reconcileOnce(t, r, a)

	ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
	if ready == nil {
		t.Fatal("no Ready condition")
	}
	if !strings.Contains(ready.Message, "cannot establish that it created it") {
		t.Errorf("the message does not say WHY the object is left alone: %s", ready.Message)
	}
	for _, forbidden := range []string{"already deleted and recreated it", "0001-01-01"} {
		if strings.Contains(ready.Message, forbidden) {
			t.Errorf("the message describes a held replace that never happened (%q): %s",
				forbidden, ready.Message)
		}
	}
}

// A replace time in the FUTURE does not hold the replace.
//
// `time.Since` of a future instant is negative, and negative is less than the
// cooldown, so a naive comparison holds forever. status is only this
// controller's to write, but a clock that jumped — or a restore from a backup
// taken on a machine whose clock was ahead — puts one there without any
// adversary. The guard is `since >= 0`, and nothing held it until this.
func TestAFutureReplaceTimeDoesNotHoldTheReplace(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "skew")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("skew", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	live := liveAgentPtr(t, a)
	ahead := metav1.NewTime(time.Now().Add(72 * time.Hour))
	live.Status.ServiceReplacedRevision, live.Status.ServiceReplacedAt = rev, &ahead
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("seed a future replace time: %v", err)
	}

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	var before corev1.Service
	if err := k8s.Get(context.Background(), key, &before); err != nil {
		t.Fatalf("get: %v", err)
	}
	headlessByPatchAlone(t, key)
	reconcileOnce(t, r, a)

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.UID == before.UID {
		t.Fatalf("a replace time %s in the future held the repair off. time.Since of a future "+
			"instant is negative, and negative is inside any window, so the Agent stays wedged "+
			"until that time passes: Ready=%+v", ahead,
			condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady))
	}
}

// The revision is part of the bound: a NEW revision gets a NEW Service, which
// the last revision's cooldown must not hold.
func TestACooldownOnAnotherRevisionDoesNotHoldThisOne(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "otherrev")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("otherrev", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	armReplaceCooldown(t, a, "someotherrevision")
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	var before corev1.Service
	if err := k8s.Get(context.Background(), key, &before); err != nil {
		t.Fatalf("get: %v", err)
	}
	headlessByPatchAlone(t, key)
	reconcileOnce(t, r, a)

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.UID == before.UID {
		t.Errorf("a cooldown recorded for a DIFFERENT revision held this one's repair: each " +
			"revision has its own Service and the bound is per revision")
	}
}

// The CREATE path reaches `ServiceRejected` too, and its remedy must not name
// an object that was never created.
//
// An earlier mutation row claimed nothing reaches that exit. An ordinary
// ValidatingAdmissionPolicy over `services` — "every Service must carry a
// cost-centre label" — reaches it on the first pass of a new Agent, before any
// Service exists, and the message said "delete that Service". Rule 8.
func TestARefusedServiceCreateSaysThereIsNothingToDelete(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "nocreate")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("nocreate", rev)

	refusing := &refuseCreate{
		Client: k8s,
		at:     types.NamespacedName{Namespace: runNS(ns), Name: svcName},
	}
	r := newGatewayReconciler("assayd-gateway", "assayd")
	r.Client = refusing
	for i := 0; i < 4; i++ {
		if _, err := r.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
			t.Logf("reconcile: %v", err)
		}
	}

	var svc corev1.Service
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: svcName}, &svc); err == nil {
		t.Fatalf("setup: the Service exists, so the create was not refused")
	}
	got := liveAgentPtr(t, a)
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != "ServiceRejected" {
		t.Fatalf("a refused Service CREATE is not reported: %+v", got.Status.Conditions)
	}
	if !strings.Contains(ready.Message, "no such object to delete") {
		t.Errorf("the message tells the reader to delete a Service that was never created: %s",
			ready.Message)
	}
	if strings.Contains(ready.Message, "repair its own Service in place") {
		t.Errorf("the create path reports the update path's remedy: %s", ready.Message)
	}
}

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
)

// Design 03 §8.1 case 20 (A86, the human's D6 answered K1, ADR-0034
// Amendment 7): a served API-key Agent whose key source holds no key reads
// PolicyApplyIncomplete=True and Ready=False, both ApiKeySourceEmpty, phase
// Degraded, and nothing is withdrawn.
//
// Every fixture that reaches `Served` under apikey writes one labelled key
// ConfigMap first (promote, refusedAdoptAgent), because under A86 every row
// built on them that asserts Ready=True would otherwise change meaning. The
// rows here remove it where they need to.

// The stable prefixes the rows read. keySourceHeld is this half's own marker
// (A86): every A80 note speaks about the Gateway, and this fact comes from a
// ConfigMap list.
const (
	keySourceHeld     = "carried from the last pass whose list of the key source succeeded"
	keySourceHeldLead = "a claim from an earlier pass is standing: that pass's live list found no key"
	keySourceAbsent   = "a live list found none there"
	keySourceFresh    = "no API key can authenticate to this Agent"
	keySetName        = "a86-keys"
)

// writeKeySet writes one labelled key ConfigMap holding one entry in the run
// namespace, as an administrator would. The value is a made-up hash of the
// shape agentgateway 1.5.0 documents: no key or secret is carried anywhere.
func writeKeySet(t *testing.T, ns string) {
	t.Helper()
	writeKeySetWith(t, ns, map[string]string{"caller": `{"keyHash":"sha256:` +
		strings.Repeat("ab", 32) + `","metadata":{"group":"` + ns + `"}}`}, nil)
}

func writeKeySetWith(t *testing.T, ns string, data map[string]string, bin map[string][]byte) {
	t.Helper()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: keySetName, Namespace: runNS(ns),
			Labels: map[string]string{compiler.APIKeySourceLabel: compiler.APIKeySourceValue}},
		Data: data, BinaryData: bin,
	}
	ctx := context.Background()
	if err := k8s.Create(ctx, cm); apierrors.IsAlreadyExists(err) {
		var live corev1.ConfigMap
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(cm), &live); err != nil {
			t.Fatalf("read the key set: %v", err)
		}
		live.Data, live.BinaryData, live.Labels = data, bin, cm.Labels
		if err := k8s.Update(ctx, &live); err != nil {
			t.Fatalf("rewrite the key set: %v", err)
		}
	} else if err != nil {
		t.Fatalf("write the key set: %v", err)
	}
}

// deleteKeySets deletes every labelled key ConfigMap in the run namespace.
func deleteKeySets(t *testing.T, ns string) {
	t.Helper()
	if err := k8s.DeleteAllOf(context.Background(), &corev1.ConfigMap{}, client.InNamespace(runNS(ns)),
		client.MatchingLabels{compiler.APIKeySourceLabel: compiler.APIKeySourceValue}); err != nil {
		t.Fatalf("delete the key set: %v", err)
	}
}

func keySourceClaim(t *testing.T, a *assaydv1alpha1.Agent) bool {
	t.Helper()
	auth := authOf(t, a)
	return auth != nil && auth.KeySourceEmpty
}

// healthyServedAPIKeyAgent is a served API-key Agent whose route and policy
// the Gateway reports healthy at their current generations, and whose key set
// has been read PRESENT.
func healthyServedAPIKeyAgent(t *testing.T, name string) (*assaydv1alpha1.Agent, *controller.AgentReconciler,
	*stubProber) {
	t.Helper()
	a, r, stub := servedAPIKeyAgent(t, name)
	acceptRoute(t, a.Namespace, a.Name)
	acceptPolicy(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Fatalf("a healthy served API-key Agent with a key set reads %+v", c)
	}
	return liveAgent(t, a), r, stub
}

// lostKeys is (a)'s stimulus: the key set deleted, one pass.
func lostKeys(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) {
	t.Helper()
	deleteKeySets(t, a.Namespace)
	reconcileOnce(t, r, a)
}

// keySourceReader records, and optionally fails, the reads A86's half makes:
// a GET of an AgentgatewayPolicy, and a LIST of ConfigMaps selected by the
// key-source label. Every other read passes through untouched — the run
// namespace's binding record and the uid-labelled lists among them.
type keySourceReader struct {
	client.Reader
	failKeyList bool
	// foreignPolicy makes every policy GET through this reader return the
	// object with another Agent's UID label. Only the key-source half GETs a
	// policy through the uncached reader (liveAuthPolicy), so nothing else
	// the pass does sees it.
	foreignPolicy bool
	mu            sync.Mutex
	policyGets    int
	keyLists      int
	policyLists   int
}

func (k *keySourceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object,
	opts ...client.GetOption) error {
	if u, ok := obj.(*unstructured.Unstructured); ok && u.GetKind() == compiler.PolicyKind {
		k.mu.Lock()
		k.policyGets++
		k.mu.Unlock()
		if err := k.Reader.Get(ctx, key, obj, opts...); err != nil || !k.foreignPolicy {
			return err
		}
		l := u.GetLabels()
		l[controller.LabelAgentUID] = "someone-else"
		u.SetLabels(l)
		return nil
	}
	return k.Reader.Get(ctx, key, obj, opts...)
}

func (k *keySourceReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	o := &client.ListOptions{}
	o.ApplyOptions(opts)
	selector := ""
	if o.LabelSelector != nil {
		selector = o.LabelSelector.String()
	}
	switch l := list.(type) {
	case *corev1.ConfigMapList:
		if strings.Contains(selector, compiler.APIKeySourceLabel) {
			k.mu.Lock()
			k.keyLists++
			k.mu.Unlock()
			if k.failKeyList {
				return apierrors.NewServiceUnavailable("injected: the key-source list failed")
			}
		}
	case *unstructured.UnstructuredList:
		if strings.HasPrefix(l.GetKind(), compiler.PolicyKind) {
			k.mu.Lock()
			k.policyLists++
			k.mu.Unlock()
		}
	}
	return k.Reader.List(ctx, list, opts...)
}

func readerOf(r *controller.AgentReconciler) client.Reader {
	if r.Reader != nil {
		return r.Reader
	}
	return k8s
}

// (a) A key set lost. The measured defect: before A86 this Agent stayed
// Ready=True, phase Ready, with every key in the namespace at 401.
//
// Mutation: ignore the LIST's answer. It compiles, and this must fail.
func TestALostKeySetIsReported(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86lost")
	gsBefore := *condition(liveAgent(t, a), assaydv1alpha1.CondGovernanceSkipped)
	rv := policyResourceVersion(t, a)

	lostKeys(t, r, a)

	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
	mustContain(t, c, "PolicyApplyIncomplete", keySourceFresh, keySourceAbsent,
		compiler.APIKeySourceLabel+"="+compiler.APIKeySourceValue, runNS(a.Namespace),
		"every request gets 401", "assayd writes no keys", "Nothing is withdrawn")
	mustNotContain(t, c, "PolicyApplyIncomplete", keySourceHeld, "none of which holds an entry")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ApiKeySourceEmpty")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a served Agent nobody can call is %s, want Degraded (K1)", got)
	}
	gs := condition(liveAgent(t, a), assaydv1alpha1.CondGovernanceSkipped)
	if gs.Status != gsBefore.Status || gs.Reason != gsBefore.Reason || gs.Message != gsBefore.Message ||
		gs.ObservedGeneration != gsBefore.ObservedGeneration {
		t.Errorf("GovernanceSkipped moved; A86 writes nothing to it:\nbefore %+v\nafter  %+v", gsBefore, *gs)
	}
	if !routePublished(t, a.Namespace, a.Name) {
		t.Error("the route was withdrawn; nothing is withdrawn for an empty key source")
	}
	if got := policyResourceVersion(t, a); got != rv {
		t.Errorf("the served <agent>-auth was rewritten (%s → %s)", rv, got)
	}
	if !keySourceClaim(t, a) {
		t.Error("status.auth.keySourceEmpty was not recorded, so an unknown reading cannot hold it")
	}
}

// (a′) A first `Create` with no keys raises the FRESH report on its `Served`
// pass — the pass whose probe has just passed on an anonymous 401, which an
// empty key source also answers. That is the H2 contradiction ADR-0034
// Amendment 7 records.
//
// Mutation: skip the Served pass's GET, so the reading is UNKNOWN and nothing
// is raised. It compiles, and this must fail.
func TestAFirstCreateWithNoKeysIsReportedOnItsServedPass(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "a86first", nil)
	r, stub := createReconciler()
	promote(t, r, a)
	deleteKeySets(t, ns)
	driveToServed(t, r, stub, a)

	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
	mustContain(t, c, "PolicyApplyIncomplete", keySourceFresh, keySourceAbsent)
	mustNotContain(t, c, "PolicyApplyIncomplete", keySourceHeld)
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("phase %s, want Degraded", got)
	}
	if !routePublished(t, a.Namespace, a.Name) {
		t.Error("nothing new gates Create: the route must be published")
	}
}

// (b) Only API-key Agents make the reads, built on the `Served` pass of an
// `auth: none` Create — the one pass where only the apikey gate stops the GET.
//
// Mutation: drop the apikey gate. It compiles, and this must fail.
func TestOnlyAPIKeyAgentsReadTheKeySource(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "a86none")
	r, _ := createReconciler()
	promote(t, r, a)
	rec := &keySourceReader{Reader: readerOf(r)}
	r.Reader = rec
	acceptRoute(t, ns, a.Name)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || auth.Mode != "none" || auth.Transaction != nil {
		t.Fatalf("the none Create did not reach Served on this pass: %+v", auth)
	}
	reconcileOnce(t, r, a)
	if rec.policyGets != 0 || rec.keyLists != 0 {
		t.Errorf("a served auth: none Agent read the key source: %d policy GETs, %d key-set LISTs",
			rec.policyGets, rec.keyLists)
	}
}

// (b′) The watch, manager-driven: deleting the key set, with no reconcile
// called by hand, raises the report on BOTH served API-key Agents in the
// namespace.
//
// The window is short on purpose. Each Agent's card fetch fails here (no
// cluster network) and requeues it after CardRetryInterval, 15 s after its
// last pass; the Agents are left quiet first, so a raise inside the window is
// the watch's and not that requeue's.
//
// Mutation: register no source. It compiles, and this must fail.
func TestTheKeySetWatchRequeuesEveryAgentInTheNamespace(t *testing.T) {
	ns := newNamespace(t)
	r, stub := createReconciler()
	var agents []*assaydv1alpha1.Agent
	for _, name := range []string{"a86watch1", "a86watch2"} {
		a := mustCreateAgent(t, ns, name, nil)
		promote(t, r, a)
		driveToServed(t, r, stub, a)
		acceptRoute(t, ns, name)
		acceptPolicy(t, ns, name)
		agents = append(agents, a)
	}
	reads := startGatewayManager(t, suiteGatewayNamespace)
	for _, a := range agents {
		key := client.ObjectKeyFromObject(a)
		eventually(t, "the manager to reconcile "+a.Name, func() bool { return reads.count(key) > 0 })
	}
	for _, a := range agents {
		waitQuiet(t, reads, client.ObjectKeyFromObject(a))
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	}
	deleteKeySets(t, ns)
	deadline := time.Now().Add(8 * time.Second)
	for _, a := range agents {
		for {
			c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete)
			if c != nil && c.Reason == "ApiKeySourceEmpty" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not raise ApiKeySourceEmpty within the window after its key set was "+
					"deleted; nothing enqueued it", a.Name)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// (b″) The watch holds what it maps by and nothing else. The informer is the
// one the operator REGISTERS — controller.KeySetWatchObject(), which
// gatewaySources passes to its source — taken from a cache built by
// GatewayWatchCacheOptions, and its store is listed. A row that built its own
// PartialObjectMetadata read could not fail whatever the operator registered
// (A87, the review's MAJOR).
//
// Mutations, one per run: drop the transform; register a full ConfigMap
// (KeySetWatchObject returns &corev1.ConfigMap{}); drop the label selector.
// Each compiles, and this must fail.
func TestTheKeySetWatchCacheHoldsNoData(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)
	for _, cm := range []*corev1.ConfigMap{
		{ObjectMeta: metav1.ObjectMeta{Name: "applied", Namespace: ns,
			Labels:      map[string]string{compiler.APIKeySourceLabel: compiler.APIKeySourceValue},
			Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"caller":"x"}}`}},
			Data: map[string]string{"caller": "x"}, BinaryData: map[string][]byte{"bin": []byte("x")}},
		{ObjectMeta: metav1.ObjectMeta{Name: "unlabelled", Namespace: ns}, Data: map[string]string{"k": "v"}},
	} {
		if err := k8s.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	opts, err := controller.GatewayWatchCacheOptions(cache.Options{Scheme: scheme}, ns)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cache.New(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = c.Start(cctx) }()
	inf, err := c.GetInformer(ctx, controller.KeySetWatchObject())
	if err != nil {
		t.Fatalf("the cache has no informer for the object the operator registers: %v", err)
	}
	shared, ok := inf.(toolscache.SharedIndexInformer)
	if !ok {
		t.Fatalf("the informer is a %T, whose store this row cannot list", inf)
	}
	var mine []any
	eventually(t, "the watch to hold the labelled key set", func() bool {
		if !shared.HasSynced() {
			return false
		}
		mine = mine[:0]
		for _, o := range shared.GetStore().List() {
			if m, ok := o.(metav1.Object); ok && m.GetNamespace() == ns {
				mine = append(mine, o)
			}
		}
		for _, o := range mine {
			if o.(metav1.Object).GetName() == "applied" {
				return true
			}
		}
		return false
	})
	for _, o := range mine {
		m := o.(metav1.Object)
		if m.GetName() == "unlabelled" {
			t.Errorf("the watch holds %s, which carries no key-source label: it is not scoped to key sets", m.GetName())
		}
		if len(m.GetAnnotations()) != 0 || len(m.GetManagedFields()) != 0 {
			t.Errorf("the watch holds %s's annotations or managedFields, which can carry its data: %v, %d "+
				"managedFields", m.GetName(), m.GetAnnotations(), len(m.GetManagedFields()))
		}
		if cm, ok := o.(*corev1.ConfigMap); ok && len(cm.Data)+len(cm.BinaryData) > 0 {
			t.Errorf("the watch holds %s's data: it is not metadata-only", cm.Name)
		}
	}
}

// agentListCounter counts LISTs of Agents: the key-set map's only read.
type agentListCounter struct {
	client.Client
	mu    sync.Mutex
	lists int
}

func (c *agentListCounter) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*assaydv1alpha1.AgentList); ok {
		c.mu.Lock()
		c.lists++
		c.mu.Unlock()
	}
	return c.Client.List(ctx, list, opts...)
}

// (b‴) The run-namespace filter, and the namespace match. A labelled
// ConfigMap outside a run namespace reads nothing; one inside maps to the
// Agents of THAT run namespace and to no other's.
//
// Mutations, one per run: drop the filter; drop the run-namespace comparison
// in keySetRequests. Each compiles, and this must fail.
func TestAKeySetOutsideARunNamespaceListsNothing(t *testing.T) {
	ns, other := newNamespace(t), nsName(t, "-other")
	if err := k8s.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: other}}); err != nil {
		t.Fatal(err)
	}
	here := mustCreateAgent(t, ns, "a86maphere", nil)
	mustCreateAgent(t, other, "a86mapthere", nil)
	r, _ := createReconciler()
	counter := &agentListCounter{Client: k8s}
	r.Client = counter
	outside := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "keys", Namespace: ns}}
	if got := r.KeySetRequests(context.Background(), outside); len(got) != 0 || counter.lists != 0 {
		t.Errorf("a key set outside a run namespace mapped to %v and made %d Agent LISTs", got, counter.lists)
	}
	inside := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "keys", Namespace: runNS(ns)}}
	got := r.KeySetRequests(context.Background(), inside)
	if len(got) != 1 || got[0].NamespacedName != client.ObjectKeyFromObject(here) {
		t.Errorf("a key set in %s mapped to %v, want only %s/%s", runNS(ns), got, here.Namespace, here.Name)
	}
}

// (c) An empty key set is the same report, and binaryData counts.
//
// Mutations, one per run: count ConfigMaps instead of entries (the first half
// must fail); count data only (the second must).
func TestAnEmptyKeySetIsReportedAndBinaryDataCounts(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		a, r, _ := healthyServedAPIKeyAgent(t, "a86empty")
		writeKeySetWith(t, a.Namespace, nil, nil)
		reconcileOnce(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
		mustContain(t, c, "PolicyApplyIncomplete", keySourceFresh, "1 ConfigMap(s) carrying that label",
			keySetName, "none of which holds an entry", "adds entries")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	})
	t.Run("binaryData", func(t *testing.T) {
		a, r, _ := healthyServedAPIKeyAgent(t, "a86binary")
		writeKeySetWith(t, a.Namespace, nil, map[string][]byte{"caller": []byte("x")})
		reconcileOnce(t, r, a)
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Errorf("a key held only under binaryData was reported: %+v", c)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	})
}

// (d) A rejected entry is the policy half's report: the operator counts and
// does not parse, and the Gateway names the entry itself.
//
// Mutation: count only entries whose value contains keyHash. It compiles, and
// this must fail.
func TestARejectedEntryIsThePolicyHalfsReport(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86rejected")
	writeKeySetWith(t, a.Namespace, map[string]string{"caller": `{"key":"not-a-hash"}`}, nil)
	partiallyValidPolicy(t, a)
	reconcileOnce(t, r, a)
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
	mustContain(t, c, "PolicyApplyIncomplete", "but not the whole of it")
	for _, cc := range liveAgent(t, a).Status.Conditions {
		if cc.Reason == "ApiKeySourceEmpty" || strings.Contains(cc.Message, "ApiKeySourceEmpty") {
			t.Errorf("%s names ApiKeySourceEmpty for a key set that holds an entry: %s", cc.Type, cc.Message)
		}
	}
}

// (e) Unknown is not absent: a LIST that fails raises nothing, with no claim
// standing and no key set.
//
// Mutation: treat a LIST error as an empty list. It compiles, and this must
// fail.
func TestAFailedKeySourceListRaisesNothing(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86unknown")
	deleteKeySets(t, a.Namespace)
	rec := &keySourceReader{Reader: readerOf(r), failKeyList: true}
	r.Reader = rec
	reconcileOnce(t, r, a)
	if rec.keyLists == 0 {
		t.Fatal("the pass made no key-source LIST, so nothing here measured a failed one")
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("a key-source list that failed was reported: %+v", c)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
}

// (f) Unknown holds, with its own note, and does not grow.
//
// Mutations, one per run: clear the flag on UNKNOWN; append to the stored
// message; reuse A80's heldNote. Each compiles, and this must fail.
func TestAFailedKeySourceListHoldsAStandingClaim(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86hold")
	lostKeys(t, r, a)
	raised := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
	at, observed := raised.LastTransitionTime, raised.ObservedGeneration

	mustEdit(t, a, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = secondImage })
	rec := &keySourceReader{Reader: readerOf(r), failKeyList: true}
	r.Reader = rec
	var lens []int
	for i := 1; i <= 3; i++ {
		reconcileOnce(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
		mustContain(t, c, "PolicyApplyIncomplete", keySourceHeldLead, keySourceHeld+"; the list failed")
		if n := strings.Count(c.Message, keySourceHeld); n != 1 {
			t.Fatalf("pass %d: the key-source marker appears %d times, want 1: %s", i, n, c.Message)
		}
		mustNotContain(t, c, "PolicyApplyIncomplete", heldMark, erroredMark, carriedMark, keySourceFresh)
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
		if !c.LastTransitionTime.Equal(&at) || c.ObservedGeneration != observed {
			t.Errorf("pass %d: a held claim moved its clock (%s → %s) or its generation (%d → %d)",
				i, at, c.LastTransitionTime, observed, c.ObservedGeneration)
		}
		lens = append(lens, len(withoutDigits(c.Message)))
	}
	if live := liveAgent(t, a); live.Generation == observed {
		t.Fatalf("the Agent's generation did not move past %d, so the held generation is unobservable", observed)
	}
	if lens[1] != lens[2] {
		t.Errorf("the held message grew from %d to %d bytes between two passes", lens[1], lens[2])
	}
	if !keySourceClaim(t, a) {
		t.Error("an unknown reading cleared status.auth.keySourceEmpty")
	}
}

// (g) An errored -auth step holds it, as case 19 (l).
//
// Mutation: leave the hold out of holdKeySource. It compiles, and this must
// fail.
func TestAnErroredPassHoldsTheKeySourceClaim(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86errored")
	lostKeys(t, r, a)
	base := readerOf(r)
	r.Reader = staleAgentReader{Reader: base}
	err := reconcileErr(t, r, a)
	r.Reader = base
	if err == nil || !strings.Contains(err.Error(), "older than the live one") {
		t.Fatalf("the injected stale read did not reach the caller: %v", err)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
	mustContain(t, c, "PolicyApplyIncomplete", keySourceHeld+"; this pass's -auth step could not complete",
		"so the key source was not listed")
	mustNotContain(t, c, "PolicyApplyIncomplete", erroredMark, "the list failed")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	if !keySourceClaim(t, a) {
		t.Error("an errored pass cleared status.auth.keySourceEmpty")
	}
}

// (h) It clears on the first pass that finds a key again.
//
// Mutation: never clear on PRESENT. It compiles, and this must fail.
func TestARestoredKeySetClearsTheClaim(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86clears")
	lostKeys(t, r, a)
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	writeKeySet(t, a.Namespace)
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("a restored key set did not clear the claim: %+v", c)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseReady {
		t.Errorf("phase %s after the keys came back, want Ready", got)
	}
	if keySourceClaim(t, a) {
		t.Error("status.auth.keySourceEmpty survived a PRESENT reading")
	}
}

// (i) Precedence: every reason that can stand beside it outranks it, on all
// three conditions.
//
// Mutation: put ApiKeySourceEmpty first in incompleteOrder. It compiles, and
// both halves must fail.
func TestAnEmptyKeySourceRanksLast(t *testing.T) {
	t.Run("a refused route leads", func(t *testing.T) {
		a, r, _ := healthyServedAPIKeyAgent(t, "a86belowroute")
		deleteKeySets(t, a.Namespace)
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", " | ApiKeySourceEmpty: ")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
		condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ServingRouteNotAccepted")
	})
	t.Run("a broken policy tuple leads", func(t *testing.T) {
		a, r, _ := healthyServedAPIKeyAgent(t, "a86belowpolicy")
		deleteKeySets(t, a.Namespace)
		summarisePolicy(t, a)
		reconcileOnce(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
		mustContain(t, c, "PolicyApplyIncomplete", " | ApiKeySourceEmpty: ")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthPolicyNotAttached")
		condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "AuthPolicyNotAttached")
	})
}

// (j) GovernanceSkipped keeps a carried ObservedGeneration: this half writes
// nothing to it, not even a note, which would re-stamp the generation.
//
// Mutation: write a noteGovernance for this half. It compiles, and this must
// fail.
func TestAnEmptyKeySourceLeavesAHeldGovernanceSkippedAlone(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "a86govheld")
	acceptRoute(t, a.Namespace, a.Name)
	unattachPolicy(t, a)
	reconcileOnce(t, r, a)
	g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
	observed := g.ObservedGeneration

	mustEdit(t, a, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = secondImage })
	driftPolicySpec(t, a)
	deleteKeySets(t, a.Namespace)
	reconcileOnce(t, r, a)

	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
	mustContain(t, c, "PolicyApplyIncomplete", " | ApiKeySourceEmpty: ")
	g = condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
	mustContain(t, g, "GovernanceSkipped", heldMark)
	mustNotContain(t, g, "GovernanceSkipped", "ApiKeySourceEmpty")
	if live := liveAgent(t, a); g.ObservedGeneration != observed {
		t.Errorf("GovernanceSkipped was stamped with generation %d; it must keep the carried %d "+
			"(the Agent is at %d)", g.ObservedGeneration, observed, live.Generation)
	}
}

// (k) `Served` re-derives the report: a claim is standing when <agent>-auth is
// deleted, and the missing-policy Lock runs to Served with the key set still
// empty. On the Served pass the report is FRESH, not the held lead.
//
// Mutation: use A80's gate, the slot as the pass found it. It compiles, and
// this must fail.
func TestAServedPassReDerivesTheKeySourceReport(t *testing.T) {
	a, r, stub := healthyServedAPIKeyAgent(t, "a86served")
	recordCard(t, a)
	lostKeys(t, r, a)
	if !keySourceClaim(t, a) {
		t.Fatal("no claim is standing, so the Served pass has nothing to re-derive")
	}
	stub.hold(a.Name, true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("want a Lock of the missing policy: %+v", tx)
	}
	acceptPolicy(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	stub.hold(a.Name, false)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || auth.Transaction != nil || auth.Mode != "apikey" {
		t.Fatalf("the Lock did not reach Served: %+v", auth)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
	mustContain(t, c, "PolicyApplyIncomplete", keySourceFresh)
	mustNotContain(t, c, "PolicyApplyIncomplete", keySourceHeldLead, "no <agent>-auth of this Agent's was found")
	if !keySourceClaim(t, a) {
		t.Error("the Served pass did not record status.auth.keySourceEmpty")
	}
}

// (l) An early return carries it, with the rewritten carriedNote, as case
// 19 (i).
//
// Mutation: leave ApiKeySourceEmpty out of carriedReason. It compiles, and this
// must fail.
func TestAKeySourceReportSurvivesAnEarlyReturn(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86early")
	lostKeys(t, r, a)
	at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)
	orig := r.LabelAuthorityPresent
	r.LabelAuthorityPresent = func(context.Context) (bool, error) { return false, nil }
	reconcileOnce(t, r, a)
	r.LabelAuthorityPresent = orig
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
	mustContain(t, c, "PolicyApplyIncomplete", carriedMark, "or from a live list of the key source")
	sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
	// Ready names the early return's own cause on that pass, which is as real;
	// what must not happen is the key-source claim dropping and its clock
	// resetting.
	if !keySourceClaim(t, a) {
		t.Error("an early return cleared status.auth.keySourceEmpty")
	}
}

// keySourcePruningClient is an Agent CRD that predates status.auth.keySourceEmpty:
// the field is stripped from EVERY status write's response, from the Agent's
// first pass onward — the real `helm upgrade` case, since helm never updates
// a chart's crds/.
type keySourcePruningClient struct{ client.Client }

func (c *keySourcePruningClient) Status() client.SubResourceWriter {
	return &keySourcePruningWriter{SubResourceWriter: c.Client.Status()}
}

type keySourcePruningWriter struct{ client.SubResourceWriter }

func (w *keySourcePruningWriter) Update(ctx context.Context, obj client.Object,
	opts ...client.SubResourceUpdateOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && a.Status.Auth != nil {
		a.Status.Auth.KeySourceEmpty = false
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && a.Status.Auth != nil {
		a.Status.Auth.KeySourceEmpty = false
	}
	return nil
}

// logCapture is a logger whose lines a row can read.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) reconcile(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) {
	t.Helper()
	logger := funcr.New(func(prefix, args string) {
		l.mu.Lock()
		l.lines = append(l.lines, prefix+" "+args)
		l.mu.Unlock()
	}, funcr.Options{})
	ctx := log.IntoContext(context.Background(), logger)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func (l *logCapture) count(part string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, part) {
			n++
		}
	}
	return n
}

func (l *logCapture) any(parts ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(line, p)
		}
		if ok {
			return true
		}
	}
	return false
}

// (m) An old CRD is never refused, and says so.
//
// Mutation: drop the read-back. It compiles, and this must fail.
func TestAnOldCRDIsNeverRefusedForTheKeySourceClaim(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "a86oldcrd", nil)
	r, stub := createReconciler()
	r.Client = &keySourcePruningClient{Client: k8s}
	promote(t, r, a)
	driveToServed(t, r, stub, a)
	acceptRoute(t, ns, a.Name)
	acceptPolicy(t, ns, a.Name)
	reconcileOnce(t, r, a)

	deleteKeySets(t, ns)
	logs := &logCapture{}
	for i := 0; i < 3; i++ {
		logs.reconcile(t, r, a)
		for _, c := range liveAgent(t, a).Status.Conditions {
			if c.Reason == "AuthRecordNotKept" || strings.Contains(c.Message, "AuthRecordNotKept") {
				t.Fatalf("pass %d: an old CRD was refused: %s %s", i, c.Type, c.Message)
			}
		}
		// Every pass re-derives the claim, so K1's report stands on a pruning
		// CRD for as long as its list succeeds.
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	}
	if !logs.any("pruned status.auth.keySourceEmpty", "charts/assayd/crds/") {
		t.Errorf("the pruning was not logged naming charts/assayd/crds/: %v", logs.lines)
	}
	// ONCE at the default level across three passes, each of which wrote the
	// field and got it back false (A87, the review's MINOR 9). Mutation: log
	// every pass at the default level; this must fail.
	if n := logs.count("pruned status.auth.keySourceEmpty"); n != 1 {
		t.Errorf("the pruning was logged %d times in three passes, want once per Agent", n)
	}
}

// (n) An upgrade that changes the render does not silence it: the key-source
// half reads its selector from the live policy under the UID guard ALONE.
//
// Mutation: take the selector from reassertServedPolicy's first return, which
// the digest guard empties. It compiles, and this must fail.
func TestARenderUpgradeDoesNotSilenceTheKeySource(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86upgrade")
	live := liveAgent(t, a)
	live.Status.Auth.AppliedDigest = "sha256:" + strings.Repeat("0", 64)
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("record a digest this build does not render: %v", err)
	}
	lostKeys(t, r, a)
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
}

// A key set whose policy is NOT this Agent's reads unknown: a foreign policy
// at the -auth name gives no selector, and the claim is held under the
// "no <agent>-auth of this Agent's" note rather than re-derived from a policy
// the operator did not write.
func TestAForeignPolicyGivesNoSelector(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86foreign")
	lostKeys(t, r, a)
	p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	labels := p.GetLabels()
	labels[controller.LabelAgentUID] = "someone-else"
	p.SetLabels(labels)
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatalf("take the -auth name over: %v", err)
	}
	reconcileOnce(t, r, a)
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
	mustContain(t, c, "PolicyApplyIncomplete", keySourceHeld+"; no <agent>-auth of this Agent's was found")
	if !keySourceClaim(t, a) {
		t.Error("an unknown reading cleared the claim")
	}
}

// keySourceKeptByTheCRD asks the API server, by a dry-run status write,
// whether it would store status.auth.keySourceEmpty on this Agent.
func keySourceKeptByTheCRD(t *testing.T, a *assaydv1alpha1.Agent) bool {
	t.Helper()
	probe := liveAgentPtr(t, a)
	if probe.Status.Auth == nil {
		t.Fatal("the Agent records no status.auth to probe")
	}
	probe.Status.Auth.KeySourceEmpty = true
	if err := k8s.Status().Update(context.Background(), probe, client.DryRunAll); err != nil {
		return false
	}
	return probe.Status.Auth != nil && probe.Status.Auth.KeySourceEmpty
}

// (m), on a REAL Agent CRD that predates the field rather than a client that
// strips it: the read-back is the response the API server decodes, so this is
// the row that shows the log fires where it matters — a response whose
// status.auth omits the field decodes it false, whatever the pass wrote.
func TestAnInstalledCRDWithoutTheFieldIsLoggedNotRefused(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "a86stalecrd", nil)
	r, stub := createReconciler()
	promote(t, r, a)
	driveToServed(t, r, stub, a)
	acceptRoute(t, ns, a.Name)
	acceptPolicy(t, ns, a.Name)
	reconcileOnce(t, r, a)

	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &installed); err != nil {
		t.Fatalf("read the Agent CRD: %v", err)
	}
	current := installed.Spec.DeepCopy()
	stale := current.DeepCopy()
	for i := range stale.Versions {
		root := stale.Versions[i].Schema.OpenAPIV3Schema
		st := root.Properties["status"]
		auth := st.Properties["auth"]
		delete(auth.Properties, "keySourceEmpty")
		st.Properties["auth"] = auth
		root.Properties["status"] = st
	}
	write := func(spec *apiextensionsv1.CustomResourceDefinitionSpec) {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var c apiextensionsv1.CustomResourceDefinition
			if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &c); err != nil {
				return err
			}
			c.Spec = *spec.DeepCopy()
			return k8s.Update(ctx, &c)
		}); err != nil {
			t.Fatalf("install the Agent CRD: %v", err)
		}
	}
	t.Cleanup(func() {
		write(current)
		eventually(t, "the release's Agent CRD to keep the field again", func() bool {
			return keySourceKeptByTheCRD(t, a)
		})
	})
	write(stale)
	eventually(t, "the stale Agent CRD to prune the field", func() bool { return !keySourceKeptByTheCRD(t, a) })

	deleteKeySets(t, ns)
	logs := &logCapture{}
	for i := 0; i < 2; i++ {
		logs.reconcile(t, r, a)
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ApiKeySourceEmpty")
	}
	if !logs.any("pruned status.auth.keySourceEmpty", "charts/assayd/crds/") {
		t.Errorf("a real pruning CRD was not logged: %v", logs.lines)
	}
}

// A transaction in the slot as the pass leaves it is not judged: a pass that
// enters or holds a missing-policy Lock makes no key-source read, and the flag
// stays as it stood (A86's gate; A87, the review's MINOR 2).
//
// Mutation: drop `auth.Transaction == nil` from judgesKeySource. It compiles,
// and this must fail.
func TestALockInTheSlotDoesNotReadTheKeySource(t *testing.T) {
	a, r, stub := healthyServedAPIKeyAgent(t, "a86lockslot")
	recordCard(t, a)
	stub.hold(a.Name, true)
	deletePolicy(t, a)
	rec := &keySourceReader{Reader: readerOf(r)}
	r.Reader = rec
	reconcileOnce(t, r, a)
	acceptPolicy(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("want a Lock of the missing policy still in the slot: %+v", tx)
	}
	if rec.keyLists != 0 || rec.policyGets != 0 {
		t.Errorf("a pass with a Lock in the slot read the key source: %d key-set LISTs, %d policy GETs",
			rec.keyLists, rec.policyGets)
	}
}

// The Served pass's GET applies §3.2's UID guard: a policy at the -auth name
// that is not this Agent's gives no selector, so the reading is UNKNOWN and
// nothing is raised, even with no key set (A87, the review's MINOR 3). A later
// steady pass, which reads this Agent's own policy, raises.
//
// Mutation: drop the UID comparison in liveAuthPolicy. It compiles, and this
// must fail.
func TestTheServedPassReadsOnlyThisAgentsPolicy(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "a86uidguard", nil)
	r, stub := createReconciler()
	promote(t, r, a)
	deleteKeySets(t, ns)
	rec := &keySourceReader{Reader: readerOf(r), foreignPolicy: true}
	r.Reader = rec
	driveToServed(t, r, stub, a)
	if rec.policyGets == 0 {
		t.Fatal("the Served pass made no policy GET, so nothing here measured the guard")
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil &&
		(c.Reason == "ApiKeySourceEmpty" || strings.Contains(c.Message, "ApiKeySourceEmpty")) {
		t.Errorf("a selector was read from a policy that is not this Agent's: %s", c.Message)
	}
	if keySourceClaim(t, a) {
		t.Error("a reading from another Agent's policy set status.auth.keySourceEmpty")
	}
	// A steady pass reads this Agent's own policy through
	// reassertServedPolicy and raises: the silence above was the guard's.
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ApiKeySourceEmpty")
}

// A87's reading 2: on a digest match reassertServedPolicy's second return is
// the policy as THIS pass wrote it, so an out-of-band selector is repaired and
// read in one pass. Read from the object as found, the drifted selector would
// match no key set and report a false empty (the review's MINOR 5).
//
// Mutation: return `existing` rather than `written` as the second return. It
// compiles, and this must fail.
func TestARepairedSelectorIsReadAsRepaired(t *testing.T) {
	a, r, _ := healthyServedAPIKeyAgent(t, "a86repaired")
	p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	if err := unstructured.SetNestedStringMap(p.Object, map[string]string{compiler.APIKeySourceLabel: "elsewhere"},
		"spec", "traffic", "apiKeyAuthentication", "configMapSelector", "matchLabels"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatalf("drift the selector: %v", err)
	}
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("the drifted selector was read before the pass repaired it: %+v", c)
	}
	sel, _, _ := unstructured.NestedStringMap(policyExists(t, runNS(a.Namespace), policyNameOf(a)).Object,
		"spec", "traffic", "apiKeyAuthentication", "configMapSelector", "matchLabels")
	if sel[compiler.APIKeySourceLabel] != compiler.APIKeySourceValue {
		t.Fatalf("the pass did not repair the selector (%v), so this row measured nothing", sel)
	}
}

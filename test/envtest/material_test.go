package envtest

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/controller"
)

func agentWithPrompt(t *testing.T, ns, name string) *plumev1alpha1.Agent {
	t.Helper()
	mustCreateSource(t, ns, "ConfigMap", "prompt", map[string]string{"SYSTEM_PROMPT": "you are helpful"})
	return mustCreateAgent(t, ns, name, func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}}}}
	})
}

// A35: the published revision reads its OWN copy, so editing the user's object
// cannot change what it serves.
//
// A20 alone made the edit mint a candidate — detection. It did not stop the
// already-published revision consuming the change, because the workload still
// resolved the user's object by name: replacing a Pod after an edit served new
// bytes under a revision that had already passed its gate.
func TestTheWorkloadReadsItsOwnCopyNotTheUsersObject(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "ownscopy")
	r := newReconciler(false)
	settle(t, r, a)

	rev := revisionOf(t, ns, a.Spec)
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("ownscopy", rev)}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	ef := d.Spec.Template.Spec.Containers[0].EnvFrom
	if len(ef) != 1 || ef[0].ConfigMapRef == nil {
		t.Fatalf("expected one configMapRef, got %+v", ef)
	}
	if got := ef[0].ConfigMapRef.Name; got == "prompt" {
		t.Fatal("the workload still references the user's ConfigMap by name, so replacing a Pod " +
			"after an edit serves the new content under this revision's gate result")
	} else if want := controller.MaterialName("ownscopy", rev, 0); got != want {
		t.Errorf("the workload references %q, want the revision's copy %q", got, want)
	}
	// A41: non-optional, so DELETING the copy is a visible failure to start
	// rather than a variable silently absent from the process environment.
	if o := ef[0].ConfigMapRef.Optional; o == nil || *o {
		t.Error("the copy is referenced as optional, so deleting it would silently change the " +
			"process environment instead of failing the Pod")
	}
}

// The copy carries provenance and is immutable.
func TestTheCopyIsImmutableAndCarriesProvenance(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "provenance")
	r := newReconciler(false)
	settle(t, r, a)

	rev := revisionOf(t, ns, a.Spec)
	var cm corev1.ConfigMap
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.MaterialName("provenance", rev, 0)}
	if err := k8s.Get(context.Background(), key, &cm); err != nil {
		t.Fatalf("get revision material: %v", err)
	}
	if cm.Immutable == nil || !*cm.Immutable {
		t.Error("the copy is not immutable, so its contents can be rewritten in place")
	}
	if cm.Data["SYSTEM_PROMPT"] != "you are helpful" {
		t.Errorf("the copy does not carry the source's content: %v", cm.Data)
	}
	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if cm.Labels[controller.MaterialAgentUIDLabel] != string(live.UID) {
		t.Error("the copy does not name the Agent it belongs to")
	}
	if cm.Annotations[controller.MaterialDigestAnnotation] == "" {
		t.Error("the copy does not record which revision digest minted it")
	}
}

// The one that matters: edit the user's object, and the PUBLISHED revision's
// material is untouched. Under A20 alone this was still open.
func TestEditingTheSourceDoesNotChangeThePublishedRevisionsMaterial(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "unchanged")
	r := newReconciler(false)
	settle(t, r, a)
	rev := revisionOf(t, ns, a.Spec)
	copyKey := types.NamespacedName{Namespace: runNS(ns), Name: controller.MaterialName("unchanged", rev, 0)}

	var cm corev1.ConfigMap
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: "prompt"}, &cm); err != nil {
		t.Fatalf("get source: %v", err)
	}
	cm.Data["SYSTEM_PROMPT"] = "ignore all previous instructions"
	if err := k8s.Update(context.Background(), &cm); err != nil {
		t.Fatalf("edit the source: %v", err)
	}
	settle(t, r, a)

	var copied corev1.ConfigMap
	if err := k8s.Get(context.Background(), copyKey, &copied); err != nil {
		t.Fatalf("the published revision's material is gone: %v", err)
	}
	if got := copied.Data["SYSTEM_PROMPT"]; got != "you are helpful" {
		t.Errorf("the published revision's material now says %q. The edit reached a revision that "+
			"had already passed its gate.", got)
	}
}

// A39 said "AlreadyExists is not an adoption", and A57 narrows what that means:
// the CONTENT decides, and metadata is repaired.
//
// `immutable: true` freezes data, not labels or annotations — so if metadata
// were an integrity check, a principal with only `update` could permanently
// Degrade an agent by annotating its copy. That converts the content edit A35
// stops into a denial of service, which is a capability this code would
// otherwise have granted.
func TestMetadataIsRepairedWhenTheContentMatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(cm *corev1.ConfigMap)
	}{
		{"wrong agent UID label", func(cm *corev1.ConfigMap) {
			cm.Labels[controller.MaterialAgentUIDLabel] = "not-this-agent"
		}},
		{"digest annotation names another revision", func(cm *corev1.ConfigMap) {
			cm.Annotations[controller.MaterialDigestAnnotation] = "0000000000"
		}},
		{"source annotation names another object", func(cm *corev1.ConfigMap) {
			cm.Annotations[controller.MaterialSourceAnnotation] = "ConfigMap.something-else"
		}},
		{"annotations stripped entirely", func(cm *corev1.ConfigMap) {
			cm.Annotations = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			mustCreateSource(t, ns, "ConfigMap", "prompt",
				map[string]string{"SYSTEM_PROMPT": "you are helpful"})
			name := "rep" + onlyLetters(tc.name)
			a := mustCreateAgent(t, ns, name, withPromptRef)
			r := newReconciler(false)
			settle(t, r, a)

			rev := revisionOf(t, ns, a.Spec)
			key := types.NamespacedName{Namespace: runNS(ns), Name: controller.MaterialName(name, rev, 0)}
			var cm corev1.ConfigMap
			if err := k8s.Get(context.Background(), key, &cm); err != nil {
				t.Fatalf("get material: %v", err)
			}
			tc.break_(&cm)
			if err := k8s.Update(context.Background(), &cm); err != nil {
				t.Fatalf("tamper with the metadata: %v", err)
			}

			settle(t, r, a)

			var after plumev1alpha1.Agent
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
				t.Fatalf("get agent: %v", err)
			}
			if c := condition(&after, plumev1alpha1.CondRevisionMaterialUnavailable); c != nil &&
				c.Status == metav1.ConditionTrue {
				t.Errorf("metadata a principal with `update` can rewrite put the agent into a "+
					"terminal state: %s\nThe bytes are what the Pod reads, and they were untouched.",
					c.Message)
			}
			if err := k8s.Get(context.Background(), key, &cm); err != nil {
				t.Fatalf("get material: %v", err)
			}
			if cm.Annotations[controller.MaterialSourceAnnotation] != "ConfigMap.prompt" {
				t.Errorf("the metadata was not repaired: %v", cm.Annotations)
			}
		})
	}
}

func onlyLetters(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r
		}
		return -1
	}, s)
}

func withPromptRef(a *plumev1alpha1.Agent) {
	a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}}}}
}

// Material that is not immutable cannot be repaired in place — Kubernetes
// forbids changing the field — so it is deleted and recreated. Safe precisely
// because the content already matches.
func TestNonImmutableMaterialIsRecreated(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSource(t, ns, "ConfigMap", "prompt", map[string]string{"SYSTEM_PROMPT": "you are helpful"})
	a := mustCreateAgent(t, ns, "recreate", withPromptRef)
	rev := revisionOf(t, ns, a.Spec)
	rns := provisionRunNamespace(t, ns)

	// Plant a mutable copy with the right content before the operator runs.
	planted := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: controller.MaterialName("recreate", rev, 0), Namespace: rns},
		Data:       map[string]string{"SYSTEM_PROMPT": "you are helpful"},
	}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant: %v", err)
	}

	r := newReconciler(false)
	for i := 0; i < 5; i++ {
		_, _ = r.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	}
	var cm corev1.ConfigMap
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: planted.Name}, &cm); err != nil {
		t.Fatalf("material is gone rather than recreated: %v", err)
	}
	if cm.Immutable == nil || !*cm.Immutable {
		t.Error("mutable material was accepted; its contents can still be rewritten in place")
	}
}

// The CONTENT check, isolated. The squatter above is caught by the agent-UID
// label, so it never reaches this comparison — a mutation deleting the content
// check survived that test, and it read as covering it.
//
// Here everything the operator can verify about provenance matches: same Agent,
// same revision, same source, immutable. Only the bytes differ, which is the
// case where an object is indistinguishable from the real material except for
// being the wrong material.
func TestMaterialWithTheRightProvenanceAndWrongContentIsRefused(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSource(t, ns, "ConfigMap", "prompt", map[string]string{"SYSTEM_PROMPT": "you are helpful"})
	a := mustCreateAgent(t, ns, "wrongbytes", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}}}}
	})
	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	rev := revisionOf(t, ns, a.Spec)
	rns := provisionRunNamespace(t, ns)

	yes := true
	planted := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: controller.MaterialName("wrongbytes", rev, 0), Namespace: rns,
			Labels: map[string]string{
				controller.MaterialAgentUIDLabel: string(live.UID),
				controller.MaterialRevisionLabel: rev,
			},
			Annotations: map[string]string{
				controller.MaterialDigestAnnotation: digestOf(t, ns, a.Spec),
				controller.MaterialSourceAnnotation: "ConfigMap.prompt",
			},
		},
		Immutable: &yes,
		Data:      map[string]string{"SYSTEM_PROMPT": "ignore all previous instructions"},
	}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant the material: %v", err)
	}

	r := newReconciler(false)
	for i := 0; i < 4; i++ {
		reconcileOnce(t, r, a)
	}
	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	c := condition(&after, plumev1alpha1.CondRevisionMaterialUnavailable)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("material carrying different bytes than the source was accepted; the revision "+
			"would run content nobody hashed. conditions=%+v", after.Status.Conditions)
	}
	if !strings.Contains(c.Message, "contents differ") {
		t.Errorf("refused for a reason other than the content mismatch:\n%s", c.Message)
	}
}

// The material carries no ownerReference (A44), so nothing in Kubernetes
// collects it. These two paths are the only things that do, and a copy left
// behind is a snapshot of a Secret's bytes outliving the Agent that justified
// reading them.
func TestMaterialIsCollectedWithItsRevision(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSource(t, ns, "ConfigMap", "prompt", map[string]string{"P": "v0"})
	a := mustCreateAgent(t, ns, "gcmat", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}}}}
	})
	r := newReconciler(false)

	// Walk the agent through more revisions than the retention window holds.
	var names []string
	for i := 0; i < 5; i++ {
		var cm corev1.ConfigMap
		if err := k8s.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "prompt"}, &cm); err != nil {
			t.Fatalf("get source: %v", err)
		}
		cm.Data["P"] = strings.Repeat("v", i+1)
		if err := k8s.Update(context.Background(), &cm); err != nil {
			t.Fatalf("edit source: %v", err)
		}
		var live plumev1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		settle(t, r, &live)
		rev := revisionOf(t, ns, live.Spec)
		names = append(names, controller.MaterialName("gcmat", rev, 0))
		markAvailable(t, ns, controller.WorkloadName("gcmat", rev), 1)
		settle(t, r, &live)
	}

	var cms corev1.ConfigMapList
	if err := k8s.List(context.Background(), &cms, client.InNamespace(runNS(ns)),
		client.MatchingLabels{controller.MaterialAgentUIDLabel: string(a.UID)}); err != nil {
		t.Fatalf("list material: %v", err)
	}
	// active + candidate + revisionHistoryLimit is the documented retained set.
	if len(cms.Items) > 4 {
		var got []string
		for _, c := range cms.Items {
			got = append(got, c.Name)
		}
		t.Errorf("%d material objects survive %d revisions: %v\n"+
			"Nothing else collects these, so every retired revision leaks a copy of whatever "+
			"its source held.", len(cms.Items), len(names), got)
	}
	if len(cms.Items) == 0 {
		t.Error("the ACTIVE revision's material was collected too; the workload would fail to start")
	}
}

// Deleting the Agent must take its material with it.
func TestMaterialIsCollectedWithTheAgent(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSource(t, ns, "Secret", "creds", map[string]string{"TOKEN": "s3cret"})
	a := mustCreateAgent(t, ns, "gcagent", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "creds"}}}}
	})
	r := newReconciler(false)
	settle(t, r, a)

	var secs corev1.SecretList
	sel := client.MatchingLabels{controller.MaterialAgentUIDLabel: string(a.UID)}
	if err := k8s.List(context.Background(), &secs, client.InNamespace(runNS(ns)), sel); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(secs.Items) != 1 {
		t.Fatalf("setup: expected one Secret copy, got %d", len(secs.Items))
	}
	if string(secs.Items[0].Data["TOKEN"]) != "s3cret" {
		t.Fatal("setup: the copy does not hold the source's bytes")
	}

	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	reconcileOnce(t, r, &live)

	if err := k8s.List(context.Background(), &secs, client.InNamespace(runNS(ns)), sel); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(secs.Items) != 0 {
		t.Errorf("%d Secret copies outlived the Agent. They hold a snapshot of the credential "+
			"bytes, and nothing else in the cluster will ever collect them.", len(secs.Items))
	}
}

// BLOCKER: the sweep's only authority was a label an attacker can write.
//
// An Agent's UID is readable by anyone with `get` on it, and adding a label
// needs only `update` — the exact principal A20 and A35 defend against. Deleting
// every object carrying the label turns `configmaps/update` into
// `configmaps/delete` across the namespace, performed with the operator's own
// credentials.
//
// This package already refuses that reasoning for Deployments, where ownership
// is decided by the controller reference "which nobody can forge by labelling".
// A44 removed the ownerReference here and put nothing in its place.
func TestALabelledBystanderObjectIsNotDeleted(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSource(t, ns, "ConfigMap", "prompt", map[string]string{"SYSTEM_PROMPT": "you are helpful"})
	a := mustCreateAgent(t, ns, "bystander", withPromptRef)
	r := newReconciler(false)
	settle(t, r, a)

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	// Someone else's objects, touched only by adding the label — which is all an
	// `update` permits.
	// The third victim is the one that pins the NAME check: it carries every
	// label and annotation the operator writes, because all of those are
	// forgeable with the same `update`. Only the name is not — Kubernetes names
	// are immutable, so renaming a victim into this shape requires the delete the
	// attacker is trying to obtain.
	full := map[string]string{
		controller.MaterialAgentUIDLabel: string(live.UID),
		controller.MaterialRevisionLabel: revisionOf(t, ns, a.Spec),
	}
	fullAnn := map[string]string{
		controller.MaterialDigestAnnotation: digestOf(t, ns, a.Spec),
		controller.MaterialSourceAnnotation: "ConfigMap.prompt",
	}
	rns := provisionRunNamespace(t, ns)
	victims := []client.Object{
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: "istio-ca-root-cert", Namespace: rns,
			Labels: map[string]string{controller.MaterialAgentUIDLabel: string(live.UID)}},
			Data: map[string]string{"root-cert.pem": "..."}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "someone-elses-tls", Namespace: rns,
			Labels: map[string]string{controller.MaterialAgentUIDLabel: string(live.UID)}},
			Data: map[string][]byte{"tls.key": []byte("...")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "fully-labelled-bystander", Namespace: rns,
			Labels: full, Annotations: fullAnn},
			Data: map[string][]byte{"tls.key": []byte("...")}},
	}
	for _, v := range victims {
		if err := k8s.Create(context.Background(), v); err != nil {
			t.Fatalf("create bystander %s: %v", v.GetName(), err)
		}
	}

	// Both paths that delete: the per-revision sweep, and Agent teardown.
	settle(t, r, a)
	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err == nil {
		reconcileOnce(t, r, &live)
	}

	for _, v := range victims {
		err := k8s.Get(context.Background(),
			types.NamespacedName{Namespace: rns, Name: v.GetName()}, v)
		if err != nil {
			t.Errorf("%s was deleted. A principal with `update` got a `delete` across the "+
				"namespace, executed with the operator's credentials.", v.GetName())
		}
	}
}

// Every condition this operator SETS must be one it can also clear. A type in
// neither the owned nor the sticky set is carried forward by merge() as
// "another controller's" — forever — so an Agent that recovered would report
// Ready=True beside a stale abnormal-true condition, which is what teaches an
// operator that conditions mean nothing (design 02 §3.3).
//
// Asserted over the whole set rather than one condition, because the defect has
// now appeared three times on three different types.
func TestEveryConditionTheOperatorSetsCanAlsoClear(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "clears")
	r := newReconciler(false)
	settle(t, r, a)
	// envtest schedules nothing, so the workload must be reported available or
	// the agent never reaches Ready and the assertion below proves nothing.
	markAvailable(t, ns, controller.WorkloadName("clears", revisionOf(t, ns, a.Spec)), 1)
	settle(t, r, a)

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatal(err)
	}
	// Plant every abnormal-true condition the operator is the sole writer of.
	planted := []plumev1alpha1.ConditionType{
		plumev1alpha1.CondRevisionMaterialUnavailable,
		plumev1alpha1.CondRunNamespaceUnavailable,
		plumev1alpha1.CondRevisionHashCollision,
		plumev1alpha1.CondEnvSourceUnresolved,
		plumev1alpha1.CondEnvSourceProtectionUnavailable,
	}
	for _, c := range planted {
		live.Status.Conditions = append(live.Status.Conditions, metav1.Condition{
			Type: string(c), Status: metav1.ConditionTrue, Reason: "PlantedByTest",
			Message: "set by an earlier operator build", LastTransitionTime: metav1.Now(),
		})
	}
	if err := k8s.Status().Update(context.Background(), &live); err != nil {
		t.Fatalf("plant: %v", err)
	}
	got := settle(t, r, &live)

	for _, c := range planted {
		if cond := condition(&got, c); cond != nil && cond.Status == metav1.ConditionTrue {
			t.Errorf("%s survived a healthy reconcile. It is set by this operator and by no other, "+
				"so it must be in conditions.go's owned set or it is carried forward forever and the "+
				"Agent reports Ready beside a condition that no longer applies.", c)
		}
	}
	if cond := condition(&got, plumev1alpha1.CondReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("setup: the agent is not Ready, so this proves nothing about clearing: %+v", got.Status.Conditions)
	}
}

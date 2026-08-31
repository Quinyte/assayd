package envtest

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/controller"
	"github.com/Quinyte/plume/internal/revision"
)

// hideOnce makes one Get of one Deployment report NotFound, which is the only
// way to drive the operator down its Create path against an object that already
// exists — the GET/Create race Codex r8 BLOCKER 2 describes. A real cluster
// produces it whenever anything creates the workload name between the two
// calls; the name is derived from the Agent name and a public digest, so it is
// guessable by anyone with deployments/create.
type hideOnce struct {
	client.Client
	hide types.NamespacedName
	done bool
}

func (c *hideOnce) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if !c.done && key == c.hide {
		if _, ok := obj.(*appsv1.Deployment); ok {
			c.done = true
			return apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, key.Name)
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// AlreadyExists was treated as success, so the operator accepted an object it
// had never validated — and the availability check then promoted it on
// AvailableReplicas alone.
func TestAnAlreadyExistsRaceDoesNotAcceptAnUnvalidatedWorkload(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "race", nil)
	name := controller.WorkloadName("race", revision.Hash(a.Spec))

	// Someone else's Deployment is already sitting on the name, Available, with
	// no stamp from this operator.
	squatter := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"squat": "yes"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"squat": "yes"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{
					{Name: "agent", Image: "ghcr.io/attacker/backdoor:1.0.0"}}},
			},
		},
	}
	if err := k8s.Create(context.Background(), squatter); err != nil {
		t.Fatalf("create squatting workload: %v", err)
	}
	markAvailable(t, ns, name, 1)

	r := &controller.AgentReconciler{
		Client:             &hideOnce{Client: k8s, hide: types.NamespacedName{Namespace: ns, Name: name}},
		Scheme:             scheme,
		EvalSuiteInstalled: func() bool { return false },
	}
	reconcileOnce(t, r, a) // Get says NotFound -> Create -> AlreadyExists
	reconcileOnce(t, r, a)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if after.Status.ActiveRevision != "" {
		t.Errorf("an object this operator never created or validated was promoted to active "+
			"revision %q, on the strength of its own status", after.Status.ActiveRevision)
	}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if img := d.Spec.Template.Spec.Containers[0].Image; img != "ghcr.io/attacker/backdoor:1.0.0" {
		return // converged over it; acceptable, the squatter did not win
	}
	if c := condition(&after, plumev1alpha1.CondRevisionHashCollision); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Error("the squatting workload was neither converged nor reported; the operator accepted " +
			"AlreadyExists as success and moved on")
	}
}

// The ACTIVE revision's workload is a different object from the desired one and
// is never validated by ensureWorkload in the same pass. Availability of an
// unstamped object must not make the operator report it as serving.
func TestAnUnstampedActiveWorkloadIsNotReportedAsServing(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unstampedactive", nil)
	r := newReconciler(false)
	settle(t, r, a)
	active := controller.WorkloadName("unstampedactive", revision.Hash(a.Spec))
	markAvailable(t, ns, active, 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision == "" {
		t.Fatal("setup: nothing became active")
	}

	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: active}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	delete(d.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("strip annotation: %v", err)
	}
	markAvailable(t, ns, active, 1)

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec.Runtime.Image = "ghcr.io/acme/agent:5.0.0" // a genuinely different revision
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update: %v", err)
	}
	reconcileOnce(t, r, &live)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, plumev1alpha1.CondReady); c != nil && c.Status == metav1.ConditionTrue &&
		c.Reason == "Available" {
		t.Errorf("Ready=True/Available claims the active revision is serving, but its workload "+
			"carries no stamp from this operator — availability alone was the evidence. %s", c.Message)
	}
}

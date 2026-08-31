package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// NFR-8 applied to a degradation this operator HAS. Design 02 §3.3 says the
// revision identity covers env-source CONTENT; internal/revision hashes the
// referent. A43 wrote that down in the design, and an independent critique
// pointed out that a design paragraph is not the mechanism NFR-8 asks for: an
// Agent with envFrom reported Ready=True with nothing said.
func TestAnAgentWithAnEnvSourceSaysItsContentIsNotGated(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "envsrc", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}}}}
		a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "KEY", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "creds"}, Key: "k"}}}}
	})
	got := settle(t, newReconciler(false), a)

	c := condition(&got, plumev1alpha1.CondEnvSourceProtectionUnavailable)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("an Agent whose behaviour comes from a ConfigMap nobody gates reported nothing. " +
			"Anyone with update on that object can change what this agent does and have it serve " +
			"under the gate result the old content earned.")
	}
	// The message must name the objects, or an operator cannot act on it: the
	// remedy is restricting update on specific ConfigMaps and Secrets.
	for _, want := range []string{"prompt", "creds", "A43"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("the condition message does not name %q, so it says a guarantee is missing "+
				"without saying which objects to protect:\n%s", want, c.Message)
		}
	}
}

// The other half: an Agent that references nothing is not exposed, and an
// abnormal-true condition on it is the noise operators learn to filter — after
// which they miss the one that matters.
func TestAnAgentWithNoEnvSourceStaysQuiet(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "noenvsrc", nil)
	got := settle(t, newReconciler(false), a)
	if c := condition(&got, plumev1alpha1.CondEnvSourceProtectionUnavailable); c != nil &&
		c.Status == metav1.ConditionTrue {
		t.Error("an Agent with no env source was told its env sources are ungated")
	}
}

// And it must CLEAR: a spec that drops its last env source is no longer exposed,
// and a stale abnormal-true condition is a lie NFR-8 does not license either.
func TestRemovingTheLastEnvSourceClearsTheCondition(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "clears", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}}}}
	})
	r := newReconciler(false)
	settle(t, r, a)

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec.Runtime.EnvFrom = nil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := settle(t, r, &live)
	if c := condition(&got, plumev1alpha1.CondEnvSourceProtectionUnavailable); c != nil &&
		c.Status == metav1.ConditionTrue {
		t.Error("the condition survived removal of the last env source; an abnormal-true condition " +
			"that no longer applies is exactly what teaches operators to ignore it")
	}
}

// A condition message is capped at 32768 bytes and the API server rejects the
// WHOLE status write past it — so an Agent with enough env sources got no status
// at all: no Ready, no Degraded, nothing, in an error loop. The condition added
// to satisfy NFR-8 would have been the thing that silenced the object.
//
// 150 sources with long-but-legal names was enough. Nothing caps envFrom length
// or ConfigMap name length, so this is a spec a user can write.
func TestTheEnvSourceMessageCannotBrickTheStatus(t *testing.T) {
	ns := newNamespace(t)
	long := strings.Repeat("n", 200)
	a := mustCreateAgent(t, ns, "bigmsg", func(a *plumev1alpha1.Agent) {
		for i := 0; i < 150; i++ {
			a.Spec.Runtime.EnvFrom = append(a.Spec.Runtime.EnvFrom, corev1.EnvFromSource{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: fmt.Sprintf("%s-%d", long, i)}}})
		}
	})
	got := settle(t, newReconciler(false), a)

	if got.Status.Phase == "" || len(got.Status.Conditions) == 0 {
		t.Fatal("the Agent has no status at all: the status write was rejected, so the object is " +
			"silent about everything — which is a worse NFR-8 outcome than the one this condition " +
			"was added to fix")
	}
	c := condition(&got, plumev1alpha1.CondEnvSourceProtectionUnavailable)
	if c == nil {
		t.Fatal("no env-source condition on an Agent with 150 env sources")
	}
	if len(c.Message) > 32768 {
		t.Errorf("message is %d bytes; the API server rejects the status write past 32768", len(c.Message))
	}
	// The COUNT stays exact even though the list is truncated: an operator needs
	// to know the scale, and "and N more" is the part that tells them.
	if !strings.Contains(c.Message, "150 env source") {
		t.Errorf("the message no longer states how many sources are affected:\n%s", c.Message)
	}
	if !strings.Contains(c.Message, "more") {
		t.Errorf("the message was truncated without saying so:\n%s", c.Message)
	}
}

// An arm this switch does not name still counts. corev1.EnvVarSource gains arms
// between releases — fileKeyRef is the most recent — and the projection already
// classifies all four of its leaves as behaviour, so an unreported arm is a
// disclosure gap on material that IS gated.
func TestAnUnnamedEnvSourceArmIsStillReported(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "filekey", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "K", ValueFrom: &corev1.EnvVarSource{
			FileKeyRef: &corev1.FileKeySelector{VolumeName: "v", Path: "p.env", Key: "K"}}}}
	})
	// fileKeyRef also needs a volume this operator does not render yet, so the
	// API server rejects the Deployment. That must not be silent either: the
	// first version returned a bare error, wrote no status, and left the Agent at
	// an empty phase forever with the reason only in operator logs.
	r := newReconciler(false)
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}
	var got plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&got, plumev1alpha1.CondEnvSourceProtectionUnavailable); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Error("an env source the switch does not name by arm was not reported; a named-arm " +
			"enumeration that skips the rest is the defect internal/revision just removed")
	}
	if c := condition(&got, plumev1alpha1.CondReady); c == nil || c.Reason != "WorkloadRejected" {
		t.Errorf("the API server rejected the rendered workload and the Agent says %+v; "+
			"a rejected render is a spec the user can fix, and they cannot fix what is only "+
			"in the operator's log", c)
	}
}

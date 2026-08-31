package envtest

import (
	"context"
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

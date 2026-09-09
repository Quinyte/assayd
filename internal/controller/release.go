package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/revision"
)

// releasePin is a resolved rollback request: which retained revision the user
// asked to serve, and the workload that proves it is still there.
type releasePin struct {
	// Digest is the full SHA-256 the user pinned. Never the 40-bit name.
	Digest string
	// Revision is that revision's 40-bit name, read off the retained workload
	// rather than recomputed — under source drift, recomputing is precisely the
	// operation that would produce a different answer.
	Revision string
}

// unresolvablePinError is terminal for this reconcile and reaches the object as
// a condition. A pin that cannot be resolved must NOT fall through to the
// ordinary desired spec: the user asked to serve R1 and silently serving R2
// instead is the opposite of a rollback, at the moment someone is most likely
// to be recovering from an incident.
type unresolvablePinError struct {
	digest string
	reason string
}

func (e *unresolvablePinError) Error() string {
	return fmt.Sprintf("spec.release.targetRevisionDigest %s cannot be served: %s. Nothing is "+
		"rolled back and the current release is untouched. Clear the field to resume the "+
		"ordinary desired spec.", e.digest, e.reason)
}

// resolveReleasePin answers "which revision does this Agent serve" when the
// user has pinned one.
//
// **It resolves against what is retained, never against current spec.** That is
// the whole reason the field exists. Design 02 promised instant rollback and
// retention delivers the material — but reapplying the old YAML does not reach
// it, because env-source CONTENT is part of the revision identity: once a
// referenced ConfigMap has drifted, the same spec computes a NEW digest and
// mints a new revision instead of returning to the evaluated one. So the pin
// names a digest and the operator goes looking for it among the revisions that
// still exist, which is a selection and not a computation.
//
// The retained workload is the evidence. It carries the full digest under
// assayd.dev/revision-digest and its name embeds the revision, and both are
// checked: the annotation because the 40-bit name is forgeable by a chosen
// collision (A57), the name shape because ownedWorkloads is the authority on
// what this operator created for this Agent.
//
// **What this does not do yet, and §5 says so.** ADR-0031 **Amendment 1** also
// requires refusing a revision that lacks the gate evidence a governed install
// demands, and rechecking current security constraints — a rollback is not
// authorization to restore a revoked credential. An earlier version of this
// comment attributed those to decision 2 itself, which does not contain them;
// they were accepted from the direction review and never recorded, and the
// amendment records them. Neither is implementable
// against what exists: status.revisions[] is not on the CRD, so there is no
// record carrying a revision's gate verdict to consult. This resolves the
// selection half and leaves the eligibility half owed, rather than implying a
// check it does not make.
func (r *AgentReconciler) resolveReleasePin(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string) (*releasePin, error) {
	if agent.Spec.Release == nil || agent.Spec.Release.TargetRevisionDigest == "" {
		return nil, nil
	}
	want := agent.Spec.Release.TargetRevisionDigest

	owned, err := r.ownedWorkloads(ctx, agent, runNS)
	if err != nil {
		return nil, err
	}
	for i := range owned {
		d := owned[i].Annotations[RevisionDigestAnnotation]
		if d == "" || d != want {
			continue
		}
		rev := owned[i].Labels[LabelRevision]
		if rev == "" {
			continue
		}
		// The name is derived, not trusted: ownedWorkloads already required
		// `<agent>-<revision>` and the Agent's UID, so reaching here means this
		// operator created this object for this Agent at this revision.
		return &releasePin{Digest: want, Revision: rev}, nil
	}
	return nil, &unresolvablePinError{
		digest: want,
		reason: "no retained revision of this Agent carries that digest. It may have left the " +
			"retained set and been collected, or it may never have existed — a digest is a full " +
			"SHA-256 and is not guessable, so a typo reads the same as a collected revision here",
	}
}

// reportUnresolvablePin puts the refusal on the object. It is Degraded rather
// than a bare error: the request failed, the user needs to see why, and
// retrying forever against a digest that will never appear writes nothing.
func (r *AgentReconciler) reportUnresolvablePin(ctx context.Context, agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, e *unresolvablePinError) error {
	// Degraded, not Ready=False. The message says the current release is
	// untouched and it is — so setting the canonical condition False would page
	// the on-call for a typo in a 64-character digest while the agent serves
	// normally. A13's rule, written into agent_controller.go: Ready=False on an
	// agent whose active revision is serving "would trip every alert keyed on the
	// canonical condition".
	conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable", e.Error())
	status.Phase = assaydv1alpha1.PhaseDegraded
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return r.writeStatus(ctx, agent, status)
}

// verifyRetainedMaterial checks that a PINNED revision's immutable copies are
// still there and are the ones it was minted with. It returns no names, and
// that is the point: since the BLOCKER fix, a pinned revision's workload is not
// re-rendered at all, so there is nothing to point at a copy — the Deployment
// already references them. What remains is a REFUSAL: a revision whose material
// has been collected, or whose spec has since changed shape, cannot be served,
// and saying so beats serving something adjacent to what was asked for.
//
// This is the operation that makes a rollback a rollback. ensureRevisionMaterial
// takes buffers resolved from the CURRENT objects and writes them; run against a
// pinned revision after the source has drifted, it tries to write the drifted
// bytes into an immutable copy that holds the evaluated ones and the copy
// correctly refuses — RevisionMaterialUnavailable/MaterialInvalid. That refusal
// is right and the call was wrong. The retained copies are already there, named
// deterministically, and the pin should point the workload at them.
//
// **The shape must still match, and where it does not this refuses.** The names
// are MaterialName(agent, rev, i) over the CURRENT spec's source list, so index
// i means "the i-th source of the spec as it is now". Rolling back to a revision
// whose spec declared different sources would mis-map them. Each copy records
// which source it came from, so that is checkable rather than assumed: if copy i
// does not name the ref the current spec has at i, the pin is refused. The
// record that would answer this properly is status.revisions[], which is not on
// the CRD (§5) — so this checks what it can and refuses what it cannot.
func (r *AgentReconciler) verifyRetainedMaterial(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS, rev, revDigest string) error {
	for i, ref := range revision.EnvSources(agent.Spec) {
		name := MaterialName(agent.Name, rev, i)
		var obj client.Object
		if ref.Kind == "Secret" {
			obj = &corev1.Secret{}
		} else {
			obj = &corev1.ConfigMap{}
		}
		err := r.Get(ctx, types.NamespacedName{Namespace: runNS, Name: name}, obj)
		if err != nil {
			return &unresolvablePinError{digest: revDigest, reason: fmt.Sprintf(
				"its immutable copy %q of %s is missing or unreadable (%v). A revision whose "+
					"material has been collected cannot be served: the bytes it was evaluated "+
					"with no longer exist and reading the user's object instead would serve "+
					"content that revision never saw", name, ref, err)}
		}
		if got := obj.GetAnnotations()[MaterialSourceAnnotation]; got != ref.Kind+"."+ref.Name {
			return &unresolvablePinError{digest: revDigest, reason: fmt.Sprintf(
				"its immutable copy %q was made from %q but this spec's source at that position "+
					"is %q. The pinned revision declared a different set of sources, and nothing "+
					"records which copy belongs to which — status.revisions[] is not on the CRD. "+
					"Refusing rather than mapping them by position", name, got, ref.Kind+"."+ref.Name)}
		}
	}
	return nil
}

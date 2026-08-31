package controller

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/revision"
)

// Design 02 A35: a revision reads its own immutable copy of every env source.
//
// A20 made a source edit MINT a candidate, which is detection. It does not stop
// an already-published revision consuming the change: the workload still
// resolved the user's object by name, so replacing a Pod after an edit served
// new bytes under a revision that had already passed its gate. Copying closes
// that — the published revision reads material the editor never had a reference
// to.
//
// This is A35 ALONE. A42's run namespace is not implemented, so copies live in
// the Agent's namespace and a principal with create/delete on ConfigMaps can
// still delete one and recreate it under the same name. That residual is stated
// in design 02 §3.3 and is what A42 exists to close.
const (
	// Provenance is a LABEL, not an ownerReference (A44). A42 will move these
	// objects to another namespace, where a cross-namespace owner reference is
	// treated as absent — so using labels from the start means the move does not
	// change the invariant.
	MaterialAgentUIDLabel = "plume.dev/agent-uid"
	MaterialRevisionLabel = "plume.dev/revision"
	// The source reference is an ANNOTATION for the same reason as the digest: a
	// label value is capped at 63 bytes and a ConfigMap NAME may be 253. Labels
	// carry only what is selected on — the agent UID and the 10-character
	// revision name, both bounded.
	MaterialSourceAnnotation = "plume.dev/source"
	// The full digest is an ANNOTATION, not a label: a label VALUE is capped at
	// 63 bytes and a SHA-256 is 64. The label carries the 40-bit revision NAME so
	// the sweep can select on it; the annotation carries the identity that is
	// actually verified. Selecting on the name is safe because the check that
	// follows compares the digest.
	MaterialDigestAnnotation = "plume.dev/revision-digest"
)

// MaterialName is derived from inputs, so a re-reconcile converges rather than
// duplicating.
func MaterialName(agent, rev string, index int) string {
	return fmt.Sprintf("%s-%s-env-%d", agent, rev, index)
}

type materialError struct{ name, why string }

func (e *materialError) Error() string {
	return fmt.Sprintf("revision material %s: %s. To recover: delete that object and let the "+
		"operator recreate it from the source", e.name, e.why)
}

// sourceBuffer is the exact bytes read ONCE, used for both the digest and the
// copy. A20's resolver fills it; nothing re-reads the source afterwards.
//
// That is A39's rule and it is not a style preference: a two-read
// implementation races an editor between the reads, so the digest is computed
// over safe content and the copy is created from malicious content — faithfully
// immutable and faithfully wrong.
type sourceBuffer struct {
	kind   string
	data   map[string]string
	binary map[string][]byte
	digest string
}

// ensureRevisionMaterial creates the copies for a revision and returns the name
// each source was copied to.
func (r *AgentReconciler) ensureRevisionMaterial(ctx context.Context, agent *plumev1alpha1.Agent,
	rev, revDigest string, buffers map[revision.SourceRef]*sourceBuffer,
) (map[revision.SourceRef]string, error) {
	names := map[revision.SourceRef]string{}
	for i, ref := range revision.EnvSources(agent.Spec) {
		buf, ok := buffers[ref]
		if !ok {
			return nil, fmt.Errorf("no buffered content for %s: every source must be resolved "+
				"before the revision is minted (A39)", ref)
		}
		name := MaterialName(agent.Name, rev, i)
		names[ref] = name
		if err := r.ensureOneCopy(ctx, agent, name, rev, revDigest, ref, buf); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func (r *AgentReconciler) ensureOneCopy(ctx context.Context, agent *plumev1alpha1.Agent,
	name, rev, revDigest string, ref revision.SourceRef, buf *sourceBuffer) error {
	labels := map[string]string{
		MaterialAgentUIDLabel: string(agent.UID),
		MaterialRevisionLabel: rev,
	}
	annotations := map[string]string{
		MaterialDigestAnnotation: revDigest,
		MaterialSourceAnnotation: ref.Kind + "." + ref.Name,
	}
	meta := metav1.ObjectMeta{Name: name, Namespace: agent.Namespace,
		Labels: labels, Annotations: annotations}
	yes := true
	key := types.NamespacedName{Namespace: agent.Namespace, Name: name}

	// The KIND is preserved. A Secret copied into a ConfigMap would be readable
	// by a wider set of principals than the original — a privilege change
	// disguised as a copy.
	if ref.Kind == "Secret" {
		var existing corev1.Secret
		switch err := r.Get(ctx, key, &existing); {
		case apierrors.IsNotFound(err):
			s := &corev1.Secret{ObjectMeta: meta, Data: buf.binary, Immutable: &yes}
			return r.createMaterial(ctx, s, name)
		case err != nil:
			return fmt.Errorf("read revision material %s: %w", name, err)
		}
		if err := checkProvenance(name, labels, existing.Labels, annotations, existing.Annotations,
			existing.Immutable); err != nil {
			return err
		}
		if !dataEqual(existing.Data, buf.binary) {
			return &materialError{name: name, why: "contents differ from the source it was minted from"}
		}
		return nil
	}

	var existing corev1.ConfigMap
	switch err := r.Get(ctx, key, &existing); {
	case apierrors.IsNotFound(err):
		cm := &corev1.ConfigMap{ObjectMeta: meta, Data: buf.data, BinaryData: buf.binary, Immutable: &yes}
		return r.createMaterial(ctx, cm, name)
	case err != nil:
		return fmt.Errorf("read revision material %s: %w", name, err)
	}
	if err := checkProvenance(name, labels, existing.Labels, annotations, existing.Annotations,
		existing.Immutable); err != nil {
		return err
	}
	if revision.ContentDigest(existing.Data, existing.BinaryData) != buf.digest {
		return &materialError{name: name, why: "contents differ from the source it was minted from"}
	}
	return nil
}

// createMaterial treats AlreadyExists as a RACE, never as success: something
// took the name between the read and the write, and the name is derived from
// public inputs. The next pass re-reads and applies the full invariant.
func (r *AgentReconciler) createMaterial(ctx context.Context, obj client.Object, name string) error {
	err := r.Create(ctx, obj)
	switch {
	case apierrors.IsAlreadyExists(err):
		return fmt.Errorf("revision material %s appeared between the read and the create; "+
			"requeueing to verify it: %w", name, err)
	case err != nil:
		return fmt.Errorf("create revision material %s: %w", name, err)
	}
	return nil
}

// checkProvenance is A39's "AlreadyExists is not an adoption": a copy this
// operator did not just create must satisfy the WHOLE invariant, or it is
// material whose origin nobody can establish.
func checkProvenance(name string, wantLabels, gotLabels, wantAnn, gotAnn map[string]string,
	immutable *bool) error {
	if gotAnn[MaterialDigestAnnotation] != wantAnn[MaterialDigestAnnotation] {
		return &materialError{name: name, why: fmt.Sprintf(
			"was minted for revision digest %q and this one is %q, so it belongs to a different revision",
			gotAnn[MaterialDigestAnnotation], wantAnn[MaterialDigestAnnotation])}
	}
	want, got := wantLabels, gotLabels
	if gotAnn[MaterialSourceAnnotation] != wantAnn[MaterialSourceAnnotation] {
		return &materialError{name: name, why: fmt.Sprintf(
			"was copied from %q and this reference is %q",
			gotAnn[MaterialSourceAnnotation], wantAnn[MaterialSourceAnnotation])}
	}
	for _, k := range []string{MaterialAgentUIDLabel, MaterialRevisionLabel} {
		if got[k] != want[k] {
			return &materialError{name: name, why: fmt.Sprintf(
				"label %s is %q and this revision expects %q, so it belongs to something else",
				k, got[k], want[k])}
		}
	}
	if immutable == nil || !*immutable {
		return &materialError{name: name, why: "not immutable, so its contents can still be rewritten"}
	}
	return nil
}

func dataEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !bytes.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// rewriteEnv and rewriteEnvFrom point the workload at the revision's own copies
// instead of the user's objects (A35), and make every reference NON-OPTIONAL
// (A41).
//
// Optionality is not a detail. A copy inheriting `optional: true` would let a
// DELETION silently change the process environment — the variable simply absent
// on the next Pod — which is a behaviour change achieved without an edit.
// Non-optional turns the same act into a visible failure to start, which
// Kubernetes reports and A41 relies on.
func rewriteEnv(in []corev1.EnvVar, material map[revision.SourceRef]string) []corev1.EnvVar {
	if len(material) == 0 {
		return in
	}
	out := make([]corev1.EnvVar, 0, len(in))
	no := false
	for _, e := range in {
		if e.ValueFrom != nil {
			v := e.ValueFrom.DeepCopy()
			if v.ConfigMapKeyRef != nil {
				if n, ok := material[revision.SourceRef{Kind: "ConfigMap", Name: v.ConfigMapKeyRef.Name}]; ok {
					v.ConfigMapKeyRef.Name, v.ConfigMapKeyRef.Optional = n, &no
				}
			}
			if v.SecretKeyRef != nil {
				if n, ok := material[revision.SourceRef{Kind: "Secret", Name: v.SecretKeyRef.Name}]; ok {
					v.SecretKeyRef.Name, v.SecretKeyRef.Optional = n, &no
				}
			}
			e.ValueFrom = v
		}
		out = append(out, e)
	}
	return out
}

func rewriteEnvFrom(in []corev1.EnvFromSource, material map[revision.SourceRef]string) []corev1.EnvFromSource {
	if len(material) == 0 {
		return in
	}
	out := make([]corev1.EnvFromSource, 0, len(in))
	no := false
	for _, f := range in {
		f = *f.DeepCopy()
		if f.ConfigMapRef != nil {
			if n, ok := material[revision.SourceRef{Kind: "ConfigMap", Name: f.ConfigMapRef.Name}]; ok {
				f.ConfigMapRef.Name, f.ConfigMapRef.Optional = n, &no
			}
		}
		if f.SecretRef != nil {
			if n, ok := material[revision.SourceRef{Kind: "Secret", Name: f.SecretRef.Name}]; ok {
				f.SecretRef.Name, f.SecretRef.Optional = n, &no
			}
		}
		out = append(out, f)
	}
	return out
}

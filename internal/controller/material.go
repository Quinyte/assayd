package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

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
		// CONTENT FIRST (A57). `immutable: true` freezes data, not labels or
		// annotations — so a principal with `update` can rewrite the metadata, and
		// if that were an integrity check they could permanently Degrade the
		// agent. That converts the content edit this design stops into a denial of
		// service: a smaller loss, and still a capability this code would have
		// granted.
		//
		// The bytes are the only thing the Pod reads. If they match, this IS the
		// material the revision was minted from, whatever the metadata says, so
		// the operator restamps rather than refusing.
		if revision.ContentDigest(nil, existing.Data) != buf.digest {
			return &materialError{name: name, why: "contents differ from the source it was minted from"}
		}
		return r.repairMetadata(ctx, &existing, labels, annotations, name)
	}

	var existing corev1.ConfigMap
	switch err := r.Get(ctx, key, &existing); {
	case apierrors.IsNotFound(err):
		cm := &corev1.ConfigMap{ObjectMeta: meta, Data: buf.data, BinaryData: buf.binary, Immutable: &yes}
		return r.createMaterial(ctx, cm, name)
	case err != nil:
		return fmt.Errorf("read revision material %s: %w", name, err)
	}
	if revision.ContentDigest(existing.Data, existing.BinaryData) != buf.digest {
		return &materialError{name: name, why: "contents differ from the source it was minted from"}
	}
	return r.repairMetadata(ctx, &existing, labels, annotations, name)
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

// isRevisionMaterial decides whether an object is something THIS operator
// created for THIS Agent, and it is the authority for every delete.
//
// The label alone is not: an Agent's UID is readable by anyone with `get` on it,
// and adding a label needs only `update` — which is the exact principal A20 and
// A35 exist to defend against. Selecting on the label and deleting the matches
// turns `configmaps/update` into `configmaps/delete` across the namespace, with
// the operator's credentials. This package already refuses that reasoning for
// Deployments: ownedWorkloads decides ownership by controller reference and UID
// "which nobody can forge by labelling", and A44 removed the ownerReference here
// without putting anything in its place.
//
// The NAME is what an attacker cannot forge. Kubernetes names are immutable, so
// renaming a victim object into this shape requires the delete they are trying
// to obtain. Everything else is corroboration.
func isRevisionMaterial(agent *plumev1alpha1.Agent, o client.Object) bool {
	rev := o.GetLabels()[MaterialRevisionLabel]
	if rev == "" || o.GetLabels()[MaterialAgentUIDLabel] != string(agent.UID) {
		return false
	}
	ann := o.GetAnnotations()
	if ann[MaterialDigestAnnotation] == "" || ann[MaterialSourceAnnotation] == "" {
		return false
	}
	// The index is bounded by the number of sources any revision of this Agent
	// could have had. Nothing records that historically, so the name is matched
	// against a generous range rather than an exact one — the point is the SHAPE,
	// which a victim object does not have.
	for i := 0; i < 64; i++ {
		if o.GetName() == MaterialName(agent.Name, rev, i) {
			return true
		}
	}
	return false
}

// deleteMaterial removes revision-scoped copies matching a selector.
//
// This exists because A44 chose LABELS over an ownerReference — a choice made so
// A42's namespace move would not change the invariant, and one that means
// Kubernetes garbage collection will not clean these up. Nothing else will
// either: the copies hold a snapshot of a Secret's bytes, so leaking them is
// leaking credential material, which is the cost A23 warned about when it
// declined copying in the first place.
func (r *AgentReconciler) deleteMaterial(ctx context.Context, agent *plumev1alpha1.Agent, ns string,
	sel client.MatchingLabels) error {
	for _, list := range []client.ObjectList{&corev1.ConfigMapList{}, &corev1.SecretList{}} {
		if err := r.List(ctx, list, client.InNamespace(ns), sel); err != nil {
			return fmt.Errorf("list revision material in %s: %w", ns, err)
		}
		var items []client.Object
		switch l := list.(type) {
		case *corev1.ConfigMapList:
			for i := range l.Items {
				items = append(items, &l.Items[i])
			}
		case *corev1.SecretList:
			for i := range l.Items {
				items = append(items, &l.Items[i])
			}
		}
		for _, o := range items {
			if !isRevisionMaterial(agent, o) {
				// Someone else's object wearing our label. Deleting it would be the
				// operator lending its credentials to a principal who only had
				// `update`.
				log.FromContext(ctx).Info("refusing to delete an object that is not this agent's "+
					"revision material", "object", o.GetName(), "namespace", ns)
				continue
			}
			if err := r.Delete(ctx, o); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete revision material %s: %w", o.GetName(), err)
			}
		}
	}
	return nil
}

// collectRevisionMaterial removes copies for revisions that no longer exist.
//
// It keys on what SURVIVES rather than on what was deleted: a sweep that
// enumerates retired revisions misses any the operator never saw — a crash
// between creating material and publishing the revision leaves copies no status
// names, and those are exactly the ones nothing else would ever remove.
func (r *AgentReconciler) collectRevisionMaterial(ctx context.Context, agent *plumev1alpha1.Agent,
	keep map[string]bool) error {
	var cms corev1.ConfigMapList
	var secs corev1.SecretList
	sel := client.MatchingLabels{MaterialAgentUIDLabel: string(agent.UID)}
	if err := r.List(ctx, &cms, client.InNamespace(agent.Namespace), sel); err != nil {
		return fmt.Errorf("list revision material: %w", err)
	}
	if err := r.List(ctx, &secs, client.InNamespace(agent.Namespace), sel); err != nil {
		return fmt.Errorf("list revision material: %w", err)
	}
	var candidates []client.Object
	for i := range cms.Items {
		candidates = append(candidates, &cms.Items[i])
	}
	for i := range secs.Items {
		candidates = append(candidates, &secs.Items[i])
	}
	for _, o := range candidates {
		if !isRevisionMaterial(agent, o) {
			log.FromContext(ctx).Info("refusing to collect an object that is not this agent's "+
				"revision material", "object", o.GetName())
			continue
		}
		if keep[o.GetLabels()[MaterialRevisionLabel]] {
			continue
		}
		if err := r.Delete(ctx, o); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("gc revision material %s: %w", o.GetName(), err)
		}
	}
	return nil
}

// repairMetadata restores the operator's labels, annotations and immutability on
// material whose CONTENT already matches (A57).
//
// This is the other half of "content first": metadata is bookkeeping the
// operator owns, so it is repaired rather than treated as evidence. It also
// resolves the same-name-recreated-Agent case — a copy surviving a finalizer
// that did not complete would otherwise wedge the new Agent permanently on a
// UID label it cannot match and cannot collect.
func (r *AgentReconciler) repairMetadata(ctx context.Context, obj client.Object,
	labels, annotations map[string]string, name string) error {
	// Immutability cannot be enabled after the fact — Kubernetes forbids changing
	// the field — so material that is not immutable is deleted and recreated.
	// That is safe here precisely because the content already matches.
	switch o := obj.(type) {
	case *corev1.ConfigMap:
		if o.Immutable == nil || !*o.Immutable {
			return r.recreateMaterial(ctx, obj, name)
		}
	case *corev1.Secret:
		if o.Immutable == nil || !*o.Immutable {
			return r.recreateMaterial(ctx, obj, name)
		}
	}
	l, a, needs := obj.GetLabels(), obj.GetAnnotations(), false
	if l == nil {
		l = map[string]string{}
	}
	if a == nil {
		a = map[string]string{}
	}
	for k, v := range labels {
		if l[k] != v {
			l[k], needs = v, true
		}
	}
	for k, v := range annotations {
		if a[k] != v {
			a[k], needs = v, true
		}
	}
	if !needs {
		return nil
	}
	obj.SetLabels(l)
	obj.SetAnnotations(a)
	if err := r.Update(ctx, obj); err != nil {
		return fmt.Errorf("restamp revision material %s: %w", name, err)
	}
	return nil
}

// recreateMaterial deletes material that cannot be repaired in place and returns
// a retriable error, so the next pass creates it fresh from the buffer.
func (r *AgentReconciler) recreateMaterial(ctx context.Context, obj client.Object, name string) error {
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete unrepairable revision material %s: %w", name, err)
	}
	return fmt.Errorf("revision material %s was not immutable and could not be repaired in place; "+
		"deleted, and the next pass recreates it from the source", name)
}

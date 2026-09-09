package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 02 A42/A60: an Agent's workload and its revision material live in an
// operator-owned namespace, `assayd-run-<agent-namespace>`, so that a principal
// with create/delete on ConfigMaps in the Agent's namespace cannot delete a
// published revision's copy and recreate it under the same name. A ConfigMap
// can only be used by Pods in its own namespace, so the material is out of the
// editor's reach only if the Pod is too — which is why this is a namespace and
// not a naming scheme.
//
// The operator must be able to prove it created that namespace, and the proof
// is the BINDING RECORD (A60): a ConfigMap in the operator's own namespace that
// carries a nonce generated before the run namespace exists and, once bound,
// the run namespace's UID. The nonce defeats pre-creation (the attacker moves
// first and cannot know it); the UID defeats delete-and-recreate (the attacker
// moves second and cannot reproduce it). A label proves nothing — Codex r8
// BLOCKER 7 — so no label is consulted as evidence here.
const (
	// RunNamespacePrefix is the fixed prefix of every run namespace name.
	RunNamespacePrefix = "assayd-run-"

	// LabelRunNamespace is what the Gateway listener admits routes from (design
	// 03 A44, design 07 A5.3). It is NOT the SPIFFE selector's label: design 26
	// A1 also stamps it on a vCluster's host sync namespace, where a tenant
	// creates the Pods.
	LabelRunNamespace = "assayd.dev/run-namespace"
	// LabelPodsBy certifies one property — nothing but the agent-operator
	// creates Pods here — and is the ClusterSPIFFEID's namespace selector
	// (design 07 A5.2). It never goes on a sync namespace.
	LabelPodsBy         = "assayd.dev/pods-by"
	PodsByAgentOperator = "agent-operator"
	// LabelAgentNamespace carries the Agent's OWN namespace, on the run
	// namespace and on every workload Pod: the SVID path segment reads it (A59),
	// so the move is invisible to authorization.
	LabelAgentNamespace = "assayd.dev/agent-namespace"
	// LabelOwnedBy is observability — the install this namespace belongs to.
	// It is never consulted as evidence.
	LabelOwnedBy = "assayd.dev/owned-by"
	// LabelAgentUID is the provenance label on every workload and copy in a run
	// namespace (A44/A60). Corroboration, not authority: the name is.
	LabelAgentUID = "assayd.dev/agent-uid"
	// AnnotationBindingNonce is stamped on the run namespace at creation. An
	// annotation, not a label: nothing selects on it.
	AnnotationBindingNonce = "assayd.dev/binding-nonce"

	// LabelBinding marks the operator's own binding records as such; a human
	// reading assayd-system sees what they are.
	LabelBinding = "assayd.dev/run-namespace-binding"
	// bindingSchemaVersion is the only version this operator decodes. A record
	// carrying another is BindingRecordInvalid, never guessed at.
	bindingSchemaVersion = "1"

	// MirrorPrefix names the ResourceQuota and LimitRange copies in a run
	// namespace (A60).
	MirrorPrefix = "assayd-mirror-"
	// AnnotationMirrorSource carries the mirrored object's source name, so the
	// reverse mapping does not depend on a truncated name.
	AnnotationMirrorSource = "assayd.dev/mirror-source"

	// The admission policies that reserve the assayd.dev namespace labels to the
	// operator identities (design 07 A5.9). Without them the labels SPIRE and the
	// Gateway act on are a convention, and the operator refuses to create run
	// namespaces.
	NamespaceLabelPolicyName = "assayd-namespace-labels"
	GatewayRoutePolicyName   = "assayd-gateway-routes"
)

// Reasons on CondRunNamespaceUnavailable (design 02 §5).
const (
	ReasonNotCreatedByOperator = "NotCreatedByOperator"
	ReasonNameCollision        = "NameCollision"
	ReasonTerminating          = "Terminating"
	ReasonBindingRecordInvalid = "BindingRecordInvalid"
	ReasonLabelAuthorityAbsent = "LabelAuthorityAbsent"
)

// pssLabels are the six Pod Security admission labels mirrored from the source
// namespace (design 02 A44, verified set in
// docs/research/tenant-namespace-primitives-2026-09.md §3).
var pssLabels = []string{
	"pod-security.kubernetes.io/enforce", "pod-security.kubernetes.io/enforce-version",
	"pod-security.kubernetes.io/audit", "pod-security.kubernetes.io/audit-version",
	"pod-security.kubernetes.io/warn", "pod-security.kubernetes.io/warn-version",
}

// RunNamespaceName maps a source namespace to its run namespace.
//
// A namespace name is a DNS label of at most 63 characters, so the naive
// prefix+source form is unconstructable for a long source. Naive truncation is
// forbidden — it is non-injective and would pool two tenants' material — so the
// name is truncated and suffixed with a 16-hex (64-bit) hash of the
// UNTRUNCATED source (design 03 §3.2's rule, widened by A44 from 8 hex because
// 32 bits is not enough for a name that is also a security boundary). A
// collision is still detected, by the binding record, never silently reused.
//
// The kept length is DERIVED from the prefix, not written down. It used to be a
// literal 36 under a comment reading "10 + 36 + 1 + 16 = 63", and the 2026-09-09
// rename made the prefix eleven characters and every long name 64 — one over the
// limit, caught by TestRunNamespaceNameIsADNSLabelAndInjectiveAtTheLimit. A
// magic number that encodes the length of a name is a bug waiting for that name
// to change.
func RunNamespaceName(source string) string {
	if len(RunNamespacePrefix)+len(source) <= 63 {
		return RunNamespacePrefix + source
	}
	const hashHex = 16
	keep := 63 - len(RunNamespacePrefix) - 1 - hashHex // 1 for the separator
	sum := sha256.Sum256([]byte(source))
	suffix := hex.EncodeToString(sum[:])[:hashHex]
	return RunNamespacePrefix + strings.TrimRight(source[:keep], "-") + "-" + suffix
}

// MirrorName names a ResourceQuota or LimitRange copy. Those names are DNS
// subdomains (253), so the same truncate-and-hash rule applies past that.
func MirrorName(source string) string {
	if len(MirrorPrefix)+len(source) <= 253 {
		return MirrorPrefix + source
	}
	sum := sha256.Sum256([]byte(source))
	return MirrorPrefix + strings.TrimRight(source[:253-len(MirrorPrefix)-17], "-.") + "-" +
		hex.EncodeToString(sum[:])[:16]
}

// BindingName names the binding record for a run namespace.
func BindingName(runNamespace string) string { return "assayd-run-binding-" + runNamespace }

type bindingState string

const (
	bindingCreating    bindingState = "Creating"
	bindingBound       bindingState = "Bound"
	bindingTerminating bindingState = "Terminating"
	bindingDeleting    bindingState = "Deleting"
)

// binding is the decoded record. obj is kept so a state change is a
// compare-and-swap on the ConfigMap's resourceVersion.
type binding struct {
	obj                *corev1.ConfigMap
	sourceNamespace    string
	sourceNamespaceUID string
	runNamespace       string
	runNamespaceUID    string
	nonce              string
	state              bindingState
}

// decodeBinding is STRICT. The record decides namespace deletion and
// credential placement, so a missing field, an unknown state or a version this
// operator does not carry is a refusal naming the field — nothing defaults.
func decodeBinding(cm *corev1.ConfigMap) (*binding, error) {
	get := func(k string) (string, error) {
		v, ok := cm.Data[k]
		if !ok || v == "" {
			return "", fmt.Errorf("binding record %s is missing %q", cm.Name, k)
		}
		return v, nil
	}
	ver, err := get("schemaVersion")
	if err != nil {
		return nil, err
	}
	if ver != bindingSchemaVersion {
		return nil, fmt.Errorf("binding record %s has schemaVersion %q and this operator decodes only %q",
			cm.Name, ver, bindingSchemaVersion)
	}
	b := &binding{obj: cm}
	for k, dst := range map[string]*string{
		"sourceNamespace": &b.sourceNamespace, "sourceNamespaceUID": &b.sourceNamespaceUID,
		"runNamespace": &b.runNamespace, "nonce": &b.nonce,
	} {
		if *dst, err = get(k); err != nil {
			return nil, err
		}
	}
	// The run namespace UID is empty until Bound; the state says whether it is
	// required.
	b.runNamespaceUID = cm.Data["runNamespaceUID"]
	st, err := get("state")
	if err != nil {
		return nil, err
	}
	switch bindingState(st) {
	case bindingCreating, bindingBound, bindingTerminating, bindingDeleting:
		b.state = bindingState(st)
	default:
		return nil, fmt.Errorf("binding record %s has unknown state %q", cm.Name, st)
	}
	if b.state != bindingCreating && b.runNamespaceUID == "" {
		return nil, fmt.Errorf("binding record %s is %s and has no runNamespaceUID", cm.Name, st)
	}
	return b, nil
}

func (b *binding) encode() {
	b.obj.Data = map[string]string{
		"schemaVersion":      bindingSchemaVersion,
		"sourceNamespace":    b.sourceNamespace,
		"sourceNamespaceUID": b.sourceNamespaceUID,
		"runNamespace":       b.runNamespace,
		"runNamespaceUID":    b.runNamespaceUID,
		"nonce":              b.nonce,
		"state":              string(b.state),
	}
}

// runNamespaceError is the typed failure of ensureRunNamespace. terminal means
// a human must act; otherwise the reconcile requeues.
type runNamespaceError struct {
	reason   string
	message  string
	terminal bool
}

func (e *runNamespaceError) Error() string { return e.reason + ": " + e.message }

func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate binding nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// isNamespaceTerminating reports the 403 the API server returns for a create
// into a namespace whose phase is Terminating (NamespaceLifecycle admission).
// Plain IsForbidden is not specific enough: RBAC denials are 403 too. The
// reconciler treats this cause as "wait" (RunNamespaceUnavailable=Terminating),
// never as an error to retry against.
func isNamespaceTerminating(err error) bool {
	return apierrors.HasStatusCause(err, corev1.NamespaceTerminatingCause)
}

// lockRunNamespace serializes every writer of one binding within this process.
// Leader election guarantees one operator process, so this is what makes the
// protocol safe under more than one reconcile worker: without it a peer mid-check-7
// (namespace created, UID not yet recorded) is indistinguishable from a
// crashed predecessor, and check 9 would delete its namespace. The
// Terminating/Deleting fence does not need this; checks 7 and 9 do.
func (r *AgentReconciler) lockRunNamespace(runName string) func() {
	v, _ := r.runNamespaceLocks.LoadOrStore(runName, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// LabelAuthorityPresent reports whether design 07 A5.9's admission policies
// exist BY NAME, policy and binding both. The operator fail-closes without
// them: a label nobody reserves must not be treated as evidence by SPIRE or
// the Gateway. Presence is what is checked; the content is the chart's, and a
// policy of the right name that validates nothing would pass here — said
// rather than pretended otherwise.
//
// This is the production adapter; the reconciler takes it as a function so
// envtest can substitute one and one test can exercise the real thing.
func LabelAuthorityPresent(c client.Reader) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		for _, name := range []string{NamespaceLabelPolicyName, GatewayRoutePolicyName} {
			var p admissionv1.ValidatingAdmissionPolicy
			if err := c.Get(ctx, types.NamespacedName{Name: name}, &p); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, fmt.Errorf("check admission policy %s: %w", name, err)
			}
			var b admissionv1.ValidatingAdmissionPolicyBinding
			if err := c.Get(ctx, types.NamespacedName{Name: name}, &b); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, fmt.Errorf("check admission policy binding %s: %w", name, err)
			}
		}
		return true, nil
	}
}

// ensureRunNamespace returns the run namespace an Agent's material and workload
// go into, or a runNamespaceError. It is design 02 A60's ordered check list; the
// numbers in comments are its rows, and the first that applies decides.
func (r *AgentReconciler) ensureRunNamespace(ctx context.Context, agent *assaydv1alpha1.Agent) (string, error) {
	if r.LabelAuthorityPresent == nil || r.OperatorNamespace == "" {
		return "", fmt.Errorf("run namespace: the reconciler was built without an operator namespace " +
			"or a label-authority check; NewAgentReconciler refuses this, so this is a test wiring error")
	}
	ok, err := r.LabelAuthorityPresent(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", &runNamespaceError{reason: ReasonLabelAuthorityAbsent, terminal: true,
			message: fmt.Sprintf("the admission policies %s and %s (design 07 A5.9) are not installed, "+
				"so the assayd.dev namespace labels SPIRE and the Gateway act on would be forgeable. "+
				"No run namespace is created until the chart's policies are present",
				NamespaceLabelPolicyName, GatewayRoutePolicyName)}
	}

	// Every namespace the protocol decides on is read LIVE, not from the
	// informer cache: a recreated namespace's old object can sit in the cache
	// between the API server's recreate and the next watch delivery, and a UID
	// compared against a stale object is no proof at all.
	var source corev1.Namespace
	if err := r.reader().Get(ctx, types.NamespacedName{Name: agent.Namespace}, &source); err != nil {
		return "", fmt.Errorf("read the Agent's namespace %s: %w", agent.Namespace, err)
	}
	sUID := string(source.UID)
	runName := RunNamespaceName(agent.Namespace)
	defer r.lockRunNamespace(runName)()

	b, err := r.readBinding(ctx, runName)
	if err != nil {
		return "", err
	}
	if b == nil {
		var existing corev1.Namespace
		switch err := r.reader().Get(ctx, types.NamespacedName{Name: runName}, &existing); {
		case err == nil && !existing.DeletionTimestamp.IsZero():
			// A namespace of that name is on its way out — the operator's own,
			// after its binding was removed, or someone else's. Either way it will
			// be gone, and check 3 follows; refusing it terminally would wedge the
			// operator's own handler path.
			return "", &runNamespaceError{reason: ReasonTerminating,
				message: fmt.Sprintf("a namespace named %s is being deleted; waiting for it to be gone "+
					"before creating this Agent's run namespace", runName)}
		case err == nil:
			// Check 2. A binding is always written before its namespace, so a
			// namespace with no binding is one this operator did not make.
			return "", &runNamespaceError{reason: ReasonNotCreatedByOperator, terminal: true,
				message: fmt.Sprintf("run namespace %s exists and this operator holds no binding record for "+
					"it, so it did not create it — it was pre-created, or created and recreated outside the "+
					"operator, or the record %s/%s was lost. Nothing will be written into it. To recover: "+
					"delete the namespace if nobody made it on purpose; or, if the record was lost, "+
					"recreate the ConfigMap %s/%s with data schemaVersion=1, sourceNamespace=%s, "+
					"sourceNamespaceUID=<the namespace's metadata.uid>, runNamespace=%s, "+
					"runNamespaceUID=<the run namespace's metadata.uid>, nonce=<any non-empty string>, "+
					"state=Bound, then annotate the Agents in %s to trigger a reconcile",
					runName, r.OperatorNamespace, BindingName(runName), r.OperatorNamespace, BindingName(runName),
					agent.Namespace, runName, agent.Namespace)}
		case !apierrors.IsNotFound(err):
			return "", fmt.Errorf("read run namespace %s: %w", runName, err)
		}
		// Check 3.
		nonce, err := newNonce()
		if err != nil {
			return "", err
		}
		b = &binding{
			obj: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: BindingName(runName), Namespace: r.OperatorNamespace,
				Labels: map[string]string{LabelBinding: "true"},
			}},
			sourceNamespace: agent.Namespace, sourceNamespaceUID: sUID,
			runNamespace: runName, nonce: nonce, state: bindingCreating,
		}
		b.encode()
		if err := r.Create(ctx, b.obj); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Another reconcile of this source namespace won. Re-read; the
				// nonce is always taken from the record, never from memory.
				return "", fmt.Errorf("binding record for %s appeared between the read and the create; "+
					"requeueing to use it: %w", runName, err)
			}
			return "", fmt.Errorf("create binding record for %s: %w", runName, err)
		}
	}

	// Check 4.
	if b.sourceNamespace != agent.Namespace {
		return "", &runNamespaceError{reason: ReasonNameCollision, terminal: true,
			message: fmt.Sprintf("namespaces %s and %s both map to run namespace %s; the first holds it "+
				"and nothing of it is touched. To recover: rename this namespace",
				b.sourceNamespace, agent.Namespace, runName)}
	}
	// Check 5.
	if b.sourceNamespaceUID != sUID {
		if b.state == bindingCreating {
			// Nothing was ever bound, so there is nothing to terminate: a
			// Terminating record without a UID is one the strict decoder refuses.
			// The crash-gap namespace, if one exists with this nonce, goes with the
			// record (check 9's authority: shape and nonce), and the next pass meets
			// check 1's wait or check 3 once it is gone.
			var ns corev1.Namespace
			switch err := r.reader().Get(ctx, types.NamespacedName{Name: runName}, &ns); {
			case err == nil && ns.Annotations[AnnotationBindingNonce] == b.nonce:
				if err := r.deleteRunNamespace(ctx, &ns); err != nil {
					return "", err
				}
			case err != nil && !apierrors.IsNotFound(err):
				return "", fmt.Errorf("read run namespace %s: %w", runName, err)
			}
			if err := r.Delete(ctx, b.obj); err != nil && !apierrors.IsNotFound(err) {
				return "", fmt.Errorf("delete binding %s: %w", b.obj.Name, err)
			}
			return "", &runNamespaceError{reason: ReasonTerminating,
				message: fmt.Sprintf("namespace %s was deleted and recreated before its run namespace was "+
					"bound; the unbound record is discarded and a fresh one follows", agent.Namespace)}
		}
		if b.state == bindingBound {
			if err := r.swapBinding(ctx, b, bindingTerminating); err != nil {
				return "", err
			}
		}
		// nil, not this Agent: the exclusion is the FINALIZER's, for the Agent being
		// deleted. An Agent reconciling here is live and must count, or it would
		// tear down its own namespace (caught by the first test of this path).
		if err := r.runNamespaceHandler(ctx, b, nil); err != nil {
			return "", err
		}
		return "", &runNamespaceError{reason: ReasonTerminating,
			message: fmt.Sprintf("namespace %s was deleted and recreated, so run namespace %s belongs to "+
				"the namespace that no longer exists and is being torn down; a fresh one follows",
				agent.Namespace, runName)}
	}
	// Check 6.
	if b.state == bindingTerminating || b.state == bindingDeleting {
		if err := r.runNamespaceHandler(ctx, b, nil); err != nil {
			return "", err
		}
		return "", &runNamespaceError{reason: ReasonTerminating,
			message: fmt.Sprintf("run namespace %s is being torn down; this Agent waits for it to be "+
				"gone and a fresh one to be created", runName)}
	}

	var ns corev1.Namespace
	nsErr := r.reader().Get(ctx, types.NamespacedName{Name: runName}, &ns)
	switch {
	case nsErr != nil && !apierrors.IsNotFound(nsErr):
		return "", fmt.Errorf("read run namespace %s: %w", runName, nsErr)

	case b.state == bindingCreating && !ns.DeletionTimestamp.IsZero():
		// Check 8: the namespace is already going — check 9's own delete
		// on the previous pass, or someone else's. Whichever nonce it carries,
		// wait for it to be gone and check 7 follows — refusing here wedged the
		// operator's own recovery terminally (found by the code review of A61).
		return "", &runNamespaceError{reason: ReasonTerminating,
			message: fmt.Sprintf("run namespace %s is being deleted; waiting for it to be gone before "+
				"creating a fresh one", runName)}

	case b.state == bindingCreating && apierrors.IsNotFound(nsErr):
		// Check 7: create, then bind FROM THE CREATE RESPONSE, in this reconcile.
		installUID, err := r.installIdentity(ctx)
		if err != nil {
			return "", err
		}
		desired := runNamespaceFor(runName, &source, b.nonce, installUID)
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return "", fmt.Errorf("run namespace %s appeared between the read and the create; "+
					"requeueing to decide whose it is: %w", runName, err)
			}
			return "", fmt.Errorf("create run namespace %s: %w", runName, err)
		}
		// Mirrors BEFORE Bound, so the first Pod cannot precede its quota; a
		// mirror that fails leaves the record Creating and the next pass meets
		// check 9, which deletes and retries rather than binding a namespace whose
		// quota never landed.
		if err := r.mirrorInto(ctx, &source, desired.Name); err != nil {
			return "", err
		}
		b.runNamespaceUID = string(desired.UID)
		if err := r.swapBinding(ctx, b, bindingBound); err != nil {
			return "", err
		}
		return runName, nil

	case b.state == bindingCreating:
		if ns.Annotations[AnnotationBindingNonce] == b.nonce {
			// Check 9: the crash gap. A namespace this operator created and did not
			// record, or a copy of it — never bound on the strength of being
			// observed. Delete it, rotate the nonce, retry.
			if err := r.deleteRunNamespace(ctx, &ns); err != nil {
				return "", err
			}
			nonce, err := newNonce()
			if err != nil {
				return "", err
			}
			b.nonce = nonce
			if err := r.swapBinding(ctx, b, bindingCreating); err != nil {
				return "", err
			}
			return "", &runNamespaceError{reason: ReasonTerminating,
				message: fmt.Sprintf("run namespace %s was created but never recorded (a restart between "+
					"the two writes); it is deleted rather than adopted and will be recreated", runName)}
		}
		// Check 10.
		return "", r.notCreatedByOperator(runName, agent.Namespace, "carries no nonce, or a nonce this "+
			"operator's record does not know — it was created by someone else")

	case b.state == bindingBound && apierrors.IsNotFound(nsErr):
		// Check 11.
		nonce, err := newNonce()
		if err != nil {
			return "", err
		}
		b.nonce, b.runNamespaceUID = nonce, ""
		if err := r.swapBinding(ctx, b, bindingCreating); err != nil {
			return "", err
		}
		return "", &runNamespaceError{reason: ReasonTerminating,
			message: fmt.Sprintf("run namespace %s was deleted by someone with cluster rights; every copy "+
				"and workload went with it and it is being recreated. A retained revision whose sources "+
				"have drifted since it was gated cannot be reproduced (A41)", runName)}

	case b.state == bindingBound && !ns.DeletionTimestamp.IsZero():
		// Check 12.
		if err := r.swapBinding(ctx, b, bindingTerminating); err != nil {
			return "", err
		}
		if err := r.runNamespaceHandler(ctx, b, nil); err != nil {
			return "", err
		}
		return "", &runNamespaceError{reason: ReasonTerminating,
			message: fmt.Sprintf("run namespace %s is being deleted by someone else; waiting for it to be "+
				"gone before a fresh one is created", runName)}

	case b.state == bindingBound && string(ns.UID) != b.runNamespaceUID:
		// Check 13.
		return "", r.notCreatedByOperator(runName, agent.Namespace, fmt.Sprintf("has UID %s and this "+
			"operator's record says it created UID %s — it was deleted and recreated by someone with "+
			"cluster rights, whatever its labels say", ns.UID, b.runNamespaceUID))

	case b.state == bindingBound:
		// Check 14.
		installUID, err := r.installIdentity(ctx)
		if err != nil {
			return "", err
		}
		if err := r.reconcileRunNamespaceLabels(ctx, &ns, &source, installUID); err != nil {
			return "", err
		}
		if err := r.mirrorInto(ctx, &source, runName); err != nil {
			return "", err
		}
		return runName, nil
	}
	return "", fmt.Errorf("run namespace %s: unreachable binding state %q", runName, b.state)
}

func (r *AgentReconciler) notCreatedByOperator(runName, source, why string) error {
	return &runNamespaceError{reason: ReasonNotCreatedByOperator, terminal: true,
		message: fmt.Sprintf("run namespace %s %s. Nothing will be written into it. To recover: delete "+
			"that namespace if nobody made it on purpose, then annotate the Agents in %s to trigger a "+
			"reconcile", runName, why, source)}
}

// readBinding reads the record UNCACHED, through the API reader, so every
// reconcile sees the latest state — a property this code owns rather than one
// it borrows from cmd/operator's cache configuration.
func (r *AgentReconciler) readBinding(ctx context.Context, runName string) (*binding, error) {
	var cm corev1.ConfigMap
	key := types.NamespacedName{Namespace: r.OperatorNamespace, Name: BindingName(runName)}
	if err := r.reader().Get(ctx, key, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read binding record %s: %w", key, err)
	}
	b, err := decodeBinding(&cm)
	if err != nil {
		return nil, &runNamespaceError{reason: ReasonBindingRecordInvalid, terminal: true,
			message: err.Error() + ". Nothing defaults: recover by correcting the record by hand from " +
				"the run namespace's metadata.uid and the source namespace's UID, then annotate the " +
				"Agents to trigger a reconcile"}
	}
	return b, nil
}

// swapBinding is a compare-and-swap: Update fails on a stale resourceVersion,
// so two workers cannot both win a transition.
func (r *AgentReconciler) swapBinding(ctx context.Context, b *binding, to bindingState) error {
	from := b.state
	b.state = to
	b.encode()
	if err := r.Update(ctx, b.obj); err != nil {
		b.state = from
		if apierrors.IsConflict(err) {
			return fmt.Errorf("binding %s moved while swapping %s→%s; requeueing to re-read: %w",
				b.obj.Name, from, to, err)
		}
		return fmt.Errorf("swap binding %s %s→%s: %w", b.obj.Name, from, to, err)
	}
	return nil
}

func runNamespaceFor(name string, source *corev1.Namespace, nonce, installUID string) *corev1.Namespace {
	labels := map[string]string{
		LabelRunNamespace:   "true",
		LabelPodsBy:         PodsByAgentOperator,
		LabelAgentNamespace: source.Name,
		LabelOwnedBy:        installUID,
	}
	for _, k := range pssLabels {
		if v, ok := source.Labels[k]; ok {
			labels[k] = v
		}
	}
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: name, Labels: labels,
		Annotations: map[string]string{AnnotationBindingNonce: nonce},
	}}
}

// reconcileRunNamespaceLabels keeps the operator's labels and the mirrored Pod
// Security labels current. Changing a namespace's PSS labels does not
// re-evaluate running Pods — Kubernetes only warns — so a tightened level
// takes effect on the next Pod, and nothing here pretends otherwise.
func (r *AgentReconciler) reconcileRunNamespaceLabels(ctx context.Context, ns, source *corev1.Namespace, installUID string) error {
	want := runNamespaceFor(ns.Name, source, "", installUID).Labels
	changed := false
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	for k, v := range want {
		if ns.Labels[k] != v {
			ns.Labels[k], changed = v, true
		}
	}
	for _, k := range pssLabels {
		if _, keep := want[k]; !keep {
			if _, has := ns.Labels[k]; has {
				delete(ns.Labels, k)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if err := r.Update(ctx, ns); err != nil {
		return fmt.Errorf("reconcile labels on run namespace %s: %w", ns.Name, err)
	}
	return nil
}

// deleteRunNamespace deletes a run namespace after the two checks that bound
// the operator's cluster-wide namespaces/delete grant: the name has the
// assayd-run- shape, and the nonce (before binding) or UID (after) is the
// binding's. The caller has verified whichever applies; the shape is checked
// here so no path can forget it.
func (r *AgentReconciler) deleteRunNamespace(ctx context.Context, ns *corev1.Namespace) error {
	if !strings.HasPrefix(ns.Name, RunNamespacePrefix) {
		return fmt.Errorf("refusing to delete namespace %s: not a run namespace", ns.Name)
	}
	if err := r.Delete(ctx, ns, client.Preconditions{UID: &ns.UID}); err != nil && !apierrors.IsNotFound(err) {
		if apierrors.IsConflict(err) {
			// The precondition failed: the name now holds a different UID.
			return nil
		}
		return fmt.Errorf("delete run namespace %s: %w", ns.Name, err)
	}
	return nil
}

// runNamespaceHandler is design 02 A60's Terminating/Deleting handler. It is
// idempotent and has one owner — whoever reads the state — so a crash anywhere
// in it re-enters the same code. `self` is the Agent being DELETED when the
// finalizer calls this, so it does not count itself; every other caller passes
// nil, because a live Agent that reached the handler must count.
func (r *AgentReconciler) runNamespaceHandler(ctx context.Context, b *binding, self *assaydv1alpha1.Agent) error {
	// Step 0.
	if b.state != bindingTerminating && b.state != bindingDeleting {
		return nil
	}
	var ns corev1.Namespace
	err := r.reader().Get(ctx, types.NamespacedName{Name: b.runNamespace}, &ns)
	switch {
	case err != nil && !apierrors.IsNotFound(err):
		return fmt.Errorf("read run namespace %s: %w", b.runNamespace, err)
	case apierrors.IsNotFound(err) || string(ns.UID) != b.runNamespaceUID:
		// Step 1, UID first: the namespace this binding named no longer exists.
		if err := r.Delete(ctx, b.obj); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete binding %s: %w", b.obj.Name, err)
		}
		return nil
	case !ns.DeletionTimestamp.IsZero():
		// Step 2.
		return nil
	}
	if b.state == bindingTerminating {
		// Step 3.
		live, err := r.runNamespaceStillNeeded(ctx, b, self)
		if err != nil {
			return err
		}
		if live {
			return r.swapBinding(ctx, b, bindingBound)
		}
		// Step 4.
		if err := r.swapBinding(ctx, b, bindingDeleting); err != nil {
			return err
		}
	}
	// Step 5.
	return r.deleteRunNamespace(ctx, &ns)
}

// runNamespaceStillNeeded is the predicate behind "last Agent": a declared
// Agent remains in the source namespace, under the binding's source UID, or a
// workload or copy of any Agent that exists there is still present in the run
// namespace. Both lists are LIVE (uncached) and both are scoped by the source
// UID, so a recreated tenant's leftovers are orphans that go with the
// namespace rather than evidence to keep it.
func (r *AgentReconciler) runNamespaceStillNeeded(ctx context.Context, b *binding, self *assaydv1alpha1.Agent) (bool, error) {
	var source corev1.Namespace
	if err := r.reader().Get(ctx, types.NamespacedName{Name: b.sourceNamespace}, &source); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read namespace %s: %w", b.sourceNamespace, err)
	}
	if string(source.UID) != b.sourceNamespaceUID {
		return false, nil
	}
	var agents assaydv1alpha1.AgentList
	if err := r.reader().List(ctx, &agents, client.InNamespace(b.sourceNamespace)); err != nil {
		return false, fmt.Errorf("live-list agents in %s: %w", b.sourceNamespace, err)
	}
	known := map[string]bool{}
	for i := range agents.Items {
		a := &agents.Items[i]
		if self != nil && a.UID == self.UID {
			continue
		}
		if a.Spec.External != nil {
			// An external Agent runs elsewhere and never uses the run namespace;
			// it is not a reason to keep one.
			continue
		}
		known[string(a.UID)] = true
		if a.DeletionTimestamp.IsZero() {
			return true, nil
		}
	}
	if len(known) == 0 {
		return false, nil
	}
	// A deleting Agent is not a reason to keep the namespace; its workload
	// still draining is.
	var ds appsv1.DeploymentList
	if err := r.reader().List(ctx, &ds, client.InNamespace(b.runNamespace)); err != nil {
		return false, fmt.Errorf("live-list workloads in %s: %w", b.runNamespace, err)
	}
	for _, d := range ds.Items {
		if known[d.Labels[LabelAgentUID]] {
			return true, nil
		}
	}
	for _, list := range []client.ObjectList{&corev1.ConfigMapList{}, &corev1.SecretList{}} {
		if err := r.reader().List(ctx, list, client.InNamespace(b.runNamespace)); err != nil {
			return false, fmt.Errorf("live-list material in %s: %w", b.runNamespace, err)
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
			if known[o.GetLabels()[LabelAgentUID]] {
				return true, nil
			}
		}
	}
	return false, nil
}

// releaseRunNamespaceIfLast is the finalizer's last step (design 02 §3.7,
// A60): after this Agent's own workloads and copies are gone, ask whether it
// was the last, swap the binding before the confirming list, and run the
// handler.
func (r *AgentReconciler) releaseRunNamespaceIfLast(ctx context.Context, agent *assaydv1alpha1.Agent) error {
	runName := RunNamespaceName(agent.Namespace)
	defer r.lockRunNamespace(runName)()
	b, err := r.readBinding(ctx, runName)
	if err != nil {
		var rerr *runNamespaceError
		if errors.As(err, &rerr) {
			// An invalid record must not block deletion of an Agent; it is
			// reported on the Agents that remain.
			log.FromContext(ctx).Info("binding record unreadable during teardown; leaving it",
				"binding", BindingName(runName), "why", rerr.message)
			return nil
		}
		return err
	}
	if b == nil || b.sourceNamespace != agent.Namespace {
		return nil
	}
	if b.state == bindingBound {
		live, err := r.runNamespaceStillNeeded(ctx, b, agent)
		if err != nil {
			return err
		}
		if live {
			return nil
		}
		if err := r.swapBinding(ctx, b, bindingTerminating); err != nil {
			if apierrors.IsConflict(errors.Unwrap(err)) {
				// Another finalizer, or this one before a crash, already moved
				// it: proceed to the handler on a fresh read.
				if b, err = r.readBinding(ctx, runName); err != nil || b == nil {
					return err
				}
			} else {
				return err
			}
		}
	}
	return r.runNamespaceHandler(ctx, b, agent)
}

// reader is the uncached reader for the live lists the protocol requires. In
// envtest the client itself is uncached and Reader may be nil.
func (r *AgentReconciler) reader() client.Reader {
	if r.Reader != nil {
		return r.Reader
	}
	return r.Client
}

// mirrorInto copies every ResourceQuota and LimitRange of the source namespace
// into the run namespace (A60). Together, because a quota that constrains CPU
// rejects a Pod with no request and the LimitRange supplies the default. Each
// namespace is accounted separately, so the pair can reach 2× the stated
// ceiling; design 26 A1 puts that to the user as an API question.
func (r *AgentReconciler) mirrorInto(ctx context.Context, source *corev1.Namespace, runName string) error {
	var quotas corev1.ResourceQuotaList
	if err := r.List(ctx, &quotas, client.InNamespace(source.Name)); err != nil {
		return fmt.Errorf("list quotas in %s: %w", source.Name, err)
	}
	wantQ := map[string]bool{}
	for i := range quotas.Items {
		q := &quotas.Items[i]
		name := MirrorName(q.Name)
		wantQ[name] = true
		desired := &corev1.ResourceQuota{
			ObjectMeta: mirrorMeta(name, runName, source.Name, q.Name), Spec: *q.Spec.DeepCopy()}
		var existing corev1.ResourceQuota
		switch err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing); {
		case apierrors.IsNotFound(err):
			if err := r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("mirror quota %s into %s: %w", q.Name, runName, err)
			}
		case err != nil:
			return fmt.Errorf("read mirrored quota %s: %w", name, err)
		default:
			if !quotaSpecEqual(existing.Spec, desired.Spec) {
				existing.Spec = desired.Spec
				if err := r.Update(ctx, &existing); err != nil {
					return fmt.Errorf("update mirrored quota %s: %w", name, err)
				}
			}
		}
	}
	var mirroredQ corev1.ResourceQuotaList
	if err := r.List(ctx, &mirroredQ, client.InNamespace(runName)); err != nil {
		return fmt.Errorf("list mirrored quotas in %s: %w", runName, err)
	}
	for i := range mirroredQ.Items {
		m := &mirroredQ.Items[i]
		if isMirror(m) && !wantQ[m.Name] {
			if err := r.Delete(ctx, m); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete stale mirrored quota %s: %w", m.Name, err)
			}
		}
	}

	var limits corev1.LimitRangeList
	if err := r.List(ctx, &limits, client.InNamespace(source.Name)); err != nil {
		return fmt.Errorf("list limit ranges in %s: %w", source.Name, err)
	}
	wantL := map[string]bool{}
	for i := range limits.Items {
		l := &limits.Items[i]
		name := MirrorName(l.Name)
		wantL[name] = true
		desired := &corev1.LimitRange{
			ObjectMeta: mirrorMeta(name, runName, source.Name, l.Name), Spec: *l.Spec.DeepCopy()}
		var existing corev1.LimitRange
		switch err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing); {
		case apierrors.IsNotFound(err):
			if err := r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("mirror limit range %s into %s: %w", l.Name, runName, err)
			}
		case err != nil:
			return fmt.Errorf("read mirrored limit range %s: %w", name, err)
		default:
			if !limitSpecEqual(existing.Spec, desired.Spec) {
				existing.Spec = desired.Spec
				if err := r.Update(ctx, &existing); err != nil {
					return fmt.Errorf("update mirrored limit range %s: %w", name, err)
				}
			}
		}
	}
	var mirroredL corev1.LimitRangeList
	if err := r.List(ctx, &mirroredL, client.InNamespace(runName)); err != nil {
		return fmt.Errorf("list mirrored limit ranges in %s: %w", runName, err)
	}
	for i := range mirroredL.Items {
		m := &mirroredL.Items[i]
		if isMirror(m) && !wantL[m.Name] {
			if err := r.Delete(ctx, m); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete stale mirrored limit range %s: %w", m.Name, err)
			}
		}
	}
	return nil
}

func mirrorMeta(name, runName, sourceNS, sourceName string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: name, Namespace: runName,
		Labels:      map[string]string{LabelAgentNamespace: sourceNS},
		Annotations: map[string]string{AnnotationMirrorSource: sourceName},
	}
}

// isMirror decides by NAME shape plus the annotation, so a stray object in a
// run namespace is never deleted for wearing a label (A57).
func isMirror(o client.Object) bool {
	return strings.HasPrefix(o.GetName(), MirrorPrefix) && o.GetAnnotations()[AnnotationMirrorSource] != ""
}

func quotaSpecEqual(a, b corev1.ResourceQuotaSpec) bool {
	if len(a.Hard) != len(b.Hard) || len(a.Scopes) != len(b.Scopes) {
		return false
	}
	for k, v := range a.Hard {
		w, ok := b.Hard[k]
		if !ok || v.Cmp(w) != 0 {
			return false
		}
	}
	as, bs := append([]corev1.ResourceQuotaScope(nil), a.Scopes...), append([]corev1.ResourceQuotaScope(nil), b.Scopes...)
	sort.Slice(as, func(i, j int) bool { return as[i] < as[j] })
	sort.Slice(bs, func(i, j int) bool { return bs[i] < bs[j] })
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return (a.ScopeSelector == nil) == (b.ScopeSelector == nil) &&
		(a.ScopeSelector == nil || a.ScopeSelector.String() == b.ScopeSelector.String())
}

func limitSpecEqual(a, b corev1.LimitRangeSpec) bool {
	if len(a.Limits) != len(b.Limits) {
		return false
	}
	for i := range a.Limits {
		if a.Limits[i].String() != b.Limits[i].String() {
			return false
		}
	}
	return true
}

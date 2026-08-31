package envtest

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// Design 02 A21, decided by the user on 2026-08-26 and never implemented until
// Codex r8 BLOCKER 1 pointed out that the design said "digest required (CEL)"
// while the API had only MinLength=1 and the generated CRD had no rule at all.
//
// The bypass needs no Agent write: gate registry/agent:prod while it resolves to
// X, retag to Y, drain the node. The replacement Pod pulls Y while the spec, the
// full revision digest, the gate result and status all still identify X.
func TestOnlyADigestPinnedImageIsAdmitted(t *testing.T) {
	ns := newNamespace(t)
	const good = "ghcr.io/acme/agent@sha256:" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	for _, tc := range []struct {
		name, image string
		admit       bool
		why         string
	}{
		{"exact digest", good, true, "the only accepted form"},
		{"mutable tag", "ghcr.io/acme/agent:1.0.0", false,
			"the whole point: a tag can be repointed after the gate passes"},
		{"no tag at all", "ghcr.io/acme/agent", false,
			"bare means :latest, which is the most mutable tag there is"},
		{"tag that looks like a digest", "ghcr.io/acme/agent:sha256-0123456789abcdef", false,
			"a tag containing the word sha256 is still a tag"},
		{"uppercase hex", "ghcr.io/acme/agent@sha256:" + strings.ToUpper(
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"), false,
			"OCI digests are lowercase; accepting both spellings would let one image have two identities"},
		{"63 hex characters", "ghcr.io/acme/agent@sha256:" +
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde", false,
			"a truncated digest is not a digest"},
		{"65 hex characters", "ghcr.io/acme/agent@sha256:" +
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0", false,
			"trailing characters would let one image match many strings"},
		{"digest plus a trailing tag", good + ":extra", false,
			"anchored at both ends, or a suffix reintroduces the mutable part"},
		{"two digests", good + "@sha256:" +
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", false,
			"the repo part must not itself contain an @, or the first digest is decorative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &plumev1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: "img-" + strings.ReplaceAll(tc.name, " ", "-"), Namespace: ns},
				Spec: plumev1alpha1.AgentSpec{
					Runtime: &plumev1alpha1.AgentRuntime{Image: tc.image},
				},
			}
			err := k8s.Create(context.Background(), a)
			switch {
			case tc.admit && err != nil:
				t.Errorf("a digest-pinned image was rejected — %s: %v", tc.why, err)
			case !tc.admit && err == nil:
				t.Errorf("%q was admitted — %s", tc.image, tc.why)
			case !tc.admit && err != nil && !strings.Contains(err.Error(), "digest-pinned"):
				t.Errorf("rejected, but not by the digest rule, so this proves nothing about it: %v", err)
			}
		})
	}
}

// The message has to name the fix, not the violation. An operator reading
// "invalid value" has to go and find out what a digest-pinned reference is; the
// rule that costs nothing to state is the one that says how to get one.
func TestTheDigestRejectionNamesTheFix(t *testing.T) {
	ns := newNamespace(t)
	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "msg", Namespace: ns},
		Spec:       plumev1alpha1.AgentSpec{Runtime: &plumev1alpha1.AgentRuntime{Image: "ghcr.io/acme/a:1"}},
	}
	err := k8s.Create(context.Background(), a)
	if err == nil {
		t.Fatal("a tagged image was admitted")
	}
	for _, want := range []string{"@sha256:", "64 lowercase hex", "imagetools"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the rejection does not mention %q, so it names the violation without "+
				"naming the fix:\n%s", want, err.Error())
		}
	}
}

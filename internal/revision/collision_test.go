package revision

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Codex r7 BLOCKER 1. The revision NAME is ten hex characters — forty bits —
// and the projection it is computed from is attacker-controlled. That makes the
// question a CHOSEN collision, not an accidental one, and forty bits costs
// about 2^20 trials: the pair below was found in 2.1 seconds on a laptop.
//
// The bypass it buys is total. The safe spec passes evaluation as revision
// 1e6dc371e2. The principal then writes the colliding spec, which names a
// different image and projects to the same ten characters. An operator
// comparing names sees activeRevision == desired, skips candidate gating, and
// converges the Deployment named 1f119ef39b — through the very block that
// exists to correct out-of-band drift — to the attacker's image, while status
// still reports the revision that passed its gate.
//
// The pair is pinned rather than searched for, so this test is deterministic
// and costs nothing. If the projection encoding changes the pair stops
// colliding, which fails loudly here and is correct: a new encoding needs its
// own pinned pair, not a deleted test.
func collidingPair() (safe, evil assaydv1alpha1.AgentSpec) {
	mk := func(image, pad string) assaydv1alpha1.AgentSpec {
		return assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
			Image: image,
			// Port is explicit and matches the CRD default. It entered the
			// projection at ADR-0031, and a spec built as a literal skips
			// defaulting — so without this the pair collides here while the Agent
			// the API actually admits hashes to something else, and the envtest
			// that reuses this pair looks for a workload that was never created.
			Port: 8080,
			Env:  []corev1.EnvVar{{Name: "PAD", Value: pad}},
		}}
	}
	return mk("ghcr.io/acme/agent@sha256:a100000000000000000000000000000000000000000000000000000000000001", "1917962"), mk("ghcr.io/attacker/backdoor@sha256:b200000000000000000000000000000000000000000000000000000000000002", "x216079")
}

func TestTheNameCollidesAndTheIdentityDoesNot(t *testing.T) {
	safe, evil := collidingPair()

	if MustHash(safe) != MustHash(evil) {
		t.Fatalf("the pinned pair no longer collides (%s vs %s).\n"+
			"The projection encoding changed. This is not a stale test to delete: find a new "+
			"colliding pair for the new encoding and pin that, or the regression is unguarded.",
			MustHash(safe), MustHash(evil))
	}
	t.Logf("both specs project to revision name %s", MustHash(safe))

	if MustDigest(safe) == MustDigest(evil) {
		t.Error("the two specs share a FULL digest — that would be a SHA-256 collision, " +
			"and every identity decision in this operator rests on it")
	}
	if MustDigest(safe)[:HashLength] != MustHash(safe) {
		t.Error("Hash is not a prefix of Digest; the name and the identity must be derived " +
			"from one encoding or they can disagree about what was hashed")
	}
}

// The name is 40 bits and must not be treated as more. This states the bound
// rather than leaving the next reader to infer it from HashLength.
func TestTheRevisionNameIsNotASecurityBoundary(t *testing.T) {
	if HashLength >= 32 {
		t.Skip("HashLength now carries 128+ bits; this test's premise no longer holds")
	}
	safe, evil := collidingPair()
	if MustHash(safe) == MustHash(evil) && MustDigest(safe) != MustDigest(evil) {
		return // the demonstrated property
	}
	t.Errorf("expected a same-name/different-identity pair; got name %s/%s", MustHash(safe), MustHash(evil))
}

package envtest

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/revision"
)

// Codex r8 MAJOR 2. internal/revision's per-leaf classification test builds each
// specimen from a ZERO AgentSpec, so many of them violate exactly-one-of
// runtime/external, an enum, or the digest pattern. That test therefore proves
// the serialization contains a Go field; it does not prove that a REACHABLE API
// transition is classified correctly — and a leaf admission rejects, or that
// defaulting normalises, cannot gate or waste a gate in production whatever the
// hash says.
//
// This runs the same walker against a real API server. Every specimen is
// dry-run created before its hash is believed, and a leaf with no admissible
// perturbation is reported as UNREACHABLE rather than counted as covered.
func TestEveryAPIReachableLeafIsClassifiedCorrectly(t *testing.T) {
	const digest = "ghcr.io/acme/agent@sha256:" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ns := newNamespace(t)

	// A valid base per family: runtime and external are mutually exclusive, so
	// one base cannot carry both and a single base would make half the leaves
	// unreachable for the wrong reason.
	// The base seeds VALID required siblings — a knowledge entry needs both name
	// and version, a tool needs a name — so perturbing one leaf does not fail
	// admission on a neighbour that was left zero.
	base := func(path string) plumev1alpha1.AgentSpec {
		if strings.HasPrefix(path, "spec.External") {
			return plumev1alpha1.AgentSpec{
				External: &plumev1alpha1.ExternalAgent{Endpoint: "https://a.example.com"},
			}
		}
		return plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{
				// Sandbox is NOT seeded: replicas>1 with a sandbox is an admission
				// error, so seeding it made the one policy-surface leaf that scales
				// look unreachable. SetLeafString allocates it when the leaf under
				// test is the profile itself.
				Image: digest,
			},
			Knowledge: []plumev1alpha1.KnowledgeBinding{{Name: "kg", Version: "v1"}},
			Tools:     []plumev1alpha1.ToolBinding{{Name: "tool"}},
			Expose: &plumev1alpha1.ExposeSpec{
				A2A: &plumev1alpha1.ExposeProtocol{Visibility: "org", Auth: "oauth"},
			},
		}
	}

	// Leaves whose schema constrains the VALUE — an enum, a pattern, a digest.
	// The generic walker writes "leaf-a"/"leaf-b", which are distinct and
	// inadmissible; these are distinct AND admissible, which is the whole point
	// of running this against a real API server.
	valid := map[string][2]string{
		"spec.Runtime.Image": {digest, "ghcr.io/acme/other@sha256:" +
			"fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"},
		"spec.Runtime.Sandbox.Profile": {"gvisor", "kata"},
		"spec.External.Endpoint":       {"https://a.example.com", "https://b.example.com"},
		"spec.Expose.A2A.Visibility":   {"cluster", "org"},
		"spec.Expose.A2A.Auth":         {"none", "oauth"},
	}

	admissible := func(t *testing.T, name string, spec plumev1alpha1.AgentSpec) error {
		t.Helper()
		a := &plumev1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       spec,
		}
		return k8s.Create(context.Background(), a, client.DryRunAll)
	}

	var unreachable []string
	leaves := revision.Leaves(reflect.TypeOf(plumev1alpha1.AgentSpec{}), "spec", nil)
	if len(leaves) < 45 {
		t.Fatalf("walked only %d leaves", len(leaves))
	}

	for i, l := range leaves {
		t.Run(l.Path, func(t *testing.T) {
			mk := func(n int) plumev1alpha1.AgentSpec {
				s := base(l.Path)
				if pair, ok := valid[l.Path]; ok {
					revision.SetLeafString(t, reflect.ValueOf(&s).Elem(), l, pair[n])
				} else {
					revision.SetLeaf(t, reflect.ValueOf(&s).Elem(), l, n)
				}
				return s
			}
			a, b := mk(0), mk(1)
			errA := admissible(t, fmt.Sprintf("leaf-a-%d", i), a)
			errB := admissible(t, fmt.Sprintf("leaf-b-%d", i), b)
			if errA != nil || errB != nil {
				// NOT a pass and NOT a failure: the perturbation this walker
				// generates is not admissible, so this leaf's classification is
				// unproven HERE. Recorded by name so the gap is countable rather
				// than absorbed into a green run.
				unreachable = append(unreachable, l.Path)
				t.Skipf("no admissible perturbation: %v / %v", errA, errB)
			}
			same := revision.Hash(a) == revision.Hash(b)
			mustMint := revision.ClassifiedAsBehaviour(l.Path)
			switch {
			case mustMint && same:
				t.Errorf("%s is behaviour-surface and an ADMISSIBLE change to it alone did not "+
					"mint a revision: it reaches production through no gate", l.Path)
			case !mustMint && !same:
				t.Errorf("%s is policy-surface and an ADMISSIBLE change to it alone minted a "+
					"revision: a routine operation now pays for an eval and canary cycle", l.Path)
			}
		})
	}

	sort.Strings(unreachable)
	if len(unreachable) > 0 {
		t.Logf("%d/%d leaves have no admissible perturbation from this walker and are classified "+
			"only by the unit test:\n  %s", len(unreachable), len(leaves), strings.Join(unreachable, "\n  "))
	}
	// The point of this test is that MOST leaves are proven against the schema.
	// If that stops being true it has quietly become a no-op.
	// A ratchet, not a bar to lower. Each unreachable leaf is a leaf classified
	// only by a unit test that builds inadmissible specimens, which is exactly
	// what r8 MAJOR 2 objected to — so the count is asserted, and lowering it is
	// a deliberate act rather than a drift.
	if len(unreachable) > 0 {
		t.Errorf("%d leaves have no admissible perturbation: %s\n"+
			"Each one is classified only against a specimen the API would reject. Add a valid "+
			"value pair for it rather than accepting the gap.", len(unreachable),
			strings.Join(unreachable, ", "))
	}
}

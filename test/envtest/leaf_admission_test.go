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

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/revision"
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
	base := func(path string) assaydv1alpha1.AgentSpec {
		if strings.HasPrefix(path, "spec.External") {
			return assaydv1alpha1.AgentSpec{
				External: &assaydv1alpha1.ExternalAgent{Endpoint: "https://a.example.com"},
			}
		}
		// The endpoint union is discriminated: an azureopenai instance block is
		// only admissible on an azureopenai entry, so a base with no arm makes
		// every instance leaf unreachable — 36 of 84, when the union landed. The
		// base therefore seeds an entry whose ARM matches the leaf under test.
		arm := assaydv1alpha1.ArmAnthropic
		for a, marker := range map[assaydv1alpha1.LLMArm]string{
			assaydv1alpha1.ArmAzureOpenAI: ".AzureOpenAI.",
			assaydv1alpha1.ArmVertexAI:    ".VertexAI.",
			assaydv1alpha1.ArmBedrock:     ".Bedrock.",
			assaydv1alpha1.ArmCustom:      ".Custom.",
		} {
			if strings.Contains(path, marker) {
				arm = a
			}
		}
		ep := assaydv1alpha1.LLMEndpoint{Arm: arm}
		ae := assaydv1alpha1.LLMAllowEntry{Arm: arm}
		switch arm {
		case assaydv1alpha1.ArmAzureOpenAI:
			inst := &assaydv1alpha1.AzureOpenAIInstance{Endpoint: "a.openai.azure.com", DeploymentName: "d"}
			ep.AzureOpenAI, ae.AzureOpenAI = inst, inst.DeepCopy()
		case assaydv1alpha1.ArmVertexAI:
			inst := &assaydv1alpha1.VertexAIInstance{ProjectID: "p", Region: "r"}
			ep.VertexAI, ae.VertexAI = inst, inst.DeepCopy()
		case assaydv1alpha1.ArmBedrock:
			inst := &assaydv1alpha1.BedrockInstance{Region: "r"}
			ep.Bedrock, ae.Bedrock = inst, inst.DeepCopy()
		case assaydv1alpha1.ArmCustom:
			inst := &assaydv1alpha1.CustomInstance{Host: "h"}
			ep.Custom, ae.Custom = inst, inst.DeepCopy()
		}
		return assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				// Sandbox is NOT seeded: replicas>1 with a sandbox is an admission
				// error, so seeding it made the one policy-surface leaf that scales
				// look unreachable. SetLeafString allocates it when the leaf under
				// test is the profile itself.
				Image: digest,
			},
			LLM: &assaydv1alpha1.LLMSpec{
				Providers:       []assaydv1alpha1.LLMEndpoint{ep},
				EgressAllowlist: []assaydv1alpha1.LLMAllowEntry{*ae.DeepCopy()},
				Fallback:        ep.DeepCopy(),
			},
			Knowledge: []assaydv1alpha1.KnowledgeBinding{{Name: "kg", Version: "v1"}},
			Tools:     []assaydv1alpha1.ToolBinding{{Name: "tool"}},
			Expose: &assaydv1alpha1.ExposeSpec{
				A2A: &assaydv1alpha1.ExposeProtocol{Visibility: "org", Auth: "oauth"},
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
		// Two full SHA-256s. The generic walker writes "leaf-a"/"leaf-b", which the
		// pattern refuses, so without this pair the leaf is classified only against
		// a specimen the API would reject.
		"spec.Release.TargetRevisionDigest": {strings.Repeat("a", 64), strings.Repeat("b", 64)},
		"spec.External.Endpoint":            {"https://a.example.com", "https://b.example.com"},
		"spec.Expose.A2A.Visibility":        {"cluster", "org"},
		"spec.Expose.A2A.Auth":              {"none", "oauth"},
		// The arm is an enum, and switching it must keep the instance block valid
		// — so the pair is the two arms that carry no instance block at all.
		"spec.LLM.Providers[].Arm":       {"anthropic", "openai"},
		"spec.LLM.EgressAllowlist[].Arm": {"anthropic", "openai"},
		"spec.LLM.Fallback.Arm":          {"anthropic", "openai"},
	}

	admissible := func(t *testing.T, name string, spec assaydv1alpha1.AgentSpec) error {
		t.Helper()
		a := &assaydv1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       spec,
		}
		return k8s.Create(context.Background(), a, client.DryRunAll)
	}

	var unreachable []string
	leaves := revision.Leaves(t, reflect.TypeOf(assaydv1alpha1.AgentSpec{}), "spec", nil)
	if len(leaves) < 45 {
		t.Fatalf("walked only %d leaves", len(leaves))
	}

	// Leaves the API refuses on purpose. Each entry is a decision, and the loop
	// below proves the refusal still holds rather than counting the leaf as an
	// unproven gap. ADR-0031: resourceFieldRef reads a resource limit or request
	// into the container while spec.runtime.resources stays editable in place,
	// so a capacity edit could change what the program reads under an unchanged
	// revision digest. The selector is projected; the value it resolves to is not.
	refusedByDesign := map[string]string{
		"spec.Runtime.Env[].ValueFrom.ResourceFieldRef.ContainerName": "ADR-0031",
		"spec.Runtime.Env[].ValueFrom.ResourceFieldRef.Resource":      "ADR-0031",
		"spec.Runtime.Env[].ValueFrom.ResourceFieldRef.Divisor":       "ADR-0031",
	}

	for i, l := range leaves {
		t.Run(l.Path, func(t *testing.T) {
			mk := func(n int) assaydv1alpha1.AgentSpec {
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
			if why, refused := refusedByDesign[l.Path]; refused {
				// This leaf is unreachable because a decision made it so. That is
				// not a gap in coverage, but it is only true while admission
				// actually refuses it — so assert the refusal rather than skipping.
				// If the rule is ever relaxed, this fails here instead of silently
				// restoring an ungated path.
				if errA == nil || errB == nil {
					t.Fatalf("%s is supposed to be refused at admission (%s) and the API accepted "+
						"it: a=%v b=%v", l.Path, why, errA, errB)
				}
				return
			}
			if errA != nil || errB != nil {
				// NOT a pass and NOT a failure: the perturbation this walker
				// generates is not admissible, so this leaf's classification is
				// unproven HERE. Recorded by name so the gap is countable rather
				// than absorbed into a green run.
				unreachable = append(unreachable, l.Path)
				t.Skipf("no admissible perturbation: %v / %v", errA, errB)
			}
			ha, aerr := revision.HashOrRefusal(a, "fixed")
			hb, berr := revision.HashOrRefusal(b, "fixed")
			if aerr != nil || berr != nil {
				// A20 refuses a spec whose env arm the operator cannot read, so this
				// leaf has no identity to classify.
				t.Skipf("refused by A20: %v / %v", aerr, berr)
			}
			same := ha == hb
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

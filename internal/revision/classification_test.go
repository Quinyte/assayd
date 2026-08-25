package revision

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// Design 02 §3.3 (A12) classifies every AgentSpec field as behaviour-surface
// (mints a revision, so a change passes a gate) or policy-surface (applied in
// place). These lists ARE that table, and the test below proves they cover the
// struct exhaustively.
//
// An earlier version of A12 said new fields default to policy-surface and called
// that safe. It is not: the critic added a SystemPrompt field — unambiguously
// behaviour — and the whole suite stayed green while it reached production
// ungated. Classification must be compulsory, so adding a CRD field fails this
// test until someone decides which surface it is on and amends A12.

var behaviourFields = map[string]string{
	"Runtime.Image":           "different code",
	"Runtime.Env":             "configuration changes behaviour",
	"Runtime.EnvFrom":         "configuration changes behaviour",
	"Runtime.Sandbox":         "isolation boundary",
	"Knowledge":               "domain data: name, version and scope all change what the agent can reach",
	"Tools":                   "a capability grant",
	"LLM":                     "the model that answers, and its drift fallback",
	"External.Endpoint":       "a different agent entirely",
	"External.OAuthClientRef": "the identity design 24 keys authorization on",
}

var policyFields = map[string]string{
	"Runtime.Replicas":    "a scale operation",
	"Runtime.Port":        "wiring; the operator dials it, the agent's behaviour does not change",
	"Runtime.Resources":   "capacity regressions are caught by design 20's behavioural drift path, and gating them would block incident response",
	"External.InlineCard": "a description, not a grant: §3.3's card-drift rule adjudicates card CONTENT, and a card advertising a skill the CR does not grant fails registration rather than reaching production",
	"Card":                "a path change is re-registration, exactly as card drift is",
	"Budget":              "how much, not what",
	"Gates":               "a gate that re-gated itself on edit could not converge",
	"Expose":              "who may call",
	"Loop":                "lineage governance, enforced at the gateway",
}

// Fields whose sub-fields are classified individually rather than as a whole.
var descend = map[string]bool{"Runtime": true, "External": true}

func TestEveryFieldIsClassified(t *testing.T) {
	var walk func(rt reflect.Type, prefix string)
	walk = func(rt reflect.Type, prefix string) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			path := f.Name
			if prefix != "" {
				path = prefix + "." + f.Name
			}

			if prefix == "" && descend[f.Name] {
				ft := f.Type
				for ft.Kind() == reflect.Ptr {
					ft = ft.Elem()
				}
				walk(ft, f.Name)
				continue
			}

			_, isBehaviour := behaviourFields[path]
			_, isPolicy := policyFields[path]
			switch {
			case isBehaviour && isPolicy:
				t.Errorf("%s is in BOTH lists — decide which surface it is on", path)
			case !isBehaviour && !isPolicy:
				t.Errorf("AgentSpec.%s is unclassified.\n"+
					"Every field must be behaviour-surface (a change to what the agent can do, "+
					"produce or reach — mints a revision and passes a gate) or policy-surface "+
					"(how much, how fast, or who may call — applied in place).\n"+
					"Add it to behaviourFields or policyFields here, project it in revision.go "+
					"if behaviour, and amend design 02 A12's table. Defaulting is not available "+
					"on purpose: an unclassified behaviour field reaches production ungated.", path)
			}
		}
	}
	walk(reflect.TypeOf(plumev1alpha1.AgentSpec{}), "")
}

// Every behaviour-surface field must actually reach the projection. A field
// classified as behaviour but never projected is the same ungated hole as one
// left unclassified, just harder to see.
func TestBehaviourFieldsAreProjected(t *testing.T) {
	for path := range behaviourFields {
		t.Run(path, func(t *testing.T) {
			// The base must already carry the surrounding structure, or a subfield
			// test passes on the block appearing rather than the field changing.
			// External.OAuthClientRef did exactly that: base had no External at
			// all, so the hash differed because External showed up and Runtime
			// vanished. The projection could drop OAuthClientRef entirely and this
			// still passed — and did, undetected, until a cross-model review
			// mutated it.
			base := baseFor(path)
			mutated := mutateField(t, path)
			if Hash(base) == Hash(mutated) {
				t.Errorf("%s is classified behaviour-surface but changing it does not mint a "+
					"revision — it is in the table and not in the projection", path)
			}
		})
	}
}

// baseFor gives a field's test the surrounding structure it needs, so that
// mutateField changes exactly one thing.
func baseFor(path string) plumev1alpha1.AgentSpec {
	s := baseSpec()
	if strings.HasPrefix(path, "External.") {
		s.Runtime = nil
		s.External = &plumev1alpha1.ExternalAgent{
			Endpoint:       "https://base.example.com",
			OAuthClientRef: "client-a",
		}
	}
	return s
}

// mutateField returns the field's base with exactly the named field changed.
func mutateField(t *testing.T, path string) plumev1alpha1.AgentSpec {
	t.Helper()
	s := baseSpec()
	switch path {
	case "Runtime.Image":
		s.Runtime.Image = "ghcr.io/acme/agent:99"
	case "Runtime.Env":
		s.Runtime.Env = []corev1.EnvVar{{Name: "MODE", Value: "x"}}
	case "Runtime.EnvFrom":
		s.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}}}}
	case "Runtime.Sandbox":
		s.Runtime.Sandbox = &plumev1alpha1.SandboxSpec{Profile: "gvisor"}
	case "Knowledge":
		s.Knowledge = []plumev1alpha1.KnowledgeBinding{{Name: "g", Version: "v1"}}
	case "Tools":
		s.Tools = []plumev1alpha1.ToolBinding{{Name: "db"}}
	case "LLM":
		s.LLM = &plumev1alpha1.LLMSpec{Providers: []string{"openai/gpt-x"}}
	case "External.Endpoint":
		s = baseFor(path)
		s.External.Endpoint = "https://other.example.com"
	case "External.OAuthClientRef":
		s = baseFor(path)
		s.External.OAuthClientRef = "client-b"
	default:
		t.Fatalf("no mutation defined for %q — add one when classifying a new field", path)
	}
	return s
}

// Every arm of corev1.EnvVarSource must produce a distinct projection. The
// blocker this replaces: envSourceRef named four arms and k8s v0.36.4 has five,
// so fileKeyRef collapsed to a constant and repointing prod.env -> staging.env
// minted no revision.
func TestEveryEnvVarSourceArmIsDistinct(t *testing.T) {
	rt := reflect.TypeOf(corev1.EnvVarSource{})
	seen := map[string]string{}

	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		// Build a source with only this arm populated, filled with a marker string
		// so two arms cannot coincide by both being zero.
		src := &corev1.EnvVarSource{}
		v := reflect.ValueOf(src).Elem().Field(i)
		if v.Kind() != reflect.Ptr {
			continue
		}
		v.Set(reflect.New(v.Type().Elem()))
		fillStrings(v.Elem(), "arm-"+f.Name)

		got := marshalOrEmpty(envSourceRef(src))
		if prev, dup := seen[got]; dup {
			t.Errorf("arms %s and %s project identically (%s) — a repoint between them "+
				"would reach production ungated", prev, f.Name, got)
		}
		seen[got] = f.Name

		if strings.Contains(got, `"kind":"raw"`) {
			t.Logf("note: %s is unhandled and falls to the raw arm, which over-gates. "+
				"That is safe; name it explicitly when convenient.", f.Name)
		}
	}
}

// fillStrings sets every string field in a struct to marker, recursing into
// embedded structs, so distinct arms carry distinct content.
func fillStrings(v reflect.Value, marker string) {
	if v.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanSet() {
			continue
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString(marker)
		case reflect.Struct:
			fillStrings(f, marker)
		}
	}
}

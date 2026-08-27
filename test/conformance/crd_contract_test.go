package conformance

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// pinnedChart is the artifact every claim below is read from, and pinnedDigest
// is what it must be. A digest mismatch means someone replaced the vendored
// dependency, which is a decision, not a refresh.
const (
	pinnedChart  = "testdata/agentgateway-crds-1.4.1.tgz"
	pinnedDigest = "16a6f36c51ec82832dd3684ea6d45e7c48ca642a6297116878fc02ce3ba8da3a"
	pinnedTag    = "v1.4.1"
)

func load(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(pinnedChart)
	if err != nil {
		t.Fatalf("the pinned chart must be vendored so this suite needs no network: %v", err)
	}
	if got := hex.EncodeToString(sha256Sum(raw)); got != pinnedDigest {
		t.Fatalf("vendored chart digest is %s, pinned is %s — replacing the dependency is a decision, not a refresh", got, pinnedDigest)
	}
	gz, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(h.Name, ".yaml") {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			continue // NOTES.txt and friends
		}
		if md, ok := doc["metadata"].(map[string]any); ok {
			if name, ok := md["name"].(string); ok {
				out[name] = doc
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no CRDs read from the pinned chart — the suite would pass vacuously")
	}
	return out
}

func sha256Sum(b []byte) []byte { s := sha256.Sum256(b); return s[:] }

// dig walks a nested schema by key, returning nil if any hop is absent.
func dig(node any, path ...string) any {
	for _, k := range path {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[k]
	}
	return node
}

// findProp returns the first schema subtree that defines the named property.
func findProp(node any, name string) map[string]any {
	switch v := node.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			if hit, ok := props[name].(map[string]any); ok {
				return hit
			}
		}
		for _, child := range v {
			if got := findProp(child, name); got != nil {
				return got
			}
		}
	case []any:
		for _, child := range v {
			if got := findProp(child, name); got != nil {
				return got
			}
		}
	}
	return nil
}

// findByShape returns the first schema whose properties contain all the named
// fields. Shape survives a rename; a name does not.
func findByShape(node any, fields ...string) map[string]any {
	switch v := node.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			all := true
			for _, f := range fields {
				if _, ok := props[f]; !ok {
					all = false
					break
				}
			}
			if all {
				return v
			}
		}
		for _, child := range v {
			if got := findByShape(child, fields...); got != nil {
				return got
			}
		}
	case []any:
		for _, child := range v {
			if got := findByShape(child, fields...); got != nil {
				return got
			}
		}
	}
	return nil
}

// TestFourCRDsShip pins ADR-0028's "fourth CRD, experimental" claim.
func TestFourCRDsShip(t *testing.T) {
	crds := load(t)
	for _, want := range []string{
		"agentgatewaybackends.agentgateway.dev",
		"agentgatewaypolicies.agentgateway.dev",
		"agentgatewayparameters.agentgateway.dev",
		"agentgatewaymodels.agentgateway.dev",
	} {
		if _, ok := crds[want]; !ok {
			t.Errorf("%s absent at %s; ADR-0028 says four CRDs ship, including the experimental Model", want, pinnedTag)
		}
	}
}

// TestLocalRateLimitShape pins every numeric claim design 03 §3.5 makes.
func TestLocalRateLimitShape(t *testing.T) {
	crds := load(t)
	pol := crds["agentgatewaypolicies.agentgateway.dev"]
	// Located by SHAPE, not by name: the property is `local`, which is a common
	// word, and the schema sits under its `items`. Matching on the field set is
	// what makes this survive a rename and fail on a real removal.
	lrl := findByShape(dig(pol, "spec"), "requests", "tokens", "unit", "burst")
	if lrl == nil {
		t.Fatal("no schema with {requests,tokens,unit,burst} found; design 03 §3.5 emits LocalRateLimit")
	}
	props, _ := lrl["properties"].(map[string]any)

	for _, f := range []string{"requests", "tokens", "burst"} {
		got := dig(props, f, "format")
		if got != "int32" {
			t.Errorf("%s is %v, design 03 §3.5 clamps to int32 because it is int32", f, got)
		}
	}
	// burst carrying no minimum is why plume must validate burst >= 0 itself (A10).
	if _, has := dig(props, "burst").(map[string]any)["minimum"]; has {
		t.Error("burst now has a minimum at " + pinnedTag + "; A10 says plume validates burst >= 0 because the CRD does not")
	}
	for _, f := range []string{"requests", "tokens"} {
		if dig(props, f, "minimum") == nil {
			t.Errorf("%s lost its minimum; §3.5's rate < 1 compile error assumes the API also rejects 0", f)
		}
	}
	// unit offers no Days, which is why a daily budget is emitted hourly.
	enum, _ := dig(props, "unit", "enum").([]any)
	var units []string
	for _, u := range enum {
		units = append(units, u.(string))
	}
	if len(units) != 3 || strings.Contains(strings.Join(units, ","), "Day") {
		t.Errorf("unit enum is %v; §3.5 emits unit: Hours because a daily window is not expressible", units)
	}
	req, _ := lrl["required"].([]any)
	if len(req) != 1 || req[0] != "unit" {
		t.Errorf("required is %v; §3.5 always emits unit", req)
	}
	// The doc comment is the sentence ADR-0028 Amendment 1 rests on.
	if d, _ := dig(props, "tokens", "description").(string); !strings.Contains(d, "future requests only") {
		t.Error("tokens no longer documents 'future requests only'; ADR-0028 withdrew 'cuts early, never late' on that sentence")
	}
	blob, _ := json.Marshal(lrl["x-kubernetes-validations"])
	if !strings.Contains(string(blob), "requests") || !strings.Contains(string(blob), "tokens") {
		t.Error("ExactlyOneOf(requests, tokens) is gone; §3.5 says one policy cannot carry both")
	}
}

// TestNoEgressFieldExists pins design 03 §3.4.1 — the control is Backend
// construction because no restriction field exists to use instead.
func TestNoEgressFieldExists(t *testing.T) {
	crds := load(t)
	blob, err := json.Marshal(crds["agentgatewaybackends.agentgateway.dev"])
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(blob))
	// The one legitimate hit is the MCP JSON-RPC method allowlist.
	if n := strings.Count(body, "allowlist"); n != 1 {
		t.Errorf("Backend mentions 'allowlist' %d times, expected exactly 1 (the MCP methods list); §3.4.1 says no egress allowlist field exists", n)
	}
	if strings.Contains(body, "egress") {
		t.Error("Backend now has an egress field; §3.4.1 compiles to construction because it did not")
	}
}

// TestAzureOpenAIHasNoModelField is the claim A18 got wrong, twenty lines below
// its own table. A19 made a models-scoped entry a compile error for this arm.
func TestAzureOpenAIHasNoModelField(t *testing.T) {
	crds := load(t)
	back := crds["agentgatewaybackends.agentgateway.dev"]
	azure := findProp(dig(back, "spec"), "azureopenai")
	if azure == nil {
		t.Fatal("azureopenai arm not found")
	}
	props, _ := azure["properties"].(map[string]any)
	if _, has := props["model"]; has {
		t.Error("azureopenai now carries model; A19 rejects models-scoped entries for this arm precisely because it does not")
	}
	for _, f := range []string{"apiVersion", "deploymentName", "endpoint"} {
		if _, has := props[f]; !has {
			t.Errorf("azureopenai lost %s; A18's endpoint-identity tuple names it", f)
		}
	}
	// The arms that DO carry model are what makes a models-scoped entry enforceable.
	for _, arm := range []string{"anthropic", "vertexai", "bedrock"} {
		a := findProp(dig(back, "spec"), arm)
		if a == nil {
			t.Errorf("%s arm absent", arm)
			continue
		}
		if p, _ := a["properties"].(map[string]any); p["model"] == nil {
			t.Errorf("%s lost its model field; A19 compiles models-scoped entries into it", arm)
		}
	}
}

// TestMCPAuthorizationBounds pins the limits design 03 §3.4.2 must emit within.
func TestMCPAuthorizationBounds(t *testing.T) {
	crds := load(t)
	m := findProp(dig(crds["agentgatewaypolicies.agentgateway.dev"], "spec"), "matchExpressions")
	if m == nil {
		t.Fatal("matchExpressions not found; §3.4.2 emits MCP authorization through it")
	}
	if got := m["minItems"]; got == nil || got.(float64) != 1 {
		t.Errorf("minItems is %v; §3.4.2 needs it to know an empty allowed-tool set has no representation", got)
	}
	if got := m["maxItems"]; got == nil || got.(float64) != 256 {
		t.Errorf("maxItems is %v; §3.4.2 bounds the emitted tool set on it", got)
	}
	if got := dig(m, "items", "maxLength"); got == nil || got.(float64) != 16384 {
		t.Errorf("per-expression maxLength is %v; §3.4.2 bounds CEL length on it", got)
	}
}

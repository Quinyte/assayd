// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"
)

// The two agentgateway CRD charts A75's classification is pinned against:
// 1.4.1, the release design 03 is written to, and 1.5.0, the release the e2e
// and the slice's conformance cases run. Both are vendored in
// test/conformance/testdata and pinned by digest, so replacing one is a
// decision and not a refresh.
var vendoredCRDCharts = map[string]string{
	"1.4.1": "16a6f36c51ec82832dd3684ea6d45e7c48ca642a6297116878fc02ce3ba8da3a",
	"1.5.0": "a6e554e344e4d57e426fd04a9f54e57ea605bc58511d2f65a5626d741e7a73fa",
}

func chartVersions() []string {
	out := make([]string, 0, len(vendoredCRDCharts))
	for v := range vendoredCRDCharts {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// vendoredPolicyCRD is the AgentgatewayPolicy CRD in one vendored chart: the
// artifact, never a fixture written here.
func vendoredPolicyCRD(t *testing.T, version string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	chart := "agentgateway-crds-" + version + ".tgz"
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "conformance", "testdata", chart))
	if err != nil {
		t.Fatalf("read the vendored chart %s: %v", chart, err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != vendoredCRDCharts[version] {
		t.Fatalf("%s has digest %s, pinned %s: replacing the dependency is a decision, not a refresh",
			chart, got, vendoredCRDCharts[version])
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("%s carries no agentgateway.dev_agentgatewaypolicies.yaml", chart)
		}
		if err != nil {
			t.Fatal(err)
		}
		if path.Base(h.Name) != "agentgateway.dev_agentgatewaypolicies.yaml" {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(b, &crd); err != nil {
			t.Fatalf("parse the policy CRD in %s: %v", chart, err)
		}
		return &crd
	}
}

// classifiedSections are the `spec` sections A75 classifies field by field.
// targetRefs and targetSelectors are what a policy targets, which A75's
// target rule reads.
var classifiedSections = []string{"backend", "frontend", "strategy", "traffic"}

// tableRow is one row of design 03's field table.
type tableRow struct{ class, in string }

// design03FieldTable reads the field table under design 03's heading
// "Gateway-level policies above the route (A75)": every row whose first cell
// is `<section>.<field>`.
func design03FieldTable(t *testing.T) map[string]tableRow {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "docs", "designs", "03-policy-compiler.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	const heading = "#### Gateway-level policies above the route (A75)"
	row := regexp.MustCompile("^\\| `((?:traffic|backend|frontend|strategy)\\.[A-Za-z]+)` \\| ([^|]+) \\| ([^|]+) \\|")
	out := map[string]tableRow{}
	in := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == heading:
			in = true
			continue
		case in && (strings.HasPrefix(line, "#### ") || strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "## ")):
			in = false
		}
		if !in {
			continue
		}
		if m := row.FindStringSubmatch(line); m != nil {
			if _, dup := out[m[1]]; dup {
				t.Errorf("design 03's field table lists %s twice", m[1])
			}
			out[m[1]] = tableRow{class: strings.TrimSpace(m[2]), in: strings.TrimSpace(m[3])}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("design 03 has no field table under %q; A75's classification has no source", heading)
	}
	return out
}

// TestTheFieldClassificationIsDesign03sTable pins three things together:
// design 03 A75's field table, the code's harmlessPolicyFields and
// wideningPolicyFields, and every field of every classified `spec` section of
// both vendored CRDs. A field a CRD adds, one it renames, a section it adds,
// or a table row the code disagrees with, fails here until a person decides.
func TestTheFieldClassificationIsDesign03sTable(t *testing.T) {
	table := design03FieldTable(t)
	for field, r := range table {
		switch r.class {
		case "harmless":
			if !harmlessPolicyFields[field] || wideningPolicyFields[field] {
				t.Errorf("design 03 calls %s harmless, and the code does not", field)
			}
		case "counts":
			if harmlessPolicyFields[field] || wideningPolicyFields[field] {
				t.Errorf("design 03 says %s counts and does not widen, and the code disagrees", field)
			}
		case "counts, widens":
			if harmlessPolicyFields[field] || !wideningPolicyFields[field] {
				t.Errorf("design 03 says %s counts and widens a served route, and the code disagrees", field)
			}
		case "by value":
			if field != "strategy.inheritance" {
				t.Errorf("design 03 classifies %s by value; only strategy.inheritance is read so", field)
			}
		default:
			t.Errorf("design 03's field table gives %s the class %q", field, r.class)
		}
	}
	for f := range harmlessPolicyFields {
		if _, ok := table[f]; !ok {
			t.Errorf("the code calls %s harmless, and design 03's table has no row for it", f)
		}
	}
	for f := range wideningPolicyFields {
		if _, ok := table[f]; !ok {
			t.Errorf("the code says %s widens, and design 03's table has no row for it", f)
		}
	}
	seen := map[string]map[string]bool{}
	for _, version := range chartVersions() {
		for _, v := range vendoredPolicyCRD(t, version).Spec.Versions {
			spec := v.Schema.OpenAPIV3Schema.Properties["spec"]
			for section := range spec.Properties {
				known := section == "targetRefs" || section == "targetSelectors"
				for _, s := range classifiedSections {
					known = known || s == section
				}
				if !known {
					t.Errorf("%s %s: spec.%s is a section A75 does not classify. Until its fields have rows "+
						"in design 03's table, a Gateway-level policy setting one holds every -auth "+
						"transaction", version, v.Name, section)
				}
			}
			for _, section := range classifiedSections {
				for f := range spec.Properties[section].Properties {
					field := section + "." + f
					if seen[field] == nil {
						seen[field] = map[string]bool{}
					}
					seen[field][version] = true
					if _, ok := table[field]; !ok {
						t.Errorf("%s %s: %s has no row in design 03's field table (A75). Decide it there, "+
							"then in harmlessPolicyFields if it cannot answer the probe", version, v.Name, field)
					}
				}
			}
		}
	}
	for field, r := range table {
		var want []string
		for _, version := range chartVersions() {
			if seen[field][version] {
				want = append(want, version)
			}
		}
		if got := strings.Join(want, ", "); r.in != got {
			t.Errorf("design 03's table says %s is in %q; the vendored CRDs define it in %q", field, r.in, got)
		}
	}
}

// TestTheKindsATrafficPolicyTargetsArePinned: the CRD lets a `traffic` policy
// target a Gateway, ListenerSet, GRPCRoute, HTTPRoute or InferencePool. A75
// reads the Gateway, holds on any ListenerSet the Gateway admits, and leaves
// HTTPRoute to §3.2's route-level detection; a GRPCRoute or InferencePool does
// not carry the serving HTTPRoute. A kind a CRD adds may sit above the route.
func TestTheKindsATrafficPolicyTargetsArePinned(t *testing.T) {
	kinds := regexp.MustCompile(`t\.kind in \[([^\]]*)\]`)
	want := []string{"GRPCRoute", "Gateway", "HTTPRoute", "InferencePool", "ListenerSet"}
	for _, version := range chartVersions() {
		for _, v := range vendoredPolicyCRD(t, version).Spec.Versions {
			seen := 0
			for _, rule := range v.Schema.OpenAPIV3Schema.Properties["spec"].XValidations {
				if !strings.Contains(rule.Message, "'traffic' field can only target") {
					continue
				}
				m := kinds.FindStringSubmatch(rule.Rule)
				if m == nil {
					t.Fatalf("%s %s: the traffic target rule no longer lists kinds: %s", version, v.Name, rule.Rule)
				}
				var got []string
				for _, k := range strings.Split(m[1], ",") {
					got = append(got, strings.Trim(strings.TrimSpace(k), "'"))
				}
				sort.Strings(got)
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("%s %s: a traffic policy may target %v, where A75 was written for %v",
						version, v.Name, got, want)
				}
				seen++
			}
			if seen != 2 {
				t.Errorf("%s %s: want the traffic target rule for targetRefs and targetSelectors, found %d",
					version, v.Name, seen)
			}
		}
	}
}

func policySpec(spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
}

func TestWhichPoliciesCountAndWhichWiden(t *testing.T) {
	set := func(section, field string, v any) map[string]any {
		return map[string]any{section: map[string]any{field: v}}
	}
	for _, c := range []struct {
		name           string
		spec           map[string]any
		counts, widens bool
	}{
		{"a harmless traffic field", set("traffic", "timeouts", map[string]any{}), false, false},
		{"a harmless frontend field", set("frontend", "accessLog", map[string]any{}), false, false},
		{"a harmless backend field", set("backend", "health", map[string]any{}), false, false},
		{"API-key authentication", set("traffic", "apiKeyAuthentication", map[string]any{}), true, false},
		{"authorization", set("traffic", "authorization", map[string]any{}), true, true},
		{"transformation", set("traffic", "transformation", map[string]any{}), true, false},
		{"backend.extAuth", set("backend", "extAuth", map[string]any{}), true, false},
		{"a field nothing has classified", set("traffic", "neverClassified", true), true, false},
		{"a section nothing has classified", set("sidecar", "anything", true), true, false},
		{"Override beside harmless fields", map[string]any{
			"traffic": map[string]any{"timeouts": map[string]any{}}, "strategy": map[string]any{"inheritance": "Override"}},
			true, true},
		{"Default beside harmless fields", map[string]any{
			"traffic": map[string]any{"timeouts": map[string]any{}}, "strategy": map[string]any{"inheritance": "Default"}},
			false, false},
		{"targets alone", map[string]any{"targetRefs": []any{}}, false, false},
	} {
		counts, widens := classifyPolicy(policySpec(c.spec))
		if counts != c.counts || widens != c.widens {
			t.Errorf("%s: counts=%v widens=%v, want %v %v", c.name, counts, widens, c.counts, c.widens)
		}
	}
}

func TestAPolicyTargetsTheGatewayOnlyOnAListenerThatCanTakeTheRoute(t *testing.T) {
	host := "pricer.team.assayd.internal"
	hn := func(s string) *gatewayv1.Hostname { h := gatewayv1.Hostname(s); return &h }
	gw := &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{
		{Name: GatewayListenerName, Port: 8080},
		{Name: "tools", Port: 8081},
		{Name: "edge", Port: 8080, Hostname: hn("*.assayd.internal")},
		{Name: "other", Port: 8080, Hostname: hn("other.example.com")},
		{Name: "exact", Port: 8080, Hostname: hn(host)},
	}}}
	capture := capturingListeners(gw, host)
	labels := map[string]string{"role": "serving"}
	ref := func(group, kind, name, section string) map[string]any {
		m := map[string]any{"group": group, "kind": kind, "name": name}
		if section != "" {
			m["sectionName"] = section
		}
		return map[string]any{"targetRefs": []any{m}}
	}
	sel := func(kind string, want map[string]any, section string) map[string]any {
		m := map[string]any{"group": gatewayv1.GroupName, "kind": kind, "matchLabels": want}
		if section != "" {
			m["sectionName"] = section
		}
		return map[string]any{"targetSelectors": []any{m}}
	}
	for _, c := range []struct {
		name string
		spec map[string]any
		want bool
	}{
		{"the Gateway", ref(gatewayv1.GroupName, "Gateway", "assayd", ""), true},
		{"the Gateway, group left empty", ref("", "Gateway", "assayd", ""), true},
		{"the serving listener", ref(gatewayv1.GroupName, "Gateway", "assayd", GatewayListenerName), true},
		{"a same-port listener whose wildcard matches the host", ref(gatewayv1.GroupName, "Gateway", "assayd", "edge"), true},
		{"a same-port listener naming the host", ref(gatewayv1.GroupName, "Gateway", "assayd", "exact"), true},
		{"a same-port listener for another host", ref(gatewayv1.GroupName, "Gateway", "assayd", "other"), false},
		{"the tools listener, on another port", ref(gatewayv1.GroupName, "Gateway", "assayd", "tools"), false},
		{"another Gateway", ref(gatewayv1.GroupName, "Gateway", "elsewhere", ""), false},
		{"another group's Gateway", ref("example.com", "Gateway", "assayd", ""), false},
		{"a route of the Gateway's name", ref(gatewayv1.GroupName, "HTTPRoute", "assayd", ""), false},
		{"a ListenerSet", ref(gatewayv1.GroupName, "ListenerSet", "assayd", ""), false},
		{"a selector matching the Gateway's labels", sel("Gateway", map[string]any{"role": "serving"}, ""), true},
		{"a selector on the tools listener", sel("Gateway", map[string]any{"role": "serving"}, "tools"), false},
		{"a selector matching other labels", sel("Gateway", map[string]any{"role": "edge"}, ""), false},
		{"a ListenerSet selector", sel("ListenerSet", map[string]any{"role": "serving"}, ""), false},
	} {
		if got := targetsGateway(policySpec(c.spec), "assayd", labels, capture, true); got != c.want {
			t.Errorf("%s: targets=%v, want %v", c.name, got, c.want)
		}
	}
	// A Gateway that could not be read has unknown listeners and labels, so
	// every sectionName captures and every Gateway selector matches; another
	// Gateway's name and another kind still do not.
	for _, c := range []struct {
		name string
		spec map[string]any
		want bool
	}{
		{"the tools listener", ref(gatewayv1.GroupName, "Gateway", "assayd", "tools"), true},
		{"a selector matching other labels", sel("Gateway", map[string]any{"role": "edge"}, ""), true},
		{"another Gateway", ref(gatewayv1.GroupName, "Gateway", "elsewhere", ""), false},
		{"a ListenerSet selector", sel("ListenerSet", map[string]any{"role": "serving"}, ""), false},
	} {
		if got := targetsGateway(policySpec(c.spec), "assayd", nil, nil, false); got != c.want {
			t.Errorf("unread Gateway, %s: targets=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestAGatewayAdmitsListenerSetsUnlessFromIsNone(t *testing.T) {
	from := func(f gatewayv1.FromNamespaces) *gatewayv1.Gateway {
		return &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{AllowedListeners: &gatewayv1.AllowedListeners{
			Namespaces: &gatewayv1.ListenerNamespaces{From: &f}}}}
	}
	for _, c := range []struct {
		name string
		gw   *gatewayv1.Gateway
		want string
	}{
		{"absent", &gatewayv1.Gateway{}, ""},
		{"{}", &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{AllowedListeners: &gatewayv1.AllowedListeners{}}}, ""},
		{"None", from(gatewayv1.NamespacesFromNone), ""},
		{"Same", from(gatewayv1.NamespacesFromSame), "Same"},
		{"Selector", from(gatewayv1.NamespacesFromSelector), "Selector"},
		{"All", from(gatewayv1.NamespacesFromAll), "All"},
	} {
		if got := admitsListenerSets(c.gw); got != c.want {
			t.Errorf("%s: admits=%q, want %q", c.name, got, c.want)
		}
	}
}

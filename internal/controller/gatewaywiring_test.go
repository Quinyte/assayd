// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

// The message agentgateway wrote when the proxy rejected a policy, verbatim as
// research/agentgateway-v1.4.1-spike.md §2.8 recorded it. A fixture, and a
// labelled one: it is the only shape measured, and a parser tested against a
// shape nobody observed would prove only that it agrees with itself.
const measuredNack = `[{"key":"policy/traffic/default/p:rl-local:default/llm-route",` +
	`"error":"error: invalid rate limit: max tokens cannot be less than the refill amount"}]`

func TestANackNamesThePolicyItRejected(t *testing.T) {
	got, _ := NackedPolicies(measuredNack)
	want := []types.NamespacedName{{Namespace: "default", Name: "p"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NackedPolicies(measured) = %v, want %v. The key is "+
			"policy/<kind>/<ns>/<name>:<section>:<ns>/<route> (design 03 §3.3): the policy is the "+
			"part before the first colon, and the route after it is not the policy", got, want)
	}
}

func TestANackNamesOnlyPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, msg string
		want      []types.NamespacedName
	}{
		{"two policies in one event",
			`[{"key":"policy/traffic/run-a/x-auth:auth:run-a/x-serving"},` +
				`{"key":"policy/traffic/run-b/y-auth:auth:run-b/y-serving"}]`,
			[]types.NamespacedName{{Namespace: "run-a", Name: "x-auth"}, {Namespace: "run-b", Name: "y-auth"}}},
		{"a key for something that is not a policy",
			`[{"key":"backend/run-a/tools"},{"key":"policy/traffic/run-a/x-auth:auth:run-a/x-serving"}]`,
			[]types.NamespacedName{{Namespace: "run-a", Name: "x-auth"}}},
		{"a policy key missing its namespace", `[{"key":"policy/traffic/x-auth:auth:run-a/x-serving"}]`, nil},
		{"a policy key with an empty name", `[{"key":"policy/traffic/run-a/:auth:run-a/x-serving"}]`, nil},
		{"not JSON", `the proxy rejected something`, nil},
		{"an empty array", `[]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := NackedPolicies(tc.msg); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NackedPolicies(%s) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func nackMessage(keys ...string) string {
	entries := make([]string, len(keys))
	for i, k := range keys {
		entries[i] = fmt.Sprintf(`{"key":%q,"error":"rejected"}`, k)
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// Each policy a NACK names is one live read in the watch's handler, so a
// policy named twice is read once.
func TestANackReadsEachPolicyOnce(t *testing.T) {
	k := "policy/traffic/run-a/x-auth:auth:run-a/x-serving"
	got, dropped := NackedPolicies(nackMessage(k, k, k))
	if want := []types.NamespacedName{{Namespace: "run-a", Name: "x-auth"}}; !reflect.DeepEqual(got, want) || dropped != 0 {
		t.Errorf("a policy named three times gave %v (dropped %d), want it once", got, dropped)
	}
}

// And a NACK names at most maxNackedPolicies of them: anyone who can write an
// Event in the Gateway's namespace chooses how many keys it holds.
func TestANackReadsABoundedNumberOfPolicies(t *testing.T) {
	keys := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		keys = append(keys, fmt.Sprintf("policy/traffic/run-a/p%d:auth:run-a/r", i))
	}
	got, dropped := NackedPolicies(nackMessage(keys...))
	if len(got) != maxNackedPolicies || dropped != 100-maxNackedPolicies {
		t.Fatalf("100 policies gave %d read and %d dropped, want %d and %d",
			len(got), dropped, maxNackedPolicies, 100-maxNackedPolicies)
	}
	if got[0].Name != "p0" || got[maxNackedPolicies-1].Name != fmt.Sprintf("p%d", maxNackedPolicies-1) {
		t.Errorf("the bound kept %v and %v, want the first %d in order", got[0], got[len(got)-1], maxNackedPolicies)
	}
}

// --gateway-serving-url names a listener, and design 03 §3.3.3's probe appends
// the Agent's card path to it. So anything past host and port is refused, and
// so is anything that is not an http(s) URL at all.
func TestTheServingURLNamesAListenerAndNothingElse(t *testing.T) {
	for _, ok := range []string{
		"http://assayd.assayd-gateway.svc.cluster.local:8080",
		"https://gw.example",
		"http://10.0.0.1:8080",
		"http://gw.example:65535",
		"http://[fd00::1]:8080",
	} {
		if err := ValidateGatewayServingURL(ok); err != nil {
			t.Errorf("%q was refused: %v", ok, err)
		}
	}
	for _, bad := range []struct{ url, want string }{
		{"gw.example:8080", "absolute http or https"},
		{"ftp://gw.example", "absolute http or https"},
		{"//gw.example:8080", "absolute http or https"},
		{"http://", "no host"},
		{"http://:8080", "no host"},
		{"http://probe:secret@gw.example:8080", "user information"},
		{"http://gw.example:8080/prefix", "has a path"},
		// The probe appends a card path that starts with "/", so even a bare
		// trailing slash would make it "//.well-known/…".
		{"http://gw.example:8080/", "has a path"},
		{"http://gw.example:99999", "not a port from 1 to 65535"},
		{"http://gw.example:0", "not a port from 1 to 65535"},
		{"http://gw.example:8080?x=1", "query or a fragment"},
		{"http://gw.example:8080?", "query or a fragment"},
		{"http://gw.example:8080#f", "query or a fragment"},
		{"http://gw example", "not a URL"},
	} {
		err := ValidateGatewayServingURL(bad.url)
		if err == nil {
			t.Errorf("%q was accepted", bad.url)
			continue
		}
		if !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%q was refused for the wrong reason: %v (want %q)", bad.url, err, bad.want)
		}
	}
}

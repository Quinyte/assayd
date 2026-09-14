// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Quinyte/assayd/internal/controller"
)

// Design 07 A6.15, the human's T1, as the chart renders it:
// admission.toolRouteWriters reaches assayd-gateway-routes, and only it. What
// the rule DOES for each identity is measured against a real API server in
// test/envtest (admission_toolroutes_test.go); this pins what the chart says,
// so that a value an administrator believes they configured cannot render
// nothing, and a narrowing cannot quietly widen.

// The rendered identities, exactly: with the value empty, which is the
// default, no identity but the operator's may attach a route on any listener.
const noToolWriter = `request.userInfo.username in [] || (has(request.userInfo.groups) && ` +
	`request.userInfo.groups.exists(g, g in []))`

func routePolicy(t *testing.T, args ...string) map[string]any {
	t.Helper()
	p := byName(render(t, args...), "ValidatingAdmissionPolicy")["assayd-gateway-routes"]
	if p == nil {
		t.Fatal("the chart renders no ValidatingAdmissionPolicy assayd-gateway-routes (design 07 A5.9)")
	}
	return p
}

func TestTheToolRouteWritersReachTheRouteReservation(t *testing.T) {
	// Empty: nobody but the operator. This is the only identity list the
	// default render may carry, and the operator's is unchanged.
	def := routePolicy(t)
	if got := variable(t, def, "toolWriter"); got != noToolWriter {
		t.Errorf("with admission.toolRouteWriters empty the route reservation admits %s, want exactly %s",
			got, noToolWriter)
	}
	if got, want := variable(t, def, "operator"), `request.userInfo.username in ["`+operatorSA+`"]`; got != want {
		t.Errorf("the route reservation's operators are %s, want exactly %s", got, want)
	}

	// Set: users into the username list and groups into the group list, and
	// into this policy only.
	docs := render(t,
		"--set", "admission.toolRouteWriters.users={tool-author}",
		"--set", "admission.toolRouteWriters.groups={tool-authors}")
	policies := byName(docs, "ValidatingAdmissionPolicy")
	want := `request.userInfo.username in ["tool-author"] || (has(request.userInfo.groups) && ` +
		`request.userInfo.groups.exists(g, g in ["tool-authors"]))`
	if got := variable(t, policies["assayd-gateway-routes"], "toolWriter"); got != want {
		t.Errorf("admission.toolRouteWriters renders %s, want exactly %s", got, want)
	}
	// A tool route writer is not an operator anywhere: not for routes on the
	// serving listener, not for namespace labels, not for AgentgatewayPolicies,
	// and not a key writer.
	for policy, v := range map[string]string{
		"assayd-gateway-routes":   "operator",
		"assayd-namespace-labels": "operator",
		"assayd-gateway-policies": "operator",
		"assayd-api-keys":         "writer",
	} {
		if got := variable(t, policies[policy], v); strings.Contains(got, "tool-author") {
			t.Errorf("a tool route writer reaches %s's %s: %s", policy, v, got)
		}
	}

	// The serving listener is the operator's own constant: the listener its
	// emitted route attaches to is the one the reservation keeps.
	serving := variable(t, def, "serving")
	if n := strings.Count(serving, `p.sectionName == "`+controller.GatewayListenerName+`"`); n != 2 {
		t.Errorf("the reservation's serving listener is not %q on both the new and the old object: %s",
			controller.GatewayListenerName, serving)
	}
	for _, v := range []string{"attached", "wasAttached"} {
		if !strings.Contains(variable(t, def, v), "request.namespace") {
			t.Errorf("%s does not resolve a bare-name parentRef to the route's namespace: %s", v, variable(t, def, v))
		}
	}
	if !strings.Contains(variable(t, def, "wasAttached"), "oldObject") {
		t.Errorf("wasAttached does not read the old object, so an UPDATE could take a route off the serving "+
			"listener: %s", variable(t, def, "wasAttached"))
	}

	// The serving hosts are the ones the operator emits: its default suffix
	// when the value is empty, the value when set, lower-cased as a hostname
	// must be.
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, controller.DefaultGatewayHostnameSuffix},
		{[]string{"--set", "gateway.hostnameSuffix="}, controller.DefaultGatewayHostnameSuffix},
		{[]string{"--set", "gateway.hostnameSuffix=Agents.Example"}, "agents.example"},
	} {
		hosts := variable(t, routePolicy(t, c.args...), "hostsClear")
		if n := strings.Count(hosts, `"`+c.want+`"`); n != 3 {
			t.Errorf("with %v the reservation clears hostnames against %s, want the suffix %q in all three "+
				"clauses", c.args, hosts, c.want)
		}
	}

	// The reservation stays cluster-wide: a route in any namespace can name
	// the Gateway, so a namespaceSelector would leave some namespace unguarded.
	spec := dig(def, "spec")
	// Both kinds that attach to an HTTP listener with parentRefs and
	// hostnames. Leaving GRPCRoute out would leave the serving listener open to
	// any author of one.
	var resources []string
	for _, r := range toList(dig(spec, "matchConstraints")["resourceRules"]) {
		rm, _ := r.(map[string]any)
		resources = append(resources, toStrings(rm["resources"])...)
	}
	sort.Strings(resources)
	if !reflect.DeepEqual(resources, []string{"grpcroutes", "httproutes"}) {
		t.Errorf("assayd-gateway-routes matches %v, want exactly httproutes and grpcroutes", resources)
	}
	if sel := dig(dig(spec, "matchConstraints"), "namespaceSelector"); len(sel) != 0 {
		t.Errorf("assayd-gateway-routes matches namespaces by %v; it must match every namespace", sel)
	}
	// And a no-op UPDATE is not a write, so the garbage collector can finish a
	// foreground delete; a CREATE always is.
	if touched := variable(t, def, "touched"); !strings.Contains(touched, "oldObject == null") {
		t.Errorf("assayd-gateway-routes's touched does not treat a CREATE as a write: %s", touched)
	}
	var messages []string
	for _, v := range toList(spec["validations"]) {
		vm, _ := v.(map[string]any)
		messages = append(messages, toStr(vm["message"]))
	}
	if len(messages) != 3 {
		t.Fatalf("assayd-gateway-routes has %d validations, want 3 (who, the serving listener, the hostnames): %v",
			len(messages), messages)
	}
	for i, frag := range []string{"admission.toolRouteWriters", "serving listener `" + controller.GatewayListenerName + "`",
		"<agent>.<namespace>." + controller.DefaultGatewayHostnameSuffix} {
		if !strings.Contains(messages[i], frag) {
			t.Errorf("validation %d's message does not name %q, so its refusal does not say how to fix it: %s",
				i, frag, messages[i])
		}
	}
	if got := toStrings(dig(byName(docs, "ValidatingAdmissionPolicyBinding")["assayd-gateway-routes"],
		"spec")["validationActions"]); !reflect.DeepEqual(got, []string{"Deny"}) {
		t.Errorf("assayd-gateway-routes's binding has validationActions %v; a reservation that only warns "+
			"reserves nothing", got)
	}
}

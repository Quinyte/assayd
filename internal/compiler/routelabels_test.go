// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package compiler

import "testing"

// §3.3.1: the marker is present exactly while the route is published under
// `auth: none`, and absent otherwise. Both halves are asserted, because a
// helper that can only add is how a planted or stale marker survives: a route
// published under the API-key policy that still says "deliberately
// unauthenticated" tells a reviewer the opposite of the truth.
func TestTheMarkerIsPresentExactlyWhileTheRouteIsPublishedUnderNone(t *testing.T) {
	base := map[string]string{LabelAgent: "pricer", "assayd.dev/revision": "abc1234567"}

	none := RouteAuthLabels(AuthModeNone, base)
	if none[LabelAuth] != "none" {
		t.Errorf("a route published under none carries %s=%q; §3.3.1's marker is exactly \"none\"",
			LabelAuth, none[LabelAuth])
	}
	if len(none) != len(base)+1 || none[LabelAgent] != "pricer" || none["assayd.dev/revision"] != "abc1234567" {
		t.Errorf("marking the route changed its other labels: %v", none)
	}

	planted := map[string]string{LabelAgent: "pricer", LabelAuth: "none"}
	for _, mode := range []AuthMode{AuthModeAPIKey, ""} {
		cleared := RouteAuthLabels(mode, planted)
		if _, ok := cleared[LabelAuth]; ok {
			t.Errorf("a route published under %q keeps %s=%q; the marker must be absent whenever "+
				"the route is not published unauthenticated at the owner's request",
				mode, LabelAuth, cleared[LabelAuth])
		}
		if cleared[LabelAgent] != "pricer" {
			t.Errorf("clearing the marker lost the route's other labels: %v", cleared)
		}
	}
}

// The labels map is copied, never written through. An emitter that passes a
// map it shares — the desired object's, or one read from the cache — would
// otherwise have the marker appear or vanish on an object nobody meant to
// change.
func TestRouteAuthLabelsNeverWritesThroughItsInput(t *testing.T) {
	shared := map[string]string{LabelAgent: "pricer"}
	_ = RouteAuthLabels(AuthModeNone, shared)
	if _, ok := shared[LabelAuth]; ok {
		t.Error("marking wrote the marker into the caller's map")
	}

	sharedPlanted := map[string]string{LabelAgent: "pricer", LabelAuth: "none"}
	_ = RouteAuthLabels(AuthModeAPIKey, sharedPlanted)
	if sharedPlanted[LabelAuth] != "none" {
		t.Error("clearing the marker deleted it from the caller's map")
	}
}

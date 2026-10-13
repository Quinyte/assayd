// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"path/filepath"
	"testing"
)

// T38 (design 16 §1.1, A6). The kubelet sends SIGTERM, waits
// terminationGracePeriodSeconds, then kills the operator. The manager's own
// GracefulShutdownTimeout (T39) waits for a reconcile in flight, and an eval
// case in flight is bounded at 5 s + 10 s + 5 s = 20 s. A grace period shorter
// than that turns every helm upgrade, drain and rolling update into a hard stop
// that leaves a claimed case unrecorded. The field must be rendered: absent, the
// floor would rest on the kubelet's default, which is rule 7's case.
func TestOperatorGracePeriodLetsACaseInFlightFinish(t *testing.T) {
	const floor = 25
	for _, profile := range []struct {
		name string
		args []string
	}{
		{"prod", nil},
		{"local", []string{"-f", filepath.Join(chartPath, "values-local.yaml")}},
	} {
		deploys := kindsOf(render(t, profile.args...), "Deployment")
		if len(deploys) == 0 {
			t.Fatalf("the %s profile renders no operator Deployment", profile.name)
		}
		for _, d := range deploys {
			spec := dig(d, "spec", "template", "spec")
			v, ok := spec["terminationGracePeriodSeconds"]
			if !ok {
				t.Errorf("%s at the %s profile renders no terminationGracePeriodSeconds, so the "+
					"shutdown floor rests on the kubelet's default (design 16 A6)", nameOf(d), profile.name)
				continue
			}
			if n := toNum(v); n < floor {
				t.Errorf("%s at the %s profile has terminationGracePeriodSeconds %d; it must be at "+
					"least %d so an eval case in flight, bounded at 20 s, can finish and record "+
					"(design 16 A6)", nameOf(d), profile.name, n, floor)
			}
		}
	}
}

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 03 §8.1 case 9, at the layer that decides it. PolicyCompileFailed is
// owned, abnormal-true and not sticky: once its cause goes, a pass that no
// longer asserts it must CLEAR it. Asserted through merge() rather than by
// reading ownedTypes, because a test that reads the map observes no behaviour
// (TestTheTierConditionSurvivesAPassThatDoesNotSpeakToIt records that failure).
//
// Left out of ownedTypes, merge()'s default arm carries it forward as another
// controller's — forever, beside Ready=True, on an Agent whose owner reverted
// the edit that caused it. That is the bug class conditions.go records for
// RevisionHashCollision. Listed in stickyTypes instead, it would survive as a
// record, which is wrong for an incident whose absence is the signal.
func TestPolicyCompileFailedClearsOnAPassThatDoesNotAssertIt(t *testing.T) {
	prior := []metav1.Condition{{
		Type: string(assaydv1alpha1.CondPolicyCompileFailed), Status: metav1.ConditionTrue,
		Reason: "AuthInputAbsent", Message: "spec.expose.a2a.auth is oauth, which the slice cannot compile",
	}}

	got := newConditionSet(2).merge(prior)

	for _, c := range got {
		if c.Type == string(assaydv1alpha1.CondPolicyCompileFailed) {
			t.Errorf("PolicyCompileFailed survived a pass that did not assert it, as %s/%s. It is "+
				"owned and abnormal-true (design 03 §1.1, §3.3.1): its absence is the signal that "+
				"the input compiles again, so carrying it forward reports a failure the owner "+
				"already fixed.", c.Status, c.Reason)
		}
	}
}

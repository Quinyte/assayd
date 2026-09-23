// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// A record is replaced only by one for the same revision AND digest.
//
// Replacing by revision alone let an adoption at another projection of the
// same 40-bit name overwrite the existing record, so "adoption never
// overwrites a record" held only per revision and digest (round four of
// design 02 A77's review, MINOR 6).
func TestARecordIsReplacedOnlyAtTheSameRevisionAndDigest(t *testing.T) {
	var st assaydv1alpha1.AgentStatus
	recordServiceUID(&st, "rev", "sha256:d1", "uid-1")
	adoptServiceRecord(&st, "rev", "sha256:d2", &corev1.Service{ObjectMeta: metav1.ObjectMeta{UID: "uid-2"}})
	if got := recordedServiceUID(&st, "rev", "sha256:d1"); got != "uid-1" {
		t.Errorf("an adoption at another digest replaced the record for the first: %q", got)
	}
	if got := recordedServiceUID(&st, "rev", "sha256:d2"); got != "uid-2" {
		t.Errorf("the adoption at the second digest was not recorded: %q", got)
	}
	recordServiceUID(&st, "rev", "sha256:d1", "uid-3")
	if got := recordedServiceUID(&st, "rev", "sha256:d1"); got != "uid-3" || len(st.RevisionServices) != 2 {
		t.Errorf("a create at the same revision and digest did not replace its record: %q, %d entries",
			got, len(st.RevisionServices))
	}
}

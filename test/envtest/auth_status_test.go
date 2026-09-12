// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// status.auth is design 03 §3.3's schema, owed to design 02 and shipped by the
// first slice before anything writes it. What this layer uniquely proves is
// that every field the compiler will write survives the real status
// subresource: a structural schema PRUNES an unknown field without an error,
// so a field missing from the CRD would read back absent and the operator
// would never learn it had written nothing.
//
// Nothing here is a state the design reaches. The first case sets every field
// at once, which no transaction does, because the point is the schema, not a
// transaction.
func TestStatusAuthRoundTripsThroughTheStatusSubresource(t *testing.T) {
	i32 := func(v int32) *int32 { return &v }
	deadline := metav1.NewTime(time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC))

	for _, tc := range []struct {
		name string
		auth assaydv1alpha1.AuthStatus
	}{
		{"every field", assaydv1alpha1.AuthStatus{
			Mode:           "apikey",
			KeySource:      "assayd.dev/api-keys=true",
			AdmittedGroups: []string{"payments"},
			AppliedDigest:  strings.Repeat("a", 64),
			Verified:       &assaydv1alpha1.AuthVerification{ReplicasProbed: 1, ReplicasDeclared: i32(3)},
			Transaction: &assaydv1alpha1.AuthTransaction{
				Kind:           "Lock",
				TargetMode:     "apikey",
				TargetDigest:   strings.Repeat("b", 64),
				Stage:          "ProbingAfter",
				Written:        true,
				Deadline:       &deadline,
				Probe:          &assaydv1alpha1.AuthProbe{Before: i32(200), After: i32(401)},
				BeforeObserved: true,
				BeforeRevision: "my-agent-0123456789",
				RefusedMode:    "none",
			},
		}},
		// A refused Adopt records its kind, its stage and the mode it refused,
		// and nothing else (design 03 §3.3, §3.3.3). It must be admissible with
		// no target mode, which is why no transaction field is required.
		{"an Adopt record alone", assaydv1alpha1.AuthStatus{
			Transaction: &assaydv1alpha1.AuthTransaction{Kind: "Adopt", Stage: "Refused", RefusedMode: "apikey"},
		}},
		// {mode: none} carries no key source, groups, digest or verification.
		// ReplicasDeclared absent is "unknown", which is the slice's case (H2).
		{"a served none", assaydv1alpha1.AuthStatus{Mode: "none"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a, err := applyYAML(t, ns, minimalAgent)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			want := tc.auth
			a.Status.Auth = &want
			if err := k8s.Status().Update(context.Background(), a); err != nil {
				t.Fatalf("status.auth was refused by the status subresource: %v", err)
			}

			var got assaydv1alpha1.Agent
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			wantJSON, _ := json.Marshal(&want)
			gotJSON, _ := json.Marshal(got.Status.Auth)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("status.auth did not survive the status subresource.\n got: %s\nwant: %s\n"+
					"A field missing from the CRD is pruned without an error, so the operator that "+
					"writes it would read back a record it never stored.", gotJSON, wantJSON)
			}
		})
	}

	// And absent stays absent. Nothing writes status.auth today, so every Agent
	// must read back without one: an empty object here would claim a compiler
	// had recorded something, and Adopt keys on status.auth being absent.
	t.Run("absent", func(t *testing.T) {
		ns := newNamespace(t)
		a, err := applyYAML(t, ns, minimalAgent)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		a.Status.Phase = assaydv1alpha1.PhasePending
		if err := k8s.Status().Update(context.Background(), a); err != nil {
			t.Fatalf("status update: %v", err)
		}
		var raw unstructured.Unstructured
		raw.SetGroupVersionKind(assaydv1alpha1.GroupVersion.WithKind("Agent"))
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &raw); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if v, found, _ := unstructured.NestedFieldNoCopy(raw.Object, "status", "auth"); found {
			t.Errorf("status.auth is present on an Agent nothing wrote it for: %v", v)
		}
		if phase, _, _ := unstructured.NestedString(raw.Object, "status", "phase"); phase != "Pending" {
			t.Fatalf("the status write did not land (phase %q), so the absence proves nothing", phase)
		}
	})
}

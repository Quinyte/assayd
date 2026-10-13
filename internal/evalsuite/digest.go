// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package evalsuite holds what the operator computes from an EvalSuite
// (design 16 §1.1). Today that is the suite digest alone.
package evalsuite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// DefaultCaseTimeoutSeconds is the CRD's default for spec.caseTimeoutSeconds.
// The API server writes it on admission; Digest applies it too, so a spec
// built or read without it digests the same as the stored one.
const DefaultCaseTimeoutSeconds = 10

// Digest is the suite digest: the SHA-256, in lowercase hex, of the canonical
// JSON of the suite's spec, with defaults applied, object keys sorted, and
// cases sorted by id (design 16 §1.1).
//
// A change to any case's id, message or expectation, to the threshold, or to
// the timeout changes it. Reordering the cases, or writing keys in another
// order, does not. A verdict is bound to the pair (revision digest, suite
// digest), so the digest is what makes a suite edit re-open a held candidate.
//
// "Defaults applied" means the CRD's defaults, which is caseTimeoutSeconds
// alone. A suite with no metrics passes only when every case passes, as one
// with threshold "1" does, but the two are different specs and digest
// differently: the digest is of what was written, not of what it means.
//
// The caller's spec is not modified.
func Digest(spec assaydv1alpha1.EvalSuiteSpec) (string, error) {
	s := *spec.DeepCopy()
	if s.CaseTimeoutSeconds == 0 {
		s.CaseTimeoutSeconds = DefaultCaseTimeoutSeconds
	}
	sort.SliceStable(s.Dataset.Cases, func(i, j int) bool {
		return s.Dataset.Cases[i].ID < s.Dataset.Cases[j].ID
	})

	b, err := canonicalJSON(s)
	if err != nil {
		return "", fmt.Errorf("canonicalize the suite spec: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON renders v in the suite digest's canonical form, which is a
// compatibility contract: status.eval.failed[] is keyed on the digest, so a
// change to any rule below reopens every failed pair on upgrade.
//
//   - JSON (RFC 8259), UTF-8, with no insignificant whitespace;
//   - every object's keys sorted bytewise;
//   - integers written as decimal digits;
//   - strings escaped as encoding/json escapes them with HTML escaping off:
//     `"` and `\` backslash-escaped; \n, \r and \t as those escapes; every
//     other control character as \u00XX; U+2028 and U+2029 as \u2028 and
//     \u2029; everything else, `<`, `>`, `&` and non-ASCII included, raw.
//
// encoding/json writes a struct's fields in declaration order, so v is decoded
// into generic maps, whose keys it writes sorted, and encoded again; numbers
// survive as written (UseNumber). TestSuiteDigestIsPinned and
// TestSuiteDigestPinsItsEscaping hold the form against an independent encoder.
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

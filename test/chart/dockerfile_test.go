// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Dockerfile is supply chain too, and the rules it has to meet are the ones
// assayd imposes on the agent images it admits (ADR-0019).

func TestBaseImagesArePinnedByDigest(t *testing.T) {
	b, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	from := regexp.MustCompile(`(?m)^FROM\s+(\S+)`)
	matches := from.FindAllStringSubmatch(string(b), -1)
	if len(matches) < 2 {
		t.Fatalf("expected a multi-stage build, found %d FROM lines", len(matches))
	}

	// The final stage is what ships, and it is the one that must be immutable:
	// a tag is rebuilt upstream, so the same source would produce different
	// bytes over time — the exact property assayd refuses to accept from agents.
	final := matches[len(matches)-1][1]
	if !strings.Contains(final, "@sha256:") {
		t.Errorf("the runtime base image %q is pinned by tag. It is rebuilt upstream, so "+
			"the same source can produce different bytes over time — which is precisely "+
			"what assayd's own admission rejects in an agent image.", final)
	}
	if strings.Contains(final, ":latest") {
		t.Errorf("the runtime base image %q floats", final)
	}
}

// The operator runs as a non-root UID and the chart asserts runAsNonRoot, so a
// base image without one would be rejected at admission — after a build that
// looked fine.
func TestRuntimeImageRunsAsNonRoot(t *testing.T) {
	b, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !regexp.MustCompile(`(?m)^USER\s+\d+`).Match(b) {
		t.Error("the Dockerfile declares no numeric USER. runAsNonRoot is enforced by " +
			"comparing the image's user to root, and a username the kubelet cannot resolve " +
			"to a UID fails at admission rather than at build.")
	}
}

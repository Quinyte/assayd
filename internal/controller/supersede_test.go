package controller

import (
	"strconv"
	"testing"
)

// The CRD caps supersededCandidates at 10, so the operator must trim rather than
// discover the cap when the API server rejects the write. Observing that needs
// eleven supersessions, which is a unit test rather than eleven envtest rollouts.
func TestSupersededCandidatesAreTrimmedToTheCRDCap(t *testing.T) {
	var list []string
	for i := 0; i < 25; i++ {
		rev := "rev" + strconv.Itoa(i)
		list = appendSuperseded(list, rev)

		if len(list) > maxSupersededCandidates {
			t.Fatalf("after %d supersessions the list is %d entries; the CRD's MaxItems is "+
				"%d, so the status write would be rejected by the API server",
				i+1, len(list), maxSupersededCandidates)
		}
	}

	// The newest must survive: an operator debugging a rollout cares about what
	// was just abandoned, not what was abandoned twenty deploys ago.
	if got := list[len(list)-1]; got != "rev24" {
		t.Errorf("the most recent superseded revision is %q, want rev24", got)
	}
	if list[0] != "rev15" {
		t.Errorf("the window starts at %q, want rev15 — the trim must drop the oldest", list[0])
	}
}

// Supersession is recorded once. A candidate that is superseded, revived and
// superseded again must not appear twice and consume two slots.
func TestSupersededCandidatesAreDeduplicated(t *testing.T) {
	var list []string
	list = appendSuperseded(list, "a")
	list = appendSuperseded(list, "b")
	list = appendSuperseded(list, "a")

	if len(list) != 2 {
		t.Errorf("got %v; a revision superseded twice must occupy one slot", list)
	}
}

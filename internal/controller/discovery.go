package controller

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// evalSuiteGK is the kind whose presence decides whether rollouts are eval-gated
// (design 02 §3.3, core tier).
var evalSuiteGK = schema.GroupKind{Group: plumev1alpha1.GroupVersion.Group, Kind: "EvalSuite"}

// EvalSuiteDetector answers "is the EvalSuite CRD installed?" by asking the
// cluster.
//
// This exists because the answer used to arrive as a struct field, which meant
// whoever wired the manager decided ADR-0006's fate by whether they remembered
// to set it (B1). Discovery cannot be forgotten.
//
// Two properties are load-bearing, both in the same direction:
//
//   - The cache EXPIRES. A CRD installed after the operator starts must take
//     effect without a restart; otherwise agents keep promoting ungated after an
//     operator installed the very thing meant to gate them.
//   - An error never becomes an ANSWER. With no previous answer it resolves to
//     INSTALLED, which holds; with one, that previous answer stands rather than
//     flipping on a transient failure. An unreachable API server must never be
//     read as "nothing needs gating" — holding is recoverable, ungated promotion
//     is not.
//
// failureBackoff is how long a failed lookup is not retried. Short enough that
// a transient outage self-heals quickly; long enough that a persistent one does
// not make every reconcile wait on a network timeout.
const failureBackoff = 5 * time.Second

type EvalSuiteDetector struct {
	client  discovery.DiscoveryInterface
	ttl     time.Duration
	mu      sync.Mutex
	cached  bool
	checked time.Time
	warmed  bool
	// last is the previous answer, so a transition can be logged once rather
	// than on every refresh.
	last *bool
}

// NewEvalSuiteDetector builds a detector against a cluster. ttl bounds how long
// a stale answer can persist; 30s is a reasonable production value and tests
// pass something short.
func NewEvalSuiteDetector(cfg *rest.Config, ttl time.Duration) (*EvalSuiteDetector, error) {
	if cfg == nil {
		return nil, fmt.Errorf("eval-suite detector: rest config is required")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("eval-suite detector: ttl must be positive, got %s", ttl)
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("eval-suite detector: build discovery client: %w", err)
	}
	return &EvalSuiteDetector{client: dc, ttl: ttl}, nil
}

// Installed reports whether the EvalSuite CRD is served by this cluster.
//
// There is no context parameter: client-go's discovery interface does not take
// one, and accepting a ctx this function cannot honour would read as
// cancellation plumbing that does not exist. The call is bounded by the rest
// config's Timeout instead, which the operator sets explicitly.
func (d *EvalSuiteDetector) Installed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.warmed && time.Since(d.checked) < d.ttl {
		return d.cached
	}

	found, err := d.lookup()
	if err != nil {
		// Hold, and back off. A failure is not cached as an ANSWER — the previous
		// answer stands, or "installed" if there is none — but the attempt time is
		// recorded, so a reachable-but-broken API server cannot make every reconcile
		// pay the discovery timeout.
		d.checked = time.Now().Add(-d.ttl).Add(failureBackoff)
		log.Log.Error(err, "eval-suite detection failed; assuming the CRD is present so "+
			"gated agents hold rather than promoting ungated",
			"retryIn", failureBackoff)
		if d.warmed {
			return d.cached
		}
		return true
	}

	d.cached, d.checked, d.warmed = found, time.Now(), true
	if d.last == nil || *d.last != found {
		log.Log.Info("eval-suite detection", "evalSuiteCRDInstalled", found,
			"consequence", map[bool]string{
				true:  "rollouts with declared gates are eval-gated",
				false: "rollouts proceed ungated with GatesSkipped (design 02 §3.3, core tier)",
			}[found])
		v := found
		d.last = &v
	}
	return d.cached
}

// lookup asks for the KIND, not a group-version.
//
// Asking for plume.dev/v1alpha1 pinned detection to one version: when EvalSuite
// ships at v1beta1 the call still succeeds (Agent lives in v1alpha1), EvalSuite
// is absent from that list, and the answer is a confident "not installed" —
// failing OPEN on an ordinary API bump.
func (d *EvalSuiteDetector) lookup() (bool, error) {
	groups, resources, err := d.client.ServerGroupsAndResources()
	if err != nil {
		// A partial discovery failure still carries usable results: some
		// aggregated API being down must not decide whether plume gates rollouts.
		if !discovery.IsGroupDiscoveryFailedError(err) {
			return false, fmt.Errorf("discover server resources: %w", err)
		}
		if failed, ok := err.(*discovery.ErrGroupDiscoveryFailed); ok {
			for gv := range failed.Groups {
				if gv.Group == evalSuiteGK.Group {
					// The group plume itself lives in failed to discover. That is not
					// an answer; hold.
					return false, fmt.Errorf("discover %s: %w", evalSuiteGK.Group, err)
				}
			}
		}
	}
	_ = groups
	for _, list := range resources {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil || gv.Group != evalSuiteGK.Group {
			continue
		}
		for _, r := range list.APIResources {
			if r.Kind == evalSuiteGK.Kind {
				return true, nil
			}
		}
	}
	return false, nil
}

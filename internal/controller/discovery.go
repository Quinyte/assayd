package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
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
//   - Errors resolve to INSTALLED, which holds. An unreachable API server must
//     never be read as "nothing needs gating" — holding is recoverable, ungated
//     promotion is not.
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
func (d *EvalSuiteDetector) Installed(ctx context.Context) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.warmed && time.Since(d.checked) < d.ttl {
		return d.cached
	}

	found, err := d.lookup()
	if err != nil {
		// Hold. Do not cache a failure as an answer: leave the previous one in
		// place if there is one, and otherwise assume installed.
		log.Log.Error(err, "eval-suite detection failed; assuming the CRD is present so "+
			"gated agents hold rather than promoting ungated")
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

func (d *EvalSuiteDetector) lookup() (bool, error) {
	resources, err := d.client.ServerResourcesForGroupVersion(plumev1alpha1.GroupVersion.String())
	if err != nil {
		if meta.IsNoMatchError(err) || discovery.IsGroupDiscoveryFailedError(err) {
			// The group exists but is not fully served, or is absent. Neither is a
			// transport failure, so it is a real answer: not installed.
			return false, nil
		}
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("discover %s: %w", plumev1alpha1.GroupVersion, err)
	}
	for _, r := range resources.APIResources {
		if r.Kind == evalSuiteGK.Kind {
			return true, nil
		}
	}
	return false, nil
}

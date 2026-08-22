// Command operator runs the plume agent-operator.
//
// The wiring here is deliberately small and deliberately unforgiving: every
// dependency the reconciler needs is constructed explicitly and every failure to
// construct one is fatal. An earlier design let the EvalSuite check arrive as an
// optional struct field, which meant this file decided ADR-0006's fate by
// whether its author remembered to set it. It is now discovered from the
// cluster, and NewAgentReconciler refuses to build without it.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
	"github.com/ejs-5/plume/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntimeMust(clientgoscheme.AddToScheme(scheme))
	utilruntimeMust(plumev1alpha1.AddToScheme(scheme))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "operator: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		metricsAddr  string
		probeAddr    string
		leaderElect  bool
		gateCheckTTL time.Duration
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "address the metric endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "address the probe endpoint binds to")
	flag.BoolVar(&leaderElect, "leader-elect", true,
		"run leader election so only one operator reconciles at a time")
	flag.DurationVar(&gateCheckTTL, "eval-suite-check-interval", 30*time.Second,
		"how long a cached answer to 'is the EvalSuite CRD installed?' may persist "+
			"before the next reconcile re-checks it. This bounds the CACHE, not the "+
			"agent: an Agent nobody touches is not re-reconciled when the answer flips, "+
			"so use `kubectl annotate` or wait for the resync period to pick it up.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "agent-operator.plume.dev",
		// Release the lease on graceful shutdown so a rolling update does not wait
		// out the full lease duration before the new pod can reconcile.
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return fmt.Errorf("build manager: %w", err)
	}

	// Discovered, not configured. A cached answer expires, and a discovery
	// failure resolves to "installed" so gated agents hold rather than promoting
	// ungated — see EvalSuiteDetector.
	// Bound how long a reconcile can block on discovery. The client default is
	// 32s, which a single-worker controller would wear on every cache miss.
	discoveryCfg := rest.CopyConfig(cfg)
	discoveryCfg.Timeout = 5 * time.Second
	detector, err := controller.NewEvalSuiteDetector(discoveryCfg, gateCheckTTL)
	if err != nil {
		return fmt.Errorf("build eval-suite detector: %w", err)
	}

	// SetupSignalHandler panics if called twice, so it is called once here and
	// the context is shared by the detector and the manager.
	ctx := ctrl.SetupSignalHandler()

	agents, err := controller.NewAgentReconciler(mgr.GetClient(), mgr.GetScheme(),
		func() bool { return detector.Installed() })
	if err != nil {
		return fmt.Errorf("build agent reconciler: %w", err)
	}
	if err := agents.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("register agent reconciler: %w", err)
	}

	// Liveness is "the process is alive"; readiness must mean "this process is
	// actually reconciling". healthz.Ping for readiness returns nil
	// unconditionally, so an operator that cannot acquire its lease — or whose
	// cache never syncs — would report Ready and reconcile nothing forever, which
	// is precisely the silence NFR-8 forbids.
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("add health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("reconciling", func(_ *http.Request) error {
		select {
		case <-mgr.Elected():
			return nil
		default:
			return fmt.Errorf("not reconciling: leader election has not completed. " +
				"If this persists, check that the operator's ClusterRole grants " +
				"coordination.k8s.io/leases — a forbidden lease is retried forever rather " +
				"than reported")
		}
	}); err != nil {
		return fmt.Errorf("add ready check: %w", err)
	}

	ctrl.Log.Info("starting agent-operator",
		"leaderElection", leaderElect, "evalSuiteCheckInterval", gateCheckTTL)
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("manager exited: %w", err)
	}
	return nil
}

func utilruntimeMust(err error) {
	if err != nil {
		panic(fmt.Sprintf("operator: register scheme: %v", err))
	}
}

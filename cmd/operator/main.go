// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Command operator runs the assayd agent-operator.
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
	corev1 "k8s.io/api/core/v1"
	"net/http"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"

	"context"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntimeMust(clientgoscheme.AddToScheme(scheme))
	utilruntimeMust(assaydv1alpha1.AddToScheme(scheme))
	// Registered unconditionally, even where gateway.enabled is false: a scheme
	// entry starts no informer and reads no CRD, and a scheme that depended on a
	// flag would make "the type is unknown" a second, later way for the disabled
	// tier to fail.
	utilruntimeMust(gatewayv1.Install(scheme))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "operator: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		gatewayURL        string
		metricsAddr       string
		probeAddr         string
		leaderElect       bool
		gateCheckTTL      time.Duration
		operatorNamespace string
		gateway           controller.GatewayConfig
	)
	// The namespace the operator runs in: where the run-namespace binding
	// records live (design 02 A60). Defaults to the ServiceAccount namespace
	// file every Pod mounts; fatal if neither is set, because there is no safe
	// default for where security state goes.
	flag.StringVar(&operatorNamespace, "operator-namespace", saNamespace(),
		"the namespace this operator runs in; run-namespace binding records are kept there "+
			"(design 02 A60). Defaults to the mounted ServiceAccount namespace")
	flag.StringVar(&gatewayURL, "gateway-url", "",
		"base URL of the agentgateway that agent egress traverses, injected into every agent "+
			"workload as ASSAYD_GATEWAY_URL (design 02 §11). Empty — the default — injects nothing, "+
			"because the chart ships no gateway subchart yet and a placeholder address would look "+
			"like an outage rather than an absent tier")
	// Design 03 §3.1: whether anything is emitted at the gateway is DECLARED,
	// never discovered, so that `true` with the CRDs absent can only mean a
	// broken install. P1 ships `false` and these three then decide nothing.
	flag.BoolVar(&gateway.Enabled, "gateway-enabled", false,
		"emit each Agent's serving HTTPRoute into its run namespace, attached to the Gateway named "+
			"by --gateway-name/--gateway-namespace. False — the default and what P1 ships — emits "+
			"NOTHING and puts GovernanceSkipped=GatewayDisabled on every Agent (design 03 §3.1). "+
			"True emits the route and STILL enforces no budget, rate limit, authentication or tool "+
			"allowlist: no policy compiler exists, so this publishes a path, not a guarantee")
	flag.StringVar(&gateway.Name, "gateway-name", "",
		"the Gateway an emitted route's parentRef names. Required when --gateway-enabled is set; "+
			"design 07 A5.9's admission policy reserves route authorship by comparing this name")
	flag.StringVar(&gateway.Namespace, "gateway-namespace", "",
		"the namespace the Gateway lives in. This is NOT the namespace the operator runs in — "+
			"design 07 A6.2 measured the route reservation failing open, silently and in the "+
			"permissive direction, when the two were conflated. Required when --gateway-enabled is set")
	flag.StringVar(&gateway.HostnameSuffix, "gateway-hostname-suffix", controller.DefaultGatewayHostnameSuffix,
		"appended to <agent>.<agent-namespace> to form the hostname an emitted route matches. No "+
			"design settles this, so it is assayd's choice and configurable. It is a ROUTING KEY "+
			"matched against the Host header, not an address: nothing here creates DNS for it")
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
	if operatorNamespace == "" {
		return fmt.Errorf("--operator-namespace is unset and no ServiceAccount namespace file is mounted; " +
			"the run-namespace binding records (design 02 A60) need a home and there is no safe default")
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}

	// ConfigMaps and Secrets are read UNCACHED, and the reason is not performance.
	//
	// A cached client LISTs and WATCHes every object of a type it serves, cluster
	// wide, the first time one is read — so resolving one Agent's env source
	// would put a plaintext copy of every Secret in the cluster into this
	// process's memory. For a feature whose whole argument is that a leaked copy
	// of a Secret's bytes is a cost worth bounding, caching all of them is the
	// larger version of the same mistake, and an OOM risk besides.
	//
	// It also removes a correctness trap: finalize lists material and releases
	// the finalizer when it sees none. A cache that has not yet observed a copy
	// created moments earlier makes that sweep a no-op, the Agent disappears, and
	// nothing collects the copy afterwards because every remaining path keys on
	// the UID of an Agent that no longer exists.
	// Namespaces are uncached too: the run-namespace protocol compares a
	// namespace's UID against its binding record on every reconcile, and a
	// comparison against a cached object that a recreate has already replaced
	// is no proof at all (design 02 A60/A61).
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Client: client.Options{Cache: &client.CacheOptions{
			DisableFor: []client.Object{&corev1.ConfigMap{}, &corev1.Secret{}, &corev1.Namespace{}},
		}},
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "agent-operator.assayd.dev",
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

	// The uncached reader is what the run-namespace teardown protocol lists
	// with (design 02 A60): a cached list is exactly what it exists to avoid.
	// The label-authority check reads the admission policies uncached too, so
	// no cluster-wide informer on ValidatingAdmissionPolicy is started.
	labelAuthority := controller.LabelAuthorityPresent(mgr.GetAPIReader())
	agents, err := controller.NewAgentReconciler(mgr.GetClient(), mgr.GetAPIReader(), mgr.GetScheme(),
		operatorNamespace, func() bool { return detector.Installed() }, labelAuthority,
		controller.InjectedEnvConfig{GatewayURL: gatewayURL}, gateway)
	if err != nil {
		return fmt.Errorf("build agent reconciler: %w", err)
	}
	// The startup half of "at startup and before every run-namespace creation"
	// (design 07 A5.9). Not fatal: the policies can arrive after the operator,
	// and every reconcile re-checks; but a fleet that cannot place a single
	// workload should say so in the first log line, not in N conditions.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		ok, err := labelAuthority(ctx)
		switch {
		case err != nil:
			ctrl.Log.Error(err, "could not check the label-reserving admission policies (design 07 A5.9)")
		case !ok:
			ctrl.Log.Info("the label-reserving admission policies are not installed; no run namespace "+
				"will be created and every Agent will report RunNamespaceUnavailable=LabelAuthorityAbsent "+
				"until they are", "policies", []string{controller.NamespaceLabelPolicyName, controller.GatewayRoutePolicyName})
		}
		return nil
	})); err != nil {
		return fmt.Errorf("add startup check: %w", err)
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
		"leaderElection", leaderElect, "evalSuiteCheckInterval", gateCheckTTL,
		"gatewayEnabled", gateway.Enabled, "gateway", gateway.Namespace+"/"+gateway.Name)
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("manager exited: %w", err)
	}
	return nil
}

// saNamespace reads the namespace the kubelet projects for every Pod, or "".
func saNamespace() string {
	b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func utilruntimeMust(err error) {
	if err != nil {
		panic(fmt.Sprintf("operator: register scheme: %v", err))
	}
}

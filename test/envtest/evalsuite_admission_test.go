// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// generatedEvalSuiteCRDPath is controller-gen's output, which these tests
// install and refuse through. The chart does not ship it yet (design 16 §1.1
// build step 6 adds it to charts/assayd/crds/).
var generatedEvalSuiteCRDPath = filepath.Join("..", "..", "config", "crd", "assayd.dev_evalsuites.yaml")

func readGeneratedEvalSuiteCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	b, err := os.ReadFile(generatedEvalSuiteCRDPath)
	if err != nil {
		t.Fatalf("read the generated EvalSuite CRD (run `make manifests`): %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(b, &crd); err != nil {
		t.Fatalf("parse the generated EvalSuite CRD: %v", err)
	}
	return &crd
}

// installGeneratedEvalSuiteCRD makes the API server serve the generated
// EvalSuite CRD, whatever an earlier test left. Envtest loads config/crd at
// start, but the detector tests replace that CRD with a hand-built one of the
// same name, so a test that relies on the generated schema installs it itself
// rather than depending on the order tests run in. It returns once a
// well-formed suite is admitted under the generated schema.
func installGeneratedEvalSuiteCRD(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	want := readGeneratedEvalSuiteCRD(t)

	var have apiextensionsv1.CustomResourceDefinition
	err := k8s.Get(ctx, client.ObjectKey{Name: want.Name}, &have)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		t.Fatalf("read the EvalSuite CRD: %v", err)
	case have.DeletionTimestamp == nil && reflect.DeepEqual(have.Spec.Versions, want.Spec.Versions):
		waitForGeneratedEvalSuiteSchema(t)
		return
	default:
		ensureEvalSuiteAbsent(t)
	}
	// A deleted CRD can stop being discoverable before its object is gone, so
	// AlreadyExists here means "still terminating" and is retried.
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := k8s.Create(ctx, want.DeepCopy())
		if err == nil {
			break
		}
		if !apierrors.IsAlreadyExists(err) || time.Now().After(deadline) {
			t.Fatalf("install the generated EvalSuite CRD: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitForGeneratedEvalSuiteSchema(t)
}

// waitForGeneratedEvalSuiteSchema waits until the API server both serves the
// kind and enforces the generated schema on it. Established alone is not
// enough: the handler that validates can lag it. A dry-run create of a suite
// only the generated schema refuses tells the two apart, and a well-formed
// suite's dry run confirms the kind is served. The refused probe breaks a
// plain schema bound (a 4097-character message) rather than a CEL rule, so a
// test that deletes one rule to prove it is load-bearing is not stopped here.
func waitForGeneratedEvalSuiteSchema(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	ns := "default"
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		ok := suite(ns, "probe-ok", evalCase("a", "ping", "pong", ""))
		bad := suite(ns, "probe-bad", evalCase("a", strings.Repeat("m", 4097), "pong", ""))
		errOK := k8s.Create(ctx, ok, client.DryRunAll)
		errBad := k8s.Create(ctx, bad, client.DryRunAll)
		if errOK == nil && apierrors.IsInvalid(errBad) {
			return
		}
		last = errOK
		if last == nil {
			last = errBad
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the generated EvalSuite schema was not enforced within 30s: %v", last)
}

func evalCase(id, message, contains, equals string) assaydv1alpha1.EvalCase {
	return assaydv1alpha1.EvalCase{ID: id, Message: message,
		Expect: assaydv1alpha1.EvalExpect{Contains: contains, Equals: equals}}
}

func suite(ns, name string, cases ...assaydv1alpha1.EvalCase) *assaydv1alpha1.EvalSuite {
	if cases == nil {
		// `cases: []`, as a person writes an empty list. A nil slice would be
		// sent as null, which the schema's required list refuses before any
		// CEL rule runs.
		cases = []assaydv1alpha1.EvalCase{}
	}
	return &assaydv1alpha1.EvalSuite{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       assaydv1alpha1.EvalSuiteSpec{Dataset: assaydv1alpha1.EvalDataset{Cases: cases}},
	}
}

// refusedByRule asserts err is an admission refusal that carries msg and none of
// the other rules' messages, so a deletion of one rule fails its own case and
// only its own.
func refusedByRule(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("admitted; want refused with %q", msg)
	}
	if !apierrors.IsInvalid(err) {
		t.Fatalf("refused with %v, which is not a validation refusal", err)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Fatalf("refused, but not by the rule that says %q: %v", msg, err)
	}
	for _, other := range evalSuiteRuleMessages {
		if other != msg && strings.Contains(err.Error(), other) {
			t.Errorf("also refused by the rule that says %q, so this case does not isolate its own rule: %v", other, err)
		}
	}
}

// A distinctive phrase from each CEL rule's message, as the generated CRDs
// carry them.
const (
	msgTwoGates     = "spec.gates holds at most one gate"
	msgNoCases      = "spec.dataset.cases is empty"
	msgDuplicateID  = "must be unique within the suite"
	msgZeroThresh   = "threshold must be greater than 0"
	msgExactlyOne   = "must set exactly one of contains or equals"
	msgEcho         = "already satisfies its own expectation"
	msgDNSLabel     = "must be a DNS label"
	msgMetricName   = "must be task_completion"
	msgThreshFormat = "must be a decimal string from 0 to 1"
	msgOneMetric    = "must have at most 1 item" // MaxItems, not CEL: see EvalDataset
)

var evalSuiteRuleMessages = []string{msgTwoGates, msgNoCases, msgDuplicateID, msgZeroThresh,
	msgExactlyOne, msgEcho, msgDNSLabel, msgMetricName, msgThreshFormat, msgOneMetric}

// T60: the generated EvalSuite CRD installs on the API server at the bounds
// design 16 §1.1 measured — a 4096-character message and 512-character
// expectations — which is a claim about the CEL cost budget that only a real
// API server can check. And the echo rule, with its has() guards, admits a
// contains case and an equals case at those bounds and refuses both echoes.
func TestTheGeneratedEvalSuiteCRDInstallsAndRefusesAnEcho(t *testing.T) {
	ctx := context.Background()
	installGeneratedEvalSuiteCRD(t)

	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(ctx, client.ObjectKey{Name: "evalsuites.assayd.dev"}, &installed); err != nil {
		t.Fatalf("read the installed CRD: %v", err)
	}
	established := false
	for _, c := range installed.Status.Conditions {
		established = established || (c.Type == apiextensionsv1.Established && c.Status == apiextensionsv1.ConditionTrue)
	}
	if !established {
		t.Fatalf("the generated EvalSuite CRD is not Established: %+v", installed.Status.Conditions)
	}

	ns := newNamespace(t)
	long := strings.Repeat("m", 4096)
	expect := strings.Repeat("e", 512)
	if err := k8s.Create(ctx, suite(ns, "at-the-bounds",
		evalCase("contains-case", long, expect, ""),
		evalCase("equals-case", long, "", expect))); err != nil {
		t.Errorf("a well-formed suite at message 4096 and expectations 512 was refused: %v", err)
	}

	refusedByRule(t, k8s.Create(ctx, suite(ns, "echo-contains",
		evalCase("echo", "please say "+expect+" back", expect, ""))), msgEcho)
	refusedByRule(t, k8s.Create(ctx, suite(ns, "echo-equals",
		evalCase("echo", expect, "", expect))), msgEcho)
}

// T65: admission refuses each malformed suite design 16 §1.1 names, and an
// Agent with a second gate, one rule per case. refusedByRule also checks that no
// other rule's message appears, so deleting a rule fails its own case only.
func TestAdmissionRefusesEachMalformedSuiteAndASecondGate(t *testing.T) {
	ctx := context.Background()
	installGeneratedEvalSuiteCRD(t)
	ns := newNamespace(t)
	ok := evalCase("arithmetic", "What is 2 + 2?", "4", "")

	t.Run("two gates", func(t *testing.T) {
		a := &assaydv1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: "twogates", Namespace: ns},
			Spec: assaydv1alpha1.AgentSpec{
				Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"},
				Gates:   []assaydv1alpha1.GateRef{{EvalSuiteRef: "a"}, {EvalSuiteRef: "b"}},
			},
		}
		refusedByRule(t, k8s.Create(ctx, a), msgTwoGates)

		one := a.DeepCopy()
		one.Name = "onegate"
		one.Spec.Gates = one.Spec.Gates[:1]
		if err := k8s.Create(ctx, one); err != nil {
			t.Errorf("an Agent with one gate was refused: %v", err)
		}
	})
	t.Run("an empty case list", func(t *testing.T) {
		refusedByRule(t, k8s.Create(ctx, suite(ns, "empty")), msgNoCases)
	})
	t.Run("duplicate case ids", func(t *testing.T) {
		dup := ok
		dup.Message = "What is 3 + 3?"
		dup.Expect.Contains = "6"
		refusedByRule(t, k8s.Create(ctx, suite(ns, "dup", ok, dup)), msgDuplicateID)
	})
	t.Run("a threshold of 0", func(t *testing.T) {
		for _, zero := range []string{"0", "0.0", "0.000"} {
			s := suite(ns, "zero", ok)
			s.Spec.Metrics = []assaydv1alpha1.EvalMetric{{Name: "task_completion", Threshold: zero}}
			refusedByRule(t, k8s.Create(ctx, s), msgZeroThresh)
		}
	})
	t.Run("two expectations on one case", func(t *testing.T) {
		refusedByRule(t, k8s.Create(ctx, suite(ns, "both",
			evalCase("both", "What is 2 + 2?", "4", "4"))), msgExactlyOne)
	})
	t.Run("no expectation", func(t *testing.T) {
		refusedByRule(t, k8s.Create(ctx, suite(ns, "neither",
			evalCase("neither", "What is 2 + 2?", "", ""))), msgExactlyOne)
	})
	t.Run("an id that is not a DNS label", func(t *testing.T) {
		refusedByRule(t, k8s.Create(ctx, suite(ns, "badid",
			evalCase("Not_A_Label", "What is 2 + 2?", "4", ""))), msgDNSLabel)
	})
	t.Run("a metric other than task_completion", func(t *testing.T) {
		s := suite(ns, "othermetric", ok)
		s.Spec.Metrics = []assaydv1alpha1.EvalMetric{{Name: "tool_correctness", Threshold: "0.9"}}
		refusedByRule(t, k8s.Create(ctx, s), msgMetricName)
	})
	t.Run("two metrics", func(t *testing.T) {
		s := suite(ns, "twometrics", ok)
		s.Spec.Metrics = []assaydv1alpha1.EvalMetric{
			{Name: "task_completion", Threshold: "0.5"}, {Name: "task_completion", Threshold: "0.9"}}
		refusedByRule(t, k8s.Create(ctx, s), msgOneMetric)
	})
	t.Run("a threshold that is not a decimal in [0, 1] to three places", func(t *testing.T) {
		for _, bad := range []string{"1.5", "2", "0.12a", "0,5", ".5", "-0.5", "1.001"} {
			s := suite(ns, "badthresh", ok)
			s.Spec.Metrics = []assaydv1alpha1.EvalMetric{{Name: "task_completion", Threshold: bad}}
			refusedByRule(t, k8s.Create(ctx, s), msgThreshFormat)
		}
	})
	t.Run("thresholds at the edges are admitted", func(t *testing.T) {
		for i, good := range []string{"1", "1.0", "1.000", "0.001", "0.66", "0.5"} {
			s := suite(ns, "goodthresh-"+string(rune('a'+i)), ok)
			s.Spec.Metrics = []assaydv1alpha1.EvalMetric{{Name: "task_completion", Threshold: good}}
			if err := k8s.Create(ctx, s); err != nil {
				t.Errorf("threshold %q was refused: %v", good, err)
			}
		}
	})
	t.Run("the timeout defaults to 10", func(t *testing.T) {
		s := suite(ns, "defaulted", ok)
		if err := k8s.Create(ctx, s); err != nil {
			t.Fatalf("create: %v", err)
		}
		var got assaydv1alpha1.EvalSuite
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(s), &got); err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Spec.CaseTimeoutSeconds != 10 {
			t.Errorf("caseTimeoutSeconds is %d, want the default 10", got.Spec.CaseTimeoutSeconds)
		}
	})
}

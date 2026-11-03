// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// scriptedReader answers the Agent CRD and the EvalSuite gets from two
// scripts, one error per call, or the object when a script has run out. It
// is what the uncached reader would return, so the rows below reach each
// message through the read that builds it.
type scriptedReader struct {
	client.Reader
	crd                  *apiextensionsv1.CustomResourceDefinition
	crdErrs, suiteErrs   []error
	crdReads, suiteReads int
}

func (s *scriptedReader) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	switch o := obj.(type) {
	case *apiextensionsv1.CustomResourceDefinition:
		s.crdReads++
		if s.crdReads <= len(s.crdErrs) {
			return s.crdErrs[s.crdReads-1]
		}
		s.crd.DeepCopyInto(o)
		return nil
	case *assaydv1alpha1.EvalSuite:
		s.suiteReads++
		if s.suiteReads <= len(s.suiteErrs) {
			return s.suiteErrs[s.suiteReads-1]
		}
		*o = assaydv1alpha1.EvalSuite{Spec: twoCaseSuite().Spec}
		o.Name = key.Name
		return nil
	}
	return fmt.Errorf("scriptedReader: unexpected get of %T", obj)
}

// connReset is a transport error as client-go returns one: its text carries
// an ephemeral source port, so it differs from pass to pass while its class,
// "an error of another class", does not (#155).
func connReset(port int) error {
	return &url.Error{Op: "Get", URL: "https://10.96.0.1:443/apis/assayd.dev/v1alpha1/namespaces/team-a/evalsuites/smoke",
		Err: &net.OpError{Op: "read", Net: "tcp", Source: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: port},
			Addr: &net.TCPAddr{IP: net.IPv4(10, 96, 0, 1), Port: 443}, Err: syscall.ECONNRESET}}
}

func gatedAgent() *assaydv1alpha1.Agent {
	a := &assaydv1alpha1.Agent{}
	a.Namespace, a.Name = "team-a", "classed"
	a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	return a
}

// sameClassTwice runs two passes, each answered by one of errs, and asserts
// both build one message, naming want and neither error's text.
func sameClassTwice(t *testing.T, errs [2]error, want string, pass func() string) {
	t.Helper()
	if errs[0].Error() == errs[1].Error() {
		t.Fatalf("setup: the two injected errors have one text: %s", errs[0])
	}
	first, second := pass(), pass()
	if first != second {
		t.Errorf("two errors of one class built two messages, so a failure that persists writes "+
			"status on every pass whose text changes:\n %s\n %s", first, second)
	}
	if !strings.Contains(first, want) {
		t.Errorf("the message does not name the class as %q: %s", want, first)
	}
	for _, err := range errs {
		if strings.Contains(first, err.Error()) || strings.Contains(second, err.Error()) {
			t.Errorf("the message carries the error's text %q: %s", err, first)
		}
	}
}

// Design 16 A27.8: EvalSuiteUnreadable names the suite read's class.
func TestEvalSuiteUnreadableNamesTheClassNotTheText(t *testing.T) {
	errs := [2]error{connReset(51234), connReset(51235)}
	rd := &scriptedReader{crd: generatedAgentCRD(t), suiteErrs: errs[:]}
	r := &AgentReconciler{Reader: rd}
	agent, status := gatedAgent(), &assaydv1alpha1.AgentStatus{}
	sameClassTwice(t, errs, `EvalSuite "smoke", which spec.gates names, could not be read (an error of another class). revision`,
		func() string {
			d := r.gateFor(context.Background(), agent, status, gRev, gDigest, true)
			if d.reason != ReasonEvalSuiteUnreadable {
				t.Fatalf("gateFor gives %q, want EvalSuiteUnreadable (%s)", d.reason, d.message)
			}
			return d.message
		})
}

// Design 16 A27.8: EvalRecordUnverifiable names the Agent CRD read's class.
func TestEvalRecordUnverifiableNamesTheClassNotTheText(t *testing.T) {
	errs := [2]error{apierrors.NewInternalError(errors.New("etcdserver: leader changed (term 41)")),
		apierrors.NewInternalError(errors.New("etcdserver: leader changed (term 42)"))}
	rd := &scriptedReader{crd: generatedAgentCRD(t), crdErrs: errs[:]}
	// One nanosecond makes every call read; with nothing cached, each failed
	// read is the answer.
	r := &AgentReconciler{Reader: rd, EvalRecordCheckTTL: time.Nanosecond}
	agent, status := gatedAgent(), &assaydv1alpha1.AgentStatus{}
	sameClassTwice(t, errs, "could not be read to check that it keeps status.eval's eval record (a server error). revision",
		func() string {
			d := r.gateFor(context.Background(), agent, status, gRev, gDigest, true)
			if d.reason != ReasonEvalRecordUnverifiable {
				t.Fatalf("gateFor gives %q, want EvalRecordUnverifiable (%s)", d.reason, d.message)
			}
			return d.message
		})
}

// Design 16 A27.8: the pin rule's admission reason names the suite read's
// class, inside Q5's sentence.
func TestThePinRulesReasonNamesTheClassNotTheText(t *testing.T) {
	errs := [2]error{apierrors.NewForbidden(evalSuiteGR, "smoke", errors.New("RBAC: role assayd-a not bound")),
		apierrors.NewForbidden(evalSuiteGR, "smoke", errors.New("RBAC: role assayd-b not bound"))}
	rd := &scriptedReader{crd: generatedAgentCRD(t), suiteErrs: errs[:]}
	r := &AgentReconciler{Reader: rd}
	agent := gatedAgent()
	status := &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: "sha256:active", CandidateRevisionDigest: "sha256:cand"}
	pin := &releasePin{Digest: "sha256:older", Revision: "older"}
	sameClassTwice(t, errs, `it is not the current candidate, and EvalSuite "smoke" could not be read (Forbidden), `+
		"so there is no current suite digest and status.eval.failed[] was not consulted",
		func() string {
			u, why := r.pinRule(context.Background(), agent, status, pin, "sha256:cand")
			if u != nil {
				t.Fatalf("the pin was refused: %s", u.reason)
			}
			return why
		})
}

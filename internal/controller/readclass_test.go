// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

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

// gatewayReadScript is the uncached reader gatewayAuthPolicies reads through:
// its Gateway gets and its policy lists are answered from two scripts, one
// error per call, and once a script has run out by an empty Gateway or an
// empty list.
type gatewayReadScript struct {
	client.Reader
	getErrs, listErrs []error
	gets, lists       int
}

func (s *gatewayReadScript) Get(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if _, ok := obj.(*gatewayv1.Gateway); !ok {
		return fmt.Errorf("gatewayReadScript: unexpected get of %T", obj)
	}
	s.gets++
	if s.gets <= len(s.getErrs) {
		return s.getErrs[s.gets-1]
	}
	return nil
}

func (s *gatewayReadScript) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if _, ok := list.(*unstructured.UnstructuredList); !ok {
		return fmt.Errorf("gatewayReadScript: unexpected list of %T", list)
	}
	s.lists++
	if s.lists <= len(s.listErrs) {
		return s.listErrs[s.lists-1]
	}
	return nil
}

func classedReads(s *gatewayReadScript) gatewayPolicyReads {
	return gatewayPolicyReads{uncached: s, Gateway: GatewayConfig{Enabled: true, Name: "assayd", Namespace: "gw"}}
}

// classedPair is two errors of one class with different texts.
type classedPair struct {
	class string
	errs  [2]error
}

// classedPairs are the classes gatewayAuthPolicies' rows inject: a transport
// reset with its ephemeral port, a server error, and a timeout.
func classedPairs() []classedPair {
	return []classedPair{
		{"an error of another class", [2]error{connReset(51234), connReset(51235)}},
		{"a server error", [2]error{apierrors.NewInternalError(errors.New("etcdserver: leader changed (term 41)")),
			apierrors.NewInternalError(errors.New("etcdserver: leader changed (term 42)"))}},
		{"a timeout", [2]error{apierrors.NewTimeoutError("request did not complete within 30s (try 1)", 1),
			apierrors.NewTimeoutError("request did not complete within 30s (try 2)", 2)}},
	}
}

// Design 03 A90, §9 D9 (S1): a Gateway get that fails with a class no branch
// of its own words is named by that class, so two texts of one class build
// one unreadable, and a failure that persists writes status once.
func TestAFailedGatewayReadNamesTheClassNotTheText(t *testing.T) {
	for _, c := range classedPairs() {
		t.Run(c.class, func(t *testing.T) {
			reads := classedReads(&gatewayReadScript{getErrs: c.errs[:]})
			sameClassTwice(t, c.errs, "Gateway gw/assayd could not be read ("+c.class+"), so its listeners, "+
				"labels and allowedListeners are unknown", func() string {
				return reads.gatewayAuthPolicies(context.Background(), "agent.team.example").unreadable
			})
		})
	}
}

// Design 03 A90, §9 D9 (S1): a failed policy list in the Gateway's namespace
// is named by its class, which readErrorClass finds through listPolicies'
// wrap.
func TestAFailedPolicyListNamesTheClassNotTheText(t *testing.T) {
	for _, c := range classedPairs() {
		t.Run(c.class, func(t *testing.T) {
			reads := classedReads(&gatewayReadScript{listErrs: c.errs[:]})
			sameClassTwice(t, c.errs, "the AgentgatewayPolicies in gw could not be listed ("+c.class+"), so "+
				"none on the Gateway can be ruled out", func() string {
				out := reads.gatewayAuthPolicies(context.Background(), "agent.team.example")
				if !out.unlisted {
					t.Fatalf("a failed list is not unlisted: %+v", out)
				}
				return out.unreadable
			})
		})
	}
}

// Design 03 A90 leaves the Forbidden get's branch as it was: its message
// quotes the refusal, which names the identity and the verb to grant.
func TestAForbiddenGatewayReadStillQuotesTheError(t *testing.T) {
	gr := schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}
	err := apierrors.NewForbidden(gr, "assayd", errors.New(`User "system:serviceaccount:assayd:op" cannot get`))
	reads := classedReads(&gatewayReadScript{getErrs: []error{err}})
	out := reads.gatewayAuthPolicies(context.Background(), "agent.team.example").unreadable
	if !strings.Contains(out, "the operator may not read Gateway gw/assayd ("+err.Error()+"); grant get on it") {
		t.Errorf("a Forbidden Gateway get does not quote the refusal: %s", out)
	}
}

// Design 03 A90, §9 D9 (L1′): each failed read whose message names a class
// instead of the text logs the full text, one line a call, at info; the reads
// whose message already says what the error says log nothing. The logger is
// funcr at verbosity 0, the level the chart's zap logger runs at, so a line at
// V(1) is not written, and an Error line carries no "level".
func TestAFailedGatewayReadLogsTheTextOnlyWhereTheMessageNamesTheClass(t *testing.T) {
	gr := schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}
	reset, listReset := connReset(51234), connReset(51299)
	for _, c := range []struct {
		name   string
		script *gatewayReadScript
		text   string
	}{
		{"a transport reset on the get", &gatewayReadScript{getErrs: []error{reset}}, reset.Error()},
		{"a failed list", &gatewayReadScript{listErrs: []error{listReset}}, listReset.Error()},
		{"a NotFound get", &gatewayReadScript{getErrs: []error{apierrors.NewNotFound(gr, "assayd")}}, ""},
		{"an unserved kind", &gatewayReadScript{getErrs: []error{&meta.NoKindMatchError{
			GroupKind: schema.GroupKind{Group: gatewayv1.GroupName, Kind: "Gateway"}}}}, ""},
		{"a Forbidden get", &gatewayReadScript{getErrs: []error{apierrors.NewForbidden(gr, "assayd",
			errors.New("RBAC: no grant"))}}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var lines []string
			logger := funcr.New(func(prefix, args string) { lines = append(lines, prefix+" "+args) }, funcr.Options{})
			ctx := log.IntoContext(context.Background(), logger)
			classedReads(c.script).gatewayAuthPolicies(ctx, "agent.team.example")
			if c.text == "" {
				if len(lines) != 0 {
					t.Errorf("a read whose message already says what the error says logged %d lines: %q",
						len(lines), lines)
				}
				return
			}
			if len(lines) != 1 {
				t.Fatalf("%d lines logged, want one: %q", len(lines), lines)
			}
			if !strings.Contains(lines[0], `"level"=0`) {
				t.Errorf("the line is not at info, verbosity 0: %s", lines[0])
			}
			// funcr quotes a value as strconv does; the list's error is
			// listPolicies' wrap of the injected one, so its text is inside.
			quoted := strconv.Quote(c.text)
			if !strings.Contains(lines[0], `"error"="`) || !strings.Contains(lines[0], quoted[1:len(quoted)-1]+`"`) {
				t.Errorf("the line does not hold the error's full text %q: %s", c.text, lines[0])
			}
		})
	}
}

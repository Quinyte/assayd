// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// #155's measurement. A read that fails on every pass with a transport error
// whose text changes each time — a connection reset quoting its ephemeral
// source port — is driven by hand for varyingPasses passes, after one pass
// that enters the failure, and every pass whose Agent resourceVersion moved is
// counted. The API server does not bump resourceVersion for an update that
// changes nothing, so a moved resourceVersion is a status write that changed
// the object. Each Gateway row runs the same passes with a STABLE text first,
// as the control, so that what the varying run counts is the text and nothing
// else. docs/research/155-varying-read-error-2026-11.md records the numbers.
const varyingPasses = 10

// transportReset is the error a connection reset by the API server's peer
// gives through client-go: a *url.Error around a *net.OpError. Its class is
// none that readErrorClass names. port is the ephemeral source port.
func transportReset(path string, port int) error {
	return &url.Error{Op: "Get", URL: "https://10.96.0.1:443" + path,
		Err: &net.OpError{Op: "read", Net: "tcp", Source: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: port},
			Addr: &net.TCPAddr{IP: net.IPv4(10, 96, 0, 1), Port: 443}, Err: syscall.ECONNRESET}}
}

// gatewayResets is an uncached reader whose every Gateway get fails with
// transportReset: at one port when vary is false, at a new port on every call
// when it is true.
type gatewayResets struct {
	client.Reader
	vary  bool
	calls atomic.Int64
}

func (g *gatewayResets) Get(ctx context.Context, key client.ObjectKey, obj client.Object,
	opts ...client.GetOption) error {
	if _, ok := obj.(*gatewayv1.Gateway); ok {
		n := g.calls.Add(1)
		port := 40000
		if g.vary {
			port += int(n)
		}
		return transportReset("/apis/gateway.networking.k8s.io/v1/namespaces/"+key.Namespace+
			"/gateways/"+key.Name, port)
	}
	return g.Reader.Get(ctx, key, obj, opts...)
}

// passesThatWrite runs one pass to enter the state, then n passes, and
// returns how many of the n moved the Agent's resourceVersion, and how many
// distinct messages typ carried across them.
func passesThatWrite(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent, n int,
	typ assaydv1alpha1.ConditionType) (writes, messages int) {
	t.Helper()
	reconcileOnce(t, r, a)
	prev := liveAgent(t, a).ResourceVersion
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		reconcileOnce(t, r, a)
		live := liveAgent(t, a)
		if live.ResourceVersion != prev {
			writes++
		}
		prev = live.ResourceVersion
		if c := condition(live, typ); c != nil {
			seen[c.Message] = true
		}
	}
	return writes, len(seen)
}

// varyingCases is each Gateway row's control and measurement.
var varyingCases = []struct {
	name   string
	suffix string
	vary   bool
}{{"stable text", "s", false}, {"varying text", "v", true}}

// The three Gateway rows below pin the CURRENT behaviour, which is the defect
// #155 names, and not the intended one. Once #155's fix lands and the message
// names the error's class instead of its text, each fails in two places:
// first at quotesRawText, its "connection reset by peer" assertion, which
// fails in the stable subtests too, since the text is gone whatever the port;
// then at the varying subtest's write count. flipNote says what to do then.
const flipNote = "if #155's fix has landed, flip this row: want 0 writes with a varying text too, " +
	"and drop the raw-text assertion (quotesRawText)"

// quotesRawText requires c's message to quote the injected error's text, as
// authabove.go:212's "(%v)" does today.
func quotesRawText(t *testing.T, c *metav1.Condition, what string) {
	t.Helper()
	if c != nil && !strings.Contains(c.Message, "connection reset by peer") {
		t.Errorf("%s does not quote the read error's text, which this row pins (#155); %s: %s",
			what, flipNote, c.Message)
	}
}

// pinnedWrites is what a Gateway row pins: no write with a stable text, and
// one on every pass with a varying one.
func pinnedWrites(t *testing.T, name string, vary bool, writes int) {
	t.Helper()
	want := 0
	if vary {
		want = varyingPasses
	}
	if writes != want {
		t.Errorf("%d of %d passes wrote status with a %s; this row pins %d (#155); %s", writes,
			varyingPasses, name, want, flipNote)
	}
}

// #155, design 03 A75: authabove.go's "Gateway %s/%s could not be read (%v)"
// quotes the error's text, and it reaches a served API-key Agent's
// GovernanceSkipped message: with no policy on the Gateway the unread Gateway
// is servedNote's cause that stands, under GovernanceSkipped=False,
// AuthVerifiedOnOneReplica, so it does not page. With a stable text the served
// Agent writes status on no pass; with a varying one it writes on every pass.
//
// Each write is an Agent update that the manager's predicate-free watch turns
// into the next pass, so a sustained, varying failure is a status write loop.
// The row pins the defect; see flipNote.
func TestAVaryingGatewayReadFailureRewritesAServedAgentsStatusEveryPass(t *testing.T) {
	for _, c := range varyingCases {
		t.Run(c.name, func(t *testing.T) {
			a, r, _ := servedAPIKeyAgent(t, "varygwserved"+c.suffix)
			reads := &gatewayResets{Reader: k8s, vary: c.vary}
			r.Reader = reads
			writes, msgs := passesThatWrite(t, r, a, varyingPasses, assaydv1alpha1.CondGovernanceSkipped)
			g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
			mustContain(t, g, "GovernanceSkipped", "could not be read", "not held")
			quotesRawText(t, g, "GovernanceSkipped")
			t.Logf("served API-key Agent, %s: %d of %d passes wrote status, %d distinct GovernanceSkipped "+
				"messages, %d Gateway reads", c.name, writes, varyingPasses, msgs, reads.calls.Load())
			pinnedWrites(t, c.name, c.vary, writes)
		})
	}
}

// #155: the served row above through the manager, so that what the hand
// count infers — each write brings the next pass — is measured as a rate. The
// Agent is served by hand, then handed to a reconciler built as the binary
// builds it, on the manager's client and an uncached reader whose Gateway get
// fails. Status writes the API server accepted are counted from the first
// message that names the failure. With a stable text, it requires none in
// 5 s. With a varying one, it waits up to 30 s for varyingPasses writes, so a
// loaded runner slows the row rather than failing it, and it requires the
// writes to track the Gateway reads, one write a pass. The row pins the
// defect; see flipNote.
func TestAVaryingGatewayReadFailureLoopsThroughTheManager(t *testing.T) {
	for _, c := range varyingCases {
		t.Run(c.name, func(t *testing.T) {
			a, setup, stub := servedAPIKeyAgent(t, "varygwmgr"+c.suffix)
			mgr := newTestManager(t)
			writes := &agentStatusWrites{Client: mgr.GetClient()}
			reads := &gatewayResets{Reader: mgr.GetAPIReader(), vary: c.vary}
			r, err := controller.NewAgentReconciler(writes, reads, mgr.GetScheme(), operatorNamespace,
				func() bool { return false }, labelAuthorityPresent, controller.InjectedEnvConfig{}, setup.Gateway)
			if err != nil {
				t.Fatal(err)
			}
			r.AuthProbe = stub
			r.CardFetchTimeout = 100 * time.Millisecond
			if err := r.SetupWithManager(mgr); err != nil {
				t.Fatal(err)
			}
			stop := startStoppable(t, mgr)
			eventually(t, "GovernanceSkipped naming the Gateway read's failure", func() bool {
				g := condition(liveAgent(t, a), assaydv1alpha1.CondGovernanceSkipped)
				return g != nil && strings.Contains(g.Message, "could not be read")
			})
			w0, r0 := writes.n.Load(), reads.calls.Load()
			counts := func() (int64, int64) { return writes.n.Load() - w0, reads.calls.Load() - r0 }
			if !c.vary {
				time.Sleep(5 * time.Second)
				w, n := counts()
				stop()
				t.Logf("served API-key Agent through the manager, stable text: %d Agent status writes and "+
					"%d Gateway reads in 5 s", w, n)
				if w != 0 {
					t.Errorf("%d status writes in 5 s with a stable text; this row pins none (#155)", w)
				}
				return
			}
			start := time.Now()
			for deadline := start.Add(30 * time.Second); time.Now().Before(deadline); {
				if w, _ := counts(); w >= varyingPasses {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			stop()
			w, n := counts()
			t.Logf("served API-key Agent through the manager, varying text: %d Agent status writes and %d "+
				"Gateway reads in %s", w, n, time.Since(start).Round(time.Millisecond))
			if w < varyingPasses {
				t.Errorf("%d status writes in 30 s with a varying text; this row pins a loop, at least %d "+
					"(#155); %s", w, varyingPasses, flipNote)
			}
			// One Gateway read and one status write a pass. The two counters
			// are read a moment apart, so a pass either way is allowed.
			if d := w - n; d < -2 || d > 2 {
				t.Errorf("%d status writes against %d Gateway reads: the loop should write once a pass", w, n)
			}
		})
	}
}

// #155, design 03 A75: the same read on a Create held at ProbingAfter, where
// the quoted text reaches PolicyApplyIncomplete=GatewayAuthPolicy's hold
// message. The row pins the defect; see flipNote.
func TestAVaryingGatewayReadFailureRewritesAHeldCreatesStatusEveryPass(t *testing.T) {
	for _, c := range varyingCases {
		t.Run(c.name, func(t *testing.T) {
			a, r, stub := probingCreate(t, "varygwcreate"+c.suffix, 0)
			stub.hold(a.Name, false)
			reads := &gatewayResets{Reader: k8s, vary: c.vary}
			r.Reader = reads
			writes, msgs := passesThatWrite(t, r, a, varyingPasses, assaydv1alpha1.CondPolicyApplyIncomplete)
			if tx := txOf(t, a); tx == nil || tx.Kind != "Create" || tx.Stage != "ProbingAfter" {
				t.Fatalf("the Create does not hold at ProbingAfter: %+v", tx)
			}
			p := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
			mustContain(t, p, "PolicyApplyIncomplete", "could not be read")
			quotesRawText(t, p, "PolicyApplyIncomplete")
			t.Logf("held Create, %s: %d of %d passes wrote status, %d distinct PolicyApplyIncomplete "+
				"messages, %d Gateway reads", c.name, writes, varyingPasses, msgs, reads.calls.Load())
			pinnedWrites(t, c.name, c.vary, writes)
		})
	}
}

// #155, design 16 A27.8: the gate's suite read, the issue's second site, by
// the same hand-driven count. A27.8 already replaced its "%v" with the error's
// class, so a varying text writes status on no pass. This is the hand-driven
// companion of TestAVaryingSuiteReadFailureWritesStatusOnce, which measures
// the same through the manager; it asserts the intended behaviour.
func TestAVaryingSuiteReadFailureWritesStatusOnNoPass(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "varysuitepass")
	gatedCandidate(t, r, a, imageB, "smoke")
	reads := &varyingSuiteReads{Reader: k8s}
	r.Reader = reads
	writes, msgs := passesThatWrite(t, r, a, varyingPasses, assaydv1alpha1.CondGatesPassed)
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalSuiteUnreadable)
	t.Logf("gated candidate, varying text: %d of %d passes wrote status, %d distinct GatesPassed messages, "+
		"%d suite reads", writes, varyingPasses, msgs, reads.calls.Load())
	if writes != 0 {
		t.Errorf("%d of %d passes wrote status while the suite read failed with one class and changing "+
			"text; the message must not change while the class stands (design 16 A27.8, #155)",
			writes, varyingPasses)
	}
}

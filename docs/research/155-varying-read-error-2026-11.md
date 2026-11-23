# #155: does a varying read error rewrite status on every pass?

Date: 2026-11-23. Base: `7a7d5ce0239627125fde304af9078485991203df`.

This note does #155's first owed item, the measurement. It changes no message and no
production behaviour. The fix belongs to amendments of designs 03 and 16, and those are the
human's to decide. The rows are in `test/envtest/varyingreaderror_test.go`.

## Answer

- **The Gateway read (`internal/controller/authabove.go`, `could not be read (%v)`): yes, it
  loops.** A Gateway read that fails on every pass, with a text that changes each time, writes
  the Agent's status on every pass. Through the manager, each write triggers the next pass.
  Over 5 s a served API-key Agent was written 47 times. With the same failure and a stable text,
  it was written 0 times.
- **The EvalSuite read (`internal/controller/gate.go`): no, not any more.** Design 16 A27.8
  (PR #160) replaced that message's `%v` with `readErrorClass`, so the issue's `could not be
  read: %v` is not in the code at this base. A varying text writes status on no pass. If the raw
  text is put back, the loop reaches 1404 writes in 5 s.

## Method

Each row uses envtest, with a real local API server and etcd and no kubelet. The injection
point is the reconciler's existing exported `Reader` field, the uncached reader that the Gateway
get and the EvalSuite get both use. Existing rows already inject through it: `gatewayUnreadable`
in `authabove_test.go` and `varyingSuiteReads` in `gate_test.go`. No production seam was added.

The injected error is what client-go returns for a connection reset: a `*url.Error` around a
`*net.OpError` with `ECONNRESET`. `readErrorClass` gives it no class of its own ("an error of
another class"). The only part that varies is the ephemeral source port, `10.0.0.7:<port>`. The
port is either fixed (the control) or increased on every call (the measurement).

There are two ways of counting:

- **By hand, N = 10.** One pass enters the failure. Then the test runs ten more passes by
  `reconcileOnce` and counts each pass after which the Agent's `resourceVersion` moved. The API
  server does not bump `resourceVersion` for an update that changes nothing, so a moved
  `resourceVersion` is a status write that changed the object.
- **Through the manager, 5 s.** The Agent is served by hand. It is then handed to a reconciler
  built as the binary builds it, on the manager's client, with `SetupWithManager`. The test
  counts the Agent status updates that the API server accepted (`agentStatusWrites`). The window
  is the 5 s after the first message that names the failure.

## Numbers

| Path, shape | Text | Writes | Distinct messages | Reads |
|---|---|---|---|---|
| Gateway read, served API-key Agent (`GovernanceSkipped=False/AuthVerifiedOnOneReplica` note), by hand | stable | 0 of 10 passes | 1 | 11 |
| same | varying | **10 of 10 passes** | 10 | 11 |
| Gateway read, served API-key Agent, through the manager | stable | 0 in 5 s | — | 1 |
| same | varying | **47 in 5 s** | — | 47 |
| Gateway read, `Create` held at `ProbingAfter` (`PolicyApplyIncomplete=True/GatewayAuthPolicy`), by hand | stable | 0 of 10 passes | 1 | 22 |
| same | varying | **10 of 10 passes** | 10 | 22 |
| EvalSuite read, gated candidate (`GatesPassed=False/EvalSuiteUnreadable`), by hand | varying | 0 of 10 passes | 1 | 11 |
| same, through the manager (`TestAVaryingSuiteReadFailureWritesStatusOnce`, from PR #160) | varying | 0 in 5 s | — | 1 |

The held `Create` reads the Gateway twice a pass, once before its probe and once after (A75). In
the manager rows, one read means one pass. The served Agent's loop ran at about one pass every
100 ms. That is close to the fixture's 100 ms `CardFetchTimeout`, but this note has not shown
that the timeout is what sets the rate. On a real cluster, the rate is however long a pass takes.
No backoff or requeue interval slows it, because each write is an Agent update, and the
Agent watch has no predicate.

The stable-text controls run the same passes with the same error class, and the counts drop
to zero. So the varying text is the only thing driving the writes.

## Mutation evidence

Each mutation was applied through `go test -overlay` from a scratch copy. The source tree
was not edited, and no mutant was committed.

- **`authabove.go:212` changed from `%v` to `readErrorClass(err)`:** all three Gateway rows
  fail. Both varying counts drop from 10 of 10 to 0 of 10. The manager row never sees the
  quoted text, because the message now says "(an error of another class)". This confirms the
  rows measure the text and would flag the fix when it lands.
- **`gate.go:272` changed from `readErrorClass(in.suiteErr)` to `in.suiteErr.Error()`:** both
  EvalSuite rows fail. The new hand row counts 10 of 10. The existing manager row counts 1404
  writes and 1405 suite reads in 5 s.

## How the rows are written

The three Gateway rows assert the **current** behaviour, which is the defect. A stable text
writes nothing, and a varying text writes on every pass (by hand) or at least ten times in 5 s
(through the manager). Each row's comment names #155 and says it must be flipped when the fix
lands. That keeps them green in `make test`, rather than leaving a permanently red row or a
skipped row that nothing runs. The EvalSuite row asserts the intended behaviour, because A27.8
already holds it.

## Not measured

- Other condition messages that quote a read error's text, which #155 does not name:
  `card.go:182` (the revision's Service read) and `authkeysource.go:59` (the live
  `<agent>-auth` read on the key-source pass). Whether either reaches status on a pass that
  would otherwise write nothing was not measured.
- The policy list on the same Gateway path (`could not be listed (%v)`, `authabove.go:229-230`).
  It quotes the error the same way. The Forbidden branch of the Gateway get (`may not read
  ... (%v)`) also quotes it. The rows here inject only a transport error on the Gateway get.
- A real transport failure on a cluster. The error is built by hand, in the shape client-go
  returns, and is not taken from a live connection reset.

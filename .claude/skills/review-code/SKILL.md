---
name: review-code
description: Adversarial review of a plume code diff before merge. Use after implementing a feature and before merging. Complements critique-design (which reviews specs); this reviews code.
---

# Reviewing a plume diff

**Stance**: you are not the author. Hunt for what breaks. If this session wrote the code, say so and prefer a fresh session or subagent.

Run every lens; report severity-tagged (BLOCKER / MAJOR / MINOR) with file:line and a concrete fix.

1. **Design fidelity** — does the code do what the approved design says? Name the design section. Silent deviation is a finding even if the code is better; the design should change first.
2. **Test quality, not test presence** — do tests assert *designed* behaviour or merely current behaviour? Would they fail if the feature regressed? Is the failing-first discipline evident? Are error paths and degraded paths tested, not just happy paths?
3. **Reconcile correctness** — idempotency (does a second reconcile change anything?), requeue behaviour, partial-failure recovery, ownership/finalizers, status vs spec writes, conflict handling.
4. **Concurrency** — races on shared state, unsynchronised maps, goroutine leaks, missing context cancellation. `go test -race` must be in the evidence.
5. **Security** — least privilege in RBAC, no credentials in logs or status, fail-closed on error paths, admission assumptions actually enforced somewhere.
6. **Doctrine** — pods added, stateful deps touched, library-over-server; the weight ledger updated in the same change if either moved.
7. **Failure modes** — does every path in the design's failure table exist in code, and does each set the condition the design names?
8. **Simplicity** — is there a smaller correct version? Premature abstraction is a finding.

**Output**: verdict `PASS | REVISE`, findings, and the evidence you checked (test output, race detector, coverage of the changed lines). On PASS the change may merge.

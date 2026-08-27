---
name: verify-change
description: The loop that closes a plume change — amend, gate, critique independently, critique cross-family, spike the unmeasured claim, correct, re-review. Use after any design amendment, ADR, or code change that asserts a property, and whenever a review returns findings. A change is not done when it reads right; it is done when the claim it makes has been measured or a test can fail on it.
when_to_use: after an amendment or fix, when a review returns findings, before calling a design ready, "verify this change", "close the loop", "is this actually done"
---

One change through the whole loop. Stop at the first stage that fails; do not carry a
finding forward as "addressed" without evidence.

## The loop

1. **Write the change.** Amend the design first — code that diverges from an approved
   design is drift. Record it as a numbered amendment in the same commit.
2. **Run the gate.** `make test`. The docs gate (`test/docs/`) fails on any superseded
   guarantee surviving in an authoritative body. If you retracted something, add its rule
   *and its independent fixture* in the same change.
3. **Critique independently** — `/critique-design`, which forks its own context.
4. **Critique cross-family** — hand the same target to the Codex peer. Give it the changed
   state as *facts* and never your conclusions; anchoring it spends the only thing it is
   for. See `docs/agent-protocol.md`.
5. **Spike every load-bearing claim you cannot already fail a test on.** See below.
6. **Correct, and propagate.** Then re-run from 2.

## The rules this loop exists because of

Each one cost a real defect in this repo.

- **Measure before asserting.** Four review rounds converged on a design built against
  "agentgateway 2.2" — a release that does not exist. Every citation resolved, because the
  stale docs path still returns HTTP 200. No amount of internal review reaches that; one
  primary-source check did.
- **A reviewer's prescribed fix can be wrong too.** The snapshot prescription for the env
  bypass was retracted by its own author on scoped review. Ask a reviewer to review its own
  fix against costs you have found; that is a review request, not a negotiation.
- **A replacement guarantee is the most dangerous sentence you will write.** Every guarantee
  retracted in this corpus was replaced by one that was also too strong: three budget bounds,
  an asymmetric revision gate, an immutability rule, a witness that could not attribute. When
  you replace a withdrawn claim, spike the replacement before committing it.
- **A test that derives its cases from production data is vacuous.** Deleting a rule from the
  docs gate left the suite green; deleting a mandatory entry from an emitter registry deletes
  its own generated test. Cases must be written independently of the thing they pin.
- **Propagation is the recurring failure, not reasoning.** Three consecutive rounds fixed the
  amended paragraph and left the mapping table, the ADR title, the index row or the consumer
  design asserting the withdrawn claim. Grep the whole corpus for every restatement, including
  commit messages.
- **Retraction must be recordable.** Ban the assertion, permit the retraction — otherwise the
  gate flags the sentence that corrects the error.

## Spiking: what counts as measured

A claim about **our own code** is measured when a test fails if the claim stops holding, and
that test has been mutation-checked — delete the rule it names, confirm it fails, restore from
a `cp` backup, never `git checkout`.

A claim about a **dependency** is measured only against the published artifact or a running
cluster at the pinned tag. Not the docs site, which serves stale versions; not `main`, which
is unreleased. `make conformance` pins what has been measured so far; extend it rather than
re-deriving.

If a claim cannot be measured either way, it is a **stated gap**, not a design sentence.
Write it into the amendment as owed and do not build on it.

## Test layers, and what each can honestly claim

- **unit** (`api/`, `internal/`) — pure logic, asserted against the generated artifact.
- **docs** (`test/docs/`) — text only. Proves no withdrawn guarantee survives; proves nothing
  about behaviour.
- **conformance** (`test/conformance/`, `make conformance`) — the pinned dependency contract.
  Needs a cluster. This is the layer that catches an upstream change, and the only layer whose
  failure means *the world moved*, not that we broke something.
- **envtest** — a real API server, no kubelet. Every availability claim here is against a
  status the test wrote.
- **e2e** — the only layer that can claim a pod runs and traffic flows.

## Refuse to finish

A finding marked addressed with no evidence · a replacement guarantee that was not spiked ·
a rule added to the gate without an independent fixture · a retraction that reached the
amendment log but not the mapping table · a claim about a dependency sourced from a docs page
rather than the pinned artifact · "owed" used for something the change actually depends on.

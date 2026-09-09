---
name: verify-change
description: The loop that closes a assayd change — amend, gate, critique independently, critique cross-family, spike the unmeasured claim, correct, re-review. Use after any design amendment, ADR, or code change that asserts a property, and whenever a review returns findings. A change is not done when it reads right; it is done when the claim it makes has been measured or a test can fail on it.
when_to_use: after an amendment or fix, when a review returns findings, before calling a design ready, "verify this change", "close the loop", "is this actually done"
---

One change through the whole loop. Stop at the first stage that fails; do not carry a
finding forward as "addressed" without evidence.

## The loop

**The spike comes first. This is the ordering, and it is the fix for a real failure.**
Until 2026-09-05 the spike was step 5, behind two critique stages, under this file's own
"stop at the first stage that fails". A design that kept failing critique therefore never
reached the measurement that would have settled it — which is exactly what happened to
design 03: three rounds returned six, then five, then five blockers, each finding the
previous fix's defect one field over, while the premise under five amendments sat unchecked
in another design's CRD schema. One person opening design 11 §3 ended it. Critique is for
reasoning you cannot measure; it is not a substitute for looking.

1. **Read the producing document, and measure the load-bearing claim.** Before writing:
   for every claim your change makes about another design, an ADR, a dependency or the
   cluster, open that source and quote it — a claim about a document is checked *in that
   document*, never inferred from a citing one. Pin dependency claims to the released tag,
   not `main` or a docs page. If it can be run, run it. See "Spiking" below.
2. **Write the change.** Amend the design first — code that diverges from an approved
   design is drift. Record it as a numbered amendment in the same commit.
3. **Run the gate.** `make test`. The docs gate (`test/docs/`) fails on any superseded
   guarantee surviving in an authoritative body. If you retracted something, add its rule
   *and its independent fixture* in the same change.
4. **Critique independently** — `/critique-design`, which forks its own context.
5. **Critique cross-family** — hand the same target to the Codex peer. Give it the changed
   state as *facts* and never your conclusions; anchoring it spends the only thing it is
   for. See `docs/agent-protocol.md`.
6. **Correct, and propagate.** Then re-run from 3.

**Two failed passes on one contract ends the loop.** Do not open a third round on another
prose patch. Stop and take one of three decisions instead: run the experiment that settles
the premise, cut the scope, or redesign the interface. Record which you chose. A falling
blocker count is neither necessary nor sufficient for a working system.

**A finding is closed by implementing the contract or by withdrawing the promise.** An
"owed to design NN" note, a new condition, or a registry entry is not closure — it is the
finding restated. If no producer exists, the capability is unsupported: say so in §5 and
reject it at the boundary. Never withdraw the underlying failure scenario to obtain a PASS.

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
- **A claim about another document is the single most common defect here.** Five design-03
  amendments rested on an input said to come from "the resolved tool server's advertised set";
  it is a declared field on the Connector CR, and the cited anchor said so. The critique that
  caught it then made the same error one table row below, citing a `KnowledgeGraph` CR that
  exists in no design. Two independent reviewers found that missing CR separately. Open the
  producing file. Quote it. Every time.
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
- **conformance, offline** (`test/conformance/`, in `make test`) — schema facts read from the
  vendored, digest-pinned dependency artifact. Hermetic; no network, no cluster.
- **conformance, cluster** (`make conformance-cluster`) — behaviour measured against a real
  gateway at the pinned tag; provisions and tears down. These two are the only layers whose
  failure means *the world moved* rather than that we broke something, and each assertion
  names the design sentence it protects.
- **envtest** — a real API server, no kubelet. Every availability claim here is against a
  status the test wrote.
- **e2e** — the only layer that can claim a pod runs and traffic flows.

## Refuse to finish

A finding marked addressed with no evidence · a replacement guarantee that was not spiked ·
a rule added to the gate without an independent fixture · a retraction that reached the
amendment log but not the mapping table · a claim about a dependency sourced from a docs page
rather than the pinned artifact · "owed" used for something the change actually depends on.

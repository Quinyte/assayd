# Handoff — plume, 2026-09-03

Written at the end of a long session so the next one starts from facts rather
than from a summary of a summary. Read this, then `AGENTS.md`, then the two
design headers. Everything here was verified by running it, not recalled.

## The one-line state

**Design 03 (the policy compiler) has not started and is still marked
`RE-OPENED, do not implement`.** No review round has returned PASS. The session
went into design 02's revision identity instead, because ten Codex rounds and
three same-family critiques kept finding the foundation design 03 sits on did
not hold.

Tree clean at `5366b60`. All gates green — see *Running tests* below, and note
which ones are **not** in the per-commit loop.

## What is implemented (as opposed to written)

The distinction this document has been wrong about most often. These run:

| Amendment | What it does |
|---|---|
| A20 | revision identity covers the resolved **content** of every env source |
| A21 | `runtime.image` must be a digest-pinned OCI reference (CEL) |
| A35 | a revision reads its own **immutable copy**; the workload never reads the user's object |
| A37 | the full digest is the revision identity; the 10-char name is a name |
| A50 | the digest reaches `evalStatus` and `cards[]` |
| A53 | typed LLM endpoint identities (arm + instance fields) |
| A56 | revision material is collected with its revision and with the Agent |
| A57 | RBAC for the above; the delete authority is the **name**, not a label |

Everything else in design 02's A1–A59 is design.

## The open decision that is not a decision any more

**A42 — the operator-owned run namespace.** The user chose "cross-design pass,
then implement". It is the last of the env-source bypass: today a principal with
`create`/`delete` on ConfigMaps can delete a revision's copy and recreate it
under the same name (`immutable: true` forbids an update and permits exactly
that). `EnvSourceProtectionUnavailable` announces this on every affected Agent.

**Four amendments owed; two are done.**

- [x] **design 03 A44** — namespace of every emitted resource, label-not-ownerRef
      provenance, Gateway `allowedRoutes` must admit run namespaces, naming
      suffix widened 8→16 hex
- [x] **design 02 A59** — the `ClusterSPIFFEID` must key on a
      `plume.dev/agent-namespace` **label**, not `.PodMeta.Namespace`, or the move
      re-identifies every agent and every grant design 24 keys on the principal
      stops matching
- [ ] **design 07** — ships the `ClusterSPIFFEID` (A59's change lands there), the
      Gateway with `allowedRoutes` selecting `plume.dev/run-namespace`, the
      default-deny NetworkPolicy §6 already requires and the chart does **not**
      ship, PodSecurity label mirroring, and the ResourceQuota question A44 names
      as unsolved
- [ ] **design 26** — which side of the vCluster split owns the run namespace

Then implement A42, then an e2e proving delete-and-recreate no longer works.

## Also open

- **r8 blockers 7–13 and majors 3, 6** — `docs/designs/reviews/03-codex-review-r8.md`.
  7–9 are A42 propagation. 10–13 are design 03 and critique amendments 03
  A41–A43, so they are the next thing after A42.
- **Cross-family review is unavailable.** Codex refused r9 on cybersecurity
  grounds after refusing r8 once and producing it on a re-frame. Recorded in
  `docs/designs/reviews/README-review-availability.md`. Same-family critiques
  found two blockers r8 had passed, so they are useful — and they are the same
  model, which is weaker evidence, not equivalent.

## Running tests — and the trap

```
make test      # 7 layers: fmt, vet, unit, docs, hermetic conformance, envtest, chart
make race      # internal/... api/...
make verify    # generation reproducible AND committed
make e2e                  # REAL k3d cluster — creates plume-local
make conformance-cluster  # REAL k3d cluster — creates plume-conformance
```

`make test`, `race` and `verify` ran before every commit this session. **The two
real-cluster suites did not**, and both were broken for several commits before
anyone ran them:

- A21's digest migration rewrote every tagged image, including two REAL images,
  to **synthetic digests** — valid to CEL, unpullable by a kubelet. The e2e
  workload test and the cluster suite's traffic pod both silently stopped
  working. Both digests are now named constants with the reason attached.
- e2e reused one image tag with `pullPolicy: Never`, so `helm upgrade` never
  rolled the pod. The operator under test had been running **nine days**. Fixed
  with a per-run tag plus `TestTheOperatorUnderTestIsTheOneJustBuilt`.
- Fixing that exposed a shipped defect: `readyz` waits on leader election, so the
  default rolling update deadlocks every upgrade. Chart now sets
  `maxSurge: 0 / maxUnavailable: 1` (design 07 A4).

**Run both real-cluster suites before believing a green report on anything they
cover.** Three of the four defects in that stretch were in the harness.

## Rules this session paid for

1. **Reproduce before fixing.** Every blocker acted on was reproduced first.
2. **Mutation-check every behavioural claim**, and distinguish INVALID (did not
   compile) from SURVIVED. Several "survivors" were bad mutations of mine.
3. **A refusal is not evidence about which rule refused.** Tests asserting "it
   was refused" passed while the check under test was unreachable — three times.
4. **Check the producing design, not the note.** Findings that answer a review in
   the document it points at, without following into the document that owns the
   other side, are the recurring failure. A42's own owed list named design 06 for
   something that has always been design 02's.
5. **Green is not evidence if it is about the wrong artifact.** See above.
6. `cp` for backups, never `git checkout`, while mutating.
7. Stage explicit paths; run `git status --porcelain` first.

## Where the defects came from

Nearly every serious one was **mine**, found by mutation or an independent pass,
not by reading: a fail-closed rule that made a weaker principal's attack
permanent; a condition enum that could lose an entire status; a GC keyed on a
forgeable label; a leak introduced in the commit that argued against leaking.
Three times a conclusion I had talked myself into was contradicted by the
mutation result.

Design 02 is 59 amendments deep and none has passed review. The defect discovery
rate is not falling — each round closes N and introduces roughly 2. That is a
process signal, and consolidating design 02 into one body was offered as an
option and not chosen; it is worth raising again if the next rounds look the
same.

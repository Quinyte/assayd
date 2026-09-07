# Code review — ADR-0030 step 2, commits `6b1c935` · `b7744b9` · `cb2a5a4` (design 02 A66 / A67 / A68)

- **Reviewer**: Claude (Fable 5.1), fresh context, reading cold. Same family as the implementer; the cross-family pass is still owed.
- **Scope**: `git diff 6b1c935~1 HEAD` — 25 files, +1925/−13. `api/v1alpha1/agent_types.go` (`ReleaseSpec`), `internal/controller/release.go` (new), `internal/controller/card.go` (new), `agent_controller.go`, `internal/revision/leaves.go`, `test/responder/` (new), `hack/e2e.sh`, `test/e2e/responder_test.go` (new), `test/envtest/{card,release}_test.go` (new), design 02 §5 and §12 A66–A68, `AGENTS.md`, `CLAUDE.md`.
- **Spec**: design 02 §3.2, §3.3, §3.4, §4, §5; ADR-0019; ADR-0029; ADR-0030 (build order step 2); ADR-0031 decision 2; design 03 §3.3 (route `backendRef`); design 09 §3 item 2 and §5; design 11 (tool resolution); `reviews/00-astra-direction-review.md` §(b).
- **Verdict: REVISE — 1 BLOCKER, 9 MAJOR, 7 MINOR.**

The three commit messages are candid and their INVALID/SURVIVED/KILLED distinctions are correctly drawn. That is not the problem. The problem is that every mutation in the three ledgers deletes something a test *asserts*, and the tests assert too little: the central property of A66 — "selected, never recomputed" — was measured for ConfigMap **content** only, and is false for everything else in the spec (BLOCKER 1). Two of the three mutations I ran that the author did not run survived the full unit and envtest suites.

## What was run

| Gate | Result |
|---|---|
| `go build ./... && go vet ./...` at HEAD | clean |
| unit (`./internal/... ./api/... ./test/responder/...`) at HEAD | green |
| envtest, full suite, at HEAD (`setup-envtest use 1.36.x`) | green, 81.0s |
| `CLUSTER=plume-e2e-resp make e2e` on a clean tree | green, `EXIT=0`; all 12 tests incl. the three new responder tests (74.1s of test time, ~3 min wall) |
| `CLUSTER=plume-e2e-resp make e2e` with mutation X3 applied | `TestTheOperatorRegistersTheCardItFetched` FAILS at `responder_test.go:324` — see ledger |
| Seven measurement probes (envtest, written for this review and deleted afterwards) | each reproduced the behaviour cited in the finding that names it (T1–T7 below) |
| Mutation ledger (this review's) | 4 mutations, each `go build`-checked before the result was believed; each restored from a `cp` backup and the restore checked with `git diff --quiet` |

`git status --porcelain` after this review shows only this file, plus one modification to `docs/agent-protocol.md` that predates my session and is not mine — I did not touch it.

## Mutation ledger — mutations the author did not run

| # | Mutation (one line, compiles) | Unit | envtest | e2e | Result |
|---|---|---|---|---|---|
| X1 | `release.go:159` `names[ref] = name` → `names[ref] = ref.Name` — the pinned workload is pointed at the **user's live object**, not the immutable copy | pass | pass | not run | **SURVIVED** |
| X2 | `release.go:152` disable the recorded-source check (`false && got != …`) — the "refused, not guessed" row of §5 | pass | pass | not run | **SURVIVED** |
| X5 | `release.go:81` `if d == "" \|\| d != want` → `if d == ""` — resolve the pin to any owned workload | pass | `TestAnUnresolvablePinRefusesRatherThanServingCurrentSpec` FAIL | — | KILLED |
| X3 | `agent_controller.go:501` `CondCardUnsigned` set `False` instead of `True` | pass | pass | `TestTheOperatorRegistersTheCardItFetched` FAIL | KILLED **by e2e only** |

X1 and X2 are the two halves of what A66 says the rollback guarantees: that the pinned revision serves *its own* copies, and that a shape mismatch is refused. Neither is pinned. X3 shows that design 09's `CardUnsigned` rule has no test below the 3-minute k3d run.

## Measurement probes (envtest, deleted afterwards)

| # | What was measured | Observed |
|---|---|---|
| T1 | The card **success** path in envtest, via an injected `CardClient` (the committed suite never exercises it) | Registers in one reconcile, `Registered=True/CardValidated`, `status.cards[0]` written. **`RequeueAfter` after registration = `0s`.** After ageing `fetchedAt` past `CardDriftInterval` and changing the served bytes (a new skill `delete-everything`, version `9.9.9`): digest silently replaced, `Registered` still `True/CardValidated`, no condition, no event, `RequeueAfter` again `0s` |
| T2 | The 15s retry gate on an unreachable card | 1 dial during settle. After the first interval elapsed: **5 back-to-back reconciles → 5 dials**; `Registered.LastTransitionTime` frozen at the first failure |
| T3 | Pin R1 after the user's referenced ConfigMap is **deleted** (R1's copy intact) | Refused before the pin is read: `Ready=False/EnvSourceUnresolved`, active stays R2, phase `Pending` |
| T4 | `replicas: 2` with a card asserting `capabilities.sharedTaskState: true` | `Registered=True/CardValidated` beside `TaskStateUnverified=True/CardNotFetched, "…card fetch (§3.4) is not implemented…"` |
| T5 | Pin R1 after R1's immutable copy is deleted | Refused, `ReleasePinUnresolvable`, message names the copy — **correct** |
| T6 | Pin R1 after the spec's source list changed (`cfg1` → `cfg2`) | Refused, `ReleasePinUnresolvable`, message names both sources — **correct** |
| T7 | Pin R1 after an **image** change: R1 = image A (promoted), R2 = image B (promoted), then `targetRevisionDigest = R1` | `status.activeRevision = R1`, `activeRevisionDigest = R1's`, phase `Ready` — **and R1's Deployment now runs image B**, stamped with R1's digest annotation |

---

## BLOCKER 1 — A pinned revision is re-rendered from the *current* spec: rollback after an image change serves the new image under the old revision's identity

**File**: `internal/controller/agent_controller.go:334` (`desired, desiredDigest = pin.Revision, pin.Digest`), `:430` (`ensureWorkload(ctx, &agent, …)`), `:848` (`desired := r.deploymentFor(agent, runNS, rev, material)` — renders from `agent.Spec`, the spec as it is *now*), `:955-965` (the whole-template rewrite when `!templateEquivalent`).

**Evidence** (T7, envtest): image A gated and promoted as R1; image B deployed and promoted as R2; `spec.release.targetRevisionDigest` set to R1's digest. Result: `active=R1`, `activeRevisionDigest=R1`, `phase=Ready`, and R1's Deployment `spec.template.spec.containers[0].image == B`, annotation `plume.dev/revision-digest == R1`.

The pin selects R1's **name, digest and material names** and nothing else. `deploymentFor` then renders the pod template from the current spec — image, port, resources, env *names*, injected env — and the drift-correction block rewrites R1's template to match, stamping the result with R1's digest. The one case the committed test exercises (`TestARollbackUnderSourceDriftSelectsTheRetainedRevision`, ConfigMap content drift) is exactly the case where the current spec renders an identical template, so the defect is invisible there.

This is the code's own nightmare scenario, in its own words (`agent_controller.go:346-347` and `:888-890`): "the operator itself installed the attacker's image on the gated revision", "rewrites H's pod template to the malicious image while status still names the revision that passed." Here no attacker is needed: the ordinary rollback path does it. A66's "selected, never recomputed" and ADR-0019's "instant rollback" are false for the most common rollback there is — a bad image. §3.3's "A rollback re-runs the material its revision was gated with" is false. The §5 row "Rollback eligibility … proven to survive source drift" is true only of ConfigMap bytes.

**Fix**: under a pin, nothing may be rendered from `agent.Spec`. Either (a) the retained Deployment *is* the record — do not call `ensureWorkload` for a pinned revision; verify the existing object's digest stamp and UID label and leave its template alone, and make drift correction for a pinned revision compare against the stamped template it already has (which needs the template to be part of what the digest vouches for), or (b) persist each revision's rendered PodSpec at mint time (the `status.revisions[]` this design keeps deferring, or a per-revision ConfigMap in the run namespace) and render the pinned revision from that. Whichever is chosen, `TestARollbackUnderSourceDriftSelectsTheRetainedRevision` must assert **what R1's Deployment runs** after the pin — image and env refs — and a second test must do it across an image change, not only a ConfigMap edit. Until fixed, §5 must say: "a pin selects the revision's name, digest and copies; its pod template is re-rendered from current spec."

---

## MAJOR 1 — The rollback tests do not observe what the pinned workload runs (mutation X1 SURVIVED)

**File**: `test/envtest/release_test.go:113-118`.

The comment says "The evaluated material is what serves — not the drifted bytes." The assertion beneath it is that R1's Deployment *exists*. Mutation X1 pointed the pinned workload's env refs at the user's live ConfigMap (`material.go:190` rewrites `ConfigMapKeyRef.Name` to whatever the map says) and the full unit and envtest suites stayed green. This is the class of test AGENTS.md rule 1 describes — and it is what let BLOCKER 1 through: the author's R4 ("pinned path rewrites material") was KILLED because it made `ensureRevisionMaterial` *refuse*; nothing checks the template that results when it does not refuse.

**Fix**: after the pin, read R1's Deployment and assert `env[0].valueFrom.configMapKeyRef.name == MaterialName(agent, r1, 0)` and `image == R1's image`. Then re-run X1 and expect a FAIL.

## MAJOR 2 — The shape-mismatch refusal and the missing-copy refusal are unpinned (mutation X2 SURVIVED)

**File**: `internal/controller/release.go:145-158`; design 02 §5 rows "Rollback across a spec whose sources changed shape" and (failure table) "Rollback target's revision COPIES are missing".

Both refusals *work* today — T5 and T6 measured them and the reasons are correct. No committed test reaches either branch; disabling the recorded-source check (X2) survives everything. Rule 5: code no test can pin reads as load-bearing and is not. Rule 1's default outcome is that it will be broken the next time `retainedMaterial` is touched.

**Fix**: two envtest cases, one deleting `MaterialName(agent, r1, 0)` before the pin and asserting `ReleasePinUnresolvable` with the "missing" message, one changing the source list and asserting the "different set of sources" message — as T5/T6 did.

## MAJOR 3 — A68's rationale for not holding the candidate is contradicted by the design body it cites

**File**: `internal/controller/card.go:48-51`, `agent_controller.go:474-479`; `test/envtest/card_test.go:57-77` (`TestAFailedCardFetchDoesNotWithholdTraffic`); design 02 §12 A68.

A68 and the code comment say: "§3.4 makes an unregistrable card a registration failure, not a serving one … blocking here would invent an enforcement the design does not describe." The design describes it. §5's failure table, verbatim: **"Card fetch fails | `Registered=False`, candidate held, backoff; failed and deleted at the registration deadline."** §4's reconcile outline places "candidate Ready? fetch + validate + cross-check card" *before* "hold/advance rollout". Design 09 §5, the consumer: **"User deletes the loop / serves no card | Registration fails with named condition (design 02) — the contract is enforced at the platform edge."** An agent that serves no card, or someone else's card, is today promoted and serves traffic; the test pins that it is.

The decision itself may be right for the slice — holding forever with no deadline is worse — but it is a decision the body must record, not one an amendment may attribute to the body. AGENTS.md: "Code that diverges from an approved design is drift, whatever it does. Amend the design first."

**Fix**: rewrite the §5 failure row and §4 to say what the code does, state the trade-off (no deadline exists, so a hold would be permanent), and add a §5 first-table row: "A candidate with no valid card is promoted; nothing withholds traffic from an unregistered revision." Correct the A68 sentence and the `card.go` comment — the rationale as written is false against the document it cites.

## MAJOR 4 — Card drift is neither scheduled nor signalled

**File**: `internal/controller/card.go:233-236` (`CardDriftInterval`, "how often a card already fetched is read again"), `:253`; `agent_controller.go:518-520` (requeue only when `!hasCardFor`); `card.go:217-231` (`upsertCard` overwrites the digest); design 02 §3.4 "Card drift at runtime (digest change without spec change) → re-fetch, directory update, event"; A68 "a per-revision digest over the served bytes so drift is detectable".

Measured (T1): after a successful registration `Reconcile` returns `RequeueAfter: 0`. No `SyncPeriod` is set (`cmd/operator/main.go`), so the manager's default of ~10h applies; a converged agent is not reconciled again until something else touches it. `CardDriftInterval` is therefore a bound nothing enforces (rule 7) — the constant's comment states a schedule that does not exist. And when a re-read does happen on an incidental reconcile, a changed digest is overwritten in place: the served card gained a skill and changed version, and the only trace was twelve hex characters in the `Registered` message. No condition changed, nothing was logged, and `Registered` stayed `True/CardValidated`. "Detectable" here means "a human diffing two status dumps could notice."

**Fix**: while a revision is registered, requeue at `CardDriftInterval`; on a digest change, say so — the condition vocabulary is closed, so either `Registered=False/CardDrifted` or a decision to add a type — and log at Info. Pin it with a test that changes the served bytes and asserts the transition. Until then, §5 needs the row: "card drift is not re-read on any schedule and a changed digest raises nothing."

## MAJOR 5 — `TaskStateUnverified` is now loud and wrong

**File**: `internal/controller/agent_controller.go:1210-1223`; `test/responder/main.go:52-56` and `main_test.go:47-52`; design 02 §3.2 line 131.

Measured (T4): an agent with `replicas: 2` whose card asserts `capabilities.sharedTaskState: true` carries `Registered=True/CardValidated` beside `TaskStateUnverified=True/CardNotFetched, "…card fetch (§3.4) is not implemented…"`. The card *was* fetched, in the same status write. §3.2: "the A2A card is the declaration point … Absent that assertion with `replicas>1`, the operator sets `TaskStateUnverified=True`." The responder's comment claims the operator keys the condition off the capability; `fetchedCard` (`card.go:87-94`) does not even parse `capabilities`. Rule 8: "a condition must name the real cause, not a plausible one that was never checked."

**Fix**: parse `capabilities` in `fetchedCard`; clear the condition when the registered card asserts the capability, otherwise set it with reason `CardDoesNotAssertSharedTaskState`; keep `CardNotFetched` only while no card is registered for the revision. Add a §5 row until then.

## MAJOR 6 — "ADR-0031 decision 2 also requires gate-evidence eligibility and a security recheck" — it does not

**File**: `internal/controller/release.go:61-64`; design 02 §5 row "Rollback eligibility"; §12 A66 ("Two halves of decision 2 remain owed"); commit `6b1c935` message.

ADR-0031 decision 2, in full, settles: the field rather than a request CR, "unset means follow desired spec, set means stay pinned until explicitly changed or cleared", and when a CR would be justified later. It says nothing about gate evidence, governed installs, or rechecking security constraints. Those requirements exist — in `reviews/00-astra-direction-review.md:190-191` ("Refuse missing/corrupt material and revisions lacking the required gate evidence for a governed install … a rollback is not authorization to restore a revoked credential") — and the ADR did not adopt them. This is the defect the project's own record calls its most common: a claim about a producing document that is false when the document is opened. The substance is right; the citation is wrong, and a reader checking ADR-0031 will conclude the §5 row invented a requirement.

**Fix**: cite the review, or fold the two requirements into ADR-0031 (or a superseding ADR) so the citation becomes true.

## MAJOR 7 — `DISTRO=kind make e2e` — a documented command and a CI matrix lane — is broken by `b7744b9`

**File**: `hack/e2e.sh:36-40` (registry created in the `k3d` branch only), `:102-118` (build, `docker push localhost:5111/…` and `PLUME_E2E_RESPONDER_IMAGE=k3d-plume-e2e-registry:5111/…`, unconditional); `.github/workflows/ci.yml:88` (`distro: [k3d, kind]`); AGENTS.md "Commands" (`DISTRO=kind make e2e`); CLAUDE.md line 5 ("pass on k3d and kind").

Read, not run — there is no kind cluster here and I did not create one. Under `set -euo pipefail`, the kind lane reaches `docker push localhost:5111/plume-responder:…` with no registry at that address and exits; if a registry happened to be present, the kind node could not resolve `k3d-plume-e2e-registry:5111` and `responderImage()` would fail the three responder tests. The commit message claims green on k3d only and does not mention kind; the commits are unpushed, so CI has not yet said this. It will.

**Fix**: a kind-compatible registry path (kind's documented local-registry pattern with `containerdConfigPatches`), or a `kind)` arm that fails loudly naming the gap — either way, stop the header line from saying "k3d and kind" until both are true.

## MAJOR 8 — After `cb2a5a4`, the first paragraph every contributor reads is false again

**File**: `AGENTS.md:11`, `CLAUDE.md:9` ("…and the operator does not fetch the card the container serves"); design 02 §5 header ("registration and card handling (§3.4) have no code at all"); §5 row "Registration, card fetch, cross-check, `status.cards[]`, directory writes | §3.4 in full is unimplemented"; §5 row "`registrationDeadline` as a spec field | Registration is unimplemented, so nothing counts down"; §3.1 line 113 and §3.4 line 389 ("registration is unimplemented"); `test/e2e/responder_test.go:100-103` ("The operator does not fetch it yet"); `test/responder/main_test.go:12-14` ("the validation does not exist yet"); `agent_controller.go:1210-1212`.

`b7744b9` corrected AGENTS.md and CLAUDE.md for its own step and said so in its message. `cb2a5a4` implemented the fetch and corrected neither, nor the four places in design 02's body that still say registration has no code, nor the test comments that say the fetch is owed. AGENTS.md:9 warns, in bold, that a false summary at the top of the file was "the first thing every contributor read." It is again. The §5 first table now contains rows that say card fetch is implemented (A68's three rows) *and* a row that says "§3.4 in full is unimplemented", six rows apart.

**Fix**: mechanical, one commit; `/audit-docs` exists for this.

## MAJOR 9 — A rollback is declined when the user's source object has been deleted, though the retained copy is intact

**File**: `internal/controller/agent_controller.go:250-264` (`resolveEnvSources` and the `reportUnresolvedSources` return run *before* `resolveReleasePin` at `:321`); `release.go:113-115` ("without writing anything and without reading the user's sources" — true of `retainedMaterial`, not of the reconcile that reaches it).

Measured (T3): R1 promoted with ConfigMap `cfg`; drift; R2 promoted; `cfg` deleted; pin R1. Result: `Ready=False/EnvSourceUnresolved`, active stays R2, phase `Pending`; the pin is never read. §5's failure table says, of the user's object having changed, "Refusing here would decline a recovery that is perfectly available." Deletion is the harder incident and the recovery is equally available — R1's copy is there (T5 shows the operator can tell). The design's row is about changed content, so this is a gap rather than a contradiction, but it is the incident path the field exists for.

**Fix**: resolve the pin before resolving current sources, and resolve sources only on the unpinned path; then §5 can state the deleted-source case as covered.

---

## MINOR 1 — The 15s retry gate holds for 15 seconds, once

**File**: `internal/controller/card.go:255-261`; `conditions.go:109-110` (LTT preserved when status is unchanged).

Measured (T2): after the first interval, five back-to-back reconciles dialled five times. The gate keys on `Registered.LastTransitionTime`, which `merge()` correctly freezes while the status stays `False` — so the gate is open forever after its first 15s. Each dial blocks the shared work queue for up to `CardFetchTimeout` (5s in production). The unit test's "failed just now" case checks age 1s and cannot see this. **Fix**: record the last *attempt* time (e.g. on a per-revision entry in `status.cards[]` with an empty digest, or an annotation), not the transition time.

## MINOR 2 — An unresolvable pin sets `Ready=False` while saying the release is untouched, and suspends convergence of the active revision

**File**: `internal/controller/release.go:106-108`; `agent_controller.go:322-325` (return before `ensureWorkload`, `ensureService`, `collectGarbage`).

The message says "Nothing is rolled back and the current release is untouched"; the condition says `Ready=False`. A13's rule, written in this file at `:526-528`, is that `Ready=False` on an agent whose active revision is serving "would trip every alert keyed on the canonical condition." A typo in a 64-character digest pages. Separately, the early return means that while a bad pin sits on the object, the active revision's Deployment is not drift-corrected and nothing is collected. **Fix**: `Degraded=True/ReleasePinUnresolvable` with `Ready` left to the active revision's state, and continue the reconcile with the unpinned `desired` rather than returning.

## MINOR 3 — `status.cards[]` is never pruned

**File**: `card.go:217-231`; `agent_controller.go:1226-1270` (`collectGarbage` touches Deployments only). §3.4: "collected with revisions." One entry per revision the agent has ever registered, forever.

## MINOR 4 — "A real A2A responder" is measured as an HTTP JSON responder

**File**: `test/responder/main.go:114-144`; `card.go:85` (`supportedA2AVersions = {"1.0"}`); AGENTS.md:11; design 09 header ("A2A v1.0 … JSON-RPC + SSE").

The fixture answers a bespoke `POST /v1/tasks` with a bespoke body (`agent`, `gateway` fields). Design 09 and the research note describe A2A v1.0 as JSON-RPC + SSE. The operator's closed version set was written to match the fixture's string, and no document in the repository states what value a v1.0 card's `protocolVersion` carries. Nothing here checked the protocol; the two sides were written by one hand to agree. What is proven is a request reaching a revision Service and an answer naming the revision — which is what step 2 needed. **Fix**: say "HTTP responder" until `/research-latest` has pinned the v1.0 card and task shapes; design 03's route will assume the real ones.

## MINOR 5 — Names and shapes in `card.go`

`getWithRetries` (`:183`) makes one attempt, inside a bare `{ … }` block. `fetchedCard.Skills` is parsed and unused. The 1 MiB `LimitReader` (`:205`) turns an oversized card into `CardUnparseable` rather than a reason that names the size. The `TaskStateUnverified` assessor comment (`agent_controller.go:1210-1212`) is stale (MAJOR 5 covers the behaviour).

## MINOR 6 — The success path is never exercised in envtest, though it can be

`test/envtest/card_test.go` covers only `CardUnreachable`. T1 registered a card in envtest in under 100ms with a redirecting `CardClient` — the mechanism `card_test.go` (unit) already uses. The success path's wiring in `Reconcile` (`upsertCard`, `Registered=True`, `CardUnsigned=True`, the no-requeue) is pinned only by the e2e (X3). One envtest case would have shown MAJOR 4 and MAJOR 5 to the author.

## MINOR 7 — `TestTheCardIsByteStableAcrossFetches` reads one `Read` call

`test/responder/main_test.go:66-68`: a single `Body.Read` into 4096 bytes. Correct for this card; silently truncating for a longer one. Use `io.ReadAll`.

---

## Cross-design claims, checked

| Claim | Where | Checked against | Verdict |
|---|---|---|---|
| ADR-0030 step 2 = "revision-scoped Services, card fetch, the runtime env contract, retained-revision selection" | all three commits | ADR-0030 Decision (2) | **true**, verbatim |
| ADR-0031 decision 2: full digest, field not CR, unset = follow spec | A66, `agent_types.go` | ADR-0031 | **true** |
| ADR-0031 decision 2 also requires gate-evidence eligibility and a security recheck | `release.go:61-64`, §5, A66 | ADR-0031 | **false** — MAJOR 6; source is `reviews/00-astra-direction-review.md:190-191` |
| ADR-0019: container is the card's source of truth | `card.go:21-23`, responder | ADR-0019 (2) | **true** |
| ADR-0029: workloads and material in `plume-run-<ns>` | `release.go` reads there | ADR-0029 | **true** |
| Design 09: unsigned BYO card registers with loud `CardUnsigned` | `card.go:31-36`, `agent_controller.go:501-503` | design 09 §3 item 2 | **true**, verbatim ("BYO/external agents with unsigned cards register with a loud `CardUnsigned` condition") |
| Design 11 owns tool resolution, which does not exist | `card.go:37-41`, §5 | design 11 line 49, §11 | **true** |
| Design 03's route names the revision Service by `backendRef` | `card.go:104-108`, A64 | design 03 line 157 | **true** |
| §3.4 "does not specify where the sleep happens" for "3 retries, backoff" | A68 | §3.4 | **defensible** — §3.4 says "fetches it in-cluster (3 retries, backoff)"; in-line is the plain reading, but nothing forbids between-reconcile retries |
| §3.4 makes an unfetchable card a registration failure, not a serving one | A68, `card.go:48-51` | §5 failure table, §4, design 09 §5 | **false** — MAJOR 3 |
| A67: CRD refuses a tagged image; workload sets `imagePullPolicy: Always` | commit, A67 | `plume.dev_agents.yaml:777`; `agent_controller.go:1096` | **true** |
| A66: the 40-bit name is refused by the CRD pattern | A66 | `plume.dev_agents.yaml:538` `^[0-9a-f]{64}$`; `TestTheShortRevisionNameIsNotAcceptedAsAPin` (envtest, real API server) | **true**, measured |

## §5 honesty

- **Claimed as working and not**: "Rollback eligibility … selects a retained revision by full digest and is proven to survive source drift" — true for ConfigMap bytes, false for the pod template (BLOCKER 1). The failure-table row "Card fetch fails | candidate held" — the candidate is promoted (MAJOR 3). `CardDriftInterval`'s comment states a schedule (MAJOR 4).
- **Listed as unimplemented and in fact implemented**: "Registration, card fetch, cross-check, `status.cards[]`, directory writes — §3.4 in full is unimplemented"; the header's "registration and card handling (§3.4) have no code at all"; §3.1 and §3.4's "registration is unimplemented" (MAJOR 8). Fetch, parse, name check, version check, digest and `status.cards[]` exist and are e2e-proven.
- **Missing rows**: an unregistered candidate is promoted; card drift raises nothing and is not re-read on a schedule; `TaskStateUnverified` ignores the fetched card; a pinned revision's template is re-rendered from current spec; a rollback is refused if the user's source object is gone; `status.cards[]` is unbounded.
- **Honest rows, confirmed by measurement**: the shape-mismatch refusal and the missing-copy refusal do what §5 says (T5, T6) — they simply have no test (MAJOR 2). The signature, skill-cross-check and `registrationDeadline` rows are accurate.

## On the author's ledgers

I did not re-run the author's eleven mutations one by one. I read them against the code and tests they name, and the classifications are correct as written: the two INVALID entries in `6b1c935` (unparseable file) and the one in `b7744b9` (harness exited before the tests) are the right label, and every KILLED entry names a test that does assert the mutated line. X5 — a neighbour of the author's R2 — was killed exactly where expected. The ledgers are honest. They are also narrow in the way rule 1 warns about: each deletes a line a test asserts, and none asks what the tests do *not* assert. Two mutations of the unasserted half survived (X1, X2), and the property that mattered most (T7) needed no mutation at all — the unmutated code fails it.

## What is sound

The pin-refusal paths and their messages (T5, T6). The unit tests in `card_test.go` name the refusing rule per case, which is the discipline §8 asks for. The requeue-on-state fix is real, was found by running, and is pinned. `cardFetchDue` is pinned directly. The CRD pattern is tested against a real API server. The e2e is green on k3d on a clean tree and the responder does answer through its revision Service — the thing ADR-0030 said nothing here could do. The commit messages are the best in this repository's recent history at saying what is not done. The one thing they say is done that is not — "selected, never recomputed" — is the one that needed to be.

# Design 16 A29(b) implementation: recheck of the review fixes

**PASS — 0 BLOCKER, 0 MAJOR, 1 MINOR (measurement-note wording).** All four findings against `3588cbb` are closed. No change to production behavior was needed. This verdict is about the implementation and its regression tests, not approval to merge or evidence of CI, real traffic or deployment.

**Scope and independence.** Recheck by the original independent reviewer (Codex/GPT-family, gpt-6.2-astra, high), not a second cross-family review. Compared `3588cbb..a351d74` in a fresh detached worktree, then checked final snapshot `70ddd580aca40e3947236b253dd3794f845175d6`. Its only delta from `a351d74` is the regenerated test-source reference for `ActiveRevisionNotEvaluated`. Fresh regeneration at `70ddd58` produced all four committed reference files byte for byte. Production code, all tests, the approved §1.1 substitutions, and reason definitions are unchanged between those two heads. The original review is preserved verbatim (Git blob `7ed4640fb11c66d5074756f3e06c53785efe32a0`).

| Original finding | Recheck |
|---|---|
| 1. Discovery boundary and same-generation refresh | **Closed.** `test/envtest/pin_test.go:978–1013` retains a bypass disappearance/reappearance cycle; in a second cycle, a controlled status-only write changes the pinned revision's recorded verdict, not the spec generation. One excluded pass must retain the original false reading and message. The first rediscovered pass, not steady state after several passes, must produce the exact true reading with revision/digests/score and clear the bypass. This does not assert that the eval controller initiates a run while discovery is absent. |
| 2. Early-exit qualifier | **Closed.** `test/envtest/a29_test.go:21–33` checks the entire approved suffix, not just two phrases from its first half. |
| 3. Listed companion comment | **Closed.** `internal/controller/passunrecorded_test.go:203–205` names the serving revision, including pin selection, and locates the pinned coverage in envtest. |
| 4. Packaging provenance | **Closed by disclosure, not by rewriting history.** `docs/designs/16-evalsuite.md:1703` identifies the mixed-commit packaging as the implementer's deviation and distinguishes it from the user's behavioral decision. |

**Persisted status.** `test/envtest/a29_test.go:49–61` gives the admission explanation time to settle, then drives three independent reconciles and reads the live Agent's resourceVersion after *each*. The test uses a direct controller-runtime client, not a manager-cache snapshot. This measures a fixed point in the stored Agent for those fixture states; it does not count update attempts, prove an event-free cluster or bound convergence on other states. The research ledger correctly distinguishes a redundant no-op write from a persisted change.

**Independent measurements.** Envtest 1.36.2, `KUBECONFIG=/dev/null`, `USE_EXISTING_CLUSTER` unset. Pristine uncached R1–R6, recorded-pass pin, CRD-return, revert/repin and unpinned comparison battery: **PASS** (25.196 s). Four separate mutations applied to the identical tested source of `a351d74` and `70ddd58`, restored from backup after each:

| Mutation | Observed failure |
|---|---|
| Reassess on pinned CRD-absent passes too | **KILLED** at `pin_test.go:1001`: true on excluded pass, expected false. |
| Skip active-pin reassertion if the existing condition observed the same generation | **KILLED** at `pin_test.go:1007`: still false on first rediscovered pass, expected true; both mutants ran at generation 4. |
| Remove only the clause qualifying the early exit | **KILLED** by exact-suffix assertion in both pin/gate orderings. |
| Prepend a fresh timestamp to the pinned condition without disturbing its approved suffix | **KILLED** on the first steady pass by the persisted resourceVersion assertion in both orderings. |

All four compiled and reached their intended assertions; none was INVALID. No full-suite/race/e2e result is inferred from these focused runs.

**MINOR — `docs/research/a29-pinned-gatespassed-2026-11.md:48–49`.** R6 is described as an “ungated serving revision” retaining unpinned wording before the pin. In the fixture it is *initially* ungated, **then gains a gate**; the unpinned condition exists only once that gate is declared. A reader following the present sentence would expect a never-gated Agent to carry a condition that the test itself checks can be absent. Suggested replacement: “In R6, adding a gate to the serving revision produces the unpinned wording; pinning that same revision replaces it with the pinned tail.” This is not a code, regression-test or approval-record deficiency.

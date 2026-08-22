# Prior-art scan: "eval-as-admission" — NOT NOVEL

**Date**: 2026-08-22 · **Method**: adversarial scan, briefed to refute rather than support.

## Verdict

**NOT NOVEL.** Every structural element of the claim is prior art, and the strongest single item — [Flagger's pre-rollout webhooks](https://docs.flagger.app/usage/webhooks) against its `-canary` service — implements the claimed sequence verbatim.

## What Flagger already does

Flagger admits a new revision with the canary **at weight 0** (primary takes all traffic), exposes `<service>-canary.<ns>.svc.cluster.local` which the docs describe as "available only during the canary analysis and can be used for conformance testing or load testing" — a candidate-only route carrying no production traffic — runs `pre-rollout` hooks against it "before routing traffic to canary", and only then steps weight up to promotion. The outcome lands in `status.conditions[type=Promoted]` with `reason` and `message`, plus `failedChecks` and `lastPromotedSpec`.

That is: held at zero → tested on a zero-traffic candidate route → pass-gated traffic shift → 100%, reason on the CR. [Argo Rollouts](https://argo-rollouts.readthedocs.io/en/stable/features/bluegreen/) `prePromotionAnalysis` + `previewService` is a near-tie, and its `setHeaderRoute` routes specific callers to the candidate.

The *content* of the gate is equally well covered. [TFX Evaluator/Pusher](https://www.tensorflow.org/tfx/guide/evaluator) "blessing" gates on model **quality vs a baseline** and refuses to push otherwise; [ease.ml/ci](https://arxiv.org/abs/1903.00278) adds statistical reliability constraints; [Braintrust's eval-action](https://www.braintrust.dev/articles/langsmith-vs-braintrust) blocks a merge when LLM eval scores drop below thresholds. [Keptn quality gates](https://v1.keptn.sh/docs/concepts/quality_gates/) owns the phrase "quality gate" for multi-stage promotion, though scored on SLOs.

Nobody appears to have published the *composition* — but the composition is a webhook URL away. Flagger's pre-rollout hook accepting an eval-suite runner is a documented, intended use of the extension point, not a new mechanism. **"We do it in a CRD" is not a contribution when the prior art is already CRDs and reconcile loops.**

## The residue

Three things survive, all small:

1. **Principal-scoped admission on the candidate route.** Flagger's `-canary` service is reachable by any workload in the cluster; `previewService` likewise; `setHeaderRoute` matches a *header*, not an authenticated identity. "Admits exactly one principal" was not found in the prior art — but it is an authz policy on a route, a known primitive applied to a known place.
2. **Single-failure terminality with a typed reason** — Flagger uses a failure *threshold* with retry; this is a policy choice inside existing status machinery.
3. **Bypass as a recorded status condition.** Flagger's `skipAnalysis` is a spec field, not a status condition. A compliance-ergonomics improvement.

## The one claim with real substance

> A progressive-delivery controller in which the pre-traffic gate is a **non-deterministic, sampling-based** quality eval whose verdict is statistically thresholded — making the gate's own flakiness a first-class controller concern (retry/quorum semantics, terminal-vs-retryable classification) rather than a webhook returning 200/500.

This is the one place the eval payload genuinely changes the controller. SLO and conformance gates are assumed deterministic, so Flagger's "threshold N failures then roll back" is a **noise filter for infrastructure flakes**. An LLM eval is stochastic by construction, so N-of-M retry semantics silently become a **bias amplifier** — retry until pass. No prior art found treats gate non-determinism as a controller design problem.

### What would have to be measured

Fixed set of agent revisions with ground truth (K known-good, K seeded-regressed: prompt regressions, a model downgrade). Run each through (a) baseline Flagger + pre-rollout webhook wrapping the same eval suite at `threshold: 5`, and (b) explicit statistical admission (fixed sample size, confidence bound, no retry-on-fail). Measure **false-admit rate on regressed revisions** and **false-terminal rate on good ones**, ≥30 repeated rollouts each to expose eval variance. Report per-revision eval-score variance.

The claim survives only if (a) shows materially higher false-admit — i.e. threshold-retry really does launder a stochastic gate — at comparable cost. **If eval variance is near zero, the contribution evaporates and we are back to Flagger.**

## Consequence for plume

None architecturally — the design is *validated* by this, not weakened: it is doing the thing the mature tools do, which is the "bind, don't build" doctrine working as intended. It should stop being described as a differentiator in any external writing. ADR-0006 and architecture §06 need their novelty language softened to "eval-gated progressive delivery, in the Flagger/Argo lineage, with the gate content being an agent eval suite."

## Coverage caveat

The scan's search budget ran out before directly verifying Vertex AI eval gates, SageMaker model-quality monitors, Seldon shadow deployments, W&B Weave, Galileo, Vellum, and LangGraph Platform. The pattern (all CI-step or dashboard-shaped) suggests they strengthen this verdict, but that is inference, not evidence.

# Design 08: The assayd CLI

- **Status**: **approved** — critique PASS at r2 (reviews/08-review.md) · ADR-0022
- **Phase**: P1+ (grows with each phase) · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0001 (binary name finalized at rename), 0002, 0019 · interfaces: every design (it is the human surface); 05 (directory reads), 06 (login), 07 (install checks)

## 1. Purpose & scope

The platform's primary interface until the UI exists (architecture §11): guided creation, the local dev loop, build/deploy, and operational verbs. In scope: command architecture, the wizard framework, dev-loop mechanics, output/UX rules, distribution. Out of scope: per-verb business logic owned by other designs (kg/eval/session verbs are thin clients of those designs' contracts).

## 2. Doctrine & charter gates

- **Plane**: slow (engine surface) — but every *behavioral* asset it scaffolds (templates, skills) is fast-plane data from packs (design 18/9). The CLI contains **no template content** — it renders what packs provide. That's the Claude-Code lesson applied to ourselves: new technique ⇒ new pack, CLI unchanged.
- **Pods**: 0. Single static Go binary (cobra), no daemon. **Stateful deps**: none (kubeconfig + OS keychain + XDG config only). **Primitives** (r1 f5): Resource (CRs it writes), Artifact (images/charts/template artifacts it builds/fetches), Agent (A2A client in `invoke`). ✓

## 3. Command architecture

```
assayd
  init [agent|workflow|app|kg]     # the wizard (§4)
  dev                              # local loop (§5)
  invoke <agent> [--task|-]        # A2A client for testing
  build                            # buildpacks/ko + cosign + SBOM (§6)
  deploy [--env]                   # GitOps commit or direct apply (§6)
  login / logout                   # OIDC device-code (design 06 §3.4)
  agent    list|status|logs|register
  kg       init|push|probe|diff|versions
  eval     run|report              # (P3)
  session  list|replay|export      # (P3)
  drift    status                  # (P4)
  dir      list|export|import|import-tools
  pack     install|list|uninstall  # (P3)
  workflow test --event
  expose <resource>
  doctor                           # install/contract health (§7)
  upgrade crds                     # design 07 §3
```

Rules: every verb is **sugar over CRs and published contracts** — anything the CLI does is achievable with `kubectl` + `git`; `--output json` on every read verb (scriptable); no verb talks to a assayd-proprietary API (there isn't one).

## 4. The wizard framework ("the platform asks, you choose")

A reusable interview engine, not per-command prompt spaghetti:

- **Steps are data**: each `init` flow is a step list `{question, options(), recommend(), why}` — options are *computed* (graphs from the directory, tools from `tools.*`, SDKs from installed template packs) and every recommendation renders its reasoning inline. A blank or unexplained choice is a bug by definition (architecture §11).
- **Every wizard run emits its answers as a rerunnable flag set** (printed at the end: `assayd init agent --sdk=… --graph=… --tools=…`) — interactive and scripted paths are the same code, and CI can replay any scaffold.
- Non-TTY ⇒ flags required; missing flag ⇒ explicit error naming the wizard question it corresponds to (never a silent default).
- Provenance display is mandatory where the data is third-party (imported tools — design 05 f2).

## 5. `assayd dev` — the local loop

1. **Cluster**: use current kubeconfig context if it has assayd core (checked via `doctor` probes); else offer to create `assayd-local` (k3d preferred, kind fallback, minikube honored if present) and `helm install --profile local` (design 07). Never silently switch contexts — print and confirm the target once per invocation.
2. **Run mode — cluster-build hot reload**: on save, rebuild via buildpacks into the local registry (k3d's built-in registry; kind: `ctr` image import) and roll the dev revision. Dev revisions **bypass eval gates by explicit profile flag** (`local` profile only; labeled condition `GatesBypassed=DevProfile` — recorded as design 02 §11 A5, r1 f2; impossible in prod by admission).
3. **Feedback**: streams the agent's receipts live to the terminal — via a **read-only NATS credential** scoped to `receipts.>` in the user's tenant account, minted by the identity bootstrap per tenant and fetched at `assayd login` into the keychain (r1 f6); the CLI never holds stream *write* credentials. `--verbose` adds gateway route/policy events.
4. `assayd invoke` sends a real A2A task through the gateway (never direct to the pod) so dev traffic exercises the same path as prod.

*Rejected alternative*: local-process mode with a tunnel into the mesh (Telepresence-style) — powerful but a large, distro-fragile machinery; deferred until demanded (recorded, not forgotten).

## 6. build / deploy

- `build`: Cloud Native Buildpacks default (SDK templates carry `project.toml`), `ko` for Go agents; cosign sign + SBOM attach (design 02 admission requires it) **+ Agent Card signing per design 09 §3.2** (Sigstore keyless, same builder identity — r1 f3). Registry from config; local dev pushes to the cluster registry.
- `deploy`: **GitOps-first** — writes CR changes to the env repo path and commits (push + PR optional flags); `--direct` applies to the cluster for dev only (refused when the target namespace is labeled `assayd.dev/gitops: enforced`). Then **streams the rollout**: watches Agent status and renders `HELD → eval 0.89 ✓ → canary 10% → 100%` from conditions (design 02/16) — the flagship UX moment; `--no-wait` for CI.

## 7. `assayd doctor`

Contract-aware health: **reads the `assayd-contracts` ConfigMap ledger** (design 07 §4 — never a hard-coded subset, r1 f4) and N/N−1-checks it against the binary; core pod health; gateway route sanity; SPIRE SVID presence; IdP discovery reachability; **receipt-pipeline liveness read-only** (JetStream stream-info last-sequence age + tap health metrics — the CLI never writes to the audit stream, r1 f1); optional `assayd invoke --probe` drives a *genuine* no-op task through the gateway when an end-to-end proof is wanted (its receipt is a real receipt of a real hop). Output: table with fix-it hints; `--output json` for CI.

## 8. UX & distribution rules

- `doctor` also reports the **tier gap**: which route classes the policy compiler is not emitting because their producing design is not installed, and whether `gateway.enabled` is false (design 03 §3.1, §5). Design 03 relies on this verb as the sole report for a whole class of un-emitted governance, so it is named here rather than assumed. Errors name the failing contract/condition and the next command (`PolicyApplyIncomplete on pa-reviewer — run: assayd agent status pa-reviewer`).
- No emoji in machine paths; human output stable-ordered; secrets never printed; `NO_COLOR` honored.
- Distribution: single binary via GitHub releases + Homebrew tap; `assayd upgrade-check` compares against the chart's platform version (skew warning, not auto-update). **No telemetry in core, at all** — trust is the funnel (ADR-0015); an explicit opt-in flag may come later, never default-on.
- Binary name is `assayd` until rename (ADR-0001); a `ASSAYD_BINARY_NAME` build var makes rename a rebuild, not a refactor.

## 9. Failure modes

| Failure | Behavior |
|---|---|
| No cluster / core absent | Every verb degrades to a named doctor hint, not a stack trace |
| Contract skew (binary vs cluster) | Read verbs warn; mutating verbs refuse beyond N/N−1 (same rule as design 07) |
| Keychain unavailable (CI) | Token via env var path, documented as the CI mode |
| GitOps repo unreachable | `deploy` fails before touching anything; nothing half-committed |
| Wizard data sources down (directory) | Wizard degrades to manual entry with a warning — never blocks on optional enrichment |
| `deploy` streaming a rollout that can never reach `Ready` | `GatewayIncompatible=CRDsAbsent` / `PolicyCompileFailed` / `PolicyApplyIncomplete` are **terminal for the stream** (`GovernanceSkipped` is not — it is the normal state of a declared-ungoverned tier, and aborting on it would break `deploy` on every P1 cluster): it stops and prints the condition, the resource it names and the next command, rather than waiting on a `Ready` design 02 A15 is deliberately withholding. Without this the verb hangs indefinitely on any cluster with no agentgateway |

## 10. Testing

Wizard engine: unit tests on step logic + golden transcripts (the printed flag-set replay is itself the fixture). e2e (design 07 CI): `init → dev → invoke → build → deploy` scripted via the flag-set path on k3d; doctor assertions against a broken install (deleted policy → named hint).

## 11. Decisions for async review

- **D1 — CLI carries no template content**; scaffolding renders pack-provided templates (fast-plane discipline applied to our own tool).
- **D2 — Wizard runs emit rerunnable flag sets** (one code path for interactive and CI).
- **D3 — GitOps-first deploy with guarded `--direct`**; rollout streaming is the default UX.
- **D4 — No telemetry in core**, ever-default-off.
- **D5 — Local-process/tunnel dev mode deferred** with rationale recorded.

## 12. Resulting ADRs

Folded into ADR-0022 (P1 infrastructure) after critique PASS.

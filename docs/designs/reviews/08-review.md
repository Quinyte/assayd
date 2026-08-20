# Review: Design 08 — The plume CLI

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/08-cli.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses; consistency vs architecture §11, designs 02–07 (+ amendments), ADRs 0001/0015/0019. Claims are internal (cobra/buildpacks/ko/keychain are settled ground); no external verification required.

## Findings

### 1. MAJOR — `doctor`'s probe receipt contaminates the audit stream and requires handing CLI users write credentials to it

`08-cli.md:66`. Receipt-stream liveness is checked by "publish a probe receipt, read it back". Two problems: (a) the `RECEIPTS` stream is the platform's *audit log* — designs 04/06 build a chain of custody where receipts are written by the tap from gateway-attested spans; a synthetic CLI-authored "receipt" is a forged audit record by construction, and every consumer (eval sampler, replay, spend aggregation, compliance hash-chaining) must now know to exclude it; (b) it means every `doctor`-running human needs *write* credentials to the tenant's receipt stream — a standing capability that exists only to support a health check. This inverts the design's own posture (04 §6: append-only, tap-only writer by SVID).

**Fix**: check liveness without writing to the audit stream — JetStream stream-info (last sequence + last-message age) plus the tap's own health metrics answers "is the pipeline moving" read-only; if an end-to-end write probe is truly wanted, drive a real no-op task through the gateway (`plume invoke --probe` against a health endpoint) so the resulting receipt is a *genuine* receipt of a genuine hop.

### 2. MINOR — `GatesBypassed=DevProfile` is another unrecorded design-02 condition

`08-cli.md:53`. The dev-loop bypass condition is well-designed (labeled, loud, admission-impossible in prod — the right shape), but it's a new Agent condition and a new rollout behavior on approved design 02, and the §11 amendment mechanism exists precisely for this (A1–A4 so far). **Fix**: one A5 line.

### 3. MINOR — `plume build` omits the card-signing step design 09 assigns to it

`08-cli.md:61` vs design 09 §3.2/D2. Design 09 has card signing "ride `plume build`"; 08's build section lists buildpacks/ko + cosign + SBOM only. The recurring cross-doc pattern (a design claiming another design does something the other doesn't say). **Fix**: add the card-signature step to §6, referencing 09 for the key/policy details (which have their own findings in 09's review).

### 4. MINOR — `doctor` hard-codes a contract subset instead of reading the ledger

`08-cli.md:66` vs design 07 §4. Doctor checks "kgp, idp, receipt, pack" — the `plume-contracts` ConfigMap (07 r2) also carries `ontology/v1` and the semconv SHA, and it exists so that exactly one artifact defines the shipped contract set. **Fix**: doctor reads the ledger ConfigMap and checks *everything in it* against the binary's supported set — future contracts join the check for free, and the two N/N−1 enforcement points (upgrade pre-hook, doctor) can never diverge.

### 5. MINOR — the charter line is incomplete

`08-cli.md:13-14`. Plane and pods are argued well (the no-template-content rule is the best doctrine sentence in the design), but socket and primitives go unstated (TEMPLATE §2). The honest answers are one line: socket n/a (engine surface); primitives — consumes Resource/Artifact, introduces none. **Fix**: write it; `audit-docs` will flag it otherwise.

### 6. MINOR — the dev loop's receipt-streaming credentials have no issuance story

`08-cli.md:54`. "Streams the agent's receipts live … (design 04 fidelity consumer, tenant-scoped creds)" — but nothing says how a logged-in human obtains tenant-scoped *NATS* credentials. Design 06 provisions OIDC clients; NATS is a different credential domain (accounts + user creds/JWTs). This is the first design to put a human directly on the stream, so it owns naming the bridge. **Fix**: state the mechanism — e.g. a NATS auth-callout that validates the user's IdP token and maps it to the tenant account (0 pods, NATS-native), or short-lived NATS user creds minted by the operator on `plume login` — with the read-only scoping stated either way.

## Lens summary

1. **Doctrine**: exemplary — 0 pods, no daemon, and D1 (CLI carries no template content) applies the fast-plane discipline to the platform's own tooling; D4 (no telemetry, ever-default-on) honors ADR-0015's trust posture.
2. **Charter**: finding 5 only.
3. **Contract consistency**: findings 2, 3, 4 — all small, all the established cross-doc patterns; verbs otherwise map cleanly onto designs 02–07 (login → 06 §3.4, upgrade crds → 07, dir verbs → 05 including `import-tools`, rollout streaming → 02's phase machine).
4. **Hidden dependencies**: the wizard degrades to manual entry when the directory is down — the right dependency posture; flag-set replay makes interactive and CI one path (D2 is quietly the best decision here).
5. **Failure modes**: strong table; "no cluster ⇒ named doctor hint, not a stack trace" is the correct floor.
6. **Security**: findings 1 and 6; the `--direct` GitOps guard (refused on labeled namespaces) and never-print-secrets rules are right.
7. **Research freshness**: nothing load-bearing and external; n/a.
8. **Testability**: golden wizard transcripts where the emitted flag set is itself the fixture — genuinely elegant; e2e via the flag-set path keeps CI honest.

## Disposition

**REVISE.** One real problem (the probe receipt — an audit-integrity mistake in an otherwise security-conscious design) and five one-line-to-one-paragraph completions. The wizard framework, flag-set replay, GitOps-first deploy with streamed rollouts, and the no-template-content rule are all exactly what architecture §11 promised. Fix and this passes quickly.

VERDICT: REVISE — 6 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS**
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved — both halves.** `doctor` checks receipt-pipeline liveness read-only (JetStream stream-info last-sequence age + tap health metrics; "the CLI never writes to the audit stream" now stated as a rule), and the end-to-end proof became `plume invoke --probe` — a *genuine* task through the gateway whose receipt is a real receipt of a real hop. Exactly the recommended shape; audit integrity and credential posture both restored. |
| 2 | MINOR | **Resolved.** `GatesBypassed=DevProfile` recorded as design 02 §11 A5. |
| 3 | MINOR | **Resolved.** `build` now includes card signing per design 09 §3.2 (Sigstore keyless, same builder identity). |
| 4 | MINOR | **Resolved.** `doctor` reads the `plume-contracts` ConfigMap ledger — "never a hard-coded subset" — so the two N/N−1 enforcement points can't diverge. |
| 5 | MINOR | **Resolved.** Primitives line added (Resource/Artifact/Agent). |
| 6 | MINOR | **Resolved.** Read-only NATS credential scoped to `receipts.>` in the tenant account, minted by the identity bootstrap, fetched at `plume login` into the keychain; write credentials never held. Composes with design 04's per-tenant streams. |

### Observation (not a finding)

The receipt-read credential is per-*tenant*, so "which human read the stream" is coarse in the audit trail. Fine for core (single team); per-user credential minting is a natural design-26 (Tenant CR) item — worth one line there when it's written.

### Verdict

**PASS.** All six findings addressed; the MAJOR was fixed structurally on both fronts (read-only checks, genuine probe). Fold into ADR-0022.

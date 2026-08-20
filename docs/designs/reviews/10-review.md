# Review: Design 10 — Observability pack

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/10-observability-pack.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses; consistency vs architecture §07, designs 02–09 (+ amendments), ADRs 0002/0003 and pending 0020/0021; OpenObserve claims verified by current-year search (sources inline).

## Findings

### 1. MAJOR — the interior-telemetry path is unimplementable as drawn: no compiled route exists, and the tap's receipt-vs-forward rule is unstated

`10-observability-pack.md:19-27`. The topology sends agents' interior OTel spans `─OTLP──► gateway ──► tap`. Two missing mechanisms:

- **The route**: agent pods run under default-deny egress (design 02 §6 — the gateway is the *only* reachable endpoint), so interior spans can only leave via a gateway route to the tap — gateway config, which by design 03's own thesis only the compiler emits. 03 §3.4 has no interior-telemetry/OTLP row (the same missing-row class as the card route and exchange policy before it). Without it, OpenLLMetry-instrumented agents' spans go nowhere and the "traces joined by trace_id" story silently degrades to gateway-hop-spans-only.
- **The discrimination rule**: design 04 D1 says interior spans "enrich OpenObserve but do not become receipts (receipts = enforced hops only)" — but once agent-authored spans arrive at the same tap on the same pipe, the tap needs a stated, *unforgeable* criterion for which spans become receipts. Span attributes won't do: an agent can emit spans that imitate gateway hop spans, and the tap would mint forged audit records from them. The criterion must be transport-derived (e.g. the gateway tags its own export connection / listener, or agent-forwarded OTLP arrives on a distinct tap listener), not content-derived.

**Fix**: add the interior-telemetry route to 03 §3.4 (an OTLP Backend + route, per-agent or shared, with the rate-limit note — telemetry is also traffic); specify the tap's receipt criterion as transport-derived and add a conformance test (an agent emitting gateway-lookalike spans must not produce receipts). Record the tap-side clause as a design 04 delta.

### 2. MINOR — OpenObserve has no access-control story (default-credential risk)

`10-observability-pack.md:14,45-47`. The design ships dashboards and a web UI but never says who can log in or how the first credential is created — and an observability UI over traces that can include headers/full capture bodies is a sensitive surface, not a convenience. **Fix**: bootstrap-generated admin secret (chart hook, landing in a k8s Secret) as the floor; OIDC against the platform IdP (design 06's discovery output — OpenObserve supports OIDC) as the stated `prod` recommendation; one line on network exposure (ClusterIP + port-forward/ingress-by-choice, never a default public route).

### 3. MINOR — "operators/CLI logs → OpenObserve" needs scoping against 08's no-telemetry rule

`10-observability-pack.md:23`. Operator logs to OpenObserve: fine. CLI logs: the CLI runs on a human's laptop, and design 08 D4 says "no telemetry in core, at all". In-cluster OpenObserve isn't vendor telemetry, but an unqualified "CLI logs ship to OpenObserve" line invites exactly the doubt D4 exists to prevent — and has no auth path anyway (see finding 2). **Fix**: scope the line to in-cluster components (operator, tap, bootstrap jobs); if `plume dev` ever ships session logs, that's an explicit opt-in flag documented in 08, not a topology default.

### 4. MINOR — charter line incomplete

`10-observability-pack.md:13-14`. The plane split (slow topology / fast-plane dashboards-signals-alerts data) is well-argued — arguably the cleanest plane statement in the series — but socket and primitives go unstated (TEMPLATE §2). **Fix**: one line — socket: gateway-adjacent sink, no new socket; primitives: Artifact (dashboards/signals as versioned pack data), introduces none.

### 5. MINOR — OpenObserve claims verified but unlanded; the design carries no research line at all

`10-observability-pack.md` (header). Verified this session: [OpenObserve ingests traces, metrics, and logs over a single OTLP endpoint (HTTP/gRPC), single binary, with dashboards and alerting built in](https://openobserve.ai/blog/opentelemetry-backends-otlp-support/), [object storage as primary store](https://github.com/openobserve/openobserve) — the design's load-bearing assumptions all hold. But rule 4 requires the landing, and this is the first design in the series with no Research header line at all. **Fix**: land `docs/research/openobserve-2026-08.md` (OTLP ingestion, dashboard JSON import + alert API — the §8 CI tests depend on those APIs existing —, OIDC support for finding 2) and cite it.

## Lens summary

1. **Doctrine**: clean — 1 budgeted pod, sink-not-system-of-record stated twice and enforced by D1 (compliance never extends OpenObserve retention — audit lives in receipts); the 07 stateful-allowlist entry composes.
2. **Charter**: the plane split is exemplary; finding 4 is the missing formality.
3. **Contract consistency**: strong — the signal catalog cross-references verify (usd/null-pricing per 04 §3.2, `late_redelivery`/gap/lag per 04 §7, candidate-held-1h per 02 §7, backstop staleness 60s per 04 §3.4, termination reasons per 09 §3.3); alerts map 1:1 to conditions. Finding 1 is the one real hole; the references to not-yet-recorded ADR-0020/0021 are acceptable given 03/04's PASS status.
4. **Hidden dependencies**: the semconv name-map layer is the right absorber for the pinned-SHA reality (ADR-0021's pin mechanism, correctly reused); finding 1's route is the hidden dependency.
5. **Failure modes**: the OpenObserve-down row states the design's best property out loud — `kubectl get agents` still tells the truth because conditions, not dashboards, are the source of truth. Cardinality rule (D3) preempts the classic metrics blowup.
6. **Security**: findings 1 (span forgery), 2, 3.
7. **Research freshness**: claims true, landing absent (finding 5).
8. **Testability**: alert rules fixture-tested in CI ("alerts are tested code, not YAML hope") is the standout — D4 should be the norm for every shipped alert in the platform; e2e assertion is concrete.

## Disposition

**REVISE.** The fast-plane treatment of dashboards/signals/alerts, the name-map layer, and tested alerts make this a strong draft; the signal catalog's cross-design consistency is the best in the series. But finding 1 is real: the topology's left edge (interior spans) has no compiled path and, worse, no unforgeable receipt boundary once agent-authored spans share the tap's pipe — an audit-integrity issue, not a wiring detail. Fix with the 03 row + transport-derived criterion + 04 delta, land the research note, and this passes.

VERDICT: REVISE — 5 findings

# AGENTS.md — assayd

**This file is the model-neutral source of truth for how to work on assayd.** Codex, Cursor, Copilot, Gemini and anything else that reads `AGENTS.md` starts here. `CLAUDE.md` carries the Claude-specific skill mechanics and defers to this file for everything else — so a rule lives in exactly one place and no model gets a different version of it.

assayd ("graphene" in early drafts) is a lightweight, Kubernetes-native agent platform.

**Phase: implementing P1, under the scope reset of ADR-0030.** The agent-operator, its CRD, the chart and CI exist and pass on k3d and kind.

**Do not read "approved" as "settled."** An earlier version of this line said all 27 designs were approved and critique-passed. That was false, and it was the first thing every contributor read. Design 02's own header says it "is not approved and must not be cited as such"; design 03's says "RE-OPENED, do not implement." Check the Status line of the design you are about to touch, and trust that over any summary — including this one. `docs/designs/README.md` carries the table.

**What is actually true of the system today**: an agent answers a request. `test/responder` is a real A2A responder, built and pushed by `hack/e2e.sh`, deployed by the operator from a registry digest, and asked a question through its revision Service — the thing no test here could do until 2026-09-06, when the e2e still ran `registry.k8s.io/pause` for everything. **Traffic now traverses a real gateway.** `hack/e2e.sh` installs Gateway API v1.6.0 and agentgateway 1.5.0 on k3d, and `TestAnAgentAnswersThroughTheGateway` gets an agent's answer through the Gateway's own Service — killed by a mutation that changes only the `Host` header, which returns `route not found`, so the route is genuinely carrying it. Getting there closed two gaps that were invisible from either side alone: the operator passed A5.9's admission policy and was then refused by RBAC that granted no `httproutes`, and the policy itself compared a route's `parentRef` against the *operator's* namespace rather than the *Gateway's*, so moving the Gateway made the reservation match nothing and silently admit any author (design 07 A6).

**What remains untrue**: the chart ships no Gateway — `gateway.enabled` defaults to `false` and is read by nothing, so the e2e's Gateway is created by the harness — and **the policy compiler does not exist**: the test authors its route while impersonating the operator's ServiceAccount, which proves the path and the identity reservation and proves nothing about a compiler. No `AgentgatewayPolicy` or `AgentgatewayBackend` is emitted by anything, so no budget, rate limit or tool allowlist is enforced anywhere. The operator fetches, validates and digests the card the container serves (A68), but does **not** verify its signature, cross-check its skills against the CR's grants, or count the registration deadline — §5 carries those three individually. So the platform's central claim — that governance becomes real **at the gateway** — now has its *path* proven and its *governance* still unbuilt. The next work is the rest of the narrow slice in ADR-0030, not the next design.

## If you are here to review

You are most likely a second opinion, deliberately from a different model. That is the point: a fresh reviewer of the *same* model found three blockers in code that had passed five rounds with a previous one, including an authorization bypass. A different model should be more orthogonal still.

Read `docs/architecture.md` (canonical), the design under review in `docs/designs/`, its ADRs, and prior reviews in `docs/designs/reviews/`. Then apply the rules below. Write findings as `BLOCKER` / `MAJOR` / `MINOR` with `file:line`, a concrete failure scenario, and a specific fix — then a verdict of `PASS` or `REVISE`.

**Disagreement with another model's review is signal, not noise.** Say so explicitly and argue it; do not defer.

Several agents run at once and can reach each other through `herdr`. Read **`docs/agent-protocol.md`** before messaging one: the channels are deliberately narrow, because two agents that talk freely converge, and a consensus reached between models is worth less than the disagreement it replaced. The short version — findings are files, clarification is a message, reviewers never reconcile with each other, and nobody asks a peer what they could determine by running something.

## The rules this project learned the hard way

These are not style preferences. Each one is here because it was violated and something broke.

1. **Mutation-check every assertion.** Delete the rule a test claims to enforce; confirm the test fails. **Nine tests in this repo passed with their subject deleted** — that is the default outcome, not bad luck. A test asserting a *value* is usually weaker than one asserting a *transition*.
2. **Restore mutations from a backup copy** (`cp file file.bak`), **never `git checkout`** — it discards uncommitted work in the same file. This has cost real work.
3. **A mutation that does not compile proves nothing.** A build failure emits no `FAIL` line, so a naive runner reports "survived" for code that never built. Distinguish INVALID from SURVIVED.
4. **Self-review has never once caught what the independent pass caught.** Five rounds with one critic still left three blockers that a *fresh* critic found immediately — including an authorization bypass. Independence beats effort. Spawn a new critic when the current one nears its context limit.
5. **Defensive code no test can pin is a liability.** It reads as load-bearing and invites the next reader to trust it. Either pin it or delete it.
6. **Derivative comparison has a shape-specific hole.** apimachinery's `DeepDerivative` skips zero values for Slice, String, Map and Ptr kinds only. Drift hides in the **unset** fields of those kinds — injected `initContainers`, `Command`, capability lists. Own the fields, then compare exactly.
7. **Never state a bound the code does not enforce.** A flag's help text, a values file comment, a design table — each has shipped a promise nothing implemented. If it is not enforced, say so.
8. **Degraded paths are never silent** (NFR-8), and never *loud and wrong*: a condition must name the real cause, not a plausible one that was never checked.

## Doctrine and charter — these gate every change

- **Lightweight doctrine** (architecture §01): bind don't build · Postgres+NATS only · library over server · the reconcile loop is the product · ≤8 core pods (CI-enforced) · tiered install.
- **Pattern charter** (architecture §15): name the plane (slow=engine / fast=technique) and the socket. Five primitives only — Agent(A2A), Tool(MCP), Event(CloudEvents), Resource(CRD), Artifact(OCI). A sixth primitive or an engine change needs an RFC.
- **`docs/architecture.md` is canonical**; `docs/decisions/` holds the ADRs. Do not relitigate an ADR casually — propose a superseding one.
- **Code that diverges from an approved design is drift**, whatever it does. Amend the design first, as a numbered amendment, in the same change.

## Commands

```bash
make test          # fmt, vet, unit, envtest, chart — the pre-commit gate
make race          # anything touching shared state
make verify        # generated files are reproducible AND committed
make e2e           # k3d: create, build, load, helm install, test
DISTRO=kind make e2e
```

**Test layers, and what each can honestly claim.** unit (`api/`, `internal/`) asserts against the *generated* artifact, never a fixture in the test file. envtest (`test/envtest/`) runs a real API server but **no kubelet**, so every availability assertion there is against a status the test itself wrote. e2e (`test/e2e/`) is the only layer that can claim a pod actually runs.

**On Colima, raise `fs.inotify.max_user_instances` before debugging a broken local cluster** — see `docs/supply-chain.md`. The default starves k3s and presents as something else entirely.

## Conventions

- Commits explain *why*, and state what is not done. Conventional prefixes for docs (`docs(designs):`).
- "assayd" is a codename (ADR-0001). Flag it before it reaches anything expensive to change — API groups, module paths.
- **The user decides, with reasoning shown.** Present options with trade-offs and a recommendation; never a silent choice, never a blank menu.

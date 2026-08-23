# CLAUDE.md — plume

plume ("graphene" in early drafts) is a lightweight, Kubernetes-native agent platform.

**Phase: implementing P1.** All 27 designs are approved and critique-passed. The agent-operator, its CRD, the chart and CI exist and pass on k3d and kind. Next by the build order is design 03, the policy compiler.

## Skills — use them, do not paraphrase them

Eight skills live in `.claude/skills/`. They load automatically **only when the session's working directory is this repo** — start with `cd ~/Developer/plume`. If `/implement-feature` returns "Unknown skill", the session was started elsewhere: say so rather than quietly following the skill from memory, because a loop enforced by discipline is the failure mode the skills exist to prevent.

| Skill | Use when |
|---|---|
| `implement-feature` | writing or changing any code under `api/ internal/ cmd/ test/ config/ charts/` |
| `review-code` | before merging a diff — forks its own context |
| `critique-design` | before approving a design or amending an approved one — forks its own context |
| `write-spec` | writing a design, an ADR, a CRD type, or any user-facing string |
| `research-latest` | any landscape, library or standard question — never answer from memory |
| `design-component` · `adr` · `audit-docs` | designing, recording a decision, checking doc consistency |

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
- "plume" is a codename (ADR-0001). Flag it before it reaches anything expensive to change — API groups, module paths.
- **The user decides, with reasoning shown.** Present options with trade-offs and a recommendation; never a silent choice, never a blank menu.

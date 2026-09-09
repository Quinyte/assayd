# CLAUDE.md — assayd

assayd ("graphene" in early drafts) is a lightweight, Kubernetes-native agent platform.

**Phase: implementing P1, under the scope reset of ADR-0030.** The agent-operator, its CRD, the chart and CI exist and pass on k3d and kind.

**Do not read "approved" as "settled."** An earlier version of this line said all 27 designs were approved and critique-passed. That was false, and it was the first thing every contributor read. Design 02's own header says it "is not approved and must not be cited as such"; design 03's says "RE-OPENED, do not implement." Check the Status line of the design you are about to touch, and trust that over any summary — including this one. `docs/designs/README.md` carries the table.

**What is actually true of the system today**: an agent answers a request. `test/responder` is a real A2A responder, built and pushed by `hack/e2e.sh`, deployed by the operator from a registry digest, and asked a question through its revision Service — the thing no test here could do until 2026-09-06, when the e2e still ran `registry.k8s.io/pause` for everything. What remains untrue: `gateway.enabled` defaults to `false`, and the policy compiler does not exist. The operator now fetches, validates and digests the card the container serves (A68), but does **not** verify its signature, cross-check its skills against the CR's grants, or count the registration deadline — §5 carries those three individually. So the platform's central claim — that governance becomes real **at the gateway** — is still unexercised; what now works is the workload half beneath it. The next work is the rest of the narrow slice in ADR-0030, not the next design.

## Skills — use them, do not paraphrase them

Nine skills live in `.claude/skills/`. **A skill with a `paths:` key does not resolve until a matching file has been read in the session** — so `/implement-feature` returns "Unknown skill" on a cold start even in the right directory, and `/write-spec`, `/adr`, `/design-component` and `/audit-docs` behave the same way. That is a property of `paths`, not a broken session. `/verify-change`, `/review-code`, `/critique-design` and `/research-latest` carry no `paths` and always resolve.

| Skill | Use when |
|---|---|
| `implement-feature` | writing or changing any code under `api/ internal/ cmd/ test/ config/ charts/` |
| `review-code` | before merging a diff — forks its own context |
| `critique-design` | before approving a design or amending an approved one — forks its own context |
| `write-spec` | writing a design, an ADR, a CRD type, or any user-facing string |
| `research-latest` | any landscape, library or standard question — never answer from memory |
| `design-component` · `adr` · `audit-docs` | designing, recording a decision, checking doc consistency |
| `verify-change` | **after any amendment or fix** — the loop that closes it: gate → independent critique → cross-family critique → spike the unmeasured claim |

## Everything else lives in AGENTS.md

**Read `AGENTS.md`.** It carries the rules this project learned the hard way, the doctrine and charter gates, the commands, and what each test layer can honestly claim.

It is model-neutral on purpose: Codex, Cursor and Copilot read `AGENTS.md` and never see this file, so a rule duplicated here would drift into two versions and reviewers would be held to different standards depending on which model they happened to be.

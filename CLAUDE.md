# CLAUDE.md — plume

plume ("graphene" in early drafts) is a lightweight, Kubernetes-native agent platform.

**Phase: implementing P1.** All 27 designs are approved and critique-passed. The agent-operator, its CRD, the chart and CI exist and pass on k3d and kind. Next by the build order is design 03, the policy compiler.

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

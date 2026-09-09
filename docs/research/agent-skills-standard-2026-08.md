# Agent Skills — a cross-vendor open standard assayd should bind

**Date**: 2026-08-22 · **Sources**: agentskills.io, github.com/agentskills/agentskills, code.claude.com/docs/en/skills

## What it is

A skill is a folder containing `SKILL.md`: YAML frontmatter (`name`, `description` at minimum; `license`, `compatibility`, `metadata`, `allowed-tools` complete the spec's six fields) plus markdown instructions, optionally bundling `scripts/`, `references/`, and `assets/`. Loading is **progressive disclosure** in three stages — discovery loads only name and description, activation reads the full body, execution pulls bundled files on demand. That is the whole mechanism.

Originated at Anthropic, **released as an open standard**, governed in the open at `github.com/agentskills/agentskills`. Adoption is broad and cross-vendor rather than Anthropic-only: Cursor, GitHub Copilot, VS Code, Gemini CLI, OpenAI Codex, OpenHands, Goose, Letta, Roo Code, Kiro, Spring AI, Pulumi Neo, Snowflake Cortex Code, Databricks Genie, Tabnine, Factory, Amp, Junie, Firebender, and roughly two dozen more.

## Why this is load-bearing for assayd

Design 18 §3's pack manifest lists a `skills` facet — `skills: [when-to-summarize.md]` — as bare markdown files with **no declared format**. Under ADR-0003 ("open standards only; thin bindings") an undeclared format is an invented one by omission: the moment a second producer writes a pack, assayd owns a de-facto schema it never specified and cannot validate.

The fit is close to exact:

| assayd needs | Agent Skills provides |
|---|---|
| a portable unit of procedural knowledge a pack can ship | a folder with a declared manifest |
| indexing without copying content (18 §4) | `name` + `description` are the discovery tier by design |
| something the CLI/wizard can list and filter | the same two fields, capped for listing cost |
| bundled scripts and reference material | `scripts/`, `references/`, `assets/` |
| content-addressed distribution | a folder — an OCI artifact under the existing Artifact primitive |
| agents built with any SDK consuming the same pack | ~45 clients already read this format |

The extensibility thesis (ADR-0008, the pattern charter) is that a new technique should arrive as *data* rather than a platform change. Agent Skills is that thesis, already standardized, already multi-vendor, and already the mechanism the user pointed at when asking why "Claude Code keeps coming with new techniques so easily."

## Proposed binding — NOT yet decided

1. **Declare `SKILL.md` (Agent Skills) as the format of the pack `skills` facet** (design 18 §3). Validate the six spec fields at install; reject a skills facet whose entries do not parse.
2. **Index `name` + `description` into the `packs.*` KV space** — the discovery tier maps onto design 18 §4's existing indexing rule with no new mechanism.
3. **Distribute a skill folder as an OCI artifact**, which is the existing Artifact primitive, not a sixth one.
4. **Do not extend the format.** Claude Code's extra frontmatter (`context: fork`, `paths`, `hooks`, `effort`) is a vendor extension; assayd binds the spec's six fields and ignores the rest, exactly as it treats other bindings.

## Open questions before an ADR

- Does a assayd skill target the *agent at runtime* (the agent's own SDK loads it) or the *platform's tooling*? Design 18 §3's comment "loop skills → indexed for templates/wizard" suggests tooling; if it is also runtime, the agent-facing surface is MCP (ADR-0009) and the two must not blur.
- Whether the `pack/v1` facet catalog gains a contract revision for this, per ADR-0024's closed-catalog rule (a tenth facet, `profile`, was the one exception granted — this would be a *specification* of an existing facet, not a new one, so probably no revision is needed).
- Licensing: the spec's `license` field vs assayd's signer-allowlist trust model (design 18 §5).

## Recommendation

Raise as an amendment to design 18 with an ADR, before design 18 is implemented. The cost of binding now is a paragraph of manifest schema; the cost later is a pack-format migration across every published pack.

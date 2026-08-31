# Cross-family review availability — a recorded gap, not a silence

**2026-08-31.** The Codex reviewer refused two of three requests in this round
on cybersecurity grounds, returning:

> This content can't be shown. We take extra caution with cybersecurity
> requests.

It produced r8 after the request was re-framed to state the defensive context
explicitly — our own unreleased repository, our own threat model, reviewed
before release. It refused r9 with the same framing.

This is recorded because `docs/agent-protocol.md` says findings are files and a
missing review is a finding about the process. The gap matters for a specific
reason the protocol already argues: the value of the Codex pass is that it has
**not** been anchored by this project's reasoning, and the material that
triggers the refusal — chosen collisions, attacker principals, bypass chains —
is exactly the material where that independence is worth most.

What was substituted, and how much weaker it is:

| | |
|---|---|
| Requested | Codex r9 — cross-family, fresh context |
| Delivered | two same-family critiques with forked contexts (`critique-design`, `review-code`) |
| Evidence value | **lower, and not a substitute.** Same family, same training. The protocol's own example is a same-model reviewer with a fresh context finding three blockers five anchored rounds had missed — useful, and still one model's blind spots |

The same-family passes were not cheap talk: between them they found six design
blockers r7 missed, and two blockers in code r8 had reviewed and passed —
including a fail-closed rule that had been applied one step too wide and had
turned a self-correcting drift into a permanent one for a *weaker* principal.

**What is owed:** an unanchored cross-family review of the r7–r9 range before
design 03 implementation starts. If Codex continues to refuse, that means either
a different second family or an explicit human decision to proceed on
same-family review alone — and the second is a decision, not a default.

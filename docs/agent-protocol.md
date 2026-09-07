# Agent-to-agent protocol

Several agents work on plume at once, from different models. They can already reach each other — `herdr` is on PATH and any of them can run `herdr agent prompt <name> "..."`. This document is about **when not to**.

## Why this is constrained rather than open

The reason a second model reviews plume at all is that it has *not* been anchored by the first one's reasoning. Two agents that talk freely converge: the reviewer softens a finding after hearing the rationale, the implementer adopts the reviewer's framing, and what looked like independent agreement is an echo. That failure is invisible — it produces a clean review and a bug in production.

The evidence is already in this repo. A reviewer of the **same** model, given a fresh context, found three blockers in code that had passed five rounds with a reviewer that had been in the conversation the whole time. Same model, same rules; the only variable was whether it had been anchored. Cross-model review buys more of that, and chat spends it.

So the rule is: **anything that would change a reviewer's mind must be a fact, not an argument.**

## Who exists

| name | model | role | independence it actually buys |
|---|---|---|---|
| `plume` | Claude Opus | implementation — writes code and designs | — |
| `critic2` | Claude, same model, fresh context | review | unanchored by the conversation, anchored by the model's priors |
| `fable` | Claude, **different model** | review | the above, plus different priors |
| `codex-critic` | Codex / GPT | review — deliberately orthogonal | the most, and the only one that is cross-family |

`herdr agent list` is authoritative; names change.

**Independence is a gradient, not a flag, and a review is worth what its
reviewer's independence is worth.** Ranked by the last column, and the ranking
is earned rather than assumed: a fresh reviewer of the *same* model found three
blockers in code that had passed five rounds with an anchored one, and a
cross-family review then found thirteen in a body two same-family rounds had
called closed. Reach for the most independent reviewer available, and when you
settle for less, **write down which you used** — a finding count means nothing
without it, and "reviewed" in a status line that hides a weaker reviewer is how
this project talked itself into "approved and critique-passed" for 27 designs
while two of them said otherwise in their own headers.

A model can also simply be unavailable — exhausted budget, a rate limit, an
outage. That is a normal condition, not a reason to skip review or to quietly
promote a weaker one into the same sentence. Use what you have, and say what it
was.

## What travels as a file, and what travels as a message

**Findings are files.** A review lands in `docs/designs/reviews/` and is committed. It is durable, diffable, readable a year later, and readable by a model that did not exist when it was written. A finding delivered only as a message is lost the moment a session ends.

**Messages are for coordination, not content.** "Review of commit abc123 is in reviews/03-codex.md" is a message. The review is not.

## The channels, and their direction

**Reviewer → implementer: findings. One-way.**
Deliver the file location. Do not accept a rebuttal and do not withdraw a finding because the implementer explained the intent — the finding is about what the code does, and intent that is not in the code is the defect.

**Implementer → reviewer: factual clarification only.**
Permitted: *"B2 says the cold-start path is unguarded — do you mean the case where the CRD was never installed, or where it was installed and then removed?"* That is asking what a sentence means.

Not permitted: *"B2 isn't a blocker because the chart doesn't install the gateway yet."* That is arguing, and it belongs in the fix commit where a human can see it — not in a channel that ends with the reviewer agreeing.

**Reviewer ↔ reviewer: nothing.**
Two reviewers must not reconcile. If Claude calls something a blocker and Codex calls it a minor, **the disagreement is the finding** — record both verdicts and escalate to the human. A consensus reached between two models is worth less than the disagreement it replaced, because the human loses the one signal that told them where to look.

## The rule that overrides all of the others

**Never ask another agent what you can determine by running something.**

This project has been wrong repeatedly by reasoning where it could have measured: four wrong hypotheses about a CI failure that a five-line workflow settled in one push; nine tests that passed with their subject deleted, each found by mutation and none by reading. Another agent's opinion is a worse source than the cluster, the test, or the log — it is the same guessing with more latency.

Ask a peer only for something it *knows and you cannot observe*: what it meant, what it already tried, where it put a file.

## Mechanics

```bash
herdr agent list                                   # who is alive, and their state
herdr agent prompt <name> "..." --wait --timeout 300000
herdr agent read <name> --source recent-unwrapped --lines 200
```

`--wait` blocks until the peer settles. Without it you have posted, not asked.

A peer that is `working` is mid-task: prompting it queues behind what it is doing, so a "quick question" can sit for ten minutes. Check `herdr agent list` first and prefer a file if the answer is not urgent.

## Never `git add -A` while another agent shares the worktree

A reviewer running a mutation ledger is *deliberately* leaving broken code in the
tree — that is how a mutation is tested. Any other agent that commits with
`git add -A` at that moment sweeps the mutation into history.

That happened here, and it is not hypothetical. A protocol-documentation commit
picked up a live mutation that had deleted `OAuthClientRef` from the revision
projection, and pushed it. That field is the identity an external agent
authenticates as, so removing it from the projection means repointing it reaches
production **ungated** — the exact blocker an earlier review had already found
and fixed. It went unnoticed because the test covering it was vacuous, which is
precisely what the mutation was proving.

Two failures compounding: one agent committed another's in-flight breakage, and
the test that should have caught it could not fail.

So, whenever `herdr agent list` shows another agent alive in this repo:

- Stage **explicit paths**: `git add docs/ AGENTS.md`, never `-A` or `.`.
- Run `git status --porcelain` first and account for every line. A modified file
  you did not touch belongs to someone else.
- Prefer committing while peers are `idle`, and never mid-ledger.

The durable fix is a worktree per agent (`git worktree add`), which herdr
supports. Until then, this discipline is the whole defence.

## When a reviewer runs out of context

Reviews degrade as a context fills — the five-round reviewer had stopped finding things well before it hit its limit. **Start a fresh one rather than pushing an exhausted one further.** Nothing is lost: findings are files, and the new reviewer reads them.

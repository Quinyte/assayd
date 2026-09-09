# Contributing to assayd

Thank you for considering it. This document is short on ceremony and specific about the two or three things this project is genuinely strict about, because those are the ones that will get a pull request sent back.

## Sign your commits (DCO)

assayd uses the [Developer Certificate of Origin](https://developercertificate.org/), not a CLA. Signing off certifies that you wrote the patch or otherwise have the right to submit it under the project's licence; it transfers nothing and takes one flag:

```
git commit -s -m "your message"
```

That appends `Signed-off-by: Your Name <your@email>`, which must match your commit author. `git commit -s --amend` fixes a commit you forgot it on; `git rebase --signoff main` fixes a branch.

**Why DCO and not a CLA**: a CLA's one real advantage is that it lets a project relicense later, and ADR-0015 has foresworn relicensing. Asking contributors to sign away rights for a power the project has promised not to use would be a cost with no purchase.

## Before you write code

**Read `AGENTS.md`.** It carries the rules this project learned the hard way, the commands, and — most usefully — what each test layer can honestly claim. It is model-neutral and is the single source; `CLAUDE.md` points at it rather than duplicating it.

**Check the design's Status line, and trust it over any summary.** "Approved" here does not mean "settled" or "buildable". Design 02's own header says it is not approved; design 03's says RE-OPENED, do not implement. `docs/designs/README.md` has the table. This matters more than it sounds: an earlier version of the front page told every new contributor that all 27 designs were approved and critique-passed, and that was false.

## The three rules that get a PR sent back

**1. Reproduce a finding before fixing it.** A fix for a bug nobody observed is a change with no evidence behind it. Show the failure first — a test that fails, a command whose output you paste, a cluster you can point at.

**2. Mutation-check every behavioural claim.** If you add a test, break the thing it tests and show the test fails. A test that passes both ways measures nothing, and this repository has shipped several. **A mutation that does not compile is INVALID, not survived** — so is a run that never reached the tests.

**3. "It was refused" is not evidence about *which* rule refused.** If you assert that a policy, an admission rule or a network rule denied something, prove it was that one. Several times here a denial has come from RBAC, a race, or an unreachable service while the test recorded it as the control working.

These are not stylistic. Each is written down because this project has been wrong in exactly that way and the record is in the amendment histories.

## Changing a design

Designs are not documentation of the code; the code is downstream of them. If your change contradicts a design, amend the design in the same pull request. Amendments are **numbered and kept** — never rewrite history to match a later decision, and never delete a line that argues against your change. Address it in the text instead.

**When an amendment claims something about another design, read that design first.** The most common defect in this repository's history is a confident claim about what a neighbouring document says. Quote it and cite the line.

## Testing

```
make test          # unit, envtest, chart, docs, conformance
make e2e           # k3d: a real cluster, a real gateway, real agents
```

`make e2e` builds and pushes the fixtures, installs Gateway API and agentgateway, and runs the suite. It needs Docker and k3d. Some tests **skip and report an axis unverified** rather than pass — NetworkPolicy enforcement is the CNI's, and on a CNI that does not implement it a policy is accepted and does nothing. A skip that says "unverified" is the honest result and is not a failure to fix by making it pass.

## Pull requests

Small and single-purpose. Explain what you observed, not only what you changed — the commit messages in this repository are long on purpose, and the good ones say what was measured and what is still not known. If something in your change is unproven, say so in the PR; an acknowledged gap is fine and a silent one is not.

By contributing you agree your work is licensed under Apache-2.0 and that you have signed off under the DCO.

## Reporting a vulnerability

Not here — see `SECURITY.md`. Please do not open a public issue or a fixing PR before disclosure.

# The assayd website — information architecture

- **Status**: **proposal, not approved.** No critique of this document has been run. It specifies pages, not prose, and no page it names exists. Where it proposes a file outside `docs/web/`, that file is owed by whoever implements the section, and this document does not create it.
- **Date**: 2026-09-22 · **Scope**: `assayd.io` and `assayd.dev`
- **Reads from**: `README.md`, `AGENTS.md`, `CLAUDE.md`, `SECURITY.md`, `docs/architecture.md`, `docs/install.md`, `docs/agent-contract.md`, `docs/agent-protocol.md`, `docs/supply-chain.md`, `docs/requirements.md`, `docs/designs/README.md`, the Status lines of designs 02, 03 and 16, and ADR-0030.
- **Does not decide**: the site scaffold (`web/**`), the generated reference tooling (`hack/**`, `docs/reference/**`), or the diagram audit (`docs/diagrams/**`). Those are owned elsewhere. This document names what those owners must produce and what a page may claim about it.

## 1. Why this document exists, and the one rule it enforces

`CLAUDE.md` opens by recording that an earlier line saying all 27 designs were "approved and critique-passed" was false, and that it was the first thing every contributor read. `docs/architecture.md` §18 records the same sentence being corrected everywhere else and surviving in one more place for weeks. `AGENTS.md` rule 7 states the general form: **never state a bound the code does not enforce.**

A public website is the highest-reach surface this project will ever have, and the lowest-friction place to restate a summary. Every mechanism in this document exists to make that specific failure mechanically expensive.

**The rule: every claim on every page carries a claim class, and no page asserts anything outside a classed claim.** The three classes are `measured`, `designed` and `not-built`; §4 defines them. The rest of this document is the page tree that rule produces, the mapping that keeps each page tied to a holder in the repository, and an honest account of which parts of that mapping fail a build and which parts rely on someone noticing.

**Rule 7 applies to this document.** Where a mechanism proposed here is not enforceable today, the text says so in the same sentence that proposes it, and §12 carries every question this document could not answer.

## 2. The two targets

The split is decided and is not relitigated here.

| Target | Holds | Reader |
|---|---|---|
| **`assayd.io`** | Landing, concepts, roadmap, releases, security posture, about | Someone deciding whether to keep reading |
| **`assayd.dev`** | Guides, generated reference, status, supply chain, contributing, and the published design and ADR archive | Someone who has decided, and now has a cluster |

One consequence is worth stating because it constrains every page below: **concepts live on `.io`, and the guides that use them live on `.dev`.** A guide therefore links across the domain boundary on first use of a concept, and never re-explains it. Re-explaining is how two copies start.

### 2.1 Prior art, and what is borrowed from it

**The design rule this section exists to serve: a reader arriving cold should have to learn exactly one unusual thing about this site, and that one thing is the claim class.** Everything else is deliberately conventional, because novelty spent on navigation is novelty not available for the idea that actually needs it.

Three conventions are therefore adopted rather than invented, and the argument for each is that the pattern already exists in this project's own neighbourhood:

- **The Diátaxis-shaped split** — *guides (how-to) / concepts (explanation) / reference*. `docs/install.md` is already a how-to, `docs/agent-contract.md` is already a reference with a sources table, and neither explains why the gateway is where a rule lives. The split falls out of the corpus rather than being imposed on it.
- **A per-claim maturity label, rendered inline.** Kubernetes has trained every reader of a Kubernetes-adjacent project to accept a small `alpha`/`beta`/`GA` marker beside a heading and not read it as clutter. The claim class is that badge with two differences: it attaches to a *claim*, not to a feature, and it names the *evidence*, not the ship stage.
- **One canonical status page** that answers "is this actually done" without the reader reconstructing it from release notes. `/status` (§7) is that page.

What is deliberately **not** adopted: the comparison table (§9.4), and the "Why *project*?" landing pattern that asserts capabilities in the present tense. Both are near-universal and both are exactly the shape this project has already been burned by.

**This section is the weakest in the document and is marked so.** The three conventions above are argued from this repository and from the Kubernetes convention a reader brings with them, not from a completed survey of comparable sites' current navigation. That survey is owed (§12, O9), and it could change the labels — though it is unlikely to change the split, which the corpus already has.

## 3. The page tree

Every page below states the question it answers for a reader who arrives cold, knowing nothing.

### 3.1 `assayd.io`

| Path | The question it answers cold |
|---|---|
| `/` | What is assayd, what does it do *today*, and is it for me? |
| `/claims` | What do `measured`, `designed` and `not-built` mean, and why is one attached to every sentence? |
| `/concepts/` | Which ideas do I need before the guides make sense? |
| `/concepts/revisions-and-cards` | Why is an agent's identity its content rather than its tag? |
| `/concepts/governance-at-the-gateway` | Why is a rule enforced at the gateway rather than in the agent's SDK? |
| `/concepts/the-auth-transaction` | What does the operator do between "I created an Agent" and "traffic flows"? |
| `/concepts/create-lock-adopt` | Why are there three ways to attach a policy, and why is one of them a refusal? |
| `/concepts/attribution` | Why is a `401` not enough on its own? |
| `/concepts/reserved-writes` | Who may write a policy or an API-key ConfigMap, and what stops anyone else? |
| `/concepts/the-revision-service` | What does one revision get of its own, and what does the stamp on it prove? |
| `/roadmap` | What is being built next, what is knowingly missing, and what is broken and unfixed? |
| `/releases` | Which versions exist, what changed, and which should I install? |
| `/security` | How do I report a vulnerability, and what is not yet enforced? |
| `/about` | Who makes this, under what licence, and who decides? |

`/claims` is the link target of every claim badge on both domains. It is one page, on `.io`, and `.dev` links to it rather than mirroring it — for the reason in §2: a mirrored page is two pages.

### 3.2 `assayd.dev`

| Path | The question it answers cold |
|---|---|
| `/` | Where do I start? |
| `/guides/install` | How do I get from a fresh cluster to one agent answering through a gateway? |
| `/guides/api-keys` | How do I let one caller in and keep another out? |
| `/guides/a2a-task` | How do I send an agent a task and read the answer? |
| `/guides/mcp-tool` | How do I publish a tool behind the gateway, and how does an agent call it? |
| `/guides/upgrading` | Why did `helm upgrade` not update my CRD, and what do I do about it? |
| `/guides/troubleshooting` | My install or my Agent is stuck — what is it telling me? |
| `/agent-contract` | What must my container do to run as an Agent? |
| `/supply-chain` | How do I verify the bytes I am about to run? |
| `/status` | Is the thing I need actually enforced, at the version I run? |
| `/reference/` | Which reference surface do I want? |
| `/reference/agent` | Every field of the Agent CRD, its default, and what refuses it |
| `/reference/values` | Every chart value and its default |
| `/reference/conditions` | Every condition type and phase the operator sets |
| `/reference/flags` | Every operator flag |
| `/reference/labels` | Every `assayd.dev/*` label and annotation, and whether it is evidence |
| `/reference/tested-versions` | Which versions of Kubernetes, Gateway API and agentgateway have been measured |
| `/archive/` | Where is the design record, and how should I read it? |
| `/archive/designs/<nn>` | What does design `<nn>` specify, and is it approved? (27 pages) |
| `/archive/decisions/<nnnn>` | What was decided, when, and why? (34 pages) |
| `/archive/architecture` | What is the target architecture, as a document of its date? |
| `/archive/reviews` | How many independent reviews has each design had, and what did each return? |
| `/contributing` | How do I contribute, and what will happen to my patch? |
| `/contributing/review` | What does "independently reviewed" mean here? |

**`/reference/reasons` is deliberately absent, and §5.4 explains why.** The short form: condition *types* are a closed vocabulary with a test that closes it; condition *reasons* are not, and a reference page listing 30 of the 56 reasons in the operator would be a reference that is wrong about the other 26.

## 4. The claim-class system

### 4.1 The three classes

| Class | What it asserts | What the claim record must carry |
|---|---|---|
| `measured` | A named test proves it. | The test function name, its file, and its layer. |
| `designed` | A design specifies it. Nothing enforces it. | The design file, the section, and the design's own Status verdict, quoted. |
| `not-built` | Neither. | Optionally the design that would specify it, if one exists. |

There is no fourth class, because the component being built for the site renders exactly three, and because a fourth would be the hedge this project keeps paying for.

### 4.2 The rules

1. **A claim is a sentence a reader could act on.** "The operator publishes a route only after an anonymous request through it gets `401`" is a claim. "assayd is Apache-2.0" is a fact about the repository, not a claim about behaviour, and carries no class. The boundary is: if it asserts what the software does or refuses, it is a claim. *(Nothing detects an unclassed claim; §5.3 says so.)*

2. **A `measured` claim may not say more than its test's layer can claim.** `AGENTS.md` fixes the layers: unit asserts against the generated artifact; envtest runs a real API server and **no kubelet**, so every availability assertion there is against a status the test itself wrote; only e2e can claim a pod actually runs. A claim about a running pod backed by an envtest is mis-classed even though a test exists. *The layer is derived from the test's directory and checked mechanically (§5.2 gate C); that the layer is high enough for the sentence is not.*

3. **A `measured` claim whose test can skip must name the condition in the claim's own text.** Fourteen e2e tests here skip under an environment variable, and the two that matter most say so loudly: `TestTheGatewayIsTheOnlyWayIn` reports "NetworkPolicy enforcement UNVERIFIED, not passed" on any CNI that does not enforce it, and everything responder-backed skips on the kind lane. A site sentence that reads "the gateway is the only way in" full stop is false on kind. It must read "on a CNI that enforces NetworkPolicy, under a hand-authored rule". *Mechanically checked: gate B.*

4. **A `designed` claim is never written in the present indicative.** It renders as "design 03 §3.5 specifies…", never "assayd enforces…". The reason is the exact failure `README.md` records: present-tense prose about a designed capability is indistinguishable from a guarantee at reading speed. *Not mechanically checked; a reviewer catches it or nobody does.*

5. **A claim's class may only be raised in the commit that adds its evidence.** `not-built` → `designed` needs the design section in the same change; `designed` → `measured` needs the test. *Mechanically checked in the raising direction only: gates A and D fail if the evidence is absent. Lowering is not checked — if the compiler is deleted and its tests with it, gate A fires; if the code is deleted and a now-vacuous test survives, nothing fires.*

6. **A diagram is a claim.** Six of the eight plates in `docs/diagrams/` depict systems that do not exist — eval-gated rollout, the on-behalf-of token exchange, knowledge-graph ingestion, the governance ring, the six-CRD surface, and a rollout lifecycle with a `Canary` state the slice never enters. Plate `07-what-runs` is titled for what runs and itemises eight core pods; the chart contains exactly one `Deployment`. A plate carries the class of what it depicts, and a `not-built` plate does not appear above the fold on any page. *The verdict per plate belongs to the diagram audit; this rule does not pre-empt it.*

### 4.3 What a claim record looks like

The minimal record, a `not-built` claim with no design:

```yaml
- id: egress-restricted
  text: "An agent's outbound traffic is restricted to the gateway."
  class: not-built
  pages: [io/roadmap]
```

A realistic record, the `measured` claim on the landing page:

```yaml
- id: gateway-carries-traffic
  text: >-
    A request reaches the agent through the Gateway's own Service, carried by a
    route the operator wrote.
  class: measured
  evidence:
    test: TestAnAgentAnswersThroughTheGateway
    file: test/e2e/gateway_test.go
    layer: e2e
  conditions:
    - "k3d only. The kind lane skips every gateway test and reports the path unverified."
  pages: [io/, io/concepts/governance-at-the-gateway, dev/status]
```

And a `designed` record:

```yaml
- id: budgets-at-the-gateway
  text: >-
    Design 03 §3.5 specifies token and USD budgets compiled onto the gateway.
    No AgentgatewayBackend is emitted, so nothing enforces one.
  class: designed
  evidence:
    design: docs/designs/03-policy-compiler.md
    section: "§3.5"
    status: "The rest of this document is not approved, and must not be implemented."
  pages: [io/roadmap, dev/status]
```

## 5. Source of truth, and what keeps the site from going stale

### 5.1 The decision

**One claims manifest, `web/claims.yaml`, is the single holder of every classed claim on both domains. Pages reference claims by `id` and never restate the text. A Go test, `test/docs/claims_test.go`, checks the manifest against the repository, and `make docs` runs it.**

Three modes of page production follow from that, and every page in §3 is assigned one in §5.4:

| Mode | Meaning | Editing the page by hand is |
|---|---|---|
| **generated** | A build step emits the page from a repo file. | a bug — the change belongs in the holder |
| **derived-and-reviewed** | A human rewrites a repo document for a site audience. Named facts inside it are bound by a gate. | expected, within the gate |
| **hand-written** | No repository holder. | the only way |

**Alternatives considered.**

- *Per-page front matter, no manifest.* Rejected. The same claim appears on the landing page, `/status` and `/roadmap`; three copies is the defect being fixed, and it is the shape of the original failure — a summary restating what a design held.
- *No manifest; derive the class from the test suite alone.* This is the simpler option and it is recorded as rejected, because Go offers no stable place to hang a claim identifier that survives a refactor, and because `designed` and `not-built` claims have no test to hang one on. Half the system would need a manifest anyway, so the hybrid is the smaller total surface, not the larger one.
- *A release checklist.* Rejected on the standard the coordinator set and this project already holds: a mechanism that requires a human to remember something is not a mechanism. It is named here because it is what the project does **today** for `README.md`'s two lists and `CLAUDE.md`'s paragraph, and that is precisely the thing that went false once.
- *A lint over page prose for assertive verbs.* Rejected. `test/docs/superseded_test.go` works because each rule bans one specific known-false phrase and permits its retraction; a rule that bans a grammatical category produces false positives at a rate that trains people to add exemptions. **What is adopted instead** is extending that existing test's scan root to cover the site's content files, so that the nine withdrawn guarantees it already bans cannot reappear on the website.

**Consequences.** Adding a page, moving a claim between classes and generating `/status`, `/roadmap` and the landing page from one place all become cheap. Two things become harder. The site can no longer be built outside a checkout, because the manifest and the tests must be read together — intended, since the site is built at a tag. And a claim whose evidence is a *mutation result* rather than a test — "three mutations kill it", which is the strongest evidence this repository produces — has no field in the record; §10 carries it. Reversing is cheap: delete the manifest and the test, and pages render an unclassified badge.

### 5.2 The gates

Each is one assertion in `test/docs/claims_test.go`, and each fails the build.

| # | Gate | Catches |
|---|---|---|
| A | Every `measured` record's `evidence.file` exists and contains `func <test>(`. | A renamed or deleted test leaving a `measured` claim standing. |
| B | Every `measured` record whose named test body (or a helper it calls) reaches a `t.Skip` has a non-empty `conditions`. | Rule 3 — the "true on k3d only" class of overclaim. |
| C | `evidence.layer` agrees with the file's directory: `test/e2e/`→`e2e`, `test/envtest/`→`envtest`, `test/conformance/`→`conformance`, `internal/`\|`api/`\|`test/chart/`→`unit`. | A unit test dressed as proof that a pod runs. |
| D | Every `designed`/`not-built` record's `evidence.design` exists and contains `evidence.section` literally. | write-spec's "a cross-reference to a section number that has moved". |
| E | Every `designed`/`not-built` record's `evidence.status` string appears in that design's `- **Status**:` bullet. | A slice's approval boundary moving without the claim moving. |
| F | Every claim `id` referenced by a page exists in the manifest, and every manifest record is referenced by at least one page. | Dangling references and orphan records. |
| G | `TestEveryClaimsGateIsPinnedByAFixture` — each gate above has a fixture that fails it. | The gate itself rotting. `test/docs/superseded_test.go` already does this as `TestEveryRuleIsPinnedByAnIndependentFixture`; rule 1 says a gate nothing can fail is not a gate. |

### 5.3 What these gates do not catch — stated, not dressed up

- **That the named test measures the claim's sentence.** Nothing in CI reads English. A test can be renamed to keep gate A green while its body is gutted. The project's answer is mutation-checking (rule 1), which is a human loop, and it stays one.
- **That a page contains no unclassed claim.** Rejected as a lint above. A reviewer catches it or nobody does.
- **That a class is lowered when code is deleted.** Gates fire on missing *evidence*, not on missing *subject*.
- **That rule 4's present-indicative ban is honoured.** A reviewer catches it or nobody does.

### 5.4 Per-page source of truth, mode, and whether a gate protects it

**The enforcement column below is written against a defect this document found and does not hide:** `make docs` — the repository's only mechanical prose gate — is in `make test` but is **not** in `.github/workflows/ci.yml`, which runs `verify`, `vet`, `unit`, `race`, `envtest`, `chart`, `chart-conform` and `e2e` individually and omits `docs` and `conformance`. **So every "CI" below is conditional on adding `make docs` to `ci.yml`**, which is the first item on the cut list (§9) and is not done by this change. Until it lands, every row reads "someone notices", including the rows for the gate that exists today.

#### `assayd.io`

| Page | Holder(s) | Mode | Protected by |
|---|---|---|---|
| `/` | `web/claims.yaml`; the governance sentence from `charts/assayd/templates/NOTES.txt` (both branches of `gateway.enabled`) | generated from the manifest, hand-written frame | CI (A–G) for every claim; the frame, someone notices |
| `/claims` | this document §4 | hand-written | someone notices |
| `/concepts/*` | `web/claims.yaml` for each assertion; the design section each concept explains | derived-and-reviewed | CI (A–G) per claim; the explanation, someone notices |
| `/roadmap` | `docs/decisions/0030-*.md`; each design's own `- **Status**:` bullet; `web/claims.yaml` | generated (status table and gap list), hand-written frame | CI (D, E) |
| `/releases` | git tags; `docs/supply-chain.md`'s published-artifact table; `.github/workflows/release.yml` | generated from tags | someone notices — there is no CHANGELOG and no release-notes file to generate from (§10) |
| `/security` | `SECURITY.md`; the `security`-tagged records in `web/claims.yaml` | derived-and-reviewed | CI (A–G) for the not-enforced list; the reporting policy, someone notices |
| `/about` | `LICENSE`, `MAINTAINERS.md`, `GOVERNANCE.md`, `SECURITY.md` §Regulatory | derived-and-reviewed | someone notices |

#### `assayd.dev`

| Page | Holder(s) | Mode | Protected by |
|---|---|---|---|
| `/` | — | hand-written | someone notices |
| `/guides/install` | `docs/install.md` §§1–3, 5 | derived-and-reviewed | someone notices; the version pins it quotes are held by `hack/e2e.sh` and are bound by `/reference/tested-versions` |
| `/guides/api-keys` | `docs/install.md` §3; `charts/assayd/values.yaml` `admission.apiKeyWriters` | derived-and-reviewed | CI via `/reference/values` for the value names |
| `/guides/a2a-task` | `docs/install.md` §5; `docs/agent-contract.md` "The A2A binding" | derived-and-reviewed | someone notices |
| `/guides/mcp-tool` | `docs/install.md` §6; `docs/agent-contract.md` "Calling an MCP tool" | derived-and-reviewed | someone notices |
| `/guides/upgrading` | `docs/install.md` §4 | derived-and-reviewed | someone notices |
| `/guides/troubleshooting` | `docs/install.md` "When it does not work" | derived-and-reviewed | someone notices |
| `/agent-contract` | `docs/agent-contract.md` | derived-and-reviewed | its own "Sources" table already names the file or test behind every row — the closest thing in the corpus to what §5.1 proposes, and the model for the manifest |
| `/supply-chain` | `docs/supply-chain.md`; `.github/workflows/release.yml` | derived-and-reviewed | someone notices |
| `/status` | `web/claims.yaml` at each release tag | generated | CI (A–G) |
| `/reference/agent` | `api/v1alpha1/agent_types.go` → `config/crd/assayd.dev_agents.yaml` | generated | the reference agent's drift check, plus `make verify` |
| `/reference/values` | `charts/assayd/values.yaml` | generated | the reference agent's drift check |
| `/reference/conditions` | the `const` block at `api/v1alpha1/agent_types.go` and `designConditions()` beside it | generated | CI — `TestNoConditionTypeIsALiteral` already closes this vocabulary |
| `/reference/flags` | `cmd/` flag registrations | generated | the reference agent's drift check |
| `/reference/labels` | `internal/compiler` and `internal/controller/runnamespace.go` label constants | generated | the reference agent's drift check |
| `/reference/tested-versions` | `hack/e2e.sh` (`GWAPI_VERSION`, `AGW_VERSION`); `charts/assayd/Chart.yaml` `kubeVersion` | generated | `test/conformance/versions_test.go` already pins that the slice cases run the e2e's agentgateway release |
| `/archive/designs/<nn>` | `docs/designs/<nn>-*.md` | generated, verbatim | CI (E) binds the Status line wherever a claim cites it |
| `/archive/decisions/<nnnn>` | `docs/decisions/<nnnn>-*.md` | generated, verbatim | someone notices |
| `/archive/architecture` | `docs/architecture.md` | generated, verbatim, with a standing banner | someone notices |
| `/archive/reviews` | a directory listing of `docs/designs/reviews/**` | generated (ledger only — §9.1) | a count assertion; see §9.1 |
| `/contributing` | `CONTRIBUTING.md`, `GOVERNANCE.md`, `CODE_OF_CONDUCT.md` | derived-and-reviewed | someone notices |
| `/contributing/review` | `docs/agent-protocol.md` | derived-and-reviewed | someone notices |

**A page that restates a claim held elsewhere points at the holder.** Concretely, and these are the five holders that matter:

- `README.md`'s *What is true today* / *What is NOT true today* — restated by `/` and `/status`.
- The *What remains untrue* paragraph in `AGENTS.md` (and its copy in `CLAUDE.md`) — restated by `/status` and `/roadmap`.
- `docs/designs/README.md`'s status table — **not** the holder for `/roadmap`. Each design's own `- **Status**:` bullet is, because `README.md`, `AGENTS.md` and the table itself all say a design's own Status line beats any summary, and the table is a summary. The table becomes a derived view like the site is.
- `api/v1alpha1` and `charts/assayd/values.yaml` — restated by `/reference/agent` and `/reference/values`, generated.
- git tags plus `docs/supply-chain.md` — restated by `/releases`.

### 5.5 The copy count, which is evidence against this proposal

The same claims are currently written out in **six** places: `README.md`'s two lists, `SECURITY.md`'s *What is NOT yet enforced*, the `AGENTS.md`/`CLAUDE.md` paragraph, `docs/architecture.md` §02 and §18's notes, `docs/install.md`'s opening paragraph, and design 02 §5. Adding `web/claims.yaml` makes **seven** unless something is retired.

The recommendation is not to retire them all. `AGENTS.md` and `CLAUDE.md` are narrative documents for contributors and read as prose for a reason; design 02 §5 is the authoritative list and must stay where an implementer reads it. The proposal is narrower and is one gate:

**Gate H (owed, not proposed as done): `README.md`'s *What is true today* bullets are generated from the manifest's `headline`-tagged `measured` records, and *What is NOT true today* from its `headline`-tagged `designed` and `not-built` records.** That is the highest-reach copy and the one that has already gone false. It requires editing `README.md`, which this change does not do, and it is the second item on the cut list. Until it lands, the manifest is a seventh copy, and the honest description of the first release is that the site has one enforced copy and the repository has six unenforced ones.

## 6. The concepts section

Eight pages. Each one states the single sentence a reader should leave with, the claim class of what the page asserts, and the evidence.

| Page | The one sentence | Class | Evidence |
|---|---|---|---|
| `revisions-and-cards` | An agent's identity is the digest of its resolved spec, so rolling back returns the exact bytes that were evaluated rather than a re-render of today's inputs — and the card that identity is checked against is fetched from the container, never taken from the CR. | `measured` | `TestTheDigestIsStampedOnCreation` (`test/envtest/collision_test.go`), `TestTheOperatorRegistersTheCardItFetched` and `TestTheAgentServesItsCardFromTheContainer` (`test/e2e/responder_test.go`) — deliberately two tests, because one would pass if either side were faked |
| `governance-at-the-gateway` | A rule enforced at the gateway holds whatever SDK the agent was written in — and today exactly one rule is compiled there: who may call this agent. | `measured` for the path and the one rule; `designed` for everything else | `TestAnAgentAnswersThroughTheGateway`, `TestTheGatewayRefusesADisallowedPrincipal` (`test/e2e/`); design 03 §3.5 for budgets, §3.4.1 for tools, both unapproved |
| `the-auth-transaction` | Attaching a policy is a transaction with stages — `PreparingRoute → ApplyingPolicies → Converging → ProbingAfter → Publishing → Served` — because a route that is published before its policy is accepted is a route serving unauthenticated. | `measured` | `TestCreatePublishesOnlyOnA401`, `TestACreateHasADeadlineOutcomeInEveryStage`, `TestCreateReentersAtTheWriteAfterACrash` (`test/envtest/authcreate_test.go`) |
| `create-lock-adopt` | `Create` builds a route that has never carried traffic, `Lock` tightens one that already is, and `Adopt` is a refusal — a route published before the compiler stays unauthenticated and says so, because the operator has no record it could have kept. | `measured` | `TestJ2LocksAServedNoneAgentInPlace` and `TestK2AnOwnersEditEndsTheRefusal` (`test/envtest/authlock_test.go`), `TestARoutePublishedBeforeTheCompilerIsNotLocked` (`test/envtest/authcreate_test.go`), `TestAServedNoneAgentIsLockedInPlace` (`test/e2e/lock_test.go`) |
| `attribution` | A `401` only counts as proof when it can only have come from this Agent's own policy — so the operator refuses to believe one while the Gateway has not accepted the route, while the route carries more than one backend, or while a Gateway-level policy could have answered instead. | `measured` | `TestTheLockProbesTheCardPathAndAttributesEvery401` and `TestAnUnattributable401DoesNotEndTheLock` (`test/envtest/`), `TestTheRouteGateStopsThePassThatMovedTheRoute` (`test/envtest/authgate_test.go`), `TestACreateHoldsWhileAGatewayLevelAuthPolicyStands` (`test/envtest/authabove_test.go`) |
| `reserved-writes` | In a run namespace, only the operator may create a policy and only a named administrator may write the API-key ConfigMap — because agentgateway reads keys live, so writing one mints a principal with no compile, no revision and no gate. | `measured` | `TestPoliciesInRunNamespacesAreReservedToTheOperator` and `TestKeySetsInRunNamespacesAreReservedToTheirWriters` (`test/e2e/reservation_test.go`), `TestOnlyTheOperatorAndAnAdministratorWriteAKeySet` (`test/envtest/`) |
| `the-revision-service` | Each revision gets a Service that selects only its own Pods, stamped with the Agent's UID and the revision digest — and the stamp is corroboration, not authority, because a label needs only `update` to forge, so the immutable object name is what decides ownership. | `measured` | `TestARevisionGetsAServiceThatSelectsOnlyItsOwnPods`, `TestTwoRevisionsGetSeparateServicesThatCannotReachEachOther`, `TestAServiceCarryingAnotherRevisionDigestIsRefusedRatherThanConverged` (`test/envtest/service_test.go`); `TestWorkloadDeletionAuthorityIsTheName` (`test/envtest/runnamespace_test.go`) |
| `standards-only` | Everything assayd speaks is an existing standard — A2A, MCP, Gateway API, OCI — and where one is unimplemented the page says which, rather than implying a binding exists. | `measured` for A2A 1.0, MCP Streamable HTTP and Gateway API; `not-built` for SPIFFE, OASF and OTel GenAI | `test/e2e/mcp_test.go`, `test/e2e/responder_test.go`; `internal/directory/` and `internal/registry/` contain zero files, and no SPIRE, Zitadel or OTel binding exists in `internal/`, `api/`, `cmd/` or `charts/` |

Two constraints on every concept page:

- **The diagram on a concept page carries the class of what it depicts.** Of the eight existing plates, only `01-architecture` and `06-api-surface` are even candidates, and both depict the target surface rather than the shipped one. `08-rollout-lifecycle` shows a `Canary` state design 16's approved slice explicitly never enters. New diagrams are owed for `the-auth-transaction` and `create-lock-adopt`, which have no plate at all and are the two concepts a reader most needs a picture for.
- **A concept page names what it is not.** `governance-at-the-gateway` states in its own body that no `AgentgatewayBackend` is emitted; `reserved-writes` states that `DELETE` is not reserved. Leaving the negation to `/status` is how a concept page becomes a promise.

## 7. The status page

`/status` answers one question: **is the thing I need actually enforced, at the version I run?**

**How it avoids being a third hand-maintained copy: it is generated from `web/claims.yaml`, and the manifest is versioned by git along with the code it describes.** There is no per-version editing step, because there is no per-version file.

**Per-version presentation.**

- The page has a version selector listing every release tag from the first tag that carries a manifest onward. Each entry renders that tag's `web/claims.yaml`. A tag predating the manifest renders a stub naming the tag and linking `README.md` at that tag — honest, and cheap.
- The default view is the **latest release tag**, not `main`. A reader on `/status` is asking about the version they can install. `main` is available as an explicit selection and is labelled unreleased.
- A **delta view** between two selections lists every claim whose class changed, in both directions. That view is also the input to `/releases`, so the release notes and the status page cannot disagree.

**Layout.** Three sections, in this order: `measured` (what is enforced), `designed` (what a design specifies and nothing enforces), `not-built`. Within `measured`, claims carrying `conditions` are grouped under a sub-heading naming the condition — so "true on k3d only" is a heading a reader cannot skim past, not a footnote.

**What the page must not do.** It must not summarise. The manifest's `text` is rendered verbatim, because a summary of a claim list is the artefact this whole document exists to prevent.

## 8. The roadmap page

Three sections.

### 8.1 The slice

ADR-0030's build order, one row per step, each carrying a class. The ADR is the holder; the classes are the manifest's.

| Step | State |
|---|---|
| 1. The supported slice, with unsupported capabilities rejected at the boundary | `measured` — `oauth` and a stored `spec.budget` are refused (`TestABudgetIsRefusedWhileNothingEnforcesIt`, `TestAnExposedAgentMustStateItsAuthentication`) |
| 2. Workload materialization — revision-scoped Services, card fetch, the runtime env contract, retained-revision selection | `measured` (`test/envtest/service_test.go`, `card_test.go`, `injectedenv_test.go`, `release_test.go`) |
| 3. The smallest real policy path through gateway, identity and network rules | **partial** — the gateway half is `measured` and operator-emitted; the network half is `measured` only under a **hand-authored** rule on a CNI that enforces NetworkPolicy, and the operator materializes none; the identity half is `not-built` (design 06 has no implementation, and `internal/registry/` is empty) |
| 4. A narrow design-16 gate, blue/green before canaries | `designed` — design 16 §1.1 is approved; its own Status line says "Nothing this design specifies is implemented" |
| 5. One real domain on a curated knowledge snapshot | `not-built` |
| 6. A second framework, measured against kagent plus existing delivery tooling | `not-built` |

### 8.2 The design status table

Generated, one row per design, **quoting each design's own `- **Status**:` bullet verbatim rather than summarising it**. `docs/designs/README.md`'s table is not the holder, for the reason in §5.4.

The generator truncates nothing and renders no shortened form. Three Status lines here run to several hundred words, and every attempt to compress one is the mechanism that produced the false line in the first place. Long rows are a collapsed disclosure, expanded by default on `designed` and `not-built` rows.

### 8.3 Open follow-ups, under the disclosure posture

**The rule, stated once at the top of the section and applied to every item: name the class, link the design section that already describes it, state the observable condition an operator would see, and stop before the sequence.** The repository is public and the linked section contains everything, so the site withholds no information — it withholds *packaging*. That distinction is the whole posture, and it is written on the page so a reader is not left guessing whether something is being hidden.

| Gap | Class of defect | Links to | Observable |
|---|---|---|---|
| A published route can be served unauthenticated by a gateway replica that has not yet taken the Agent's policy. One probe proves one replica, and no replica count is declared. | Authentication bypass, window, multi-replica only | design 03 §3.3.3 and §8.1 | `GovernanceSkipped=False`, reason `AuthVerifiedOnOneReplica` |
| Deleting the API-key ConfigMap takes every key to `401`, and nothing reports it. `DELETE` is not reserved by admission, and the operator does not watch key sets. *(The coordinator's brief names this **D6**; the repository carries no such label — §12, O1.)* | Availability, not bypass | design 03 §3.4.4 ("The key set is outside every gate") and §8.1's reservation scope | **nothing** — that is the defect |
| A route published before the compiler, or one whose status was lost, stays unauthenticated and is never adopted. | Authentication bypass, upgrade and restore path | design 03 §3.3.3 | `GovernanceSkipped=CompilerUpgradeUnsupported` |
| A served policy the Gateway reports as attached to nothing is announced and not closed — the fail-open half. | Authentication bypass, announced | design 03 §3.3.3 (A80/A81) | `PolicyApplyIncomplete` and `GovernanceSkipped=True`, both `AuthPolicyNotAttached` |
| No NetworkPolicy is materialized in any namespace, so an agent Pod is reachable directly and any gateway control is bypassable. | Bypass, by default, on every install | design 07 A6.6; `SECURITY.md` | none — this is the documented default |
| A policy deleted between a pass's check and its route update leaves the route published without it until the next pass. | Authentication bypass, window | design 03 §3.3.3 | depends where the next pass finds the transaction |

**Nothing pages.** Design 10 is not built and no alert rule ships in this repository, so every "observable" above is a condition a human must go and read. The roadmap page says that in the section, because a list of conditions implies a monitor.

## 9. What does not go on the site, and why

### 9.1 `docs/designs/reviews/**` — 107 files, 21,458 lines — is not published as pages

**Decision: publish a generated *ledger*, not the review texts.** The ledger is one page, `/archive/reviews`, with one row per review: design, date, reviewer family (same-model / fresh-context / cross-family), verdict, and finding counts, linking each row to the file on GitHub.

The argument, in order:

1. **The repository is public, so this is not concealment.** Every file is already readable. The question is only whether each becomes a site page with site navigation, site search and the implicit currency a site page carries.
2. **A review is a snapshot of a document that has since changed, and the project's own rule forbids updating it.** write-spec: "Review records — `docs/designs/reviews/**` — are history: never edit them to match a later decision." A site page is the one artefact a reader assumes is current. Publishing 107 never-updated pages into a searchable site creates exactly the failure this IA exists to prevent: a reader finds "BLOCKER: authorization bypass" in site search and has no way to learn it was answered eight amendments ago.
3. **A review is step-by-step, which the disclosure posture forbids.** §8.3 permits naming a class and linking a section. A review contains reproduction, sequence and the patch that would fix it. Publishing all 107 as site pages would be the most detailed unfixed-defect catalogue this project could produce, and would sit one search box away from the roadmap that carefully does not produce it.
4. **The ledger carries the claim the reviews are evidence for, and carries it better.** The claim is "this project reviews adversarially, repeatedly, and across model families" — 107 rows with verdicts and reviewer families says that in one screen. The full texts say it in 21,458 lines that nobody reads.
5. **The ledger is checkable and the corpus is not.** A test can assert that the ledger's row count equals the file count in `docs/designs/reviews/`. Nothing can assert that 107 published pages are still accurate, because they are not meant to be.

`/contributing/review`, derived from `docs/agent-protocol.md`, publishes what "independent" means — the reviewer-family gradient, findings travelling as committed files, and the rule that two reviewers never reconcile. Without that page the ledger's reviewer-family column is unreadable, which is why `docs/agent-protocol.md` is on the site at all despite being an internal working document.

### 9.2 `docs/architecture.html` is never published, and should be deleted or marked

This is the most dangerous artefact in the tree for a public site. It is the rendered companion to `docs/architecture.md`, it was partially corrected and its header and footer were missed, and it still carries verbatim:

- "27/27 designs approved · 26 ADRs" — the exact false sentence `CLAUDE.md` opens by recording;
- "Every component below is designed, adversarially critiqued, and approved";
- "Every claim in this document is backed by an ADR and a critique-passed design."

Partial correction is worse than uniform staleness, because the file looks current. It must not be published, must not be linked, and should be either regenerated from `docs/architecture.md` or deleted. That change is outside this document's one file; §11.1 records it.

### 9.3 `docs/architecture.md` is published only inside the archive

`docs/architecture.md` is canonical and stays canonical in the repository. It is **not** the site's architecture page and does not appear in the main navigation. It is at `/archive/architecture`, verbatim, under a standing banner.

The reason: its honest warnings are per-*section* ("Designed, not shipped" at the head of §02, §03, §12, §16, §17), and a reader skimming a table two screens below the warning takes the table and leaves the warning. §4's claim classes are per-*claim*, which is the granularity the failure demands. The concepts section (§6) is the site's architecture story, and it is classed sentence by sentence.

Three specific things in it must not be lifted onto any other page, because they escape their own section's fence:

- §01's doctrine rules 5 and 6 — "minikube, k3d, kind, k3s included" (CI runs k3d and kind only) and "each tier independently removable" (the chart hard-fails on `tier: plus`) — carry no fence at all;
- §07's bullets asserting shipped alert rules, OTel spans and receipts to JetStream, which the section's own preamble retracts but the bullets still state in the present tense;
- §16's competitor table — see below.

### 9.4 The competitor comparison table is not published

`docs/architecture.md` §16's table has six capability rows. Its own header says the assayd column "states the target, not what ships", and only the gateway-path row is true today. A comparison table on which five of six rows would render `designed` or `not-built` is not a comparison; it is the original failure with a competitor's name beside it.

It also still contains the retracted first-mover claim — "nobody ships it as a k8s primitive — 12–18-month window" — which ADR-0006 recorded as wrong, ADR-0030 withdrew, and the same file retracts three sections later in §19.

A comparison page can exist when its rows are `measured`. Until then there is none, and the landing page's positioning is the thesis sentence and the honest list, not a grid.

### 9.5 The rest

| Not published | Why |
|---|---|
| `CLAUDE.md`, `AGENTS.md` | Contributor-facing working rules. `/status` and `/roadmap` carry what a user needs from them; republishing the paragraph creates the copy §5.5 is trying to reduce. |
| `docs/HANDOFF.md` | Its own first line marks it a dated record, superseded and deliberately not rewritten. That is the exact class of document a site page must never be, because a site page is the one artefact a reader assumes is current. |
| `docs/research/**` (33 notes) as a section | Each note is dated evidence about a third party, several past their re-verify dates. A stale note on our own site reads as our current position on someone else's product. Notes are linked individually from the concept or reference page that cites one. |
| `docs/requirements.md` as a page | Of 30 FR/NFR identifiers, **zero `FR-` identifiers appear in any code or test file**, and only NFR-8 and NFR-3 appear at all. Publishing a requirements list where the overwhelming majority is `not-built` adds nothing `/roadmap` does not say better. Individual requirements are cited from claims where they are the holder. |
| The "≤8 core pods" figure as a landing-page footprint claim | It is CI-enforced *as a budget* (`TestCorePodBudget`, currently measuring 2 of 8) but it is a **target** as a description: the chart contains exactly one `Deployment`. It may appear on `/concepts` or `/roadmap` as `designed`; it may not appear on `/` as a footprint. |
| Six of the eight diagram plates, above the fold anywhere | Rule 6 of §4.2. |

## 10. First-ship cut list

Ordered. Each item is shippable on its own, and each is a prerequisite of the one below it unless stated.

| # | Ship | Why here |
|---|---|---|
| 0 | **`make docs` added to `.github/workflows/ci.yml`** | Not a page. Every "CI" in §5.4 is false until this lands, including for the gate that already exists. It is one line and it is the difference between a mechanism and an intention. |
| 1 | `web/claims.yaml` + `test/docs/claims_test.go` (gates A–G) | Infrastructure, not a page. Every badge on every page depends on it; shipping a page first means shipping unclassed prose and retrofitting, which is how the copies start. |
| 2 | `assayd.io/` and `assayd.io/claims` | The landing page is the highest-reach surface. `/claims` ships with it, because a badge with no definition is decoration. |
| 3 | `assayd.dev/status` | Generated from item 1. The landing page's honest list has to resolve to something. |
| 4 | `assayd.dev/guides/install`, `/guides/api-keys`, `/guides/troubleshooting` | The first thing a reader can actually *do*. Derived from `docs/install.md`, which is already written to this standard. |
| 5 | `assayd.dev/agent-contract` | The second thing a reader can do. Its existing Sources table means it needs the least rewriting of any page here. |
| 6 | `assayd.io/roadmap` | Needs items 1 and 3. Its §8.3 gap list is the reason the disclosure posture was decided, so it should not wait. |
| 7 | **Gate H — `README.md` generated from the manifest** | Not a page. Deliberately after the roadmap, because it edits a file outside the site and should land once the manifest's shape has survived six pages of real use. |
| 8 | `assayd.dev/supply-chain` | Standalone; a reader verifying signatures needs no other page. |
| 9 | `assayd.io/concepts/*` — eight pages | Deferred behind the guides on purpose: a concept page is only worth reading once the reader has hit the thing it explains. Order within: `governance-at-the-gateway`, `revisions-and-cards`, `create-lock-adopt`, `attribution`, `the-auth-transaction`, `reserved-writes`, `the-revision-service`, `standards-only`. |
| 10 | `assayd.dev/reference/*` | Blocked on the generated-reference agent. Ships as a unit when it lands; `/reference/conditions` can ship first, because `TestNoConditionTypeIsALiteral` already closes that vocabulary. |
| 11 | `assayd.dev/archive/*` and the review ledger | High value, zero urgency — the files are already on GitHub. |
| 12 | `assayd.io/releases`, `/security`, `/about`; `assayd.dev/` home, remaining guides, `/contributing*` | Completeness. |

**Deferred, with the reason:**

| Deferred | Until |
|---|---|
| Site search | There is more than one page worth searching and `/archive` is published. Search over a site where `/archive` and `/status` return adjacent results needs a scoping decision this document has not made. |
| Versioned documentation (more than one version of the guides) | A second supported version exists. `/status` is versioned from item 3; the guides are not, and pin one tested version. |
| A blog, case studies, a newsletter | There is something measured to write about that `/releases` does not cover. |
| A comparison page | Its rows are `measured` (§9.4). |
| Full-text publication of `docs/designs/reviews/**` | Never, on the argument in §9.1. |
| New diagrams for `the-auth-transaction` and `create-lock-adopt` | Item 9. They are owed and they do not block the concept pages' text. |

## 11. Claims in the existing corpus that are false or stale

Found while verifying this document. Each is stated as *what the text says* / *what is true*. These are inputs to whoever owns each file; this change corrects none of them.

### 11.1 Blocking for the website

| # | Where | Says | Actually |
|---|---|---|---|
| 1 | `docs/architecture.html:356`, `:359`, `:1877` | "Every component below is designed, adversarially critiqued, and approved"; "27/27 designs approved · 26 ADRs"; "Every claim in this document is backed by an ADR and a critique-passed design" | The exact sentence `CLAUDE.md` opens by recording as false. Design 02 is not approved; 03 and 16 approve only their first slices; there are 34 ADRs. The file was partially corrected and its header and footer were missed, so it reads as current. **Must not be published, and should be regenerated or deleted.** |
| 2 | `docs/architecture.md:433` (§16) | "nobody ships it as a k8s primitive — 12–18-month window" | ADR-0006 recorded that window as wrong and eval-gating as "Not a differentiator"; ADR-0030 withdrew the first-mover framing; `architecture.md:479` retracts it in the same file. §16 was never edited. |
| 3 | `docs/decisions/0026-p5-enterprise.md:6` | "the design phase is complete — 27/27 approved, every one through independent critique. Implementation may begin." | Contradicted by ADR-0030 and by `docs/designs/README.md`'s own banner. ADR-0026 carries no supersession note. |

### 11.2 Stale counts and cross-references

| # | Where | Says | Actually |
|---|---|---|---|
| 4 | `docs/architecture.md:5`, `:488` | "32 ADRs"; "`docs/research/` (12 dated notes)" | 34 ADRs (0001–0034); 33 research notes plus a `research/paper/` directory. The correction at `:5` is itself stale. |
| 5 | `docs/architecture.md:492–507` | An ADR index ending at 0027 | ADRs 0028–0034 have no row, including ADR-0028 (which supersedes the 0020 the table still lists as live), ADR-0030 (the scope reset) and ADR-0034 (the auth decisions). A reader using the table as the decision index misses every decision that narrowed the scope. |
| 6 | `docs/architecture.md:533` | "design 02 carries eleven (A1–A11)" | Design 02 consolidated 61 amendments and runs to A76. |
| 7 | `docs/architecture.md:88` | `# API group TBD at rename` | The rename is done (ADR-0001 Amendment 1, recorded at `:3` of the same file); `assayd.dev` ships in `api/v1alpha1/groupversion_info.go`. |
| 8 | `docs/architecture.md:481` vs `:525` | R1 is an open item / R1 is CLOSED 2026-08-27 | Both, in one document. |
| 9 | `docs/architecture.md:527`, `:529` | The agentgateway floor is v1.4.1 and conformance runs "against a real v1.4.1 gateway" | `hack/conformance-cluster.sh` runs both: 1.4.1 in phase 1 and 1.5.0 in phase 2. `install.md`, `agent-contract.md`, `CLAUDE.md` and `hack/e2e.sh` all use 1.5.0. §527 never learned that ADR-0030 made the 1.5.0 re-run a precondition. |

### 11.3 Present-tense claims that escape their section's fence

| # | Where | Says | Actually |
|---|---|---|---|
| 10 | `docs/architecture.md:44` (doctrine rule 5) | "minikube, k3d, kind, k3s included; local distros are a CI target, not a courtesy" | `.github/workflows/ci.yml` runs a matrix of k3d and kind only. minikube and k3s are CI targets nowhere in the repo. Doctrine §01 carries no "designed, not shipped" fence. |
| 11 | `docs/architecture.md:45` (doctrine rule 6) | "Tiered install… Each tier independently removable" | `charts/assayd/templates/_helpers.tpl` hard-`fail`s on `tier: plus` — "tier: plus is not implemented yet" — and `TestUnimplementedTierIsRefusedNotIgnored` pins the refusal. §05 discloses this; the doctrine rule does not. |
| 12 | `docs/architecture.md:235`, `:236`, `:238` (§07 bullets) | Receipts to JetStream; OTel spans; "alert rules shipped in the chart, each fixture-tested in CI" | None exists. The section's preamble retracts them; the bullets still state them in the present tense, so any extraction that lifts bullets without their preamble republishes them. |
| 13 | `docs/architecture.md:447` (§17) | "≈ 8 pods" | The ledger sums to 8 **or 9** at its own upper bound (agentgateway is listed as 1–2). The shipped-side statement at `:445` is correct and the CI budget claim at `:451` is true. |

### 11.4 Requirements the code does not hold

| # | Where | Says | Actually |
|---|---|---|---|
| 14 | `docs/requirements.md:3` | "Each requirement is testable; NFRs are release gates." | `.github/workflows/release.yml` contains no test job. Nothing gates a release on any NFR or on CI passing. Of 30 identifiers, zero `FR-` ids appear in any code or test file; only NFR-8 and NFR-3 appear at all. |
| 15 | `docs/requirements.md:8` (NFR-2) | "Postgres and NATS JetStream are the only stateful dependencies, **ever**." | The test that enforces it (`test/chart/chart_test.go`) allows three: `postgres`, `nats` and `openobserve`. "Ever" is not what the code enforces. |
| 16 | `docs/requirements.md:9` (NFR-3) | "CI runs full e2e on k3d + kind on every merge." | The kind lane runs a reduced suite: `hack/e2e.sh` sets `ASSAYD_E2E_RESPONDER_SKIP` for any non-k3d distro and gates the whole gateway setup behind `DISTRO = k3d`. `docs/install.md:18` states this correctly; `requirements.md` contradicts it. |
| 17 | `docs/requirements.md:13` (NFR-7) | "`core` and `plus` independently installable/removable" | `plus` is not installable; see #11. |

### 11.5 Release and supply chain

| # | Where | Says | Actually |
|---|---|---|---|
| 18 | `docs/supply-chain.md:23` | "The current release is v0.4.0." | `git tag` shows **v0.4.1**, cut 2026-09-16, the day after the v0.4.0 verification date the page cites. The page does not mention v0.4.1 at all. `docs/install.md:15`, `:148` and `:444` pin `0.4.0` for the same reason. Either the page is stale or the tag published nothing — §12 carries the question. |
| 19 | `docs/supply-chain.md:12` | "published only by `.github/workflows/release.yml`, on a `v*` tag" | The workflow also triggers on `workflow_dispatch` with a free-text tag input, so a publish can happen with no tag existing. |
| 20 | `docs/supply-chain.md:9` (table) | The operator image row lists "SLSA provenance attached" unconditionally | The provenance step is guarded by `if: github.event.repository.visibility == 'public'` and warns instead otherwise. The prose at `:55` discloses this; the table row does not. |
| 21 | `docs/supply-chain.md:12`, `:21`, `:63` | The workflow "verifies its own signatures from outside before it finishes", including provenance | It runs `cosign verify` and `verify-attestation --type spdxjson` for the image and `cosign verify` for the chart. It **never** runs `verify-attestation --type slsaprovenance1` or `gh attestation verify`. Those were done by hand on 2026-09-15 and no CI job reproduces them. |

### 11.6 The gap that shaped §5

| # | Where | Says | Actually |
|---|---|---|---|
| 22 | `.github/workflows/ci.yml` | — | CI runs `verify`, `vet`, `unit`, `race`, `envtest`, `chart`, `chart-conform` and `e2e` as separate steps. It never runs `make test`, and so never runs `make docs` or `make conformance`. **`test/docs/superseded_test.go` — the only mechanical gate against a withdrawn guarantee reappearing — does not run in CI.** A pull request that reintroduces one of its nine banned claims passes. This is not a false statement anywhere; it is an absent gate, and it is why §5.4's enforcement column reads the way it does and why cut-list item 0 exists. |

### 11.7 Documented honestly, listed so nobody "fixes" them

Not defects. Recorded because they look like defects and will be reported as such.

- `charts/assayd/Chart.yaml` says `version: 0.1.0` and `charts/assayd/values.yaml` pins `tag: "0.1.0"`, against a newest tag of v0.4.1. `.github/workflows/release.yml` rewrites both at release time, and `docs/install.md:444` states plainly that the checkout's chart names a version "which is not published".
- `docs/agent-contract.md` scopes every guarantee to one fixture — "it is the only agent any test here runs, so it is the only one this contract has been measured against". **That sentence must survive the rewrite to `assayd.dev/agent-contract`.** It is the single most load-bearing caveat in the corpus and it is exactly the kind of sentence a documentation restyle deletes.
- `charts/assayd/templates/NOTES.txt` is the most accurate governance statement in the repository, on both branches of `gateway.enabled`. The landing page inherits its sentence — "YOUR AGENTS ARE REACHABLE, AUTHENTICATED BY API KEY, AND OTHERWISE UNGOVERNED" — rather than composing a new one.

## 12. Open items

Carried forward rather than hedged in the prose above.

| # | Item | Why it is open |
|---|---|---|
| O1 | **The brief names a gap "D6"; the repository carries no such label.** `grep -n '\bD6\b' docs/` returns nothing. Design 03's D-list runs D1–D4 and design 07's D1–D3, and neither includes this defect. The defect itself is real and is described at design 03 §3.4.4 and §8.1. Either the label comes from a conversation not in the repository, or it needs assigning. §8.3 states the defect and flags the label. |
| O2 | Whether `v0.4.1` published a chart and image, or is a tag with no release. `docs/supply-chain.md` and `docs/install.md` both stop at `0.4.0`. `/releases` and `/status` cannot be generated from tags until this is settled. |
| O3 | **The strongest evidence this project produces has no field in the claim record.** "Three mutations kill it" (`TestTheGatewayIsTheOnlyWayIn`) and "every case was run with a mutation of its own, in five batches" (design 03 §8.1) are stronger than "a test exists", and the record in §4.3 cannot express either. A `mutations:` field is the obvious answer and nothing would check it, which is the argument against adding it. Unresolved. |
| O4 | **`/reference/reasons` is not specified, because it cannot be generated correctly.** Condition *types* are 38 constants closed by `designConditions()` and `TestNoConditionTypeIsALiteral`. Condition *reasons* are 30 constants in `internal/controller/` plus **26 bare string literals** at their call sites, with no constant and no closure test. A reasons reference would be right about 30 and silently absent on 26. The fix is a closure test for reasons mirroring the one for types; that is a code change this document does not make. Until then `/reference/conditions` documents types and phases only, and says so. |
| O5 | Where the claim manifest lives. `web/claims.yaml` is proposed because the site consumes it, but `web/**` is another agent's territory and a manifest under it is a site asset rather than a repository one. `docs/web/claims.yaml` is the alternative. Not decided. |
| O6 | Whether `/archive/designs/<nn>` should render 27 designs whose bodies contradict their own Status lines in places. Design 03's body is 2,000+ lines specifying an unapproved system; publishing it verbatim is honest and is also 2,000 lines of `designed` prose with one approval banner at the top. A per-section banner was considered and needs the design's own section structure, which varies. Not decided. |
| O7 | Whether the landing page may state the thesis — "what was evaluated is what runs" — as an unclassed sentence. It is a statement of intent, not of behaviour, and §4.2 rule 1's boundary does not cleanly settle it. The conservative reading is that it is a claim and is `measured` only for revision identity, not for evaluation, since the eval gate is `designed`. |
| O8 | Gate B's implementation. Detecting that a named test "reaches a `t.Skip`" requires following helper calls — `responderImage`, `requireGateway` and `requireCluster` are where the skips actually live, not the test bodies. A one-level call-graph walk covers today's cases; nothing guarantees it covers tomorrow's. |
| O9 | **The prior-art survey behind §2.1 is owed.** A survey of how comparable projects' documentation sites are currently structured — Gateway API and its implementations, cert-manager, Crossplane, Flux, Knative, Sigstore, and the projects that already split a marketing domain from a docs domain — was dispatched and had not returned when this document was written. §2.1 is argued from this repository and from the Kubernetes maturity-label convention instead, and says so. The survey could change the navigation labels; the guides/concepts/reference split is independently supported by the corpus and would survive it. |

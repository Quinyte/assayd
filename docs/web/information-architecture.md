# The assayd website — information architecture

- **Status**: **proposal, not approved.** No critique of this document has been run. It specifies pages, not prose, and no page it names exists. Where it proposes a file outside `docs/web/`, that file is owed by whoever implements the section, and this document does not create it.
- **Date**: 2026-09-22 · **revised 2026-09-23** against `main` `6ae20e1`, PR #60 (`3bc7e6b`) and PR #59 (`939f964`), neither merged · **Scope**: `assayd.io` and `assayd.dev`
- **What the revision changed**: §11 was a list of 22 defects; PR #60 has addressed all 22, so §11 is now a reconciliation — fixed, changed shape, or still live — because a fixed-defect list published as a record reads as a to-do list. §5.5 replaces this document's own first finding, which is **void**: `make docs` was not in CI at `22b65c2` where this branch started and was added in `80b63b5` the same day. What replaces it is stronger and is PR #60's: the gate *ran*, and had never scanned either of the two documents that mattered. §12's O1, O2 and O4 are closed.
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

### 2.1 Prior art

Surveyed live on 2026-09-22: Gateway API, Envoy Gateway, Istio, Cilium, kgateway, agentgateway, cert-manager, Crossplane, Knative, KEDA, Flux, Argo CD, Tekton, Sigstore, SLSA, in-toto, and seven projects that already split a marketing domain from a docs domain (Traefik, Grafana, Vault, Temporal, Dagger, Pulumi, with Prometheus as the single-domain contrast).

**The design rule the survey serves: a reader arriving cold should have to learn exactly one unusual thing about this site, and that one thing is the claim class.** Everything else is conventional, because novelty spent on navigation is novelty not available for the idea that needs it.

**The conventional docs navigation is nine slots**, and essentially every site surveyed is a subset or a renaming of it: *Overview · Getting Started · Concepts · Guides/Tasks · Reference · Operations · Troubleshooting/FAQ · Releases · Contributing*. §3.2 is that set with `Operations` folded into the guides, because assayd has no day-2 surface yet, and with `Feature status`, `Supply chain` and `Archive` added.

Three conventions are adopted:

- **`Concepts` as a top-level section, spelled that way.** Top-level in Flux (2nd item), Argo CD (3rd), KEDA (2nd), Gateway API, Tekton, Istio, Envoy Gateway and Prometheus. Nested under `Reference` only in cert-manager; absent entirely in Crossplane and Cilium, where the explanation is visibly scattered across `Component Overview`, `eBPF Datapath` and `Internals`.
- **An inline per-claim maturity label.** Kubernetes has trained every reader of a Kubernetes-adjacent project to accept a small stage marker beside a heading and not read it as clutter. §4.4 specifies the rendering from the strongest examples found.
- **One canonical page answering "is this actually done"**, rather than leaving a reader to reconstruct it from release notes — Istio's `Feature Stages`, Argo CD's `Feature Maturity`, kgateway's `Feature Maturity`, OpenTelemetry's per-signal matrix. §7 is that page.

**Not adopted:** the comparison table (§9.4), and the "Why *project*?" landing pattern that asserts capabilities in the present tense. Both are near-universal and both are the shape this project has already been burned by.

#### Evidence against this document's own frame, recorded rather than removed

Two survey findings cut against the decided `.io`/`.dev` split. The split is decided and is not relitigated here; the cost is named so that it is arguable rather than discovered.

- **Every one of the seven two-domain splits puts its concept *pages* on the docs side.** Traefik, Grafana, Vault, Temporal, Dagger and Pulumi all do, and Prometheus — which has no marketing site — keeps `Concepts` top-level in docs. What marketing gets in all seven is *one linear narrative* "how it works" page using the same nouns. Temporal is the clean case: `temporal.io/how-it-works` is a nine-step narrative, and `docs.temporal.io/…/Encyclopedia` is the addressable set. **Nobody duplicates the concept pages themselves.** The reconciliation this document adopts, which honours the decided frame: the eight concept pages stay on `.io` as decided, `.dev` links to them and never restates them (§2), and no second narrative page is created — the landing page *is* the narrative. The cost is that a reader in the middle of `/guides/install` who needs a concept crosses a domain boundary. §12, O9.
- **The supported-versions and end-of-life table belongs on the docs side**, on the same evidence: cert-manager, Grafana, Flux and Istio all keep it in docs because it is operator-facing, and Envoy Gateway puts `(EOL)` labels inside its docs version dropdown. `/releases` stays on `.io` as decided and carries the announcement view — what changed, and which claim classes moved. The **supported-versions table is a `.dev` reference page**, `/reference/tested-versions`, which already exists in §3.2 for the same reason. §12, O10.

`/roadmap` on `.io` is confirmed by the survey rather than contradicted: Prometheus is the only project of the sixteen that keeps a real roadmap in its docs, and it has no marketing site to keep it out of. The design and ADR archive on `.dev` is likewise confirmed — Gateway API carries its GEPs at `/geps/`, inside the docs site, linked from the header as `Enhancements`.

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
| `/feature-status` | Is the thing I need actually enforced, at the version I run? |
| `/reference/` | Which reference surface do I want? |
| `/reference/crd-agent` | Every field of the Agent CRD, its default, and what refuses it |
| `/reference/helm-values` | Every chart value and its default |
| `/reference/conditions` | Every condition the operator writes, every reason it can set, and whether traffic is withdrawn |
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

**Reasons are on `/reference/conditions`, not on a page of their own — and an earlier revision of this document said they could not be documented at all.** That was right about the code and wrong about the remedy: reasons were 30 constants plus 26 bare string literals with nothing closing the set. **PR #59 closes it** by extracting the vocabulary with `go/packages` and `go/types` rather than reading the constants — 39 condition types, 64 reason strings and their call sites — and by holding a hand-written annotation file to that set, so a reason with no entry or an entry with no reason fails `make reference` and so fails CI. §12, O4 carries what the page still cannot promise.

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
  pages: [io/, io/concepts/governance-at-the-gateway, dev/feature-status]
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
  pages: [io/roadmap, dev/feature-status]
```

### 4.4 How a claim class renders, and the failure mode that must not be repeated

**The dominant failure in the surveyed projects is definitions without application: a careful definitions page, and then no signal on the pages the reader actually lands on.** It is worth naming three cases, because this document could produce all three.

- **Istio** has the best definitions artifact found anywhere — a 15-row × 4-stage table specifying what each stage promises about security, performance, support, deprecation, testing and upgradeability. It then signals an Alpha feature with **a bare asterisk appended to the sidebar link**, whose meaning lives only in a `title` tooltip. The Alpha feature's own page body carries nothing.
- **kgateway** has a good three-table maturity page and **zero** inline signal: its Alpha feature's page is indistinguishable from a GA one, and the maturity page is not even in the site's `/llms.txt`.
- **Knative Serving** defines its lifecycle stages carefully and then lists all 24 feature flags **with no stage on any of them**. Knative *Eventing*, on the same site, does it right — one table with a compound `Maturity` cell reading `Beta, disabled by default`.

So `/claims` alone is not the system. Gate F of §5.2 — every manifest record must be referenced by at least one page — is the gate that stops this project shipping Istio's failure, and it is why the gate exists.

**The rendering**, drawn from the four strongest examples:

| Borrowed from | What is taken |
|---|---|
| **SLSA** | One templated line under the heading — literally `Status: Approved` — where the **status word hyperlinks to the definitions page**. It is the cheapest mechanism in the survey and the one that makes a class impossible to render without also rendering its definition. Every assayd badge links to `/claims`. |
| **Gateway API** | A collapsible, colour-bordered box whose summary *is* the claim and whose body carries the evidence, placed **beside the sub-feature, mid-page** — not only under the H1. `/reference/api-types/httproute/` carries four of them. This matters here because the concept and reference pages are exactly where a deep-linked reader arrives. |
| **Elastic `applies_to`** | Annotation at three levels — front matter, the line after a heading, and **inline at the start of a paragraph or list item** — with an explicit rule against annotating page titles. This is the only surveyed system that is genuinely per-*claim* rather than per-*feature*, and it is the model for a document whose unit is the sentence. |
| **Argo CD** | Maturity keyed to the **exact CRD property path, ConfigMap key and env var** (`Skip Application Reconcile \| metadata.annotations[argocd.argoproj.io/skip-reconcile] \| Alpha`), with the inline admonition and the central table **hyperlinking to each other in both directions**. That two-way link is what keeps the two from drifting, and it maps onto assayd directly: `spec.budget` → refused at admission; `auth: oauth` → `PolicyCompileFailed`; `GovernanceSkipped=AuthVerifiedOnOneReplica` → measured on one replica only. |

**Cilium's mechanism is adopted as a supplement and never as the only signal.** Baking the stage into the H1 (`Standalone DNS Proxy (alpha)`) propagates it free into the sidebar, breadcrumb, search results and browser tab, which nothing else does. But Cilium's casing drifts between `(beta)` and `(Beta)` across its eight such pages, and its accompanying note links nowhere because the project has no definitions page. Taken as a supplement to the SLSA line, it is free reach; taken alone, it is the Istio failure with better propagation.

**One technique is adopted that no other project in the survey has: making the badge's absence a build failure.** Crossplane's Hugo partial reads a page's front-matter `state` and its required `alphaVersion`/`betaVersion`, and **`errorf`s — failing the build — if a page declares a state without a version.** Given this project's history, a convention people are asked to remember is the wrong shape; §5.2's gates are the assayd equivalent, and gate F is its direct analogue.

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

- *Per-page front matter, no manifest.* Rejected. The same claim appears on the landing page, `/feature-status` and `/roadmap`; three copies is the defect being fixed, and it is the shape of the original failure — a summary restating what a design held.
- *No manifest; derive the class from the test suite alone.* This is the simpler option and it is recorded as rejected, because Go offers no stable place to hang a claim identifier that survives a refactor, and because `designed` and `not-built` claims have no test to hang one on. Half the system would need a manifest anyway, so the hybrid is the smaller total surface, not the larger one.
- *A release checklist.* Rejected on the standard the coordinator set and this project already holds: a mechanism that requires a human to remember something is not a mechanism. It is named here because it is what the project does **today** for `README.md`'s two lists and `CLAUDE.md`'s paragraph, and that is precisely the thing that went false once.
- *A lint over page prose for assertive verbs.* Rejected. `test/docs/superseded_test.go` works because each rule bans one specific known-false phrase and permits its retraction; a rule that bans a grammatical category produces false positives at a rate that trains people to add exemptions. **What is adopted instead** is extending that existing test's scan root to cover the site's content files, so that the nine withdrawn guarantees it already bans cannot reappear on the website.

**Consequences.** Adding a page, moving a claim between classes and generating `/feature-status`, `/roadmap` and the landing page from one place all become cheap. Two things become harder. The site can no longer be built outside a checkout, because the manifest and the tests must be read together — intended, since the site is built at a tag. And a claim whose evidence is a *mutation result* rather than a test — "three mutations kill it", which is the strongest evidence this repository produces — has no field in the record; §10 carries it. Reversing is cheap: delete the manifest and the test, and pages render an unclassified badge.

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
| H | `TestEveryClaimedPageAndHolderExists` — every page path the manifest references, and every `evidence.file`/`evidence.design` holder, is asserted to exist **by path**, and the count of scanned pages is asserted against the count on disk. | **A file dropping out of the gate's own corpus.** §5.5 is why this gate exists and why it is not optional. |

### 5.3 What these gates do not catch — stated, not dressed up

- **That the named test measures the claim's sentence.** Nothing in CI reads English. A test can be renamed to keep gate A green while its body is gutted. The project's answer is mutation-checking (rule 1), which is a human loop, and it stays one.
- **That a page contains no unclassed claim.** Rejected as a lint above. A reviewer catches it or nobody does.
- **That a class is lowered when code is deleted.** Gates fire on missing *evidence*, not on missing *subject*.
- **That rule 4's present-indicative ban is honoured.** A reviewer catches it or nobody does.

### 5.4 Per-page source of truth, mode, and whether a gate protects it

**The enforcement column below is real, not conditional.** `make docs` and `make conformance` both run in `.github/workflows/ci.yml` as their own steps, added in `80b63b5` — whose comment records that each was "Also in `make test` and, like conformance, in no CI job before." An earlier revision of this document named that absence as its first cut-list item; it was true at `22b65c2`, where this branch started, and was fixed the same day. **The stronger finding is §5.5's, and it is not that a gate was missing.**

#### `assayd.io`

| Page | Holder(s) | Mode | Protected by |
|---|---|---|---|
| `/` | `web/claims.yaml`; the governance sentence from `charts/assayd/templates/NOTES.txt` (both branches of `gateway.enabled`) | generated from the manifest, hand-written frame | CI (A–G) for every claim; the frame, someone notices |
| `/claims` | this document §4 | hand-written | someone notices |
| `/concepts/*` | `web/claims.yaml` for each assertion; the design section each concept explains | derived-and-reviewed | CI (A–G) per claim; the explanation, someone notices |
| `/roadmap` | `docs/decisions/0030-*.md`; each design's own `- **Status**:` bullet; `web/claims.yaml` | generated (status table and gap list), hand-written frame | CI (D, E) |
| `/releases` | git tags; `docs/supply-chain.md`'s published-artifact table and its per-version verification dates; `.github/workflows/release.yml` | derived-and-reviewed | someone notices. **Not generated from tags alone**: `supply-chain.md` now records `v0.4.1` as tagged and never verified from outside, so a tag is not evidence of a published artifact (§11.6.3). A version with no recorded check renders as unverified, never as a release. There is also no CHANGELOG to generate notes from (§12). |
| `/security` | `SECURITY.md`; the `security`-tagged records in `web/claims.yaml` | derived-and-reviewed | CI (A–G) for the not-enforced list; the reporting policy, someone notices |
| `/about` | `LICENSE`, `MAINTAINERS.md`, `GOVERNANCE.md`, `SECURITY.md` §Regulatory | derived-and-reviewed | someone notices |

#### `assayd.dev`

| Page | Holder(s) | Mode | Protected by |
|---|---|---|---|
| `/` | — | hand-written | someone notices |
| `/guides/install` | `docs/install.md` §§1–3, 5 | derived-and-reviewed | someone notices; the version pins it quotes are held by `hack/e2e.sh` and are bound by `/reference/tested-versions` |
| `/guides/api-keys` | `docs/install.md` §3; `charts/assayd/values.yaml` `admission.apiKeyWriters` | derived-and-reviewed | CI via `/reference/helm-values` for the value names |
| `/guides/a2a-task` | `docs/install.md` §5; `docs/agent-contract.md` "The A2A binding" | derived-and-reviewed | someone notices |
| `/guides/mcp-tool` | `docs/install.md` §6; `docs/agent-contract.md` "Calling an MCP tool" | derived-and-reviewed | someone notices |
| `/guides/upgrading` | `docs/install.md` §4 | derived-and-reviewed | someone notices |
| `/guides/troubleshooting` | `docs/install.md` "When it does not work" | derived-and-reviewed | someone notices |
| `/agent-contract` | `docs/agent-contract.md` | derived-and-reviewed | its own "Sources" table already names the file or test behind every row — the closest thing in the corpus to what §5.1 proposes, and the model for the manifest |
| `/supply-chain` | `docs/supply-chain.md`; `.github/workflows/release.yml` | derived-and-reviewed | someone notices |
| `/feature-status` | `web/claims.yaml` at each release tag | generated | CI (A–G) |
| `/reference/crd-agent` | `api/v1alpha1/agent_types.go` → `config/crd/assayd.dev_agents.yaml`, via `cmd/refgen` | generated | **CI** — `make verify` regenerates `docs/reference/` and fails if what is committed differs (PR #59) |
| *(note to the reference owner, not a decision of this document)* | The survey checked the two usual generators. **`ahmetb/gen-crd-api-reference-docs` has self-deprecated** — its README says "not super actively maintained… consider crd-ref-docs" and its last release was 2019, though cert-manager, Flux and Knative all still use it. **`elastic/crd-ref-docs` is active** (v0.3.0, 2026-02) and is what **Gateway API and Cluster API** both use. Separately: **agentgateway — the gateway assayd pins at 1.5.0 — renders its own CRD docs with `kubespec-render`**, and is indexed on `kubespec.dev` beside Gateway API, cert-manager, Cilium and Argo CD. If assayd's Agent CRD should sit next to agentgateway's, that is the shape to match. | — | — |
| `/reference/helm-values` | `charts/assayd/values.yaml`, via `cmd/refgen` | generated | **CI** — same `make verify` gate |
| `/reference/conditions` | `api/v1alpha1` and `internal/controller`, read with `go/types` by `cmd/refgen`; the three per-reason annotations that are not in the code live in `internal/refgen/reasons.yaml` | generated | **CI** — `make verify`, plus `TestNoConditionTypeIsALiteral`. The join pins the *set* of reasons; it does not prove the annotations are still true of the code, and the page says so |
| `/reference/flags` | `cmd/` flag registrations | generated | **owed** — PR #59 generates three pages, not six; this is not one of them |
| `/reference/labels` | `internal/compiler` and `internal/controller/runnamespace.go` label constants | generated | **owed** — not generated by PR #59 |
| `/reference/tested-versions` | `hack/e2e.sh` (`GWAPI_VERSION`, `AGW_VERSION`); `charts/assayd/Chart.yaml` `kubeVersion` | generated | `test/conformance/versions_test.go` already pins that the slice cases run the e2e's agentgateway release |
| `/archive/designs/<nn>` | `docs/designs/<nn>-*.md` | generated, verbatim | CI (E) binds the Status line wherever a claim cites it |
| `/archive/decisions/<nnnn>` | `docs/decisions/<nnnn>-*.md` | generated, verbatim | someone notices |
| `/archive/architecture` | `docs/architecture.md` | generated, verbatim, with a standing banner | someone notices |
| `/archive/reviews` | a directory listing of `docs/designs/reviews/**` | generated (ledger only — §9.1) | a count assertion; see §9.1 |
| `/contributing` | `CONTRIBUTING.md`, `GOVERNANCE.md`, `CODE_OF_CONDUCT.md` | derived-and-reviewed | someone notices |
| `/contributing/review` | `docs/agent-protocol.md` | derived-and-reviewed | someone notices |

**A page that restates a claim held elsewhere points at the holder.** Concretely, and these are the five holders that matter:

- `README.md`'s *What is true today* / *What is NOT true today* — restated by `/` and `/feature-status`.
- The *What remains untrue* paragraph in `AGENTS.md` (and its copy in `CLAUDE.md`) — restated by `/feature-status` and `/roadmap`.
- `docs/designs/README.md`'s status table — **not** the holder for `/roadmap`. Each design's own `- **Status**:` bullet is, because `README.md`, `AGENTS.md` and the table itself all say a design's own Status line beats any summary, and the table is a summary. The table becomes a derived view like the site is.
- `api/v1alpha1` and `charts/assayd/values.yaml` — restated by `/reference/crd-agent` and `/reference/helm-values`, generated.
- git tags plus `docs/supply-chain.md` — restated by `/releases`.

### 5.5 The failure this section is actually designed against: a gate that ran and saw nothing

The lesson is not "a gate was missing". It is worse than that, and it is the reason gate H exists.

`test/docs/superseded_test.go` has run on every `make test` since it was written, and in CI since `80b63b5`. It passed. **It had never once scanned either of the two documents that mattered most**, and PR #60 found both holes:

- **`docs/architecture.md` — the file `AGENTS.md` calls canonical — was exempt from the day the gate was written.** `isFrozen` reads the first 800 bytes for a document announcing its own supersession, so that a frozen ADR is not scanned. The file's Status line reads "superseded in part — read §18 before relying on this document", and the bare word `superseded` sits at byte 608, inside that window. "Superseded in part" is the *opposite* of frozen: the parts that stand are precisely what an implementer builds from. PR #60 adds `partiallySuperseded`, which returns `false` from `isFrozen` before the bare-word marker is consulted.
- **`docs/architecture.html` was never scanned because the walk stopped at the file extension.** It is the rendered face of the canonical document, hand-maintained beside it with no generator in between, and it carried the "27/27 designs approved · 26 ADRs" header for weeks after both bodies had been corrected. PR #60 adds `renderHTML` and scans `.html` too.

**The rule, and it is the one this whole section exists to teach: an unscanned file reports exactly the same silence as a clean one.** A green gate is evidence about the corpus the gate *saw*, and nothing at all about the corpus that exists. `TestAnEmptyCorpusIsAFailure` already guards that at the level of the whole tree; neither hole tripped it, because the corpus was not empty — it was one or two files short, and nothing counted.

PR #60's answer is `TestTheCanonicalDocumentIsScanned`, which names the two files **by path**, because a corpus-level guard cannot see one file going missing. **Gate H is that pattern applied to the claims manifest**, and it is why the gate list is eight and not seven: without it, a page dropping out of the site build, or a holder file being moved, leaves every other gate passing on a manifest that no longer describes the site.

Two further limits PR #60 states about itself, which transfer directly and are not hypothetical:

- **Every rule is a lexical tripwire, not a semantic guarantee.** It catches the phrasings someone wrote down, and PR #60 records the corpus defeating that twice from the inside — once by emphasis inside a banned phrase, once because a rule knew only a reviewer's paraphrase of the live wording. This is §5.3's first bullet arriving from a different direction, and it is the argument against ever treating a green build as proof that a page is true.
- **A rescue is per block, and an HTML block is bigger than a table cell.** Markdown splits a table row into cells, so a retraction on one side cannot launder a claim on the other. HTML splits on block-level tags, so a `<div>` of inline `<span>` pills renders as **one** block, and a retraction in one pill rescues its neighbour. PR #60 measured this while writing it: re-planting the false header claim beside the corrected one did not fail the gate until the rescue clause was narrowed. **The consequence for the `ClaimClass` component: a claim badge and its claim must render as one block, and two claims must never share one.** An inline badge row is a surface on which one classed claim can vouch for an unclassed neighbour, and nothing would catch it.

### 5.6 The copy count, which is evidence against this proposal

The same claims are currently written out in **six** places: `README.md`'s two lists, `SECURITY.md`'s *What is NOT yet enforced*, the `AGENTS.md`/`CLAUDE.md` paragraph, `docs/architecture.md` §02 and §18's notes, `docs/install.md`'s opening paragraph, and design 02 §5. Adding `web/claims.yaml` makes **seven** unless something is retired.

The recommendation is not to retire them all. `AGENTS.md` and `CLAUDE.md` are narrative documents for contributors and read as prose for a reason; design 02 §5 is the authoritative list and must stay where an implementer reads it. The proposal is narrower and is one gate:

**Gate R (owed, not proposed as done): `README.md`'s *What is true today* bullets are generated from the manifest's `headline`-tagged `measured` records, and *What is NOT true today* from its `headline`-tagged `designed` and `not-built` records.** That is the highest-reach copy and the one that has already gone false. It requires editing `README.md`, which this change does not do, and it is the second item on the cut list. Until it lands, the manifest is a seventh copy, and the honest description of the first release is that the site has one enforced copy and the repository has six unenforced ones.

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
- **A concept page names what it is not.** `governance-at-the-gateway` states in its own body that no `AgentgatewayBackend` is emitted; `reserved-writes` states that `DELETE` is not reserved. Leaving the negation to `/feature-status` is how a concept page becomes a promise.

## 7. The status page

`/feature-status` answers one question: **is the thing I need actually enforced, at the version I run?**

**It is not called `/status`, because "status" reads as uptime.** `status.sigstore.dev` is literally an uptime dashboard, and a reader following a link labelled "status" from a landing page expects to learn whether the service is up. The surveyed names for this page are `Feature Stages` (Istio), `Feature Maturity` (Argo CD, kgateway) and `/status/` (OpenTelemetry, which gets away with it because it has no hosted service). `Feature status` is the closest conventional label; the page differs from all of them in being keyed to **measurement** rather than to intent, which the survey found no project doing — so the convention is borrowed for the name and not for the content.

**How it avoids being a third hand-maintained copy: it is generated from `web/claims.yaml`, and the manifest is versioned by git along with the code it describes.** There is no per-version editing step, because there is no per-version file.

**Per-version presentation.**

- The page has a version selector listing every release tag from the first tag that carries a manifest onward. Each entry renders that tag's `web/claims.yaml`. A tag predating the manifest renders a stub naming the tag and linking `README.md` at that tag — honest, and cheap.
- The default view is the **latest release tag**, not `main`. A reader on `/feature-status` is asking about the version they can install. `main` is available as an explicit selection and is labelled unreleased.
- A **delta view** between two selections lists every claim whose class changed, in both directions. That view is also the input to `/releases`, so the release notes and the status page cannot disagree.

**Layout.** Three sections, in this order: `measured` (what is enforced), `designed` (what a design specifies and nothing enforces), `not-built`. Within `measured`, claims carrying `conditions` are grouped under a sub-heading naming the condition — so "true on k3d only" is a heading a reader cannot skim past, not a footnote.

**What the page must not do.** It must not summarise. The manifest's `text` is rendered verbatim, because a summary of a claim list is the artefact this whole document exists to prevent.

**Where this sits against prior art.** The survey found **no project publishing what it claims against what it has measured**. The nearest things are Gateway API's per-implementation conformance matrix — which is the only mechanism found anywhere where "does not work" is *produced by tests* rather than asserted by a writer, complete with ❌ cells and an `Extended Features: 25/27` count — and OpenTelemetry's `/status/` per-signal-per-language grid, which states outright that a signal's status in the specification may differ from its status in an SDK. Argo CD's feature-maturity page is a third relative: it indexes *only* unstable surface, so the page is by construction a list of what not to rely on.

Dedicated "limitations" or "non-goals" pages essentially do not exist in this cohort; the genre exemplar is outside it, in SQLite's *Quirks, Caveats, and Gotchas*, which is a first-class maintained document rather than a caveat scattered through prose.

**The consequence for assayd is a claim, not a boast, and it should be stated carefully on the page**: `/feature-status` is keyed to measurement, and this project already produces the input — `AuthVerifiedOnOneReplica`, "the policy half is still not measured", "nothing pages, because design 10 is not built". The ambitious version, which Gateway API shows is reachable, is to **generate the `measured` section from a test run rather than from a manifest field**. Nothing here proposes that, and §12, O3 carries why: the manifest asserts that a test exists, and only a person can assert that it measures the sentence.

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

**And the archive is browsable by state, where the state is the claim class.** This is taken from Gateway API, and the survey named it the single most transferable structure found. Gateway API has **no roadmap page at all**: its enhancement proposals are browsable *By State* — `Provisional`, `Prototyping`, `Implementable`, `Experimental`, `Standard`, `Completed`, `Deferred`, `Declined`, `Withdrawn` — and those states are **the same vocabulary as its release channels**, so "where is this in the design pipeline" and "can I use it" are one axis rather than two that can drift apart.

That is a structural answer to the exact failure `CLAUDE.md` opens by recording. Here the two axes are already one, because a design's state *is* the claim class of what it specifies: a design whose slice is implemented and tested produces `measured` claims, an approved-but-unimplemented slice produces `designed` claims, and a hypothesis under ADR-0030 produces `not-built` ones. So `/archive/designs/` is faceted by claim class and needs no second vocabulary, and "approved" stops being a word that can drift from "built" because it is not the word on the facet.

The counter-example is Tekton, which runs the two ladders separately — TEP status (`proposed`/`implementable`/`implementing`/`implemented`/`deferred`/`withdrawn`) against API stability (`alpha`/`beta`/`stable`) — joined only by a `Proposal` column in a hand-maintained table. That table has visibly drifted: one row has a blank Beta Release and a version sitting in the wrong column. Two vocabularies joined by hand is the shape to avoid.

**§8.1's slice table takes its status labels from Flux**, whose roadmap the survey rated the most honest found: each milestone opens with a literal status line (`Status: Completed - Flux v2.8 GA`), unshipped milestones are labelled `provisional` with the reason ("subject to change based on the project's priorities and the community's feedback"), and items include **explicit removals and end-of-support**, not only additions.

### 8.3 Open follow-ups, under the disclosure posture

**The rule, stated once at the top of the section and applied to every item: name the class, link the design section that already describes it, state the observable condition an operator would see, and stop before the sequence.** The repository is public and the linked section contains everything, so the site withholds no information — it withholds *packaging*. That distinction is the whole posture, and it is written on the page so a reader is not left guessing whether something is being hidden.

| Gap | Class of defect | Links to | Observable |
|---|---|---|---|
| A published route can be served unauthenticated by a gateway replica that has not yet taken the Agent's policy. One probe proves one replica, and no replica count is declared. | Authentication bypass, window, multi-replica only | design 03 §3.3.3 and §8.1 | `GovernanceSkipped=False`, reason `AuthVerifiedOnOneReplica` |
| Deleting the API-key ConfigMap takes every key to `401`, and nothing reports it. `DELETE` is not reserved by admission, and the operator does not watch key sets. | Availability, not bypass | design 03 §3.4.4 ("The key set is outside every gate") and §8.1's reservation scope ("`DELETE` is not reserved") | **nothing** — that is the defect |
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

### 9.2 `docs/architecture.html` is not published — and the reason has changed

An earlier revision of this document called this file the most dangerous artefact in the tree, because it carried "27/27 designs approved · 26 ADRs" in its header and footer, plus "Every component below is designed, adversarially critiqued, and approved" and "Every claim in this document is backed by an ADR and a critique-passed design" — the sentence `CLAUDE.md` opens by recording as false. It recommended deleting or regenerating the file.

**That was fixed by PR #60, and the fix is better than the recommendation.** The header now reads `status superseded in part · 27 designs, not 27 approved · 34 ADRs`; the approval sentence is rewritten in place; the footer claim is quoted and withdrawn. More importantly, the file is now **inside the gate**: `test/docs/superseded_test.go` scans `.html` through `renderHTML`, and `TestTheCanonicalDocumentIsScanned` names it by path so the exclusion cannot quietly return (§5.5).

**The page still does not go on the site, on a different and smaller argument.** It is a hand-maintained rendering of `docs/architecture.md` with no generator between the two — PR #60's own comment says so, and that duplication is exactly how the header survived correction for weeks. Publishing it would make a third rendering of one document. `/archive/architecture` renders the Markdown (§9.3); the HTML stays in the repository as the reviewers' artefact it is.

### 9.3 `docs/architecture.md` is published only inside the archive

`docs/architecture.md` is canonical and stays canonical in the repository. It is **not** the site's architecture page and does not appear in the main navigation. It is at `/archive/architecture`, verbatim, under a standing banner.

The reason: its honest warnings are per-*section* ("Designed, not shipped" at the head of §02, §03, §12, §16, §17), and a reader skimming a table two screens below the warning takes the table and leaves the warning. §4's claim classes are per-*claim*, which is the granularity the failure demands. The concepts section (§6) is the site's architecture story, and it is classed sentence by sentence.

An earlier revision listed three things in it that escaped their own section's fence and must not be lifted elsewhere. **PR #60 fenced two of them** — doctrine rules 5 and 6 now state what CI actually runs and that only one tier renders, and §07's bullets each carry an inline `*(intended)*` marker so a lifted bullet carries its own fence (§11.3). **The third stands**: §16's competitor table, §9.4.

One new hazard replaces the two that were fixed, and it applies to the whole archive rather than to this file: **PR #60's corrections quote the sentences they withdraw**, so `docs/architecture.md` now contains "27/27", "12–18-month window" and "the design phase is complete" as *quotations inside retractions*. Rendering the file verbatim is safe; extracting sentences from it is not. §11.6.1 makes that a rule rather than a warning.

### 9.4 The competitor comparison table is not published

`docs/architecture.md` §16's table has six capability rows. Its own header says the assayd column "states the target, not what ships", and only the gateway-path row is true today. A comparison table on which five of six rows would render `designed` or `not-built` is not a comparison; it is the original failure with a competitor's name beside it.

The first-mover claim that used to close the section — "nobody ships it as a k8s primitive — 12–18-month window" — was withdrawn by PR #60, which quotes and retracts it at `:435` rather than deleting it. **The table itself is unchanged**, so the argument here is the one it always was: five of its six rows are not `measured`.

A comparison page can exist when its rows are `measured`. Until then there is none, and the landing page's positioning is the thesis sentence and the honest list, not a grid.

### 9.5 The rest

| Not published | Why |
|---|---|
| `CLAUDE.md`, `AGENTS.md` | Contributor-facing working rules. `/feature-status` and `/roadmap` carry what a user needs from them; republishing the paragraph creates the copy §5.6 is trying to reduce. |
| `docs/HANDOFF.md` | Its own first line marks it a dated record, superseded and deliberately not rewritten. That is the exact class of document a site page must never be, because a site page is the one artefact a reader assumes is current. |
| `docs/research/**` (33 notes) as a section | Each note is dated evidence about a third party, several past their re-verify dates. A stale note on our own site reads as our current position on someone else's product. Notes are linked individually from the concept or reference page that cites one. |
| `docs/requirements.md` as a page | Of 30 FR/NFR identifiers, **zero `FR-` identifiers appear in any code or test file**, and only NFR-8 and NFR-3 appear at all. Publishing a requirements list where the overwhelming majority is `not-built` adds nothing `/roadmap` does not say better. Individual requirements are cited from claims where they are the holder. |
| The "≤8 core pods" figure as a landing-page footprint claim | It is CI-enforced *as a budget* (`TestCorePodBudget`, currently measuring 2 of 8) but it is a **target** as a description: the chart contains exactly one `Deployment`. It may appear on `/concepts` or `/roadmap` as `designed`; it may not appear on `/` as a footprint. |
| Six of the eight diagram plates, above the fold anywhere | Rule 6 of §4.2. |

## 10. First-ship cut list

Ordered. Each item is shippable on its own, and each is a prerequisite of the one below it unless stated.

| # | Ship | Why here |
|---|---|---|
| 0 | ~~`make docs` added to `.github/workflows/ci.yml`~~ — **void.** | Kept as a struck row rather than deleted, because a cut list that silently loses its first item is unreviewable. It was true at `22b65c2`, where this branch started; `80b63b5` added `make docs` **and** `make conformance` to CI the same day. The finding that replaces it is §5.5's, it is not a missing gate, and it is not this document's — the work it implies is **gate H**, already inside item 1. |
| 1 | `web/claims.yaml` + `test/docs/claims_test.go` (gates A–H) | Infrastructure, not a page. Every badge on every page depends on it; shipping a page first means shipping unclassed prose and retrofitting, which is how the copies start. |
| 2 | `assayd.io/` and `assayd.io/claims` | The landing page is the highest-reach surface. `/claims` ships with it, because a badge with no definition is decoration. |
| 3 | `assayd.dev/feature-status` | Generated from item 1. The landing page's honest list has to resolve to something. |
| 4 | `assayd.dev/guides/install`, `/guides/api-keys`, `/guides/troubleshooting` | The first thing a reader can actually *do*. Derived from `docs/install.md`, which is already written to this standard. |
| 5 | `assayd.dev/agent-contract` | The second thing a reader can do. Its existing Sources table means it needs the least rewriting of any page here. |
| 6 | `assayd.io/roadmap` | Needs items 1 and 3. Its §8.3 gap list is the reason the disclosure posture was decided, so it should not wait. |
| 7 | **Gate R — `README.md` generated from the manifest** | Not a page. Deliberately after the roadmap, because it edits a file outside the site and should land once the manifest's shape has survived six pages of real use. |
| 8 | `assayd.dev/supply-chain` | Standalone; a reader verifying signatures needs no other page. |
| 9 | `assayd.io/concepts/*` — eight pages | Deferred behind the guides on purpose: a concept page is only worth reading once the reader has hit the thing it explains. Order within: `governance-at-the-gateway`, `revisions-and-cards`, `create-lock-adopt`, `attribution`, `the-auth-transaction`, `reserved-writes`, `the-revision-service`, `standards-only`. |
| 10 | `assayd.dev/reference/*` | **Three of the six exist.** PR #59 generates `crd-agent`, `helm-values` and `conditions` from `cmd/refgen`, gated by `make verify` in CI; those three ship as soon as that PR lands and need nothing from this document. `flags`, `labels` and `tested-versions` are owed and are the reference owner's, not the site's. |
| 11 | `assayd.dev/archive/*` and the review ledger | High value, zero urgency — the files are already on GitHub. |
| 12 | `assayd.io/releases`, `/security`, `/about`; `assayd.dev/` home, remaining guides, `/contributing*` | Completeness. |

**Deferred, with the reason:**

| Deferred | Until |
|---|---|
| Site search | There is more than one page worth searching and `/archive` is published. Search over a site where `/archive` and `/feature-status` return adjacent results needs a scoping decision this document has not made. |
| Versioned documentation (more than one version of the guides) | A second supported version exists. `/feature-status` is versioned from item 3; the guides are not, and pin one tested version. |
| A blog, case studies, a newsletter | There is something measured to write about that `/releases` does not cover. |
| A comparison page | Its rows are `measured` (§9.4). |
| Full-text publication of `docs/designs/reviews/**` | Never, on the argument in §9.1. |
| New diagrams for `the-auth-transaction` and `create-lock-adopt` | Item 9. They are owed and they do not block the concept pages' text. |

## 11. Claims in the existing corpus, and where each one now stands

An earlier revision of this document listed 22 claims as false or stale, found while verifying it. **PR #60 (`docs-honesty-gate`, head `3bc7e6b`, on `main` `6ae20e1`) has since addressed all 22.** That PR is under independent review and is not merged.

**This section is rewritten rather than deleted, and the reason is the subject of this whole document.** A list of defects that have been fixed, published as a committed record, reads as a to-do list. The same shape was found in design 03 §11 on 2026-09-23, where A82's entry still read "The fix is OWED" for something A83 had fixed. A record wrong in the safe direction is still wrong.

Each row was re-verified against PR #60's head on 2026-09-23 rather than taken from its description. Three outcomes:

- **Fixed** — the claim is gone, or is present only inside an explicit retraction.
- **Changed shape** — the text a site author will find is not what the old row described, and the difference constrains the site. These matter most and each carries its consequence.
- **Still live** — nothing has changed.

### 11.1 The three that blocked a public site — all fixed, two changed shape

| # | Old finding | Now |
|---|---|---|
| 1 | `docs/architecture.html` carried "27/27 designs approved · 26 ADRs" in its header and footer, and "Every component below is designed, adversarially critiqued, and approved" | **Fixed.** The header `meta-row` reads `status superseded in part · 27 designs, not 27 approved · 34 ADRs (0020 and 0033 superseded)`; the approval sentence is rewritten in place; the footer's "Every claim in this document is backed by an ADR and a critique-passed design" is quoted and withdrawn at `:1878`. **And the file is now inside the gate** — §5.6. |
| 2 | `docs/architecture.md:433` §16 sold a "12–18-month window" | **Fixed, changed shape.** The sentence is not deleted — `:435` now quotes it and withdraws it: *"The sentence that used to close this section is withdrawn, and is quoted here rather than deleted."* The string `12–18` is therefore still in the file, twice, both inside retractions. |
| 3 | `docs/decisions/0026-p5-enterprise.md:6` said "the design phase is complete — 27/27 approved… Implementation may begin" | **Corrected, not rewritten — and the false sentence is still at `:6`, deliberately.** The Status line carries an Amendment 2 marker and Amendment 2 withdraws the clause, but the frozen Consequences body is untouched, because the `adr` skill forbids editing an ADR's content to match a later decision. **This has a direct IA consequence — §11.6.** |

### 11.2 Counts and cross-references — all fixed

| # | Old finding | Now |
|---|---|---|
| 4 | "32 ADRs"; "12 dated notes" | **Fixed at `:493`** — "**34 ADRs**, 0001–0034, of which 0020 and 0033 are superseded" and "**33 dated notes** plus the `paper/` set", with the stale pair named: *"Those counts were last printed as 32 and 12."* |
| 5 | The ADR index stopped at 0027 | **Fixed.** Rows 0028–0034 exist, each carrying its own status — 0020 and 0033 marked superseded, 0030 flagged as "the ADR that governs what may be claimed anywhere in this document". |
| 6 | "design 02 carries eleven (A1–A11)" | **Fixed at `:562`** — "**seventy-six**, A1–A76, the tip dated 2026-09-16". |
| 7 | `# API group TBD at rename` | **Fixed.** The string is gone from the corpus. |
| 8 | R1 both open and closed in one file | **Fixed at `:486`** — the risk row now says what R1 is not: *"R1 is not what tracks that, and this row used to say it did."* |
| 9 | The agentgateway floor stated as v1.4.1 against an e2e on 1.5.0 | **Fixed at `:556`** — *"A floor is not the only version that runs"*, with the two-phase conformance run named. |

### 11.3 Present-tense escapes — all fixed, and one changed shape usefully

| # | Old finding | Now |
|---|---|---|
| 10 | Doctrine rule 5 listed four distros as CI targets | **Fixed.** Rule 5 now reads "**Two distros are a CI target, not four**", names `hack/e2e.sh`'s non-zero exit on anything else, and says minikube and k3s are "intended and untested". |
| 11 | Doctrine rule 6 claimed independently removable tiers | **Fixed.** "**removability is untested because only one tier renders**", citing `_helpers.tpl`'s `fail` and what `plus` would add. |
| 12 | §07's bullets asserted alert rules, OTel spans and JetStream receipts in the present tense | **Fixed, changed shape — and the new shape helps the site.** Each bullet now carries its own `*(intended)*` marker inline, and the preamble says the fence "applies to every bullet below, not to one clause in one of them". **A generator lifting a bullet now lifts its marker with it**, which is what §5.4 needs from a derived-and-reviewed page. |
| 13 | §17's "≈ 8 pods" ledger summing to 9 | **Fixed at `:450`–`:452`** — "**8 pods at the low end of that range and 9 at the high end**", and the "≈" is named as having "hid a row that breaks the rule rather than a rounding". |

### 11.4 Requirements — all four fixed, all changed shape

`docs/requirements.md` now keeps each original wording and states the gap beside it, rather than lowering its bar silently. The opening line is explicit: *"a requirements document that lowers its bar without saying so is the defect this correction exists to fix."*

| # | Old finding | Now |
|---|---|---|
| 14 | "Each requirement is testable; NFRs are release gates" | **Fixed at `:5`–`:8`.** Both halves withdrawn, with the grep result stated: a standalone `FR-` id returns **zero** matches across `api/ internal/ cmd/ test/ config/ charts/ hack/`, and every apparent hit is a substring of `NFR-`. |
| 15 | NFR-2's "only stateful dependencies, **ever**" | **Fixed at `:15`** — the enforced rule is named as an allowlist of three, with `openobserve` recorded in the test as "observability sink, not substrate". |
| 16 | NFR-3's "full e2e on k3d + kind" | **Fixed at `:16`** — "**Two distros are exercised, and only one of them fully.**" |
| 17 | NFR-7's installable `plus` | **Fixed at `:25`** — "**`plus` does not render at all today**, so this requirement is unmet". |

### 11.5 Release and supply chain — all four fixed; one answers an open item

| # | Old finding | Now |
|---|---|---|
| 18 | "The current release is v0.4.0" against a tagged `v0.4.1` | **Fixed at `:27`, and it answers §12's O2 — as "nobody has checked".** The page now states that `v0.4.1` is the newest tag and *"this page has not been re-verified against it… no `cosign verify` of them is recorded"*, and closes with *"Read a version claim here as 'last checked', never as 'current'."* **The IA consequence is in §11.6.** |
| 19 | "published only on a `v*` tag" | **Fixed at `:14`** — `workflow_dispatch` with a `tag` input publishes identically, and is how `v0.2.0` was re-released. |
| 20 | The table listed SLSA provenance unconditionally | **Fixed at `:9` and `:12`** — the row carries "**provenance is conditional, see below**", and the visibility guard is explained. |
| 21 | The workflow never verifies its own provenance | **Fixed at `:16` and `:90`** — stated outright, with the consequence: *"Every provenance statement on this page is a dated manual check by a person."* |

### 11.6 What PR #60's shapes require of the site

Four of the fixes above change what a site author will find, in ways this document must answer rather than note.

1. **Withdrawn claims are now quoted verbatim inside their own retractions**, in at least eight places across `architecture.md`, `requirements.md`, `supply-chain.md` and ADR-0026's Amendment 2. That is correct for the corpus — write-spec requires that a correction quote what it retracts — and it is a trap for any pipeline that extracts *sentences*. **The rule that follows: no page on either site is produced by extracting sentences from repository prose.** A page is generated from `web/claims.yaml`, or it renders a whole block with its surrounding retraction intact, or it is rewritten by a person. There is no third mode, and §5.1's three modes already encode this — this is the evidence for why.

2. **`/archive/decisions/0026` renders a false sentence by design, and the archive is the one place the repository's own gate deliberately does not look.** ADR-0026's Consequences still says "the design phase is complete — 27/27 approved"; the `adr` skill forbids editing it; and PR #60's new `blanket-approval-claim` rule carries `exceptDir: "decisions"` for exactly that reason — enforcing it there would mean breaking one repository rule to satisfy another. **So the requirement lands on the site**: an archive page rendering a document verbatim must render its Status line and every Amendment marker **adjacent to the corrected clause**, not only at the top and bottom of the page. This is §4.4's Gateway API pattern — the box beside the sub-feature rather than under the H1 — and here it is load-bearing rather than stylistic, because a reader deep-linking to `#consequences` otherwise lands on the false sentence alone. `/archive` carries `someone notices` in §5.4's enforcement column, and this is the specific thing they must notice.

3. **A tag is not evidence of a published, verified artifact.** `supply-chain.md` now records that `v0.4.1` is tagged and unverified. §5.4 lists `/releases` as *generated from tags*; that is wrong on its own and is corrected there. `/releases` renders a tag, its verification state, and the date it was last checked — and a tag with no recorded check renders as unverified, never as a release.

4. **Anything the site publishes as inline SVG is outside the gate.** PR #60's `renderHTML` drops `<script>`, `<style>` and inline `<svg>` whole, and says so: *"a withdrawn guarantee typed into an SVG's `aria-label` is NOT scanned."* §4.2 rule 6 already says a diagram is a claim; this is the mechanism by which a diagram's text can carry one invisibly. A diagram's claim text belongs in the manifest and in the page's prose, never only in the artwork.

### 11.7 Still live

One. **`docs/architecture.md` §16's competitor table itself** — six capability rows whose own header says the assayd column "states the target, not what ships". PR #60 withdrew the first-mover sentence that closed the section; the table is unchanged, and §9.4's refusal to publish it stands for the original reason.

### 11.8 Documented honestly, listed so nobody "fixes" them

Not defects. Recorded because they look like defects and will be reported as such.

- `charts/assayd/Chart.yaml` says `version: 0.1.0` and `charts/assayd/values.yaml` pins `tag: "0.1.0"`, against a newest tag of `v0.4.1`. `.github/workflows/release.yml` rewrites both at release time with `yq`, and `docs/install.md` states plainly that the checkout's chart names a version "which is not published".
- `docs/agent-contract.md` scopes every guarantee to one fixture — "it is the only agent any test here runs, so it is the only one this contract has been measured against". **That sentence must survive the rewrite to `assayd.dev/agent-contract`.** It is the single most load-bearing caveat in the corpus and exactly the kind a documentation restyle deletes.
- `charts/assayd/templates/NOTES.txt` is the most accurate governance statement in the repository, on both branches of `gateway.enabled`. The landing page inherits its sentence — "YOUR AGENTS ARE REACHABLE, AUTHENTICATED BY API KEY, AND OTHERWISE UNGOVERNED" — rather than composing a new one.
- **ADR-0026's Consequences body will keep saying something false.** See §11.6.2. It is frozen on purpose; do not open a PR against it.

## 12. Open items

Carried forward rather than hedged in the prose above.

| # | Item | Why it is open |
|---|---|---|
| O1 | ~~The brief names a gap "D6"~~ — **closed 2026-09-23. The label was never in the repository; it was invented in the brief this document was written from, and the coordinator has confirmed it.** The defect it named is real and is located at design 03 **§3.4.4** ("The key set is outside every gate") and **§8.1**'s reservation scope ("`DELETE` is not reserved"). §8.3 now cites the location and carries no label. Kept as a closed row because a spec that quietly drops a question it once raised teaches the next reader nothing. |
| O2 | ~~Whether `v0.4.1` published a chart and image~~ — **answered, and the answer is that nobody has checked.** `docs/supply-chain.md` (PR #60) now records `v0.4.1` as the newest tag, states it has not been re-verified from outside, and closes "Read a version claim here as 'last checked', never as 'current'." That is not a blocker for `/releases`; it is the page's content. §11.6.3 carries the consequence: a tag renders as unverified, never as a release. |
| O3 | **The strongest evidence this project produces still has no field in the claim record.** "Three mutations kill it" (`TestTheGatewayIsTheOnlyWayIn`) and "every case was run with a mutation of its own, in five batches" (design 03 §8.1) are stronger than "a test exists", and §4.3's record cannot express either. A `mutations:` field is the obvious answer and nothing would check it, which is the argument against adding it. PR #59 meets the same wall from the other side and says so: "a test names the string, which is weaker than pinning it — nine tests in this repository once passed with their subject deleted." Unresolved. |
| O4 | ~~`/reference/reasons` cannot be generated correctly~~ — **answered by PR #59**, which extracts the vocabulary with `go/types` rather than reading constants: 39 condition types, 64 reasons, every call site resolved, and a hand-written annotation file held to the set by `make reference` so that a reason with no entry fails CI. **What remains open is narrower and is the page's own statement**: the join pins the *set* of reasons and does **not** prove the three annotated answers — what produces a reason, what the operator does, whether traffic is withdrawn — are still true of the code. `AuthPolicyNotAttached`'s annotation was found stale after design 03 A83 by exactly that route. And **17 of the 64 reasons are named by no test file**, so nothing fails if one changes or stops being set. `/reference/conditions` must render both facts; it does. |
| O5 | Where the claim manifest lives. `web/claims.yaml` is proposed because the site consumes it, but `web/**` is another agent's territory and a manifest under it is a site asset rather than a repository one. `docs/web/claims.yaml` is the alternative. Not decided. |
| O6 | Whether `/archive/designs/<nn>` should render 27 designs whose bodies contradict their own Status lines in places. Design 03's body is 2,000+ lines specifying an unapproved system; publishing it verbatim is honest and is also 2,000 lines of `designed` prose with one approval banner at the top. A per-section banner was considered and needs the design's own section structure, which varies. Not decided. |
| O7 | Whether the landing page may state the thesis — "what was evaluated is what runs" — as an unclassed sentence. It is a statement of intent, not of behaviour, and §4.2 rule 1's boundary does not cleanly settle it. The conservative reading is that it is a claim and is `measured` only for revision identity, not for evaluation, since the eval gate is `designed`. |
| O8 | Gate B's implementation. Detecting that a named test "reaches a `t.Skip`" requires following helper calls — `responderImage`, `requireGateway` and `requireCluster` are where the skips actually live, not the test bodies. A one-level call-graph walk covers today's cases; nothing guarantees it covers tomorrow's. |
| O9 | **Whether the eight concept pages should move to `assayd.dev`.** The `.io`/`.dev` split is decided and §2.1 does not relitigate it, but all seven surveyed two-domain projects put concept *pages* on the docs side and give marketing one narrative page instead. The cost of the decided frame is a cross-domain hop mid-guide. Raised for the human, not decided here. |
| O10 | **Whether `/releases` on `.io` should keep the supported-versions and end-of-life table, or hand it to `/reference/tested-versions` on `.dev`.** §2.1 proposes the latter on the survey's evidence (cert-manager, Grafana, Flux and Istio all keep it operator-side), but this splits one reader's question across two domains and may be worse than either whole. |
| O11 | Whether to serve every docs URL as Markdown by appending `.md`, and publish `/llms.txt`. kgateway, agentgateway and Temporal all do. It is cheap, and this project's documentation is demonstrably read by agents as well as people — `AGENTS.md` exists for exactly that reason. Not specified above because it is a scaffold decision, and the scaffold is another agent's. |

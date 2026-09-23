# The assayd website — information architecture

- **Status**: **proposal, not approved.** No critique of this document has been run. It specifies pages, not prose, and no page it names exists. Where it proposes a file outside `docs/web/`, that file is owed by whoever implements the section, and this document does not create it.
- **Date**: 2026-09-22 · **revised 2026-09-23**, twice · **second revision** against `main` `c4e5020`, on which PR #57 (the `web/` scaffold, `0e94a15`), PR #59 (the generated reference, `425bd59`) and PR #60 (the docs honesty gate, `4f96ca7`) are all merged · **Scope**: `assayd.io` (the site) and `assayd.dev` (the docs)
- **What the first revision changed**: §11 was a list of 22 defects; PR #60 addressed all 22, so §11 is now a reconciliation — fixed, changed shape, or still live — because a fixed-defect list published as a record reads as a to-do list. §5.5 replaces this document's own first finding, which is **void**: `make docs` was not in CI at `22b65c2` where this branch started and was added in `80b63b5` the same day.
- **What the second revision changed**: **the claims system is rewritten against what PR #57 actually built**, which is not what this document specified — there is no manifest; evidence is inline on the `ClaimClass` component; the check is `web/scripts/check-claims.sh` over the built HTML; the definitions page is `/claim-classes.html`. §3.1, §4.3, §4.4, §5.1, §5.2 and §10 item 1 say so, and §5.2 marks each gate built, partly built or not built. **The roadmap's holder of truth follows the human's decision of 2026-09-23** (§5.4, §8.2). §6's revision-Service sentence is corrected after design 02 A77 (PR #54). §5.5 and §11.6.2 describe PR #60's gate as merged rather than as proposed. Hard-coded reason counts are replaced by a pointer to `docs/reference/conditions.md`. §11.2 row 6 is live again. §12: O2 and O4 closed, O5 overtaken. **A third pass the same day, against `main` at `9a74178`**, records PR #66: the twenty-three false Status lines (not eight) are corrected, ADR-0030 Amendment 1 withdrew "arbitrates", `make docs` holds Status lines and the index to each other and allows an approval claim only on designs 03 and 16, and only of a part, so §5.4, §8.2 and §10 item 6 now let the roadmap generate its design table; §11.2 row 6 is fixed; §9's hand-typed counts are dropped; §6's A77 reasons and permissions are corrected.
- **Decisions that bind this document, made by the human and not relitigated here**: `assayd.io` is the site and `assayd.dev` is the docs · a security gap on the roadmap is named and linked, never given with a reproduction recipe · the system font stack, no webfont · **no Mermaid anywhere — every diagram is a generated `.webp` plate**, placed with `web/shared`'s `Figure` component · a design's own Status line is the source of truth for its state, and `docs/designs/README.md`'s index summarises it (2026-09-23; §5.4).
- **Reads from**: `README.md`, `AGENTS.md`, `CLAUDE.md`, `SECURITY.md`, `docs/architecture.md`, `docs/install.md`, `docs/agent-contract.md`, `docs/agent-protocol.md`, `docs/supply-chain.md`, `docs/requirements.md`, `docs/designs/README.md`, the Status lines of designs 02, 03 and 16, and ADR-0030.
- **Does not decide**: the site scaffold (`web/**`, PR #57), the generated reference tooling (`cmd/refgen`, `internal/refgen`, `docs/reference/**`, PR #59), or the diagram audit (`docs/diagrams/**`). Those are owned elsewhere. This document names what those owners must produce and what a page may claim about it.

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
| `/index.html` | What is assayd, what does it do *today*, and is it for me? |
| `/claim-classes.html` | What do `measured`, `designed` and `not-built` mean, and why is one attached to every sentence? |
| `/concepts/index.html` | Which ideas do I need before the guides make sense? |
| `/concepts/revisions-and-cards.html` | Why is an agent's identity its content rather than its tag? |
| `/concepts/governance-at-the-gateway.html` | Why is a rule enforced at the gateway rather than in the agent's SDK? |
| `/concepts/the-auth-transaction.html` | What does the operator do between "I created an Agent" and "traffic flows"? |
| `/concepts/create-lock-adopt.html` | Why are there three ways to attach a policy, and why is one of them a refusal? |
| `/concepts/attribution.html` | Why is a `401` not enough on its own? |
| `/concepts/reserved-writes.html` | Who may write a policy or an API-key ConfigMap, and what stops anyone else? |
| `/concepts/the-revision-service.html` | What does one revision get of its own, and what decides whether an object at its name is the operator's? |
| `/concepts/standards-only.html` | Which existing standards does assayd speak, and which ones does it only name? |
| `/roadmap.html` | What is being built next, what is knowingly missing, and what is broken and unfixed? |
| `/releases.html` | Which versions exist, what changed, and which should I install? |
| `/security.html` | How do I report a vulnerability, and what is not yet enforced? |
| `/about.html` | Who makes this, under what licence, and who decides? |

**Every path in §3 is the served file, `.html` included.** Both targets build with `build.format: "file"` (`web/README.md`), so a page is `about.html` and not `about/index.html`, and `web/README.md` requires every cross-reference to be written with its explicit `.html`, which `web/scripts/check-links.sh` enforces over the built output. Prose elsewhere in this document names a page by its short form — `/roadmap`, `/feature-status` — for readability; the table is the address.

`/claim-classes.html` is the definitions page for the three classes. **It exists** — PR #57 built it as `web/site/src/pages/claim-classes.astro` — and it is one page, on `.io`; `.dev` links to it rather than mirroring it, for the reason in §2: a mirrored page is two pages. An earlier revision of this document called it `/claims` and made it the link target of every badge. **The badge PR #57 built links nowhere**: `ClaimClass` is deliberately non-interactive (its own comment: "the badge is NOT interactive … Nothing in this component is focusable"), so a reader reaches the definitions from the page's own navigation, not from the badge. §4.4 says what that costs.

### 3.2 `assayd.dev`

| Path | The question it answers cold |
|---|---|
| `/index.html` | Where do I start? |
| `/guides/install.html` | How do I get from a fresh cluster to one agent answering through a gateway? |
| `/guides/api-keys.html` | How do I let one caller in and keep another out? |
| `/guides/a2a-task.html` | How do I send an agent a task and read the answer? |
| `/guides/mcp-tool.html` | How do I publish a tool behind the gateway, and how does an agent call it? |
| `/guides/upgrading.html` | Why did `helm upgrade` not update my CRD, and what do I do about it? |
| `/guides/troubleshooting.html` | My install or my Agent is stuck — what is it telling me? |
| `/agent-contract.html` | What must my container do to run as an Agent? |
| `/supply-chain.html` | How do I verify the bytes I am about to run? |
| `/feature-status.html` | Is the thing I need actually enforced, at the version I run? |
| `/reference/index.html` | Which reference surface do I want? |
| `/reference/crd-agent.html` | Every field of the Agent CRD, its default, and what refuses it |
| `/reference/helm-values.html` | Every chart value and its default |
| `/reference/conditions.html` | Every condition the operator writes, every reason it can set, and whether traffic is withdrawn |
| `/reference/flags.html` | Every operator flag |
| `/reference/labels.html` | Every `assayd.dev/*` label and annotation, and whether it is evidence |
| `/reference/tested-versions.html` | Which versions of Kubernetes, Gateway API and agentgateway have been measured |
| `/archive/index.html` | Where is the design record, and how should I read it? |
| `/archive/designs/<nn>.html` | What does design `<nn>` specify, and is it approved? (27 pages) |
| `/archive/decisions/<nnnn>.html` | What was decided, when, and why? (34 pages) |
| `/archive/architecture.html` | What is the target architecture, as a document of its date? |
| `/archive/reviews.html` | How many independent reviews has each design had, and what did each return? |
| `/contributing.html` | How do I contribute, and what will happen to my patch? |
| `/contributing/review.html` | What does "independently reviewed" mean here? |

**Reasons are on `/reference/conditions.html`, not on a page of their own — and an earlier revision of this document said they could not be documented at all.** That was right about the code and wrong about the remedy: reasons were constants plus bare string literals with nothing closing the set. **PR #59, merged, closes it** by extracting the vocabulary with `go/packages` and `go/types` rather than reading the constants, and by holding a hand-written annotation file, `internal/refgen/reasons.yaml`, to that set, so a reason with no entry or an entry with no reason fails `make reference` and so fails CI. **This document does not restate the counts.** They move with every amendment that adds a reason — they have moved twice while this document was open — and `docs/reference/conditions.md` states them in its own opening paragraph, generated, alongside how many reasons no test file names. Cite that page; a count copied here is a count that goes stale. §12, O4 carries what the page still cannot promise.

## 4. The claim-class system

### 4.1 The three classes

| Class | What it asserts | What the built `ClaimClass` component requires (PR #57) | What an earlier revision of this document required, and is **not built** |
|---|---|---|---|
| `measured` | A named test proves it. | `test` — the Go test function's name, verbatim. The build throws if it is empty. | the test's file and its layer |
| `designed` | A design specifies it. Nothing enforces it. | `design` (e.g. `"design 03"`) and `section` (e.g. `"§3.5"`). The build throws if either is empty. | the design's own Status verdict, quoted |
| `not-built` | Neither. | nothing; `note` is optional | — |

The holder of these rules is `web/shared/src/claim-class.ts`, whose resolver **throws, failing the build, on a claim that cannot name its evidence**. There is no fourth class, because the component renders exactly three and its own comment refuses one "for 'partly' or 'soon'", and because a fourth would be the hedge this project keeps paying for.

### 4.2 The rules

1. **A claim is a sentence a reader could act on.** "The operator publishes a route only after an anonymous request through it gets `401`" is a claim. "assayd is Apache-2.0" is a fact about the repository, not a claim about behaviour, and carries no class. The boundary is: if it asserts what the software does or refuses, it is a claim. *(Nothing detects an unclassed claim; §5.3 says so.)*

2. **A `measured` claim may not say more than its test's layer can claim.** `AGENTS.md` fixes the layers: unit asserts against the generated artifact; envtest runs a real API server and **no kubelet**, so every availability assertion there is against a status the test itself wrote; only e2e can claim a pod actually runs. A claim about a running pod backed by an envtest is mis-classed even though a test exists. *Not mechanically checked.* The built badge carries no layer and no file, so gate C (§5.2) is not built; a reviewer reading the test's directory catches a mis-classed layer or nobody does.

3. **A `measured` claim whose test can skip must name the condition in the claim's own text.** Fourteen e2e tests here skip under an environment variable, and the two that matter most say so loudly: `TestTheGatewayIsTheOnlyWayIn` reports "NetworkPolicy enforcement UNVERIFIED, not passed" on any CNI that does not enforce it, and everything responder-backed skips on the kind lane. A site sentence that reads "the gateway is the only way in" full stop is false on kind. It must read "on a CNI that enforces NetworkPolicy, under a hand-authored rule". *Not mechanically checked: gate B is not built, so a reviewer catches a missing condition or nobody does.*

4. **A `designed` claim is never written in the present indicative.** It renders as "design 03 §3.5 specifies…", never "assayd enforces…". The reason is the exact failure `README.md` records: present-tense prose about a designed capability is indistinguishable from a guarantee at reading speed. *Not mechanically checked; a reviewer catches it or nobody does.*

5. **A claim's class may only be raised in the commit that adds its evidence.** `not-built` → `designed` needs the design section in the same change; `designed` → `measured` needs the test. *Partly checked, in the raising direction only.* `check-claims.sh` fails a `measured` badge whose test function does not exist anywhere under `test/`, `internal/` or `api/`, and a `designed` badge whose design file does not exist (gates A and D, partly built — §5.2). It does not check the section. Lowering is not checked at all — if the compiler is deleted and its tests with it, gate A fires; if the code is deleted and a now-vacuous test survives, nothing fires.

6. **A diagram is a claim.** Six of the eight plates in `docs/diagrams/` depict systems that do not exist — eval-gated rollout, the on-behalf-of token exchange, knowledge-graph ingestion, the governance ring, the six-CRD surface, and a rollout lifecycle with a `Canary` state the slice never enters. Plate `07-what-runs` is titled for what runs and itemises eight core pods; the chart contains exactly one `Deployment`. A plate carries the class of what it depicts, and a `not-built` plate does not appear above the fold on any page. **The mechanism exists**: `web/shared`'s `Figure` component takes one `claim` prop, rendered as a `ClaimClass` badge in the caption, and a `depicts` prop naming the version, tag or commit whose behaviour the plate shows — the staleness stamp. One figure carries one claim. Every plate is a generated `.webp` (the human's decision, and `web/README.md`: "No Mermaid and no diagram-as-code integration"), so the plate's pixels cannot be checked against the code and the `depicts` stamp is what a reader has instead. *Neither prop is required by the component, so nothing fails a plate that carries no class; a reviewer catches it or nobody does. The verdict per plate belongs to the diagram audit (`docs/diagrams/AUDIT-2026-09.md`); this rule does not pre-empt it.*

### 4.3 What a claim looks like on a page

**There is no claim record.** An earlier revision of this document specified a manifest of records, each with an `id`, its `text`, its `class`, an `evidence` block and the `pages` that cite it. PR #57 built something different and simpler: **a claim is a `ClaimClass` element placed in the page itself, carrying its evidence as props**, and the component emits that evidence onto the built HTML as `data-claim`, `data-claim-test`, `data-claim-design` and `data-claim-section` attributes, which `web/scripts/check-claims.sh` reads back (§5.2). The sentence the badge vouches for is the page's own prose beside it; nothing holds that sentence anywhere else.

The minimal claim, a `not-built` one, in MDX:

```mdx
An agent's outbound traffic is restricted to the gateway. <ClaimClass claim="not-built" />
```

A realistic `measured` claim, the one on the landing page. The skip condition is written into the sentence, because the component has no field for it (§4.2 rule 3):

```mdx
On k3d, a request reaches the agent through the Gateway's own Service, carried by a
route the operator wrote; the kind lane skips every gateway test and reports the path
unverified. <ClaimClass claim="measured" test="TestAnAgentAnswersThroughTheGateway" />
```

And a `designed` claim:

```mdx
Design 03 §3.5 specifies token and USD budgets compiled onto the gateway. No
AgentgatewayBackend is emitted, so nothing enforces one.
<ClaimClass claim="designed" design="design 03" section="§3.5" />
```

A page-level class uses `variant="header"`, which renders a masthead rule rather than an inline pill (`web/docs/src/content/docs/index.mdx` carries one).

**What the simpler shape costs**, recorded rather than hidden:

- **A claim appearing on three pages is three copies.** The same sentence on `/`, `/feature-status` and `/roadmap` is typed three times, and nothing checks that the three agree. The manifest existed to prevent exactly that. §5.1 says why this document now accepts the cost for the first pages and when it stops being acceptable.
- **`/feature-status` cannot be generated from one place**, because there is no one place. §7 says what that changes.
- **Nothing records the design's Status verdict beside a `designed` claim**, so a slice's approval boundary can move under a claim with nothing firing (gate E, not built).

### 4.4 How a claim class renders, and the failure mode that must not be repeated

**The dominant failure in the surveyed projects is definitions without application: a careful definitions page, and then no signal on the pages the reader actually lands on.** It is worth naming three cases, because this document could produce all three.

- **Istio** has the best definitions artifact found anywhere — a 15-row × 4-stage table specifying what each stage promises about security, performance, support, deprecation, testing and upgradeability. It then signals an Alpha feature with **a bare asterisk appended to the sidebar link**, whose meaning lives only in a `title` tooltip. The Alpha feature's own page body carries nothing.
- **kgateway** has a good three-table maturity page and **zero** inline signal: its Alpha feature's page is indistinguishable from a GA one, and the maturity page is not even in the site's `/llms.txt`.
- **Knative Serving** defines its lifecycle stages carefully and then lists all 24 feature flags **with no stage on any of them**. Knative *Eventing*, on the same site, does it right — one table with a compound `Maturity` cell reading `Beta, disabled by default`.

So `/claim-classes.html` alone is not the system. **What stops this project shipping Istio's failure today is structural rather than a gate**: a claim exists only as a badge on the page that makes it, so a definitions page with no application is not a state the built component can express. Gate F of §5.2 — no orphan claim, no dangling reference — was written for a manifest and has nothing to check without one; it is not built. The residual risk is the reverse of Istio's: a page of prose with **no** badge at all, which nothing detects (§5.3).

**The rendering**, drawn from the four strongest examples:

| Borrowed from | What is taken |
|---|---|
| **SLSA** | One templated line under the heading — literally `Status: Approved` — where the **status word hyperlinks to the definitions page**. It is the cheapest mechanism in the survey and the one that makes a class impossible to render without also rendering its definition. **Not taken by the built component.** `ClaimClass` is non-interactive by design — nothing in it is focusable, and its visible text is hidden from assistive technology in favour of one announced sentence, which is only safe because there is no control (WCAG 2.5.3 governs controls). The badge therefore links nowhere, and a reader learns what `designed` means from `/claim-classes.html` in the navigation, not from the badge. That is a real cost against SLSA's pattern; making the badge a link would reopen the component's accessibility contract, which is `web/`'s decision, not this document's. |
| **Gateway API** | A collapsible, colour-bordered box whose summary *is* the claim and whose body carries the evidence, placed **beside the sub-feature, mid-page** — not only under the H1. `/reference/api-types/httproute/` carries four of them. This matters here because the concept and reference pages are exactly where a deep-linked reader arrives. |
| **Elastic `applies_to`** | Annotation at three levels — front matter, the line after a heading, and **inline at the start of a paragraph or list item** — with an explicit rule against annotating page titles. This is the only surveyed system that is genuinely per-*claim* rather than per-*feature*, and it is the model for a document whose unit is the sentence. |
| **Argo CD** | Maturity keyed to the **exact CRD property path, ConfigMap key and env var** (`Skip Application Reconcile \| metadata.annotations[argocd.argoproj.io/skip-reconcile] \| Alpha`), with the inline admonition and the central table **hyperlinking to each other in both directions**. That two-way link is what keeps the two from drifting, and it maps onto assayd directly: `spec.budget` → refused at admission; `auth: oauth` → `PolicyCompileFailed`; `GovernanceSkipped=AuthVerifiedOnOneReplica` → measured on one replica only. |

**The stroke carries the class, not colour.** The built badge tells the three classes apart by stroke and mark — filled `●` for `measured`, hairline `◐` for `designed`, dashed `○` for `not-built` — in ink, never in gold and never in hue, so the class survives greyscale and a colour-blind reader (WCAG 2.1 SC 1.4.1). Its inline form is a pill; its header form is a rule across the top of the block it governs.

**Cilium's mechanism is adopted as a supplement and never as the only signal.** Baking the stage into the H1 (`Standalone DNS Proxy (alpha)`) propagates it free into the sidebar, breadcrumb, search results and browser tab, which nothing else does. But Cilium's casing drifts between `(beta)` and `(Beta)` across its eight such pages, and its accompanying note links nowhere because the project has no definitions page. Taken as a supplement to the SLSA line, it is free reach; taken alone, it is the Istio failure with better propagation.

**One technique is adopted that no other project in the survey has: making the badge's absence a build failure.** Crossplane's Hugo partial reads a page's front-matter `state` and its required `alphaVersion`/`betaVersion`, and **`errorf`s — failing the build — if a page declares a state without a version.** Given this project's history, a convention people are asked to remember is the wrong shape. **The built equivalent is `resolveClaim` in `web/shared/src/claim-class.ts`**, which throws, failing the build, on a `measured` badge with no `test` or a `designed` badge with no `design` or `section`; `check-claims.sh` then fails a build whose badges name evidence that does not exist, and fails a build that rendered no badge at all. Neither fails a page that asserts behaviour with no badge.

## 5. Source of truth, and what keeps the site from going stale

### 5.1 The decision, and what was built instead

**What this document specified**: one claims manifest, `web/claims.yaml`, holding every classed claim on both domains; pages referencing claims by `id` and never restating the text; a Go test, `test/docs/claims_test.go`, checking the manifest against the repository under `make docs`.

**What PR #57 built, and what is true today**: no manifest and no Go test. **A claim is a `ClaimClass` element in the page, carrying its evidence inline** — `test=` for `measured`, `design=` and `section=` for `designed`, an optional `note=` for `not-built` — and the component emits that evidence as `data-claim-*` attributes on the built HTML. Two checks hold it:

1. `resolveClaim` (`web/shared/src/claim-class.ts`) throws at build time on a claim missing its required evidence.
2. `web/scripts/check-claims.sh` reads the **built** HTML of both targets and resolves every `data-claim-test` to a top-level `func <name>(` under `test/`, `internal/` or `api/`, and every `data-claim-design` to a `docs/designs/<NN>-*.md` file; it then runs `web/scripts/block_scope.py`, which fails two badges sharing one enclosing block (§5.5). It refuses to report success on a build with no HTML or no badges. It runs as its own step of `.github/workflows/web.yml`.

**This document now adopts the built shape for the first pages**, rather than asking for the manifest to be retrofitted before anything ships. The reasons: the built check reads what ships rather than a side file, which is the property §5.5 argues for; and the manifest's main benefit — one copy of a claim that appears on several pages — only pays once several pages exist. **The manifest is not withdrawn, it is deferred**, and the trigger for it is concrete: the first time one claim sentence must appear on a second page, or `/feature-status` is built (§7), whichever is first. Until then, a claim repeated on two pages is two copies that nothing compares, and that is stated here rather than discovered.

**One gap in the built check that no gate below would close on its own: `web.yml` runs only on changes under `web/**`.** Its trigger is `paths: ["web/**", ".github/workflows/web.yml"]`, so a pull request that renames or deletes a Go test a badge names runs `ci.yml` and not `web.yml`, merges green, and the badge dangles until the next change to `web/`. The check is real; its schedule is not the one the evidence moves on. The fix is owed and is `web/`'s: run `check-claims.sh` on changes to `test/**`, `internal/**`, `api/**` and `docs/designs/**` as well, or from `ci.yml`. **Closed by PR #67**: `web.yml`'s path filters now include `test/**`, `internal/**`, `api/**`, `docs/designs/**` and `docs/reference/**`, identical for push and pull request (a step fails if they differ), and a draft PR changing only a cited test's name was measured to run `web.yml` and fail at the claims step.

Three modes of page production follow, and every page in §3 is assigned one in §5.4:

| Mode | Meaning | Editing the page by hand is |
|---|---|---|
| **generated** | A build step emits the page from a repo file. | a bug — the change belongs in the holder |
| **derived-and-reviewed** | A human rewrites a repo document for a site audience. Named facts inside it are bound by a gate. | expected, within the gate |
| **hand-written** | No repository holder. | the only way |

**Alternatives considered** when the manifest was specified, kept because the argument still stands:

- *Per-page front matter, no manifest.* Rejected then, because the same claim appears on the landing page, `/feature-status` and `/roadmap`, and three copies is the shape of the original failure. **The built shape is per-element rather than per-page, and it has this cost** — §4.3 records it.
- *A release checklist.* Rejected on the standard this project already holds: a mechanism that requires a human to remember something is not a mechanism. It is named because it is what the project does **today** for `README.md`'s two lists and `CLAUDE.md`'s paragraph, and that is precisely the thing that went false once.
- *A lint over page prose for assertive verbs.* Rejected. `test/docs/superseded_test.go` works because each rule bans one specific known-false phrase and permits its retraction; a rule that bans a grammatical category produces false positives at a rate that trains people to add exemptions. **What is proposed instead** is extending that existing test's corpus to cover the site's content files (`web/**/*.mdx`, `web/**/*.astro`), so that the withdrawn guarantees it already bans cannot reappear on the website. **Not built**: its corpus today is `docs/` and the root entry documents.

**Consequences.** A claim whose evidence is a *mutation result* rather than a test — "three mutations kill it", which is the strongest evidence this repository produces — has no prop on the component; §12, O3 carries it. The site is built from a checkout, because `check-claims.sh` needs the Go tree beside the HTML.

### 5.2 The gates — which are built

The gates were specified as assertions in a Go test over the manifest. **Two exist in partial form, in `web/scripts/check-claims.sh`; six do not exist.** Each row says which.

| # | Gate, as specified | Catches | Built? |
|---|---|---|---|
| A | Every `measured` claim's test exists, in the file it names. | A renamed or deleted test leaving a `measured` claim standing. | **Partly.** `check-claims.sh` requires `^func <test>(` somewhere under `test/`, `internal/` or `api/`, and requires the name to be a plain Go identifier so a pattern cannot certify itself. There is no file to check it against. Since PR #67 it also runs when only the Go tree changes (§5.1). |
| B | Every `measured` claim whose test reaches a `t.Skip`, directly or through a helper, states the skip condition. | Rule 3 — the "true on k3d only" class of overclaim. | **Not built.** The component has no field for a condition; it must be in the sentence, and nothing checks that it is (§12, O8). |
| C | The test's layer agrees with its directory. | A unit test dressed as proof that a pod runs. | **Not built.** The badge carries no layer. |
| D | Every `designed` claim's design exists and contains its section literally. | write-spec's "a cross-reference to a section number that has moved". | **Partly.** `check-claims.sh` resolves `design 03` to `docs/designs/03-*.md` and fails if no such file exists. It **does not read the section**, so a `§3.5` that has moved or been deleted passes. |
| E | Every `designed` claim quotes a string found in its design's `- **Status**:` bullet. | A slice's approval boundary moving without the claim moving. | **Not built.** The badge carries no Status quote. |
| F | Every referenced claim exists, and every claim is referenced by a page. | Dangling references and orphan records. | **Not built, and has no subject without a manifest** (§4.4). |
| G | Each gate has a fixture that fails it. | The gate itself rotting — `test/docs/superseded_test.go` does this as `TestEveryRuleIsPinnedByAnIndependentFixture`, and rule 1 says a gate nothing can fail is not a gate. | **Not built.** `check-claims.sh` has no fixtures; no test plants a bad badge and expects the script to fail. |
| H | Every page and holder the claims name is asserted to exist **by path**, and the count of scanned pages is asserted against the count on disk. | **A file dropping out of the gate's own corpus** — §5.5's lesson. | **Not built as specified.** `check-claims.sh` refuses an empty `dist/` and a build with no badges, which is `TestAnEmptyCorpusIsAFailure`'s shape; it does not name pages by path, so one page dropping out of the build is invisible to it. |

### 5.3 What these checks do not catch — stated, not dressed up

- **That the named test measures the claim's sentence.** Nothing in CI reads English. A test can be kept by name while its body is gutted. The project's answer is mutation-checking (rule 1), which is a human loop, and it stays one. `check-claims.sh` says the same of itself: "that the named test actually proves the sentence the badge sits beside … is a human judgement and no script can make it."
- **That the named test passes.** `check-claims.sh` checks existence only; `make test` is what says a test passes.
- **That a page contains no unclassed claim.** Rejected as a lint above. A reviewer catches it or nobody does.
- **That a class is lowered when code is deleted.** The checks fire on missing *evidence*, not on missing *subject*.
- **That rule 4's present-indicative ban is honoured.** A reviewer catches it or nobody does.

### 5.4 Per-page source of truth, mode, and whether a gate protects it

**Read the enforcement column literally.** Three kinds of entry appear in it. **CI** names a check that runs on `main` today: `make docs`, `make verify` and `make conformance` in `.github/workflows/ci.yml` (the first and last added in `80b63b5`), or `check-claims.sh` and `check-links.sh` in `.github/workflows/web.yml` — since PR #67 on changes under `web/**`, `test/**`, `internal/**`, `api/**`, `docs/designs/**` and `docs/reference/**` (§5.1). **owed** names a check this document asks for that does not exist. **someone notices** means no check at all. An earlier revision named the absence of `make docs` from CI as its first cut-list item; it was true at `22b65c2`, where this branch started, and was fixed the same day. **The stronger finding is §5.5's, and it is not that a gate was missing.**

#### `assayd.io`

| Page | Holder(s) | Mode | Protected by |
|---|---|---|---|
| `/index.html` | the `ClaimClass` badges on the page; the governance sentence from `charts/assayd/templates/NOTES.txt` (both branches of `gateway.enabled`) | hand-written, with inline claims | CI (`check-claims.sh`: gates A and D, partly) for each badge; the prose, someone notices |
| `/claim-classes.html` | `web/shared/src/claim-class.ts`; this document §4 | hand-written — **exists**, `web/site/src/pages/claim-classes.astro` | the build, for the component it demonstrates; the prose, someone notices |
| `/concepts/*.html` | the badges on each page; the design section each concept explains | derived-and-reviewed | CI (A and D, partly) per badge; the explanation, someone notices |
| `/roadmap.html` | `docs/decisions/0030-*.md`; each design's own `- **Status**:` bullet; the badges on the page | the status table generated from the Status lines; the gap list and frame hand-written | the status table: **CI** — `make docs` runs `test/docs/approval_test.go` (below); the badges: CI (A and D, partly) |
| `/releases.html` | git tags; `docs/supply-chain.md`'s published-artifact table and its per-version verification dates; `.github/workflows/release.yml` | derived-and-reviewed | someone notices. **Not generated from tags alone**: `supply-chain.md` now records `v0.4.1` as tagged and never verified from outside, so a tag is not evidence of a published artifact (§11.6.3). A version with no recorded check renders as unverified, never as a release. There is also no CHANGELOG to generate notes from, so the notes are written by a person. |
| `/security.html` | `SECURITY.md`; the badges on the page | derived-and-reviewed | CI (A and D, partly) for the not-enforced list's badges; the reporting policy, someone notices |
| `/about.html` | `LICENSE`, `MAINTAINERS.md`, `GOVERNANCE.md`, `SECURITY.md` §Regulatory | derived-and-reviewed | someone notices |

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
| `/agent-contract` | `docs/agent-contract.md` | derived-and-reviewed | someone notices — though its own "Sources" table already names the file or test behind every row, which is the closest thing in the corpus to a claim with inline evidence, and the model for this page's badges |
| `/supply-chain` | `docs/supply-chain.md`; `.github/workflows/release.yml` | derived-and-reviewed | someone notices |
| `/feature-status` | the badges on the page — **no single holder exists** without the deferred manifest (§5.1) | hand-written until the manifest exists; generated after | CI (A and D, partly) per badge; that it is complete, someone notices |
| `/reference/crd-agent` | `api/v1alpha1/agent_types.go` → `config/crd/assayd.dev_agents.yaml`, via `cmd/refgen`, into `docs/reference/` | generated in the repository, and copied into the site by `web/scripts/copy-reference.sh` (PR #67) — see below the table | the Markdown: **CI** — `make verify` regenerates `docs/reference/` and fails if what is committed differs (PR #59). The site page: **CI** — `check-links.sh` over the built copy, and `web.yml` names it by path |
| *(note to the reference owner, not a decision of this document)* | The survey checked the two usual generators. **`ahmetb/gen-crd-api-reference-docs` has self-deprecated** — its README says "not super actively maintained… consider crd-ref-docs" and its last release was 2019, though cert-manager, Flux and Knative all still use it. **`elastic/crd-ref-docs` is active** (v0.3.0, 2026-02) and is what **Gateway API and Cluster API** both use. Separately: **agentgateway — the gateway assayd pins at 1.5.0 — renders its own CRD docs with `kubespec-render`**, and is indexed on `kubespec.dev` beside Gateway API, cert-manager, Cilium and Argo CD. If assayd's Agent CRD should sit next to agentgateway's, that is the shape to match. | — | — |
| `/reference/helm-values` | `charts/assayd/values.yaml`, via `cmd/refgen`, into `docs/reference/` | generated in the repository; copied into the site (PR #67) | the Markdown: **CI**, the same `make verify` gate. The site page: **CI**, as above |
| `/reference/conditions` | `api/v1alpha1` and `internal/controller`, read with `go/types` by `cmd/refgen`; the three per-reason annotations that are not in the code live in `internal/refgen/reasons.yaml` | generated in the repository; not yet ingested | the Markdown: **CI** — `make verify`, plus `TestNoConditionTypeIsALiteral`. The join pins the *set* of reasons; it does not prove the annotations are still true of the code, and the page says so. The site page: **CI**, as above |
| `/reference/flags` | `cmd/` flag registrations | generated | **owed** — PR #59 generates three reference pages, not six; this is not one of them |
| `/reference/labels` | `internal/compiler` and `internal/controller/runnamespace.go` label constants | generated | **owed** — not generated by PR #59 |
| `/reference/tested-versions` | `hack/e2e.sh` (`GWAPI_VERSION`, `AGW_VERSION`); `charts/assayd/Chart.yaml` `kubeVersion` | generated | `test/conformance/versions_test.go` already pins that the slice cases run the e2e's agentgateway release |
| `/archive/designs/<nn>` | `docs/designs/<nn>-*.md` | generated, verbatim | someone notices — gate E, which would have bound a cited Status line, is not built (§5.2) |
| `/archive/decisions/<nnnn>` | `docs/decisions/<nnnn>-*.md` | generated, verbatim | someone notices |
| `/archive/architecture` | `docs/architecture.md` | generated, verbatim, with a standing banner | someone notices |
| `/archive/reviews` | a directory listing of `docs/designs/reviews/**` | generated (ledger only — §9.1) | a count assertion; see §9.1 |
| `/contributing` | `CONTRIBUTING.md`, `GOVERNANCE.md`, `CODE_OF_CONDUCT.md` | derived-and-reviewed | someone notices |
| `/contributing/review` | `docs/agent-protocol.md` | derived-and-reviewed | someone notices |

**A page that restates a claim held elsewhere points at the holder.** Concretely, and these are the five holders that matter:

- `README.md`'s *What is true today* / *What is NOT true today* — restated by `/` and `/feature-status`.
- The *What remains untrue* paragraph in `AGENTS.md` (and its copy in `CLAUDE.md`) — restated by `/feature-status` and `/roadmap`.
- **Each design's own `- **Status**:` bullet — the holder for `/roadmap`'s status table, and for every `designed` claim's approval boundary.** `docs/designs/README.md`'s index summarises the Status lines; it is not a second holder. This is the human's decision of 2026-09-23, and **PR #66 (`9a74178`, merged) made the corpus agree with it**: ADR-0030 Amendment 1 withdraws the Consequences clause that said the index "arbitrates" (ADR-0030's body is left unedited, per the ADR rule, with a marker in its Status line), and `README.md` and `test/docs/superseded_test.go` no longer say it. An earlier revision of this document said `README.md`, `AGENTS.md` and the table "all say" the Status line wins; before #66 that was not true — `README.md`, ADR-0030 and the gate's own message said the opposite — and the sentence is withdrawn rather than silently made true by a later change.
- `api/v1alpha1` and `charts/assayd/values.yaml` — restated by `/reference/crd-agent` and `/reference/helm-values`, generated.
- git tags plus `docs/supply-chain.md` — restated by `/releases`.

**The roadmap reads the Status lines.** Until PR #66, **twenty-three** designs — 04–15 and 17–27 — opened their Status line with "approved", and the human had approved none of them; a generator reading them then would have published twenty-three false approvals under the claim that it read the source of truth. PR #66 corrected all twenty-three, so each now says "not approved by the human", and added two checks to `test/docs/approval_test.go`, both run by `make docs` in CI: `TestADesignsStatusLineAndItsIndexRowAgreeAboutApproval` fails when a Status line and its index row disagree about approval, whole, in part or not at all; `TestNoDesignClaimsAnApprovalTheHumanDidNotGive` pins that only designs 03 and 16 may claim an approval, and only of a part — its `humanApprovals` table records *which designs* and *whole or part*, **not which part**. The human's approvals are design 03's first slice (2026-09-12), design 16's first slice (2026-09-14), and design 03's amendments A84 and A85 (both 2026-09-23; A85 is recorded by PR #65). **So `/roadmap`'s status table may be generated from the Status lines, and those two tests are its protection** — the enforcement column above reads **CI** for it, for the question *approved, in part, or not*. What they do not establish, and the ADR amendment says so, is which part the human approved, or that the human decided anything at all; a new approval must be added to the test in the same change that records it.

**Closed by PR #67; the paragraph below is the finding as it stood.** `web/scripts/copy-reference.sh` copies `docs/reference/*.md` into the docs target before every Astro build of it — run by `@assayd/docs`'s own `build` and `dev`, not only by `build.sh`, because from `build.sh` alone `pnpm run build:docs` shipped no reference and three dead sidebar links while exiting 0 — and the sidebar autogenerates a Reference group. **The reference pages are generated into the repository, and are not on the site.** `cmd/refgen` writes `docs/reference/crd-agent.md`, `helm-values.md` and `conditions.md`, and `make verify` holds them to the code. **Nothing copies them into `web/docs/`**: `web/scripts/build.sh` runs the two Astro builds and nothing else, `web/docs/src/content/docs/` holds only the scaffold's two pages, and the Starlight sidebar (`web/docs/astro.config.mjs`) autogenerates one group, `guides`, and nothing else. **An ingestion step is owed, and its place is fixed**: it goes in `web/scripts/build.sh`, before the Astro build, copying `docs/reference/*.md` into the docs target's content directory, so that `web/scripts/check-links.sh` — which runs over the built `dist/` — checks every link in the copied pages. A copy made anywhere the link gate does not see would publish the generated pages' internal links unchecked, and those pages cite source files and sections by path. The sidebar entry is part of the same change.

### 5.5 The failure this section is actually designed against: a gate that ran and saw nothing

The lesson is not "a gate was missing". It is worse than that, and it is the reason gate H was specified.

`test/docs/superseded_test.go` has run on every `make test` since it was written, and in CI since `80b63b5`. It passed. **It had never once scanned either of the two documents that mattered most**, and PR #60, now merged, found and closed both holes:

- **`docs/architecture.md` — the file `AGENTS.md` calls canonical — was exempt from the day the gate was written.** `isFrozen` read the first 800 bytes for a document announcing its own supersession, so that a frozen ADR is not scanned. The file's Status line reads "superseded in part — read §18 before relying on this document", and the bare word `superseded` sits at byte 608, inside that window. "Superseded in part" is the *opposite* of frozen: the parts that stand are precisely what an implementer builds from. **PR #60 did not narrow the pattern; it removed it.** Freezing is now a **closed list**, `frozenDocuments` — ADR-0020 (superseded by ADR-0028), ADR-0033 and `docs/HANDOFF.md`, matched by exact repository-relative path — so no phrasing in a document can take it out of the gate. The test's own comment gives the reason: narrowing the pattern to spare "in part" fixed one sentence "and left the trapdoor". Both directions are pinned — `TestEveryFrozenDocumentSaysSo` and `TestEveryDocumentDeclaringItselfSupersededIsListed` — and `TestTheCanonicalDocumentIsNotFrozen` pins this file. (One comment in the test still names a `partiallySuperseded` function that the merged gate does not have.)
- **`docs/architecture.html` was never scanned because the walk stopped at the file extension.** It is the rendered face of the canonical document, hand-maintained beside it with no generator in between, and it carried the header claim "27/27 designs approved · 26 ADRs" — which was false — for weeks after both bodies had been corrected. PR #60 added `renderHTML`, and the gate scans `.html` too.

**The rule, and it is the one this whole section exists to teach: an unscanned file reports exactly the same silence as a clean one.** A green gate is evidence about the corpus the gate *saw*, and nothing at all about the corpus that exists. `TestAnEmptyCorpusIsAFailure` already guards that at the level of the whole tree; neither hole tripped it, because the corpus was not empty — it was one or two files short, and nothing counted.

PR #60's answer is `TestTheCanonicalDocumentIsScanned`, which names the files **by path** — the two architecture files and, since, `AGENTS.md`, `CLAUDE.md` and `README.md` — because a corpus-level guard cannot see one file going missing. **Gate H was that pattern applied to the site, and it is not built** (§5.2). `check-claims.sh` has the corpus-level half — it refuses an empty `dist/`, a target with no HTML, and a build with no badges — and not the by-path half, so a page that drops out of the site build takes its badges with it and the check stays green. That is this section's lesson, standing unanswered in the check that exists.

Two further limits PR #60 states about itself, which transfer directly and are not hypothetical:

- **Every rule is a lexical tripwire, not a semantic guarantee.** It catches the phrasings someone wrote down, and PR #60 records the corpus defeating that twice from the inside — once by emphasis inside a banned phrase, once because a rule knew only a reviewer's paraphrase of the live wording. This is §5.3's first bullet arriving from a different direction, and it is the argument against ever treating a green build as proof that a page is true.
- **A rescue is per block, and an HTML block is bigger than a table cell.** Markdown splits a table row into cells, so a retraction on one side cannot launder a claim on the other. HTML splits on block-level tags, so a `<div>` of inline `<span>` pills renders as **one** block, and a retraction in one pill rescues its neighbour. PR #60 measured this while writing it: re-planting the false header claim beside the corrected one did not fail the gate until the rescue clause was narrowed. **The consequence for the `ClaimClass` component: a claim badge and its claim must render as one block, and two claims must never share one.** An inline badge row is a surface on which one classed claim can vouch for an unclassed neighbour. **This one is built**: `web/scripts/block_scope.py`, run by `check-claims.sh`, parses the built HTML, keys every badge by the block-level element that encloses it, and fails any block holding two. It deliberately does not count `td`, `th` or `tr` as boundaries, because the gate it mirrors treats a block as bigger than a table cell. It does not — and cannot — check that the sentence beside the one badge is the sentence the badge's evidence proves.

### 5.6 The copy count, which is evidence against this proposal

The same claims are currently written out in **six** places: `README.md`'s two lists, `SECURITY.md`'s *What is NOT yet enforced*, the `AGENTS.md`/`CLAUDE.md` paragraph, `docs/architecture.md` §02 and §18's notes, `docs/install.md`'s opening paragraph, and design 02 §5. The site makes **seven** unless something is retired — and with inline badges rather than a manifest (§5.1), the seventh copy is itself one copy per page that repeats a claim.

The recommendation is not to retire them all. `AGENTS.md` and `CLAUDE.md` are narrative documents for contributors and read as prose for a reason; design 02 §5 is the authoritative list and must stay where an implementer reads it. The proposal is narrower and is one gate:

**Gate R (owed, and blocked on the deferred manifest): `README.md`'s *What is true today* bullets are generated from the manifest's `headline`-tagged `measured` records, and *What is NOT true today* from its `headline`-tagged `designed` and `not-built` records.** That is the highest-reach copy and the one that has already gone false. It needs a manifest to generate from, which PR #57 did not build (§5.1), and it requires editing `README.md`, which this change does not do. Until it lands, the honest description of the first release is that the site's badges are existence-checked and the repository's six copies are not checked at all.

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
| `the-revision-service` | Each revision gets a ClusterIP Service that selects only its own Pods, and **the UID the operator recorded when it created that Service decides whether an object at the revision's name is its own** — not the labels or the digest stamp on the object, which anyone who can `create` a Service in the run namespace can forge, and anyone who can `patch` one can strip. A matching recorded UID skips provenance entirely; any other object is judged on provenance and refused as `ForeignObject`, `DigestMismatch` or `Unstamped`, and an addressable one that passes may be adopted and recorded. Only the recorded UID authorises the delete-and-recreate of an unrepairable Service (one whose `spec.clusterIP` is `None`); an unrecorded object is refused, `RevisionServiceNotRecorded`, and never deleted. **Garbage collection of retired revisions is the one path still decided by name** — the immutable `<agent>-<revision>` name, corroborated by the UID label. Design 02 A77 (PR #54); the reasons it added — `ForeignObject` and `Unstamped` on `RevisionHashCollision` (both grounds, for the workload as well as the Service, used to report `DigestMismatch`), and `RevisionServiceNotRecorded`, `RevisionServiceRecordNotKept`, `RevisionServiceReplaceFailed`, `RevisionServiceReplaceHeld`, `RevisionServiceUnreadable` and `ActiveRevisionServiceUnaddressable` — are on `docs/reference/conditions.md`, and so is the change A77 made to an existing one: `ServiceRejected` used to cover only an `Invalid` render and now covers any non-transient API-server rejection of the Service write, an admission `Forbidden` included, and gained a re-check and a remedy. | `measured` | `TestAnUnrepairableRevisionServiceIsDeletedAndRecreated`, `TestTheReplaceDeletesTheObjectItReadAndNotTheName`, `TestAnUnrepairableServiceThatIsNotOursIsRefusedAndNeverDeleted`, `TestAPlainServicePlantedAtARevisionsNameIsRefusedForWantOfProvenance`, `TestAnUnstampedServiceAtAVouchedRevisionIsAdoptedAndRestamped`, `TestTheReplaceBoundIsNotWritableByWhoeverBreaksTheService` (`test/envtest/service_test.go`); `TestARecordIsReplacedOnlyAtTheSameRevisionAndDigest` (`internal/controller/service_record_test.go`); `TestDeletingAnAgentCollectsItsServices` (`test/envtest/service_test.go`) for collection. **All envtest or unit — no kubelet**, so none of them shows traffic reaching or leaving a Service; the page must say so (§4.2 rule 2) |
| `standards-only` | Everything assayd speaks is an existing standard — A2A, MCP, Gateway API, OCI — and where one is unimplemented the page says which, rather than implying a binding exists. | `measured` for A2A 1.0, MCP Streamable HTTP and Gateway API; `not-built` for SPIFFE, OASF and OTel GenAI | `test/e2e/mcp_test.go`, `test/e2e/responder_test.go`; `internal/directory/` and `internal/registry/` do not exist in git — `internal/` holds `compiler`, `controller`, `refgen` and `revision` — and no SPIRE, Zitadel or OTel binding exists in `internal/`, `api/`, `cmd/` or `charts/` |

Two constraints on every concept page:

- **The diagram on a concept page carries the class of what it depicts**, through `Figure`'s `claim` prop, and names the version it depicts through `depicts` (§4.2 rule 6). Every diagram is a generated `.webp`; there is no Mermaid and no diagram-as-code anywhere on either target, by the human's decision. Of the eight existing plates, only `01-architecture` and `06-api-surface` are even candidates, and both depict the target surface rather than the shipped one. `08-rollout-lifecycle` shows a `Canary` state design 16's approved slice explicitly never enters. New diagrams are owed for `the-auth-transaction` and `create-lock-adopt`, which have no plate at all and are the two concepts a reader most needs a picture for.
- **A concept page names what it is not.** `governance-at-the-gateway` states in its own body that no `AgentgatewayBackend` is emitted; `reserved-writes` states that `DELETE` is not reserved. Leaving the negation to `/feature-status` is how a concept page becomes a promise.

## 7. The status page

`/feature-status` answers one question: **is the thing I need actually enforced, at the version I run?**

**It is not called `/status`, because "status" reads as uptime.** `status.sigstore.dev` is literally an uptime dashboard, and a reader following a link labelled "status" from a landing page expects to learn whether the service is up. The surveyed names for this page are `Feature Stages` (Istio), `Feature Maturity` (Argo CD, kgateway) and `/status/` (OpenTelemetry, which gets away with it because it has no hosted service). `Feature status` is the closest conventional label; the page differs from all of them in being keyed to **measurement** rather than to intent, which the survey found no project doing — so the convention is borrowed for the name and not for the content.

**How it was meant to avoid being a third hand-maintained copy: by being generated from the claims manifest, versioned by git along with the code it describes.** That manifest was not built (§5.1). **So this page is the trigger for building it**: `/feature-status` is, by definition, every claim on the site in one place, and hand-writing it as a page of inline badges would make it a copy of every other page with nothing checking the copies agree. It does not ship hand-written. Everything below describes the page as generated from a manifest, and is owed with it.

**Per-version presentation.**

- The page has a version selector listing every release tag from the first tag that carries a manifest onward. Each entry renders that tag's manifest. A tag predating the manifest renders a stub naming the tag and linking `README.md` at that tag — honest, and cheap.
- The default view is the **latest release tag**, not `main`. A reader on `/feature-status` is asking about the version they can install. `main` is available as an explicit selection and is labelled unreleased.
- A **delta view** between two selections lists every claim whose class changed, in both directions. That view is also the input to `/releases`, so the release notes and the status page cannot disagree.

**Layout.** Three sections, in this order: `measured` (what is enforced), `designed` (what a design specifies and nothing enforces), `not-built`. Within `measured`, claims whose tests can skip are grouped under a sub-heading naming the condition — so "true on k3d only" is a heading a reader cannot skim past, not a footnote.

**What the page must not do.** It must not summarise. Each claim's sentence is rendered verbatim, because a summary of a claim list is the artefact this whole document exists to prevent.

**Where this sits against prior art.** The survey found **no project publishing what it claims against what it has measured**. The nearest things are Gateway API's per-implementation conformance matrix — which is the only mechanism found anywhere where "does not work" is *produced by tests* rather than asserted by a writer, complete with ❌ cells and an `Extended Features: 25/27` count — and OpenTelemetry's `/status/` per-signal-per-language grid, which states outright that a signal's status in the specification may differ from its status in an SDK. Argo CD's feature-maturity page is a third relative: it indexes *only* unstable surface, so the page is by construction a list of what not to rely on.

Dedicated "limitations" or "non-goals" pages essentially do not exist in this cohort; the genre exemplar is outside it, in SQLite's *Quirks, Caveats, and Gotchas*, which is a first-class maintained document rather than a caveat scattered through prose.

**The consequence for assayd is a claim, not a boast, and it should be stated carefully on the page**: `/feature-status` is keyed to measurement, and this project already produces the input — `AuthVerifiedOnOneReplica`, "the policy half is still not measured", "nothing pages, because design 10 is not built". The ambitious version, which Gateway API shows is reachable, is to **generate the `measured` section from a test run rather than from a manifest field**. Nothing here proposes that, and §12, O3 carries why: the manifest asserts that a test exists, and only a person can assert that it measures the sentence.

## 8. The roadmap page

Three sections.

### 8.1 The slice

ADR-0030's build order, one row per step, each carrying a class. The ADR is the holder; each row's class is a `ClaimClass` badge on the page, with its evidence inline (§4.3).

| Step | State |
|---|---|
| 1. The supported slice, with unsupported capabilities rejected at the boundary | `measured` — `oauth` and a stored `spec.budget` are refused (`TestABudgetIsRefusedWhileNothingEnforcesIt`, `TestAnExposedAgentMustStateItsAuthentication`) |
| 2. Workload materialization — revision-scoped Services, card fetch, the runtime env contract, retained-revision selection | `measured` (`test/envtest/service_test.go`, `card_test.go`, `injectedenv_test.go`, `release_test.go`) |
| 3. The smallest real policy path through gateway, identity and network rules | **partial** — the gateway half is `measured` and operator-emitted; the network half is `measured` only under a **hand-authored** rule on a CNI that enforces NetworkPolicy, and the operator materializes none; the identity half is `not-built` (design 06 has no implementation, and `internal/registry/` does not exist in git) |
| 4. A narrow design-16 gate, blue/green before canaries | `designed` — design 16 §1.1 is approved; its own Status line says "Nothing this design specifies is implemented" |
| 5. One real domain on a curated knowledge snapshot | `not-built` |
| 6. A second framework, measured against kagent plus existing delivery tooling | `not-built` |

### 8.2 The design status table

One row per design, **generated, quoting each design's own `- **Status**:` bullet verbatim rather than summarising it**. The Status line is the holder and `docs/designs/README.md`'s index is a summary of it — the human's decision of 2026-09-23, written into ADR-0030 Amendment 1 by PR #66.

**The table can be generated now.** PR #66 corrected the twenty-three Status lines (04–15, 17–27) that claimed an approval the human never gave, and `make docs` now fails if a Status line and its index row disagree about approval, or if any design other than 03 and 16 claims an approval, or either claims more than a part (§5.4). The test does not check which part. A generator reading the Status lines therefore reads text that a CI gate holds to the human's decisions at that grain — which designs, whole or part — and no finer. The earlier revision of this section said the table must wait; it waited for exactly this change.

The generator truncates nothing and renders no shortened form. Three Status lines here run to several hundred words, and every attempt to compress one is the mechanism that produced the false line in the first place. Long rows are a collapsed disclosure, expanded by default on `designed` and `not-built` rows.

**And the archive is browsable by state, where the state is the claim class.** This is taken from Gateway API, and the survey named it the single most transferable structure found. Gateway API has **no roadmap page at all**: its enhancement proposals are browsable *By State* — `Provisional`, `Prototyping`, `Implementable`, `Experimental`, `Standard`, `Completed`, `Deferred`, `Declined`, `Withdrawn` — and those states are **the same vocabulary as its release channels**, so "where is this in the design pipeline" and "can I use it" are one axis rather than two that can drift apart.

That is a structural answer to the exact failure `CLAUDE.md` opens by recording. Here the two axes are already one, because a design's state *is* the claim class of what it specifies: a design whose slice is implemented and tested produces `measured` claims, an approved-but-unimplemented slice produces `designed` claims, and a hypothesis under ADR-0030 produces `not-built` ones. So `/archive/designs/` is faceted by claim class and needs no second vocabulary, and "approved" stops being a word that can drift from "built" because it is not the word on the facet.

The counter-example is Tekton, which runs the two ladders separately — TEP status (`proposed`/`implementable`/`implementing`/`implemented`/`deferred`/`withdrawn`) against API stability (`alpha`/`beta`/`stable`) — joined only by a `Proposal` column in a hand-maintained table. That table has visibly drifted: one row has a blank Beta Release and a version sitting in the wrong column. Two vocabularies joined by hand is the shape to avoid.

**§8.1's slice table takes its status labels from Flux**, whose roadmap the survey rated the most honest found: each milestone opens with a literal status line (`Status: Completed - Flux v2.8 GA`), unshipped milestones are labelled `provisional` with the reason ("subject to change based on the project's priorities and the community's feedback"), and items include **explicit removals and end-of-support**, not only additions.

### 8.3 Open follow-ups, under the disclosure posture

**The rule, stated once at the top of the section and applied to every item, is the human's decision: a security gap is named and linked, never given with a reproduction recipe.** Concretely: name the class, link the design section that already describes it, state the observable condition an operator would see, and stop before the sequence. The repository is public and the linked section contains everything, so the site withholds no information — it withholds *packaging*. That distinction is the whole posture, and it is written on the page so a reader is not left guessing whether something is being hidden.

| Gap | Class of defect | Links to | Observable |
|---|---|---|---|
| A published route can be served unauthenticated by a gateway replica that has not yet taken the Agent's policy. One probe proves one replica, and no replica count is declared. | Authentication bypass, window, multi-replica only | design 03 §3.3.3 and §8.1 | `GovernanceSkipped=False`, reason `AuthVerifiedOnOneReplica` |
| Deleting the API-key ConfigMap takes every key to `401`, and nothing reports it. `DELETE` is not reserved by admission, and the operator does not watch key sets. | Availability, not bypass | design 03 §3.4.4 ("The key set is outside every gate") and §8.1's reservation scope ("`DELETE` is not reserved") | **nothing** — that is the defect |
| A route published before the compiler, or one whose status was lost, stays unauthenticated and is never adopted. | Authentication bypass, upgrade and restore path | design 03 §3.3.3 | `GovernanceSkipped=CompilerUpgradeUnsupported` |
| A served policy the Gateway reports as not wholly in force is announced, not withdrawn. Measured on agentgateway 1.5.0, the report covers two different states: one still refuses anonymous callers, and one does not until the operator's next pass repairs it. | Authentication bypass, window, in one of the two reported states | design 03 §3.3.3 and §11 (A80–A84) | `PolicyApplyIncomplete` and `GovernanceSkipped=True`, both `AuthPolicyNotAttached`; since A83 the message says which of the Gateway's answers fired |
| No NetworkPolicy is materialized in any namespace, so an agent Pod is reachable directly and any gateway control is bypassable. | Bypass, by default, on every install | design 07 A6.6; `SECURITY.md` | none — this is the documented default |
| A policy deleted between a pass's check and its route update leaves the route published without it until the next pass. | Authentication bypass, window | design 03 §3.3.3 | depends where the next pass finds the transaction |

**Nothing pages.** Design 10 is not built and no alert rule ships in this repository, so every "observable" above is a condition a human must go and read. The roadmap page says that in the section, because a list of conditions implies a monitor.

## 9. What does not go on the site, and why

### 9.1 `docs/designs/reviews/**` is not published as pages

*This section carries no file or line counts. Both directories grow with every review and note, and an earlier revision's counts were stale within a day; the ledger this section proposes is what counts them.*

**Decision: publish a generated *ledger*, not the review texts.** The ledger is one page, `/archive/reviews`, with one row per review: design, date, reviewer family (same-model / fresh-context / cross-family), verdict, and finding counts, linking each row to the file on GitHub.

The argument, in order:

1. **The repository is public, so this is not concealment.** Every file is already readable. The question is only whether each becomes a site page with site navigation, site search and the implicit currency a site page carries.
2. **A review is a snapshot of a document that has since changed, and the project's own rule forbids updating it.** write-spec: "Review records — `docs/designs/reviews/**` — are history: never edit them to match a later decision." A site page is the one artefact a reader assumes is current. Publishing every review as a never-updated page into a searchable site creates exactly the failure this IA exists to prevent: a reader finds "BLOCKER: authorization bypass" in site search and has no way to learn it was answered eight amendments ago.
3. **A review is step-by-step, which the disclosure posture forbids.** §8.3 permits naming a class and linking a section. A review contains reproduction, sequence and the patch that would fix it. Publishing all of them as site pages would be the most detailed unfixed-defect catalogue this project could produce, and would sit one search box away from the roadmap that carefully does not produce it.
4. **The ledger carries the claim the reviews are evidence for, and carries it better.** The claim is "this project reviews adversarially, repeatedly, and across model families" — one row per review, with verdicts and reviewer families, says that in one screen. The full texts say it in tens of thousands of lines that nobody reads.
5. **The ledger is checkable and the corpus is not.** A test can assert that the ledger's row count equals the file count in `docs/designs/reviews/`. Nothing can assert that a published page per review is still accurate, because they are not meant to be.

`/contributing/review`, derived from `docs/agent-protocol.md`, publishes what "independent" means — the reviewer-family gradient, findings travelling as committed files, and the rule that two reviewers never reconcile. Without that page the ledger's reviewer-family column is unreadable, which is why `docs/agent-protocol.md` is on the site at all despite being an internal working document.

### 9.2 `docs/architecture.html` is not published — and the reason has changed

An earlier revision of this document called this file the most dangerous artefact in the tree, because it carried three claims that were false: "27/27 designs approved · 26 ADRs" in its header and footer, "Every component below is designed, adversarially critiqued, and approved", and "Every claim in this document is backed by an ADR and a critique-passed design" — the sentence `CLAUDE.md` opens by recording as false. It recommended deleting or regenerating the file.

**That was fixed by PR #60, now merged, and the fix is better than the recommendation.** The header now reads `status superseded in part · 27 designs, not 27 approved · 34 ADRs`; the approval sentence is rewritten in place; the footer claim is quoted and withdrawn. More importantly, the file is now **inside the gate**: `test/docs/superseded_test.go` scans `.html` through `renderHTML`, and `TestTheCanonicalDocumentIsScanned` names it by path so the exclusion cannot quietly return (§5.5).

**The page still does not go on the site, on a different and smaller argument.** It is a hand-maintained rendering of `docs/architecture.md` with no generator between the two — PR #60's own comment says so, and that duplication is exactly how the header survived correction for weeks. Publishing it would make a third rendering of one document. `/archive/architecture` renders the Markdown (§9.3); the HTML stays in the repository as the reviewers' artefact it is.

### 9.3 `docs/architecture.md` is published only inside the archive

`docs/architecture.md` is canonical and stays canonical in the repository. It is **not** the site's architecture page and does not appear in the main navigation. It is at `/archive/architecture`, verbatim, under a standing banner.

The reason: its honest warnings are per-*section* ("Designed, not shipped" at the head of §02, §03, §12, §16, §17), and a reader skimming a table two screens below the warning takes the table and leaves the warning. §4's claim classes are per-*claim*, which is the granularity the failure demands. The concepts section (§6) is the site's architecture story, and it is classed sentence by sentence.

An earlier revision listed three things in it that escaped their own section's fence and must not be lifted elsewhere. **PR #60 fenced two of them** — doctrine rules 5 and 6 now state what CI actually runs and that only one tier renders, and §07's bullets each carry an inline `*(intended)*` marker so a lifted bullet carries its own fence (§11.3). **The third stands**: §16's competitor table, §9.4.

One new hazard replaces the two that were fixed, and it applies to the whole archive rather than to this file: **PR #60's corrections quote the sentences they withdraw**, so `docs/architecture.md` now contains "27/27", "12–18-month window" and "the design phase is complete" as *quotations inside retractions*. Rendering the file verbatim is safe; extracting sentences from it is not. §11.6.1 makes that a rule rather than a warning.

### 9.4 The competitor comparison table is not published

`docs/architecture.md` §16's table has six capability rows. Its own header says the assayd column "states the target, not what ships", and only the gateway-path row is true today. A comparison table on which five of six rows would render `designed` or `not-built` is not a comparison; it is the original failure with a competitor's name beside it.

The first-mover claim that used to close the section — "nobody ships it as a k8s primitive — 12–18-month window" — was withdrawn by PR #60, which quotes and retracts it at the close of §16 rather than deleting it. **The table itself is unchanged**, so the argument here is the one it always was: five of its six rows are not `measured`.

A comparison page can exist when its rows are `measured`. Until then there is none, and the landing page's positioning is the thesis sentence and the honest list, not a grid.

### 9.5 The rest

| Not published | Why |
|---|---|
| `CLAUDE.md`, `AGENTS.md` | Contributor-facing working rules. `/feature-status` and `/roadmap` carry what a user needs from them; republishing the paragraph creates the copy §5.6 is trying to reduce. |
| `docs/HANDOFF.md` | Its own first line marks it a dated record, superseded and deliberately not rewritten. That is the exact class of document a site page must never be, because a site page is the one artefact a reader assumes is current. |
| `docs/research/**` as a section | Each note is dated evidence about a third party, several past their re-verify dates. A stale note on our own site reads as our current position on someone else's product. Notes are linked individually from the concept or reference page that cites one. |
| `docs/requirements.md` as a page | Of 30 FR/NFR identifiers, **zero `FR-` identifiers appear in any code or test file**, and only NFR-8 and NFR-3 appear at all. Publishing a requirements list where the overwhelming majority is `not-built` adds nothing `/roadmap` does not say better. Individual requirements are cited from claims where they are the holder. |
| The "≤8 core pods" figure as a landing-page footprint claim | It is CI-enforced *as a budget* (`TestCorePodBudget`, currently measuring 2 of 8) but it is a **target** as a description: the chart contains exactly one `Deployment`. It may appear on `/concepts` or `/roadmap` as `designed`; it may not appear on `/` as a footprint. |
| Six of the eight diagram plates, above the fold anywhere | Rule 6 of §4.2. |

## 10. First-ship cut list

Ordered. Each item is shippable on its own, and each is a prerequisite of the one below it unless stated.

| # | Ship | Why here |
|---|---|---|
| 0 | ~~`make docs` added to `.github/workflows/ci.yml`~~ — **void.** | Kept as a struck row rather than deleted, because a cut list that silently loses its first item is unreviewable. It was true at `22b65c2`, where this branch started; `80b63b5` added `make docs` **and** `make conformance` to CI the same day. The finding that replaces it is §5.5's, it is not a missing gate, and it is not this document's. |
| 1 | **The claim component and its check — built, in part, by PR #57.** `ClaimClass` with inline evidence, `resolveClaim`'s build-time throw, `web/scripts/check-claims.sh` over the built HTML, `web/scripts/block_scope.py`, and `/claim-classes.html`, all run from `.github/workflows/web.yml`. **What remains of this item, in order**: ~~(a) run `check-claims.sh` when the Go tree or `docs/designs/` changes, not only `web/**` (§5.1)~~ — done by PR #67; (b) read the section in gate D; (c) fixtures that fail each check, gate G; (d) the by-path page list, gate H. Gates B, C, E and F are not built and wait for the manifest (§5.2). | Infrastructure, not a page. Every badge on every page depends on it. (a) comes first because without it the check that exists is blind on exactly the change that breaks it. |
| 2 | `assayd.io/index.html`, with `/claim-classes.html` already built | The landing page is the highest-reach surface, and a badge needs its definitions page to exist — it does. |
| 3 | **The claims manifest, then `assayd.dev/feature-status.html` generated from it** | Deferred from item 1 (§5.1): the manifest's trigger is this page, which is every claim in one place and must not be a hand-written copy (§7). The landing page's honest list has to resolve to something. |
| 4 | `assayd.dev/guides/install`, `/guides/api-keys`, `/guides/troubleshooting` | The first thing a reader can actually *do*. Derived from `docs/install.md`, which is already written to this standard. |
| 5 | `assayd.dev/agent-contract` | The second thing a reader can do. Its existing Sources table means it needs the least rewriting of any page here. |
| 6 | `assayd.io/roadmap.html` | Needs items 1 and 3. Its §8.3 gap list is the reason the disclosure posture was decided, so it should not wait. Its design status table is generated from the Status lines, which PR #66 corrected and `make docs` holds to the human's approvals (§8.2). |
| 7 | **Gate R — `README.md` generated from the manifest** | Not a page. Needs item 3's manifest. Deliberately after the roadmap, because it edits a file outside the site and should land once the manifest's shape has survived real use. |
| 8 | `assayd.dev/supply-chain` | Standalone; a reader verifying signatures needs no other page. |
| 9 | `assayd.io/concepts/*` — eight pages | Deferred behind the guides on purpose: a concept page is only worth reading once the reader has hit the thing it explains. Order within: `governance-at-the-gateway`, `revisions-and-cards`, `create-lock-adopt`, `attribution`, `the-auth-transaction`, `reserved-writes`, `the-revision-service`, `standards-only`. |
| 10 | `assayd.dev/reference/*` | **Three of the six are generated into the repository, and since PR #67 all three are on the site** (§5.4); what follows is the item as it stood. **Three of the six are generated into the repository, and none is on the site.** PR #59 (merged) generates `docs/reference/crd-agent.md`, `helm-values.md` and `conditions.md` from `cmd/refgen`, gated by `make verify` in CI. **An ingestion step is owed**: a copy into the docs target in `web/scripts/build.sh`, before the Astro build, so that `web/scripts/check-links.sh` runs over the copied pages, plus a sidebar entry in `web/docs/astro.config.mjs` (§5.4). `flags`, `labels` and `tested-versions` are owed and are the reference owner's, not the site's. |
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

An earlier revision of this document listed 22 claims in the corpus as false or stale, found while verifying it. **PR #60 (`4f96ca7`, merged) addressed all 22.** Since then, one has gone stale again (row 6), for the reason §11.2 gives.

**This section is rewritten rather than deleted, and the reason is the subject of this whole document.** A list of defects that have been fixed, published as a committed record, reads as a to-do list. The same shape was found in design 03 §11 on 2026-09-23, where A82's entry still read "The fix is OWED" for something A83 had fixed. A record wrong in the safe direction is still wrong.

**Rows cite a section, a heading or a symbol, never a line number.** The first revision cited lines, and they had drifted within a day. Each row was re-verified against `main` at `c4e5020`. Three outcomes:

- **Fixed** — the claim is gone, or is present only inside an explicit retraction.
- **Changed shape** — the text a site author will find is not what the old row described, and the difference constrains the site. These matter most and each carries its consequence.
- **Live** — nothing has changed, or the fix has gone stale.

### 11.1 The three that blocked a public site — all fixed, two changed shape

| # | Old finding | Now |
|---|---|---|
| 1 | `docs/architecture.html` carried three claims that were false: "27/27 designs approved · 26 ADRs" in its header and footer, and "Every component below is designed, adversarially critiqued, and approved" in its body | **Fixed.** The header's `meta-row` reads `status superseded in part · 27 designs, not 27 approved · 34 ADRs (0020 and 0033 superseded)`; the approval sentence is rewritten in place; the footer's "Every claim in this document is backed by an ADR and a critique-passed design" is quoted and withdrawn in the footer paragraph. **And the file is now inside the gate** — §5.5. |
| 2 | `docs/architecture.md` §16 sold a "12–18-month window" | **Fixed, changed shape.** The sentence is not deleted: the close of §16 now quotes it and withdraws it — *"The sentence that used to close this section is withdrawn, and is quoted here rather than deleted."* The string `12–18` is therefore still in the file, inside retractions. |
| 3 | ADR-0026's Consequences said the design phase was complete with every design approved, and that implementation may begin — a sentence that was false | **Corrected, not rewritten — and the false sentence is still in the Consequences bullet, deliberately.** The Status line carries an Amendment 2 marker saying the clause "is withdrawn by ADR-0030", and Amendment 2 withdraws it, but the Consequences body is untouched, because the `adr` skill forbids editing an ADR's content to match a later decision. **This has a direct IA consequence — §11.6.2.** |

### 11.2 Counts and cross-references — all fixed, one of them twice

| # | Old finding | Now |
|---|---|---|
| 4 | "32 ADRs"; "12 dated notes" | **Fixed in §20, The decision record** — "**34 ADRs**, 0001–0034, of which 0020 and 0033 are superseded" and "**33 dated notes** plus the `paper/` set", with the stale pair named. |
| 5 | The ADR index stopped at 0027 | **Fixed.** Rows 0028–0034 exist, each carrying its own status — 0020 and 0033 marked superseded, 0030 flagged as "the ADR that governs what may be claimed anywhere in this document". |
| 6 | "design 02 carries eleven (A1–A11)" | **Fixed, twice.** PR #60 corrected it in §20's *Process note* to "seventy-six, A1–A76"; design 02 A77 (PR #54) then made that stale, and it now reads "**seventy-seven**, A1–A77", recording both earlier values. **The lesson for the site is the row itself**: a hand-typed count of a growing set is stale on the next amendment, which is why §3.2 cites `docs/reference/conditions.md` for reason counts and §9 carries no file counts. |
| 7 | `# API group TBD at rename` | **Fixed.** The string is gone from the corpus. |
| 8 | R1 both open and closed in one file | **Fixed in §19, Risks held honestly** — the risk row now says what R1 is not: *"R1 is not what tracks that, and this row used to say it did."* |
| 9 | The agentgateway floor stated as v1.4.1 against an e2e on 1.5.0 | **Fixed in §20's *Open items carried into implementation*** — *"A floor is not the only version that runs"*, with the two-phase conformance run named. |

### 11.3 Present-tense escapes — all fixed, and one changed shape usefully

| # | Old finding | Now |
|---|---|---|
| 10 | Doctrine rule 5 listed four distros as CI targets | **Fixed in §01, The lightweight doctrine.** Rule 5 now reads "**Two distros are a CI target, not four**", names `hack/e2e.sh`'s non-zero exit on anything else, and says minikube and k3s are "intended and untested". |
| 11 | Doctrine rule 6 claimed independently removable tiers | **Fixed in §01.** "**removability is untested because only one tier renders**", citing `_helpers.tpl`'s `fail` and what `plus` would add. |
| 12 | §07's bullets asserted alert rules, OTel spans and JetStream receipts in the present tense | **Fixed, changed shape — and the new shape helps the site.** Each bullet in §07, Harness & observability, now carries its own `*(intended)*` marker inline, and the preamble says the fence "applies to every bullet below, not to one clause in one of them". **A person lifting a bullet now lifts its marker with it**, which is what §5.4's derived-and-reviewed mode needs. |
| 13 | §17's "≈ 8 pods" ledger summing to 9 | **Fixed in §17, Weight budget** — "**8 pods at the low end of that range and 9 at the high end**", and the "≈" is named as having "hid a row that breaks the rule rather than a rounding". |

### 11.4 Requirements — all four fixed, all changed shape

`docs/requirements.md` now keeps each original wording and states the gap beside it, rather than lowering its bar silently. Its opening is explicit: *"a requirements document that lowers its bar without saying so is the defect this correction exists to fix."*

| # | Old finding | Now |
|---|---|---|
| 14 | "Each requirement is testable; NFRs are release gates" | **Fixed in the document's opening.** Both halves withdrawn, with the grep result stated: a standalone `FR-` id returns **zero** matches across `api/ internal/ cmd/ test/ config/ charts/ hack/`, and every apparent hit is a substring of `NFR-`. |
| 15 | NFR-2's "only stateful dependencies, **ever**" | **Fixed in NFR-2's row** — the enforced rule is named as an allowlist of three, with `openobserve` recorded in the test as "observability sink, not substrate". |
| 16 | NFR-3's "full e2e on k3d + kind" | **Fixed in NFR-3's row** — "**Two distros are exercised, and only one of them fully.**" |
| 17 | NFR-7's installable `plus` | **Fixed in NFR-7's row** — "**`plus` does not render at all today**, so this requirement is unmet". |

### 11.5 Release and supply chain — all four fixed; one answers an open item

| # | Old finding | Now |
|---|---|---|
| 18 | "The current release is v0.4.0" against a tagged `v0.4.1` | **Fixed in *What is published, and where*, and it answers §12's O2 — as "nobody has checked".** The page states that `v0.4.1` is the newest tag and *"this page has not been re-verified against it… no `cosign verify` of them is recorded"*, and closes with *"Read a version claim here as 'last checked', never as 'current'."* **The IA consequence is in §11.6.3.** |
| 19 | "published only on a `v*` tag" | **Fixed in the same section** — `workflow_dispatch` with a `tag` input publishes identically, and is how `v0.2.0` was re-released. |
| 20 | The table listed SLSA provenance unconditionally | **Fixed in the published-artifact table and the note under it** — the row carries "**provenance is conditional, see below**", and the visibility guard is explained. |
| 21 | The workflow never verifies its own provenance | **Fixed in the same section and in *What is NOT yet true*** — stated outright, with the consequence: *"Every provenance statement on this page is a dated manual check by a person."* |

### 11.6 What PR #60's shapes require of the site

Four of the fixes above change what a site author will find, in ways this document must answer rather than note.

1. **Withdrawn claims are now quoted verbatim inside their own retractions**, in at least eight places across `architecture.md`, `requirements.md`, `supply-chain.md` and ADR-0026's Amendment 2. That is correct for the corpus — write-spec requires that a correction quote what it retracts — and it is a trap for any pipeline that extracts *sentences*. **The rule that follows: no page on either site is produced by extracting sentences from repository prose.** A page is generated whole from a repository file, or it renders a whole block with its surrounding retraction intact, or it is rewritten by a person. There is no third mode, and §5.1's three modes already encode this — this is the evidence for why.

2. **`/archive/decisions/0026` renders a false sentence by design, and the repository's gate reads it through a head-level rescue that the site does not have.** ADR-0026's Consequences bullet still carries the claim, withdrawn by ADR-0030, that the design phase was complete; the `adr` skill forbids editing it. An earlier revision of this document said PR #60's `blanket-approval-claim` rule exempted `docs/decisions/` with an `exceptDir`. **That was PR #60's first version, and it is not what merged.** The merged gate has no directory exemption: freezing is the closed list `frozenDocuments` (§5.5), which does not include ADR-0026, so ADR-0026 **is scanned**, and the rule's `headRescue` spares it only while its **head** says the Consequences line "is withdrawn by ADR-0030" — delete the marker and the gate reports the ADR (`TestTheCorrectedADRKeepsItsMarker`). The rescue is restricted to ADRs (`TestTheHeadRescueIsRestrictedToADRs`). **So the premise changes and the requirement does not**: the repository keeps the sentence honest by pairing it with a marker at the top of the file, and a reader who deep-links to `#consequences` on the site sees the sentence and not the head. **The requirement lands on the site**: an archive page rendering a document verbatim must render its Status line and every Amendment marker **adjacent to the corrected clause**, not only at the top of the page. This is §4.4's Gateway API pattern — the box beside the sub-feature rather than under the H1 — and here it is load-bearing rather than stylistic. `/archive` carries `someone notices` in §5.4's enforcement column, and this is the specific thing they must notice.

3. **A tag is not evidence of a published, verified artifact.** `supply-chain.md` records that `v0.4.1` is tagged and unverified. `/releases` therefore renders a tag, its verification state, and the date it was last checked — and a tag with no recorded check renders as unverified, never as a release (§5.4).

4. **Anything the site publishes as inline SVG is outside the gate.** `renderHTML` drops `<script>`, `<style>` and inline `<svg>` whole, and says so: *"a withdrawn guarantee typed into an SVG's `aria-label` is NOT scanned."* §4.2 rule 6 already says a diagram is a claim. **The human's decision closes this route for diagrams**: every diagram is a generated `.webp` placed with `Figure`, never inline SVG and never Mermaid. The same holds for a raster's pixels — no gate reads the text in a `.webp` — so a diagram's claim belongs in `Figure`'s `claim` prop and in the page's prose, never only in the artwork.

### 11.7 Live

- **`docs/architecture.md` §16's competitor table** — six capability rows whose own header says the assayd column "states the target, not what ships". PR #60 withdrew the first-mover sentence that closed the section; the table is unchanged, and §9.4's refusal to publish it stands for the original reason.

### 11.8 Documented honestly, listed so nobody "fixes" them

Not defects. Recorded because they look like defects and will be reported as such.

- `charts/assayd/Chart.yaml` says `version: 0.1.0` and `charts/assayd/values.yaml` pins `tag: "0.1.0"`, against a newest tag of `v0.4.1`. `.github/workflows/release.yml` rewrites both at release time with `yq`, and `docs/install.md` states plainly that the checkout's chart names a version "which is not published".
- `docs/agent-contract.md` scopes every guarantee to one fixture — "it is the only agent any test here runs, so it is the only one this contract has been measured against". **That sentence must survive the rewrite to `assayd.dev/agent-contract`.** It is the single most load-bearing caveat in the corpus and exactly the kind a documentation restyle deletes.
- `charts/assayd/templates/NOTES.txt` is the most accurate governance statement in the repository, on both branches of `gateway.enabled`. The landing page inherits its sentence — "YOUR AGENTS ARE REACHABLE, AUTHENTICATED BY API KEY, AND OTHERWISE UNGOVERNED" — rather than composing a new one.
- **ADR-0026's Consequences body will keep saying something false.** See §11.6.2. It is frozen on purpose; do not open a PR against it.

## 12. Open items

Carried forward rather than hedged in the prose above. Closed and overtaken rows are kept struck through, because a spec that quietly drops a question it once raised teaches the next reader nothing.

| # | Item | Why it is open |
|---|---|---|
| O1 | ~~The brief names a gap "D6"~~ — **closed 2026-09-23. The label was never in the repository; it was invented in the brief this document was written from, and the coordinator has confirmed it.** The defect it named is real and is located at design 03 **§3.4.4** ("The key set is outside every gate") and **§8.1**'s reservation scope ("`DELETE` is not reserved"). §8.3 now cites the location and carries no label. Kept as a closed row because a spec that quietly drops a question it once raised teaches the next reader nothing. |
| O2 | ~~Whether `v0.4.1` published a chart and image~~ — **closed: the answer is that nobody has checked.** `docs/supply-chain.md` records `v0.4.1` as the newest tag, states it has not been re-verified from outside, and closes "Read a version claim here as 'last checked', never as 'current'." That is not a blocker for `/releases`; it is the page's content. §11.6.3 carries the consequence: a tag renders as unverified, never as a release. |
| O3 | **The strongest evidence this project produces still has no field in a claim.** "Three mutations kill it" (`TestTheGatewayIsTheOnlyWayIn`) and "every case was run with a mutation of its own, in five batches" (design 03 §8.1) are stronger than "a test exists", and neither the specified record nor the built `ClaimClass` can express either. A `mutations` prop is the obvious answer and nothing would check it, which is the argument against adding it. PR #59 meets the same wall from the other side and says so: "a test names the string, which is weaker than pinning it — nine tests in this repository once passed with their subject deleted." Unresolved. |
| O4 | ~~`/reference/reasons` cannot be generated correctly~~ — **closed by PR #59**, which extracts the vocabulary with `go/types` and holds a hand-written annotation file to the set, so that a reason with no entry fails CI. **What remains is the page's own statement, not an open item of this document**: the join pins the *set* of reasons and does **not** prove the three annotated answers — what produces a reason, what the operator does, whether traffic is withdrawn — are still true of the code; `AuthPolicyNotAttached`'s annotation was found stale after design 03 A83 by exactly that route. And some reasons are named by no test file, so nothing fails if one changes or stops being set. `docs/reference/conditions.md` states both facts, with the current counts, in its own opening; this document does not copy the counts (§3.2). |
| O5 | ~~Where the claim manifest lives~~ — **overtaken by PR #57**, which built no manifest: evidence is inline on each `ClaimClass` badge (§5.1). The question returns only when the manifest is built (§10 item 3), and the argument then is unchanged — `web/claims.yaml` if the site is its only consumer, `docs/web/claims.yaml` if Gate R's generated `README.md` makes it a repository asset. |
| O6 | Whether `/archive/designs/<nn>` should render 27 designs whose bodies contradict their own Status lines in places. Design 03's body is 2,000+ lines specifying an unapproved system; publishing it verbatim is honest and is also 2,000 lines of `designed` prose with one approval banner at the top. A per-section banner was considered and needs the design's own section structure, which varies. Not decided. |
| O7 | Whether the landing page may state the thesis — "what was evaluated is what runs" — as an unclassed sentence. It is a statement of intent, not of behaviour, and §4.2 rule 1's boundary does not cleanly settle it. The conservative reading is that it is a claim and is `measured` only for revision identity, not for evaluation, since the eval gate is `designed`. |
| O8 | Gate B's implementation, when it is built. Detecting that a named test "reaches a `t.Skip`" requires following helper calls — `responderImage`, `requireGateway` and `requireCluster` are where the skips actually live, not the test bodies. A one-level call-graph walk covers today's cases; nothing guarantees it covers tomorrow's. And since the built badge has no condition field, gate B would first need somewhere to find the condition it checks for. |
| O9 | **Whether the eight concept pages should move to `assayd.dev`.** The `.io`/`.dev` split is decided and §2.1 does not relitigate it, but all seven surveyed two-domain projects put concept *pages* on the docs side and give marketing one narrative page instead. The cost of the decided frame is a cross-domain hop mid-guide. Raised for the human, not decided here. |
| O10 | **Whether `/releases` on `.io` should keep the supported-versions and end-of-life table, or hand it to `/reference/tested-versions` on `.dev`.** §2.1 proposes the latter on the survey's evidence (cert-manager, Grafana, Flux and Istio all keep it operator-side), but this splits one reader's question across two domains and may be worse than either whole. |
| O11 | Whether to serve every docs URL as Markdown by appending `.md`, and publish `/llms.txt`. kgateway, agentgateway and Temporal all do. It is cheap, and this project's documentation is demonstrably read by agents as well as people — `AGENTS.md` exists for exactly that reason. Not specified above because it is a scaffold decision, and the scaffold is another agent's. |

# Design 02 consolidated body — Codex cross-family review

- **Target:** `docs/designs/02-agent-crd-operator.md` sections 1–11 at `af1f1c9`. Section 12 is provenance and was not used as normative text.
- **Method:** cold read against `docs/architecture.md`, ADRs 0002/0003/0006/0014/0016/0019/0025–0029, designs 01/03/04/05/06/07/08/09/11/16/20/22/24/26/27, the generated CRD, the reconciler, and the test suites. Producer documents were read from `af1f1c9`, not from later working-tree edits.
- **Verdict:** **REVISE — 13 BLOCKER, 5 MAJOR, 2 MINOR.**

The rewrite removed the amendment-reading burden. It did not produce a closed design. Several security identities disappear in the consolidated schema, three fields classified as safe live edits can change evaluated behaviour, and multiple downstream contracts have no producer or no namespace in which they can exist. Do not treat sections 1–11 as an implementation specification yet.

## BLOCKER

### BLOCKER 1 — `runtime.resources` is a direct behavioural input through `resourceFieldRef`, not merely capacity

**Files:** `docs/designs/02-agent-crd-operator.md:287`, `:304`; `config/crd/plume.dev_agents.yaml:632`; `internal/revision/leaves.go:258-261`

The design classifies `runtime.resources` as policy surface and justifies the live edit as capacity whose regressions design 20 will detect. The admitted `runtime.env` schema contains Kubernetes `resourceFieldRef`, however. An approved image can branch on an environment variable sourced from `limits.cpu`, `limits.memory`, or the corresponding request. The selector is hashed, but the resource value it resolves is not.

**Concrete failure:** evaluate revision R1 with `MODE` sourced from `limits.cpu` and a CPU limit of `500m`; after promotion, change only the limit to `501m`. The operator rewrites R1's Pod template in place and creates replacement Pods under R1's unchanged digest. The image can serve the safe path at `500m` and a different path at `501m`. No candidate or eval exists. Design 20 cannot roll this back to the evaluated resource value: resources are absent from the revision record's behaviour projection and are reconstructed from current spec.

**Fix:** move every `runtime.resources` leaf to the behaviour surface and include it in the projection. If live capacity repair is a required incident path, design a separate status-owned emergency override with explicit audit, expiry, and rollback; do not silently make the user-controlled spec field ungated. The weaker alternative is to reject every `resourceFieldRef` arm and narrow the claim to performance only, but that still needs a deliberate decision about observable task behaviour.

### BLOCKER 2 — `runtime.port` lets one approved image select a different server after the gate

**Files:** `docs/designs/02-agent-crd-operator.md:287`; `internal/revision/leaves.go:257`; `test/envtest/reconciler_blockers_test.go:815-846`

The port is classified as “wiring.” An image digest may listen on more than one port; Kubernetes does not enforce that each port serves the same program, route set, or authorization behavior.

**Concrete failure:** a reviewed image serves the evaluated A2A implementation on 8080 and an administrative or deliberately different handler on 9090. R1 passes on 8080. Editing `runtime.port` to 9090 keeps R1's digest and updates its Pod template live. Once the missing Service is implemented, its backend port must move too, and production reaches code the gate never exercised.

**Fix:** put `runtime.port` on the behaviour surface and project it. Add a valid differential test showing 8080→9090 changes the full digest and mints a candidate; replace the existing test that asserts the unsafe inverse.

### BLOCKER 3 — loop authorization is live-editable even though design 22 defines it as executable policy

**Files:** `docs/designs/02-agent-crd-operator.md:289`; `internal/revision/leaves.go:264-265`; `docs/designs/22-loop-governance.md:19-29`

Design 22 compiles `allowReentry` and `maxVisits` into the gateway's CEL decision over each request lineage. Those fields decide whether a call is denied or admitted. Calling them “policy” does not make a widening safe.

**Concrete failure:** R1 is evaluated with reentry denied. An Agent editor changes `allowReentry: false` to `true` and `maxVisits: 1` to `2`. The same revision may now execute cycles that were forbidden during evaluation. This is the same capability-widening argument design 02 correctly applies to tool names and egress destinations.

**Fix:** move both loop leaves to the behaviour surface and the revision projection. Any future emergency kill remains a status-owned guard; it is not a reason to make the declarative loop ceiling ungated.

### BLOCKER 4 — the consolidated status schema discards the full digest at the two places where a verdict can be spent

**Files:** `docs/designs/02-agent-crd-operator.md:83-96`; `docs/designs/03-policy-compiler.md:50-56`; `docs/designs/16-evalsuite.md:42-46`; `api/v1alpha1/agent_types.go:518-555`

The body correctly separates the short workload name from the full SHA-256 at `:78-82`, then drops that rule in its own schema. Each `status.cards[]` entry and `status.eval` carries only `revision`; `status.revisions[].hash` is illustrated as `pa-reviewer-7f3a2`, the short object name. Design 16 requires `{name,digest}` at every gate step, and design 03 says `RevisionRecord.hash` is the revision identity and lookup key. The implemented API already contains `CardStatus.RevisionDigest` and `EvalStatus.RevisionDigest`, so the consolidation regressed behind its code and producers.

**Concrete failure:** an implementer rebuilding from the authoritative body accepts an eval or card under a chosen 40-bit name collision, or keys two different projections to one `RevisionRecord`. The gate verdict is attached before the workload-level collision check can save it.

**Fix:** add `revisionDigest` to every card and eval entry in the schema example. Make the RevisionRecord key the full digest; preferably name the fields `name` and `revisionDigest` rather than overloading `hash`. Require full-digest equality in design 16, design 03, code, CRD, and transition tests.

### BLOCKER 5 — “instant rollback” has no request surface and cannot be derived by reapplying old spec

**Files:** `docs/designs/02-agent-crd-operator.md:249`, `:473`, `:517`; `api/v1alpha1/agent_types.go:14-64`; `docs/designs/08-cli.md:18-27`; `docs/designs/20-drift-controllers.md:22-34`

The body defines retention and repeatedly promises rollback, but neither the Agent API nor the CLI has a way to request a retained digest. Design 20 says its controller auto-rolls back, but does not define the operator input it writes or calls.

Reapplying R1's old Agent YAML is not equivalent. Env-source content is part of the digest: if the source object has moved since R1, the same spec computes a new R3 digest even though R1's immutable material is retained. Status is operator-owned, so a user cannot safely set `activeRevisionDigest` directly.

**Concrete failure:** R2 is promoted and immediately fails. R1's workload and material remain, exactly as the retention contract promises, but no declarative operation selects them. The user can only manufacture another candidate and wait for another gate—the opposite of “instant.” The design-20 controller has the same missing socket.

**Fix:** define one durable rollback request carrying the full digest, its authorization, idempotency, conflict behavior with a live candidate, and clearing/pinning semantics. A spec field or a dedicated RollbackRequest CR can work; a status patch by users cannot. Make manual rollback and design-20 rollback use the same path and test that source drift still selects the retained material rather than minting a new revision.

### BLOCKER 6 — no Service exists, and neither design defines the per-revision backend topology required for weighted rollout

**Files:** `docs/designs/02-agent-crd-operator.md:125-148`, `:414`; `docs/designs/03-policy-compiler.md:155-177`; `internal/controller/agent_controller.go:881-975`; `test/e2e/e2e_test.go:140-162`

The body says the operator creates “the Service,” yet the repository contains no Service constructor or reconcile path. More importantly, the singular is insufficient: R1 and R2 coexist and design 03 shifts `backendRefs` weights between revisions. One Service selector cannot independently address both revision Deployments.

**Concrete failure:** the operator reports a Pod available and eventually `Ready`, but there is no stable backend for an HTTPRoute. If one shared Service is added naively, it selects both R1 and R2 and sends production traffic to the weight-zero candidate before its gate. The current e2e uses `pause` and explicitly does not exercise A2A traffic, so the full gate stays green.

**Fix:** specify revision-scoped Services (name, selector, port, provenance, retained-set GC, collision handling) and make design 03's per-revision backends reference them. Implement them before claiming workload materialization is complete. Add a real-cluster test that sends production traffic while R2 is held and proves every response came from R1, then observes the weighted transition.

### BLOCKER 7 — external Agents need gateway resources, but the only permitted resource namespace does not exist for them

**Files:** `docs/designs/02-agent-crd-operator.md:9-12`, `:133`, `:397`; `docs/designs/03-policy-compiler.md:91-93`, `:159-175`; `internal/controller/agent_controller.go:213-218`; `test/envtest/runnamespace_test.go:1108-1127`

Design 03 places every per-Agent HTTPRoute, Backend, and Policy in the run namespace. At the same snapshot it states that an external Agent has no run namespace, and the implementation/test deliberately preserve that fact. External Agents still require an OAuth-authenticated route and Backend.

**Concrete failure:** registering an external endpoint reaches policy compilation with nowhere authorized to create its resources. Creating them in the user's namespace gives that namespace's editor mutation/deletion control over governance objects; creating them in the operator namespace changes listener admission and ownership and is not specified.

**Fix:** decide and specify a protected home for external-Agent gateway resources. The simplest consistent shape is a run namespace for every Agent namespace, with external Agents contributing no workload or env material but retaining the namespace while their routes exist. Otherwise introduce a separately bound policy namespace and propagate its RBAC, admission, routing, and teardown rules through designs 03 and 07.

### BLOCKER 8 — `knowledge[]` names a Kubernetes producer that no producing design defines

**Files:** `docs/designs/02-agent-crd-operator.md:48-55`, `:121`, `:485`; `docs/designs/01-knowledgegraph-provider-contract.md:1-16`, `:121-136`; `docs/designs/13-graphiti-adapter.md`

The Agent schema binds a named, versioned KnowledgeGraph and says the operator resolves it. Design 01 defines `kgp/v1alpha1`, a provider protocol. Design 13 defines the Graphiti adapter. Neither defines a `KnowledgeGraph` CR, its status, its version-to-endpoint mapping, or the controller that resolves the Agent binding. This is not merely an implementation gap; the producing resource contract does not exist.

**Concrete failure:** an Agent declares `knowledge: [{name: payer-policies, version: v12}]`. There is no object kind the operator can read to establish `active|superseded`, no source for design 03's resolved endpoint, and no writer for `KnowledgeBound`. Implementers must invent incompatible APIs or silently omit the binding.

**Fix:** assign ownership and design the KnowledgeGraph CR/controller contract before keeping `knowledge[]` in the front door: schema, namespace resolution, version states, endpoint identity, readiness, deletion, and PolicyIntent handoff. Until then, list it as unavailable and refuse the binding loudly rather than presenting it as a complete interface.

### BLOCKER 9 — the receipt backstop aliases same-named Agents across namespaces

**Files:** `docs/designs/02-agent-crd-operator.md:24`, `:475-478`; `docs/designs/04-receipt-tap.md:52-60`, `:127`

Design 02 consumes a “per-agent” daily spend aggregate as the hard budget backstop. Design 04's own producer audit says its row key lacks Agent namespace: two same-named Agents in different namespaces share one budget row, and the run-namespace move makes the namespace observable from gateway data the wrong namespace anyway.

**Concrete failure:** `team-a/reviewer` spends its allowance. `team-b/reviewer`, with a separate spec and budget, is held from traffic because both aggregate as the same Agent—or the combined spend is compared with the wrong revision's ceiling. That is cross-tenant denial and incorrect budget enforcement.

**Fix:** do not call the backstop available until design 04 chooses and implements an authoritative source-namespace producer. Index and query by at least `(tenant, sourceNamespaceUID or namespace, agent UID/name)`, carry that identity in receipts, and mutation-test two same-named Agents in different namespaces. Add the unresolved seam to §5.

### BLOCKER 10 — the operator-owned runtime environment contract vanished from the body and does not exist in code

**Files:** `docs/designs/02-agent-crd-operator.md:527-529`; `docs/designs/09-sdk-templates.md:21-28`; `internal/controller/agent_controller.go:941-971`

Section 11 says design 02 owns injection of `PLUME_GATEWAY_URL`, `PLUME_KG_ENDPOINTS`, `PLUME_NATS_URL`, and tenant credentials. The consolidated body contains no contract for their sources, requiredness, Secret shape, per-revision behavior, or disabled-tier values. The workload renderer only copies user `env`/`envFrom`; it injects none of them. Design 09's reference SDK consumes these variables and uses `PLUME_NATS_URL` for the shared task-state store that makes `replicas>1` safe.

**Concrete failure:** a template-built Agent starts, advertises shared task state, and the operator may clear `TaskStateUnverified` after card support lands, but every replica lacks the NATS address/credential and falls back to local state. A2A collaboration, KG access, and task continuity fail despite the contract being presented as operator-owned.

**Fix:** restore a self-contained table to the normative body with each variable's exact producer, value grammar, Secret/key source, profile behavior, and whether it is revision material. Implement injection in `deploymentFor`, and test it with the real template and two replicas. Add an honest §5 row until that exists.

### BLOCKER 11 — design 02 reintroduces the NetworkPolicy shape that design 07 explicitly disproves

**Files:** `docs/designs/02-agent-crd-operator.md:149`, `:493`; `docs/designs/07-umbrella-chart-ci.md:167-176`

Design 02 requires default-deny with “egress to the gateway only.” Design 07 says that exact slogan was wrong and replaces it with a traffic matrix: ingress from gateway **and operator** for card fetch; egress to gateway, DNS, and tenant NATS for the SDK task store.

**Concrete failure:** implementing the consolidated design literally blocks DNS and NATS, so the agent cannot resolve or use the gateway/task store; it also blocks the operator's in-cluster card fetch. Every candidate looks like a registration or gateway failure.

**Fix:** replace both design-02 statements with the design-07 A5.4 matrix, or make that section the single normative owner and cite it without restating a weaker shape. Correct architecture §06 at the same time. The implementation/e2e must prove both required paths and a forbidden-destination negative control.

### BLOCKER 12 — design 02 knowingly selects a weaker KG readiness rule than canonical architecture and the compiler

**Files:** `docs/designs/02-agent-crd-operator.md:485`; `docs/architecture.md:148-152`; `docs/designs/03-policy-compiler.md:231-246`

The failure table admits the conflict and says this design “follows the narrower rule”: traffic is withheld only when the failed graph is the sole knowledge source. Architecture is canonical and says an Agent bound to a non-Ready graph gets no traffic. Design 03 makes every declared binding required, so it also withholds the route for any declared unavailable graph.

**Concrete failure:** an Agent declares two graphs and one becomes non-Ready. The operator can report the Agent serving under design 02 while the compiler withholds its route under design 03. A consumer sees either an Agent answering without a declared dependency or a false operator status, depending on which document its implementer followed.

**Fix:** use the canonical rule: every declared binding is required until the CRD gains an explicit optional marker through an ADR. If the narrower rule is desired, supersede architecture and add requiredness to the API; a subordinate design cannot resolve a canonical conflict by announcing that it chose the other side.

### BLOCKER 13 — hard mode is described as an operating mechanism while every load-bearing producer says it is unavailable

**Files:** `docs/designs/02-agent-crd-operator.md:150`, `:391`, `:405`; `docs/designs/26-tenant-cr.md:109-120`; `docs/designs/06-identity-glue.md:104-106`; `docs/designs/03-policy-compiler.md:837-838`

The body states where hard-mode resources land, how identity works, and how a revocation marker orders deletion. The producing documents say the opposite of readiness: the compiler lacks the resolved host Service mapping and deletion lifecycle; the marker has no producer; and the virtual-Pod UID may not match the host SPIRE agent's attestation, yielding no SVID. Design 26 explicitly says hard mode is not implementable until those contracts and the spike exist.

**Concrete failure:** a hard tenant either gets no identity, a route to a stale/guessed synced Service, or an Agent finalizer that waits forever for a marker no controller can write. Treating the body as complete turns all three into implementation choices.

**Fix:** mark hard mode unavailable in the normative body and §5. Keep only the consumer requirements until design 03 A45 has a concrete producer/lifecycle, the vCluster/SPIRE spike succeeds or changes design 06, and design 26's tenant-admin gate bypass is propagated. Reintroduce present-tense behavior only after those producing contracts pass review.

## MAJOR

### MAJOR 1 — §5's universal “stated but not enforced” claim is still false

**File:** `docs/designs/02-agent-crd-operator.md:427-448`

The table is accurate for the rows it contains; I found no row labelled unimplemented whose subject is in fact enforced. It is not complete in the other direction. Missing present-tense guarantees include the Service and revision backend topology (BLOCKER 6), rollback request (BLOCKER 5), env injection (BLOCKER 10), the receipt backstop (BLOCKER 9), hard-mode mapping/marker/SPIRE behavior (BLOCKER 13), the kill-switch guard at `:474`, and the CLI warning at `:471`. The header carve-out names gateway, identity, registration, and eval execution; it does not cover all of these.

**Concrete failure:** a reader uses §5 as instructed and concludes the only gaps are its listed rows, then builds against a Service, rollback socket, budget backstop, or environment contract that does not exist.

**Fix:** remove “Every” until it is mechanically true, or inventory every present-tense guarantee with an exact current behavior and owner. Make the table testable from an independent fixture catalog; a row deleted from the document currently leaves the docs suite green.

### MAJOR 2 — the material-collision condition exists in the design and can never be emitted by the collision path

**Files:** `docs/designs/02-agent-crd-operator.md:346`, `:457`; `internal/controller/agent_controller.go:354-371`; `api/v1alpha1/agent_types.go:422-438`; `test/envtest/material_test.go:251-301`

The design says wrong bytes produce `RevisionMaterialCollision`. The reconciler maps every typed material error—including wrong bytes—to `RevisionMaterialUnavailable` with reason `MaterialInvalid`; `CondRevisionMaterialCollision` is unused. The test asserts the implementation's broader condition, so changing the code to match the design makes the test fail while the safety action remains correct.

**Concrete failure:** automation and an operator looking specifically for a collision/tamper signal never see it. They are told material is unavailable and investigate missing storage instead of an object existing under the expected name with unapproved bytes.

**Fix:** split typed causes: missing/read failure → `RevisionMaterialUnavailable`; existing wrong-kind/wrong-bytes collision → `RevisionMaterialCollision`. Keep `Ready=False`, make both owned/clearable, and change the test to assert the exact type and reason rather than treating any refusal as success.

### MAJOR 3 — deletion is label-prefiltered, so metadata drift escapes the name-authoritative GC contract

**Files:** `docs/designs/02-agent-crd-operator.md:143`, `:346`, `:362`; `internal/controller/material.go:273-343`; `internal/controller/agent_controller.go:1169-1208`, `:1233-1245`

The body says name is deletion authority and labels are corroboration, and it explicitly permits/restamps metadata drift. Both workload and material enumeration prefilter by mutable labels before applying the name check. An object whose label is removed is invisible to finalization; normal reconcile would repair it, but deletion can race before that repair.

**Concrete failure:** strip `plume.dev/agent-uid` from an immutable Secret copy, then delete the Agent while another Agent keeps the shared run namespace alive. The finalizer releases and the Secret survives indefinitely with credential bytes. The same shape on a Deployment leaves a workload running after its authorizing Agent is gone.

**Fix:** enumerate from a non-forgeable/recorded set rather than a mutable-label selector. Persist exact created object names per retained revision, or list the bounded run namespace and apply the deterministic name shape plus status digest/UID checks before deletion. Add tests that strip each metadata key immediately before Agent deletion while a sibling retains the namespace.

### MAJOR 4 — the consolidated `resolvedInputs` storage contract is already stale at its requested snapshot

**Files:** `docs/designs/02-agent-crd-operator.md:87-95`; `docs/designs/03-policy-compiler.md:74-95` at `af1f1c9`

Design 02 says inline below 4 KiB and by-reference above it. At the same snapshot, design 03's A51 text says the producer input is bounded plume CR data and concludes one inline form, while older paragraphs in design 03 still contradict it. Design 03 marks itself RE-OPENED and instructs consolidation against A51, not the stale storage table.

**Concrete failure:** an implementer creates `<agent>-<revision>-inputs` ConfigMaps because design 02 is called authoritative, while the compiler writes inline only. The objects have no agreed writer/reader/GC contract and can leak resolved policy data.

**Fix:** do not freeze either inconsistent shape into design 02. After design 03 is consolidated, mirror its single reviewed `RevisionRecord` schema exactly and delete the other form. One design must own the type; the other should link to it rather than paraphrase it.

### MAJOR 5 — canonical and generated public contracts still teach claims this body retracts

**Files:** `docs/architecture.md:81-103`, `:177-191`, `:434-453`; `api/v1alpha1/agent_types.go:44-57`; `internal/controller/agent_controller.go:953-960`; `docs/designs/02-agent-crd-operator.md:38`, `:259`, `:475`

Canonical architecture's Agent example uses a mutable image tag, `2M` for an int64, and numeric `usdPerDay`; the admitted CRD requires a digest, integer, and decimal string. Architecture says every hop crosses the gateway while P1 deliberately ships `gateway.enabled: false`, and says signed images are enforced by a built-in VAP while this body correctly says no verifier ships. The generated API comment still says at least one gate is required in prod although the reconciler explicitly promotes with none, and calls the receipt tier exact although ADR-0028 retracts exact USD. The controller comment says plume permits tags next to code that can only receive a digest-pinned image.

**Concrete failure:** users copy the canonical manifest and admission rejects it; users trust `kubectl explain` and believe a missing gate is prevented or a USD ceiling exact when neither is true.

**Fix:** update architecture and Go comments in the same correction, regenerate the CRD, and add doc fixtures that apply the canonical minimal Agent against the generated schema and reject the retracted phrases in public API descriptions.

## MINOR

### MINOR 1 — `RevisionMaterialChanged` is unexplained dead vocabulary

**Files:** `docs/designs/02-agent-crd-operator.md:104-110`, `:115-117`; `api/v1alpha1/agent_types.go:661`

`EnvSourceProtectionUnavailable` is retained with an explicit stale-condition-clearing rationale. `RevisionMaterialChanged` is also in the closed vocabulary and owned set, but no body rule defines it and no code sets it. It is residue from the superseded in-place seal design.

**Concrete failure:** a controller consumer waits on a documented condition that cannot occur, or a future implementer reuses the name with incompatible semantics.

**Fix:** remove it before release, or document the same compatibility-only clearing rationale and prove a stale `True` is cleared. Because plume is unreleased, removal is cleaner.

### MINOR 2 — ADR-0019 still presents `revisionHistoryLimit` as a field

**Files:** `docs/decisions/0019-agent-revisions-and-runtime.md:4`; `docs/designs/02-agent-crd-operator.md:113`, `:442`

The consolidated body carefully says the limit is an operator constant, not a field. The accepted ADR still writes `revisionHistoryLimit: 2`, which reads as the field the body says is absent.

**Concrete failure:** an implementer adds an unreviewed CRD field to satisfy the ADR, or a user searches for a setting that cannot exist.

**Fix:** amend ADR-0019 to say “operator constant, default 2” and link to the retained-set definition.

## Mutation and execution ledger

All temporary edits were restored from `/tmp` backup copies; no `git checkout` was used. Every mutation below compiled, so none is `INVALID`.

| ID | Change | Result | What it proves |
|---|---|---|---|
| Baseline | `make test` | **PASS** | fmt, vet, unit, docs, conformance, envtest, and chart gate are green at the snapshot lineage |
| Baseline | `make verify` | **PASS** | generated API/CRD/RBAC artifacts reproduce |
| M1 | Change only `spec.Runtime.Port` from `inPlace` to `mints` in the independent leaf inventory | **KILLED** by `TestEveryAgentSpecLeafBehavesAsClassified/spec.Runtime.Port` | Once the semantic classification is corrected, the leaf test exposes the missing projection; the test cannot decide which semantic class is correct |
| M2 | Change only `spec.Loop.AllowReentry` and `spec.Loop.MaxVisits` from `inPlace` to `mints` | **KILLED** by the two corresponding leaf cases | Same: the projection omission is pinned once the independent inventory carries the corrected rule |
| M3 | Change only the four `runtime.resources` leaves from `inPlace` to `mints` | **KILLED** by all four corresponding leaf cases | The projection omission is pinned once the inventory accounts for the behaviour reachable through `resourceFieldRef` |
| M4 | Emit `RevisionMaterialCollision` instead of `RevisionMaterialUnavailable` for typed material collisions | **KILLED** by `TestMaterialWithTheRightProvenanceAndWrongContentIsRefused` | The test is coupled to the implementation's wrong condition type; matching the design fails it |
| M5 | Delete the default-deny NetworkPolicy row from §5 | **SURVIVED** `go test ./test/docs/... -count=1` | The “complete unenforced list” has no independent fixture catalog; deleting a load-bearing disclosure is invisible |
| R1 | Temporary envtest: create a revision Secret, strip only `plume.dev/agent-uid`, delete its Agent while a sibling retains the run namespace | **REPRODUCED** | Finalization releases while the Secret copy remains; label prefiltering defeats the stated name-authority cleanup |
| R2 | Repository-wide search for Service constructors/reconcile paths, followed by the full green gate | **REPRODUCED** | No Agent Service exists and no test requires one; e2e explicitly stops at a running `pause` Pod |
| R3 | Repository-wide search for rollback API/CLI and injected `PLUME_*` variables | **REPRODUCED** | Neither claimed interface has an implementation or a complete declarative socket |

## Disagreement with the same-family consolidation critique

I disagree with two of its closure judgments.

First, round 2 marks the architecture §04 conflict closed because design 02 names the disagreement and announces that it follows the narrower design-01 rule (`docs/designs/reviews/02-consolidation-critique.md:570`). That is not closure. `AGENTS.md` makes architecture canonical, and design 03—the controller that owns route aggregation—also implements declaration-as-required. Naming a contradiction is honest; choosing against the canonical source in a subordinate design is still drift and still yields two executable answers.

Second, round 3 concludes that the remaining counterexamples to §5's universal claim are Sandbox and scratchpad (`docs/designs/reviews/02-consolidation-critique.md:922-974`). Those rows were added, but the universal still fails on Service creation, rollback, env injection, the receipt backstop, hard-mode producers, kill, and the CLI warning. Mutation M5 shows why this keeps recurring: the table is prose checked by prose, so removing a disclosure remains green.

The same-family review did valuable work on local consistency, but its claim that the cross-design anchors were resolved is weak evidence. Opening the producers exposes a missing KnowledgeGraph CR, a non-namespaced budget aggregate, an impossible external-resource namespace, and a hard-mode lifecycle explicitly labelled unimplementable by its owners.

## Verdict

**REVISE.** The consolidated body is easier to read and still unsafe to implement as a whole. Resolve the 13 blockers, make §5 honest by construction rather than assertion, and run another independent pass over the resulting cross-design contracts.

# Design 03 r3 BLOCKER 3 — scoped fix review

**Verdict: REVISE — 1 BLOCKER. Revise the fix, not the finding.**

Review snapshot: `c258e78`. Scope is only r3 BLOCKER 3 and A20. The universal
“copy every referenced ConfigMap and Secret into every revision” prescription
does **not** survive unchanged. The bypass is severe enough that generic
environment sources must be frozen, but copying credential bytes into retained
revision objects is not the best shape. The sound design is a **field-typed
split**:

1. generic `runtime.env` / `runtime.envFrom` references are behavioral and must
   be sealed or snapshotted for the lifetime of every retained revision; and
2. fast-rotating credentials use a separate, typed credential binding whose
   stable principal/audience/scope is revisioned while its short-lived bytes are
   rotated by a trusted credential issuer/controller.

A Kubernetes `Secret` is not evidence that its content is a credential. A field
named and constrained as a credential binding can be. Arbitrary Secrets may not
opt themselves into the live path merely by label or user assertion.

## BLOCKER — A20 needs a field-typed materialization contract

**Files:** `docs/designs/02-agent-crd-operator.md:110-128,233-237,367-379`;
`docs/designs/reviews/03-codex-review-r3.md:93-117`.

**Concrete failure:** content hashing alone lets a replacement R1 Pod read R2
behavior from a mutable name. Snapshotting every source closes that bypass but
archives every historical credential and forces all credential replacement
through semantic admission. Exempting all Secrets restores the original bypass
because a Secret can hold behavior.

**Specific fix:** keep generic env references behavioral and freeze them with a
measured admission lock/finalizer transaction (snapshots are the fallback), then
add a separate credential-binding field whose stable identity and permissions
are gated while bytes rotate under a trusted issuer. Reject arbitrary or
dual-use Secrets from the live credential path. The remainder of this review
specifies both lifecycles and failure states.

## The two stated costs

### 1. Copying credentials is a real cost; the universal snapshot fix is overbroad

**Files:** `docs/designs/reviews/03-codex-review-r3.md:93-117`;
`docs/designs/02-agent-crd-operator.md:110-128,367-379`.

The operator already needs read access to each referenced Secret, so a live
operator compromise can read the originals. Copies do not create a new set of
bytes available to that particular compromise. They do create three different
risks:

- old credentials persist for the entire revision-retention horizon after the
  source has rotated;
- every snapshot adds an API object, backup/audit entry, RBAC target and GC path;
  and
- rollback can accidentally resurrect a credential value whose source owner
  intended to retire.

Kubernetes itself recommends short-lived, rotating credentials rather than
persisted long-lived token Secrets, and notes that kubelet mounts Secret data in
node `tmpfs` rather than durable storage. A revision Secret copy moves in the
opposite direction by deliberately retaining old values in etcd
([Kubernetes Secrets](https://kubernetes.io/docs/concepts/configuration/secret/),
“ServiceAccount token Secrets” and “Information security for Secrets”).

**Conclusion:** do not snapshot a source merely because its Kind is `Secret`.
Snapshot or seal it only because the API field declares it behavioral. Never
snapshot the bytes behind the proposed credential-binding field.

### 2. The claimed no-restart rotation behavior does not exist for this API surface

**Files:** `api/v1alpha1/agent_types.go:96-101`;
`docs/designs/02-agent-crd-operator.md:33-36,124-128`.

A20 covers `envFrom` and `env[].valueFrom`. Kubernetes does **not** refresh a
process environment when a ConfigMap or Secret changes. New Pods see the new
value; existing containers require replacement. Kubernetes documents both the
unchanged environment and the mixed-old/new result when only some Pods are
recreated
([ConfigMap environment variables](https://kubernetes.io/docs/tutorials/configuration/updating-configuration-via-a-configmap/#update-environment-variables-of-a-pod-via-a-configmap)).
The Secret task says the same: a Secret used as an environment variable is not
seen until the container restarts
([Secret environment variables](https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/#define-container-environment-variables-using-secret-data)).

Mounted Secret/ConfigMap volumes can update after a kubelet sync, provided the
application reloads the file. That is a different delivery contract and plume's
Agent API does not expose it today. The local Kubernetes types confirm the
reference has only name/key/optional; there is no UID or resourceVersion pin for
kubelet to resolve.

**Conclusion:** snapshots do not remove an existing live, no-restart rotation
feature from `envFrom`; there is none. They do add an eval delay before plume
deliberately replaces Pods. That delay is still unacceptable for emergency
credential recovery, but it needs a separate credential path, not a false claim
about current env behavior.

Revocation and replacement must also be separated. When a credential is
compromised, the issuer should revoke the old credential immediately; no plume
eval may delay revocation. Supplying replacement bytes to an env-based process
then requires a same-revision rolling restart. Supplying them without restart
requires file/CSI delivery plus application reload.

## Why the bypass still wins over doing nothing

The original B3 scenario is unchanged. If a name-resolved behavioral source is
mutable, a node drain can create an R1 Pod from R2 content while R1's gated hash
and route remain active. This is not an availability trade; it defeats semantic
admission.

**Concrete failure:** a Secret contains `SYSTEM_PROMPT=safe`, because Kubernetes
does not distinguish that from an API key. R1 passes eval. The Secret changes to
`SYSTEM_PROMPT=exfiltrate`; R2 is Held. A replacement R1 Pod reads the new value
and serves it under R1's old result. Exempting Secret content for rotation makes
this bypass intentional.

If plume cannot distinguish behavioral data from credentials safely, the strict
fallback is to freeze both. Slow rotation is worse operations; ungated behavior
is broken correctness. The final API should not force that fallback on genuine
credentials.

## Recommended shape

### A. Keep generic env sources on the behavioral surface

`runtime.env` and `runtime.envFrom` remain the strict surface. Literal values are
already in spec. Every external ConfigMap/Secret reference is behavioral by
default, including a Secret reference. Mixed objects are rejected operationally:
users split behavior and credentials into different sources.

For those behavioral references, prefer **sealing the source in place** over
copying it:

1. Before hashing or creating a revision, the operator applies a
   `plume.dev/env-source-protection` finalizer plus protected metadata to the
   exact source UID.
2. A chart-shipped, fail-closed `ValidatingAdmissionPolicy` denies data changes,
   deletion, protected-label changes and finalizer removal by every principal
   except the agent-operator while any retained revision holds the lock.
3. Only after the lock is accepted does the operator re-read the source, record
   `{kind, namespace, name, uid, selected-keys, digest}`, and mint the revision.
4. New Pods still reference the original name. The admission lock, not a hopeful
   re-read, guarantees that name continues to resolve to the recorded UID and
   content.
5. The chart protects the Policy and Binding from non-operator changes, and the
   operator continuously verifies them. If either is absent or skewed at
   startup/reconcile, it withholds new revisions and raises
   `EnvSourceProtectionUnavailable`. There is no race-free controller reaction
   to an authorized deletion of the admission guard itself, so those
   cluster-scoped admission objects are explicitly part of the trust boundary,
   not drift that prose claims to heal after the fact. After restoration, every
   retained source is re-read and compared before traffic resumes.

Finalizers keep an object present until the responsible controller releases it
([Kubernetes finalizers](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/)).
A finalizer **alone is insufficient here**: the threat actor has `update` on the
source and could remove it. The admission rule is load-bearing. Kubernetes
`ValidatingAdmissionPolicy` is stable from v1.30, can compare `object` and
`oldObject`, inspect the request principal, and defaults `failurePolicy` to
`Fail`
([Validating Admission Policy](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/)).
Its match must select on both old and new protected state so removing the
protection label cannot opt the request out.

This corrects the first immutability proposal rather than returning to it.
Immutability stops data updates; protection stops delete/recreate and protection
removal while a retained revision exists. The operator need not copy any bytes.
After the last retained revision releases a source, the operator removes the
lock; if the source was protected only by admission rather than permanently set
`immutable: true`, it may become mutable again.

**Required caveat:** this is a proposed Kubernetes transaction, not yet measured
in plume. It must be spiked before becoming the design. In particular, test
update, delete, finalizer removal, label removal, controller restart, two Agents
sharing one source, and a node drain. If the lock/refcount transaction cannot be
made race-free, behavioral snapshots are the simpler safe fallback.

### B. Add a typed credential binding; do not reuse arbitrary `secretRef`

The fast path needs a field with semantic content, for example:

```yaml
runtime:
  credentials:
    - name: model-api
      bindingRef: openai-production
      delivery: env       # env | file
```

The referenced binding must carry the stable facts that affect capability:
issuer/provider, logical principal, audience, scopes, destination and injected
name/path. Those facts and the binding identity mint a revision. Rotating
short-lived bytes behind the unchanged binding does not.

The binding is trustworthy only when a chart-approved issuer/controller owns
the material and guarantees that a byte rotation cannot change those stable
facts. Examples include projected service-account tokens, workload identity, or
an external secret/CSI provider operating behind a fixed binding. Kubernetes
documents external Secret Store CSI delivery as mounted files
([Secrets good practices](https://kubernetes.io/docs/concepts/security/secrets-good-practices/#configure-access-to-external-secrets)).

An ordinary user-writable Secret cannot satisfy that contract: changing an API
key can change principal and permissions, not only key material. Labels such as
`credential=true` are not proof because the same writer can label a system
prompt. Unsupported or unverifiable bindings fall back to behavioral sealing or
are rejected; they never silently gain the live exemption.

For `delivery: env`, a rotation watch triggers a rolling restart of the **same
revision**, not a new eval, because the binding's logical identity is unchanged.
For `delivery: file`, kubelet/CSI may update mounted content and the application
must implement reload; plume must not promise no-restart rotation without that
application contract. Changes to provider, principal, audience, scope,
destination, source path or delivery mode mint and gate a new revision.

### C. Reject dual-use sources

A single source may not feed both a behavioral env reference and a live
credential binding. Otherwise the live writer can still change behavioral data
inside the credential object. Split it into two objects/bindings. For `envFrom`,
whose wildcard semantics import every key, the entire object is behavioral; it
cannot be partly exempted.

## Alternatives considered

| Shape | Closes B3? | Copies credential bytes? | Fast rotation? | Judgment |
|---|---:|---:|---:|---|
| Hash content only (current A20) | no | no | after Pod restart | rejected: rescheduled R1 reads R2 content |
| Snapshot every ConfigMap/Secret | yes | **yes** | no; new revision/eval | safe but overbroad; emergency fallback only |
| Snapshot ConfigMaps, leave Secrets live | no | no | after Pod restart | rejected: Kind is not semantics |
| Pin UID/resourceVersion in Pod env ref | no native mechanism | no | n/a | unavailable: selectors carry name/key/optional only |
| Admission lock + finalizer on all generic env sources | yes, if transaction is measured | no | no for generic env | preferred behavioral path |
| Typed credential binding backed by trusted rotator | yes for declared invariant | no | same-revision restart for env; reload for file | preferred credential path |
| User label says “credential” | no | no | yes | rejected: attacker can relabel behavior |

## If snapshots remain the implementation choice

Snapshots are still a valid implementation for **behavioral** sources or as a
short-term strict fallback. Correct lifecycle is:

### Creation and ownership

- Resolve only the effective keys. `envFrom` means the whole object; individual
  key refs mean the selected keys plus key/name/optional semantics.
- Create the immutable snapshot **before** the workload, then re-read and verify
  it. `AlreadyExists` with different content is tamper/collision, never update.
- Use an opaque keyed identity for Secret-derived digests, not a raw public hash;
  r3 MAJOR 15's offline-guessing oracle applies to names and status too.
- Own snapshots from the Agent/revision record, not the Deployment or Pod. Pod
  replacement must not garbage-collect revision material.
- Do not deduplicate Secrets across Agents. Cross-Agent sharing creates ownership
  and disclosure edges larger than the saved objects.

### Retention and garbage collection

Keep material for every active revision, candidate, `revisionHistoryLimit`
member, explicit rollback hold and in-flight eval/replay reference. GC order is:

1. route weight zero and drain complete;
2. candidate/eval/rollback references released;
3. workload deleted;
4. revision record removed; and
5. snapshots deleted **last**.

Agent finalization uses the same order. A source rotation never deletes retained
behavior snapshots. Credential bindings and their source objects are never GC'd
by this snapshot controller.

### Rollback

Rollback uses the exact snapshot references stored with the retained revision
and verifies UID, immutable state and keyed digest. It never reconstructs an old
revision from the current source. With snapshots, A20's “rollback refused if the
source moved” rule is replaced: source movement is irrelevant because the
snapshot is the revision material.

### Missing or tampered material

- **Candidate snapshot missing:** withhold/delete the candidate workload and
  route; `RevisionMaterialMissing` names the revision and source.
- **Rollback snapshot missing:** refuse rollback. Never substitute current
  content under the old hash.
- **Active snapshot missing while old Pods still run:** mark
  `RevisionMaterialMissing`, forbid scale/restart mutations, and report
  stale-but-serving while Ready Pods remain. Existing env values do not vanish
  from running processes. When no verified Ready Pod remains, set route weight
  zero; do not recreate from the mutable source.
- **Digest mismatch or mutable snapshot:** identical to missing. “Repairing” it
  in place would rewrite gated history.

Deletion protection and backup/restore of revision material are therefore
correctness, not convenience. The e2e battery must delete a snapshot during
Held, active and rollback states and pin each transition.

## Required tests before A20 can close

The test subject is the transition, not a stored digest:

1. mutate a behavioral ConfigMap while R2 is Held, delete an R1 Pod, and prove
   the replacement sees R1 content;
2. repeat with behavior stored in a Secret;
3. attempt update, delete/recreate, label removal and finalizer removal as the
   original source editor; every attempt must fail while R1 is retained;
4. rotate a trusted credential binding and prove no new revision is minted,
   `delivery: env` restarts the same revision, and the old credential is revoked;
5. mutate the binding's principal/audience/scope and prove a new revision is
   Held;
6. delete retained material and pin candidate, active and rollback failure
   states; and
7. delete the enforcement rule itself and confirm at least one test emits
   `FAIL`; a compile error is INVALID, not KILLED.

Envtest can pin admission and controller reactions but has no kubelet. The
old/new process-value assertions, restart behavior and node-drain scenario are
e2e-only.

## Final assessment

The B3 finding survives both costs. The universal snapshot prescription does
not. The better shape is:

> **Seal generic behavioral sources; bind and rotate credentials through a
> separate typed, provenance-constrained field.**

If plume is unwilling to add the typed credential contract now, freezing all
generic env sources is the only safe interim behavior. It should be described as
a correctness-first limitation, not as a production-grade credential rotation
story.

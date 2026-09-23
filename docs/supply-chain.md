# Supply chain

ADR-0019 says agent images must be cosign-signed and design 07 §4 says the platform ships "as a cosign-signed OCI chart, all images pinned by digest, SBOM attached — **the same admission story agents get**." This page is how you check that assayd holds itself to the half that exists, without taking our word for anything.

**One correction before anything else, because it was the first sentence of this page.** It read: *"assayd's admission rejects agent images that are not cosign-signed (ADR-0019)"*. **Admission does no such thing.** What admission enforces is **digest-pinning** — a CEL rule on `spec.runtime.image` — and **nothing in this repository verifies any signature on an agent image or an agent card**. Signature verification arrives with the Sigstore policy-controller binding design 07 A2 chose, and digest-pinning is its precondition, because a signature is verified against a digest. The same false claim was corrected in the CRD comment and in the chart's `_helpers.tpl`; it survived here, in the opening line of the document about signatures, which is the worst place in the repository for it to survive.

So read what follows as a claim about **assayd's own released artifacts**, which are signed and verifiable, and not as a claim about what assayd enforces on yours.

## What is published, and where

| Artifact | Location | State, as verified from outside at v0.4.0 |
|---|---|---|
| Operator image | `ghcr.io/quinyte/assayd-operator` (amd64, arm64) | published, cosign-signed, SBOM attested, SLSA provenance attached — **provenance is conditional, see below** |
| Helm chart | `oci://ghcr.io/quinyte/charts/assayd` | published, cosign-signed, pinned to the image by digest |

**SLSA provenance is not unconditional, and this table used to read as though it were.** The `SLSA provenance` step in `release.yml` carries `if: github.event.repository.visibility == 'public'`, because GitHub's attestation API refuses user-owned *private* repositories; when it does not run, the release emits a `::warning::` and the image ships signed and SBOM-attested with **no provenance**. The repository is public today, so the step runs — the state above is what was measured at v0.4.0, not a guarantee the workflow makes for every tag.

Both are published by `.github/workflows/release.yml`. **Two things trigger it, not one**, and this page said "only … on a `v*` tag": a push of a `v*` tag, **and `workflow_dispatch` with a `tag` input**.

**A dispatch is NOT equivalent to a tag push, and treating it as one was the hazard.** Only the version *string* comes from the input — `github.event.inputs.tag || github.ref_name` — and **neither job's checkout takes a `ref:`** (`release.yml:54`, `:193`), so both build `github.sha`, the commit of the ref the run was started from. Before the guard below, **dispatching `tag: v0.4.1` from `main` published `main`'s code as `0.4.1`**, signed it, and attested it.

**The guard.** Since 2026-09-23 (PR #60), the image job's `version` step — the step right after checkout, before `push`, `cosign sign`, both attestations and the whole chart job — fails the run when the event is `workflow_dispatch` and `github.ref` is not exactly `refs/tags/<the tag input>`. That test is false on a tag push, so a tag push publishes exactly as before. It closes three things:

- a dispatch from a branch, `main` included, with any tag input;
- a dispatch from one tag that names another, such as `v0.4.0` dispatched as `v0.4.1`;
- a dispatch whose input is not the tag's exact name, such as `0.4.1` for `v0.4.1`. That one used to publish the right code; it is now refused too, because the rule is one string comparison and not a guess.

What the guard does **not** do:

- **It only guards runs of a workflow file that contains it.** GitHub runs the `release.yml` *on the ref you dispatch from*. Every tag up to and including `v0.4.1` carries the old workflow. A dispatch from one of them still builds that tag under whatever name you type, unguarded, and so does a dispatch from any branch whose `release.yml` predates the guard.
- **It changes nothing a verifier downstream sees.** The certificate still names the ref the run started from. The workflow's own `cosign verify` steps still pass only `--certificate-identity-regexp '^https://github.com/<owner>/<repo>/'` and the issuer. That regexp is anchored at the start only, so it matches **any** ref. A consumer cannot learn from the signature that the guard ran. Use the commands below to check what you pulled.

**The certificate can tell you, and the default verification command does not ask.** An earlier version of this paragraph said "nothing downstream can tell" and then told you to check the certificate's `Subject` for the commit. Both were wrong, and the second more dangerously: the **Subject (SAN) is the workflow identity, and it ends in a `ref`, not a commit** — `https://github.com/Quinyte/assayd/.github/workflows/release.yml@refs/tags/v0.4.1` for a tag build, `…@refs/heads/main` for a dispatch from `main`. Someone following that instruction would read a ref and believe they had checked a commit. The **commit** lives in a different claim, which cosign exposes as its own flag.

Two claims, two flags — ask for the one you mean. The block runs as written against v0.4.0, whose digest is recorded below; for another release, substitute its tag and its digest from `helm show values` or the release notes:

```bash
IMAGE=ghcr.io/quinyte/assayd-operator
DIGEST=sha256:232673c6ecbc0a497a6076cd0914e56286ae6f960ecc35ebffacb7bcf0241823   # v0.4.0

# Was it built from the TAG's ref? (a ref, not an event: a dispatch started from
# refs/tags/v0.4.0 passes this too. The trigger is its own claim, checked with
# --certificate-github-workflow-trigger.)
cosign verify "${IMAGE}@${DIGEST}" \
  --certificate-identity "https://github.com/Quinyte/assayd/.github/workflows/release.yml@refs/tags/v0.4.0" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# Was it built from a specific COMMIT? (the sha claim, not the Subject)
cosign verify "${IMAGE}@${DIGEST}" \
  --certificate-identity-regexp '^https://github.com/Quinyte/assayd/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-sha "$(git rev-parse v0.4.0^{commit})"
```

Both were run against v0.4.0 on 2026-09-23 and both pass. The same identity with `…@refs/heads/main` fails, and so does the sha check with a wrong commit. The second command needs a clone that has the tag; `v0.4.0^{commit}` is `e061faca8863dabae4b475717a60db0175e9557d`. `--certificate-github-workflow-trigger push` also passes for v0.4.0, and `workflow_dispatch` fails.

**Tightening the workflow's own verification is still owed, and is not made here.** Replacing its regexp with an exact `--certificate-identity …@refs/tags/v${version}`, or adding `--certificate-github-workflow-ref refs/tags/v${version}`, would make the release refuse a wrong ref from the outside as well. It alters release behaviour and cannot be exercised without cutting a release. And it runs *after* the artifact has been pushed and signed, so a wrong claim string would fail a release half-published — precisely the v0.2.0 shape this page documents below. The guard above covers the same wrong-ref case *before* anything is published, for runs of a workflow that contains it.

So: **prefer a tag push.** If you must dispatch, start it from the tag itself (`refs/tags/<tag>` in "Use workflow from") on a tag whose `release.yml` has the guard — no tag up to and including `v0.4.1` does — and run the first command above against what was published.

**What the workflow verifies of itself, and what it does not.** Before it finishes, the image job runs `cosign verify` and `cosign verify-attestation --type spdxjson` against the digest it just pushed, and the chart job runs `cosign verify` against the chart digest. It **never** runs `cosign verify-attestation --type slsaprovenance1` and **never** runs `gh attestation verify`. So the signature and the SBOM attestation are checked by the release itself; **the SLSA provenance is not** — every provenance claim on this page comes from a dated manual check, named as such below.

**GHCR creates a new package private, whatever the repository's visibility,** and GitHub offers no API to change it. Each package is made public by hand in its package settings. **Both existing packages were made public on 2026-09-11.** Without any credentials, `ghcr.io/quinyte/assayd-operator:0.2.1` and `ghcr.io/quinyte/charts/assayd:0.2.1` pull (HTTP 200), `helm show chart oci://ghcr.io/quinyte/charts/assayd --version 0.2.1` answers, and `cosign verify` of the image signature succeeds. The same holds for `0.3.0`, checked on 2026-09-14 with no credentials: the image and the chart manifests answer HTTP 200. Any **new** package a future release creates will still start private, and needs the same manual change before anyone outside the organization can pull it.

**Use v0.2.1, not v0.2.0.** On 2026-09-11 a history scrub re-pointed the `v0.2.0` tag, and that re-ran its release. The re-run moved the image tag `ghcr.io/quinyte/assayd-operator:0.2.0` to a newly built image. It signed that image, then failed before attaching an SBOM or provenance: the SBOM action tried to upload to the already-existing GitHub Release without write permission, and `release.yml` no longer does that. So:

- **The `0.2.0` chart is still sound.** It pins the original, fully attested image by digest.
- **The `0.2.0` image tag is not.** Pulled by tag, it gets the unattested image, and `cosign verify-attestation` against that tag fails.

v0.2.1 is a clean re-release. It was verified from outside: signature, SBOM attestation (verified by the release run's own step), SLSA provenance, and a chart pinned to exactly the image's digest.

**The newest tag is `v0.4.1`** (commit `f480d9c`), and **this page has not been re-verified against it** — the paragraph below still describes v0.4.0, which is the last release anyone checked from outside. Nothing here should be read as a statement about v0.4.1's published artifacts: no `cosign verify` of them is recorded, and this page does not claim one.

**The last release verified from outside is v0.4.0**, checked on 2026-09-15 by hand. The image `sha256:232673c6ecbc0a497a6076cd0914e56286ae6f960ecc35ebffacb7bcf0241823` passes `cosign verify` and both `verify-attestation` types, and the chart `0.4.0` pins exactly that digest and passes its own `cosign verify`. v0.3.0 remains published and verifiable; its image is `sha256:30449f7ea1348ec393158997439bb2a6fddc78cb0fbf149b04614251add8643d`.

**v0.1.x were published under the project's old name,** as `plume-operator` and `charts/plume`. v0.1.0 came from the repository's earlier home, and v0.1.1 from the Quinyte organization. Those packages were **deleted on 2026-09-11**, when the rename was finished, because nothing will be published under the old name again. Their tags remain, and so do their signatures' Rekor entries, which are immutable. The artifacts do not, so neither release can be installed or verified today. v0.2.0 is the first release under the assayd name.

## Signing is keyless, and that is the point

Signatures come from Fulcio and are logged in Rekor, using GitHub's OIDC token — **there is no private key**. Nothing to store, rotate, or leak, and no "someone with the key signed it" to reason about. The signature binds an artifact to *a workflow, in a repository, at a commit*, which is a stronger and more checkable claim.

So the identity you verify against is a workflow, not a person:

```bash
IMAGE=ghcr.io/quinyte/assayd-operator
DIGEST=sha256:...          # from `helm show values`, or the release notes

cosign verify "${IMAGE}@${DIGEST}" \
  --certificate-identity-regexp '^https://github.com/Quinyte/assayd/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Verification failing is the correct outcome for anything that did not come from that workflow. **Do not relax the identity regexp to make it pass** — a signature that verifies against any identity verifies nothing.

## Read the SBOM before you run it

The SBOM is attached as a signed attestation rather than a release file, because an SBOM nobody can verify is documentation, not evidence:

```bash
cosign verify-attestation --type spdxjson "${IMAGE}@${DIGEST}" \
  --certificate-identity-regexp '^https://github.com/Quinyte/assayd/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq -r '.payload | @base64d | fromjson | .predicate.packages[].name'
```

**SLSA build provenance is attached to the registry from v0.2.0 on — while the repository stays public.** v0.1.x had none, because GitHub's attestation API refuses a user-owned private repository, and that is still a live condition rather than history: the step is guarded by `if: github.event.repository.visibility == 'public'`, so a release cut while the repository is private ships signed and SBOM-attested with no provenance and only a `::warning::` in the log. Check, do not assume: Verify it against the release workflow's identity. `cosign verify` checks the signature only, so the attestation needs its own command:

```bash
cosign verify-attestation --type slsaprovenance1 "${IMAGE}@${DIGEST}" \
  --certificate-identity-regexp '^https://github.com/Quinyte/assayd/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

`gh attestation verify oci://ghcr.io/quinyte/assayd-operator:0.4.0 --owner Quinyte` reads the same provenance, and needs no credentials now that the package is public. Checked on 2026-09-15: it verified one `https://slsa.dev/provenance/v1` attestation, signed by `.github/workflows/release.yml@refs/tags/v0.4.0`.

## Digests, not tags

The chart takes `operator.image.digest`, and **the digest wins over the tag when both are set** — the tag is dropped from the rendered reference rather than carried alongside it, because `repo:tag@digest` resolves by digest and the tag then reads as though it mattered.

A tag can be repointed at other content after it was signed. A digest names the content. The release workflow pins the published chart to the digest it just published and verified, so installing the published chart at defaults runs the artifact that was signed:

```bash
helm install assayd oci://ghcr.io/quinyte/charts/assayd --version 0.4.0
```

The chart **in the repository** carries an empty `digest`, because only a release knows it. Installing from a checkout therefore resolves by tag, unless you pass `--set operator.image.digest=sha256:...`.

## What is NOT yet true

Stated plainly, because a supply-chain page that overclaims is worse than none:

- **The chart in a checkout is not pinned.** Only the published chart carries the digest; see above.
- **No `.sig` verification at install time.** Nothing forces a cluster to reject an unsigned assayd chart; that is the Sigstore policy-controller's job (design 07 A2 chose it; A3 defines its enforcement contract) and assayd does not ship one for itself yet — while it *does* enforce exactly this for agent images.
- **A new package starts private.** GHCR sets visibility at creation, and GitHub has no API to change it. If a release ever publishes under a new package name, someone with admin rights on the organization has to make it public by hand. Until then, anyone outside the organization is refused with `403`. See above.
- **The release never verifies its own SLSA provenance.** It verifies the signature and the SBOM attestation from outside before it finishes; it runs neither `cosign verify-attestation --type slsaprovenance1` nor `gh attestation verify`. Every provenance statement on this page is a dated manual check by a person, and a release that attached no provenance — because the visibility guard skipped the step — would still finish green.
- **This page trails the tags.** It is hand-maintained, nothing regenerates it, and `v0.4.1` is tagged while the verified-from-outside record stops at `v0.4.0`. Read a version claim here as "last checked", never as "current".

## For reviewers

The bar this page claims to meet is the one assayd imposes on its users. If any item under "not yet true" would block adoption, say so — the gap is recorded here precisely so it is arguable rather than discovered.

## Running the loop locally

```bash
make e2e          # k3d: create, build, load, helm install, test
DISTRO=kind make e2e
```

A k3d run names its own cluster, so any number can run at once. A kind run does
not: it uses `kind-assayd-local` unless `CLUSTER` names another, so two kind runs
at the same time collide. Give each its own cluster, created first with
`kind create cluster --name`.

**On Colima, raise the inotify instance limit first.** The default is 128, and
every Kubernetes component wants watchers — with more than one k3d or kind
cluster resident, the k3s API server fails to start with `error creating
fsnotify watcher: too many open files`. The cluster is *created*, so the failure
presents as a missing load balancer or an unreachable API, which sends you
looking at the wrong thing:

```bash
colima ssh -- sudo sysctl -w fs.inotify.max_user_instances=8192
colima ssh -- sudo sh -c 'echo fs.inotify.max_user_instances=8192 > /etc/sysctl.d/99-inotify.conf'
```

Give the VM real resources too — `colima start --cpu 8 --memory 16`. This is not
assayd-specific; it affects any multi-cluster local Kubernetes work.

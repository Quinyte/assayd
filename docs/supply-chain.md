# Supply chain

assayd's admission rejects agent images that are not cosign-signed (ADR-0019), and design 07 §4 says the platform ships "as a cosign-signed OCI chart, all images pinned by digest, SBOM attached — **the same admission story agents get**." This page is how you check that assayd holds itself to it, without taking our word for anything.

## What is published, and where

| Artifact | Location | State at v0.2.1 |
|---|---|---|
| Operator image | `ghcr.io/quinyte/assayd-operator` (amd64, arm64) | published, cosign-signed, SBOM attested, SLSA provenance attached |
| Helm chart | `oci://ghcr.io/quinyte/charts/assayd` | published, cosign-signed, pinned to the image by digest |

Both are published only by `.github/workflows/release.yml`, on a `v*` tag, and the workflow verifies its own signatures from outside before it finishes.

**GHCR creates a new package private, whatever the repository's visibility,** and GitHub offers no API to change it. Each package is made public by hand in its package settings. **Both existing packages were made public on 2026-09-11.** Without any credentials, `ghcr.io/quinyte/assayd-operator:0.2.1` and `ghcr.io/quinyte/charts/assayd:0.2.1` pull (HTTP 200), `helm show chart oci://ghcr.io/quinyte/charts/assayd --version 0.2.1` answers, and `cosign verify` of the image signature succeeds. Any **new** package a future release creates will still start private, and needs the same manual change before anyone outside the organization can pull it.

**Use v0.2.1, not v0.2.0.** On 2026-09-11 a history scrub re-pointed the `v0.2.0` tag, and that re-ran its release. The re-run moved the image tag `ghcr.io/quinyte/assayd-operator:0.2.0` to a newly built image. It signed that image, then failed before attaching an SBOM or provenance: the SBOM action tried to upload to the already-existing GitHub Release without write permission, and `release.yml` no longer does that. So:

- **The `0.2.0` chart is still sound.** It pins the original, fully attested image by digest.
- **The `0.2.0` image tag is not.** Pulled by tag, it gets the unattested image, and `cosign verify-attestation` against that tag fails.

v0.2.1 is a clean re-release. It was verified from outside: signature, SBOM attestation (verified by the release run's own step), SLSA provenance, and a chart pinned to exactly the image's digest.

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

**SLSA build provenance is attached to the registry, from v0.2.0 on.** v0.1.x had none, because GitHub's attestation API refused the repository while it was private. Verify it against the release workflow's identity. `cosign verify` checks the signature only, so the attestation needs its own command:

```bash
cosign verify-attestation --type slsaprovenance1 "${IMAGE}@${DIGEST}" \
  --certificate-identity-regexp '^https://github.com/Quinyte/assayd/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

`gh attestation verify oci://ghcr.io/quinyte/assayd-operator:0.2.1 --owner Quinyte` reads the same provenance, once it can pull the image.

## Digests, not tags

The chart takes `operator.image.digest`, and **the digest wins over the tag when both are set** — the tag is dropped from the rendered reference rather than carried alongside it, because `repo:tag@digest` resolves by digest and the tag then reads as though it mattered.

A tag can be repointed at other content after it was signed. A digest names the content. The release workflow pins the published chart to the digest it just published and verified, so installing the published chart at defaults runs the artifact that was signed:

```bash
helm install assayd oci://ghcr.io/quinyte/charts/assayd --version 0.2.1
```

The chart **in the repository** carries an empty `digest`, because only a release knows it. Installing from a checkout therefore resolves by tag, unless you pass `--set operator.image.digest=sha256:...`.

## What is NOT yet true

Stated plainly, because a supply-chain page that overclaims is worse than none:

- **The chart in a checkout is not pinned.** Only the published chart carries the digest; see above.
- **No `.sig` verification at install time.** Nothing forces a cluster to reject an unsigned assayd chart; that is the Sigstore policy-controller's job (design 07 A2 chose it; A3 defines its enforcement contract) and assayd does not ship one for itself yet — while it *does* enforce exactly this for agent images.
- **A new package starts private.** GHCR sets visibility at creation, and GitHub has no API to change it. If a release ever publishes under a new package name, someone with admin rights on the organization has to make it public by hand. Until then, anyone outside the organization is refused with `403`. See above.

## For reviewers

The bar this page claims to meet is the one assayd imposes on its users. If any item under "not yet true" would block adoption, say so — the gap is recorded here precisely so it is arguable rather than discovered.

## Running the loop locally

```bash
make e2e          # k3d: create, build, load, helm install, test
DISTRO=kind make e2e
```

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

# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
# assayd — the loop: nothing merges without green tests and a critic PASS.
SHELL := /bin/bash
GOBIN := $(shell go env GOPATH)/bin
CONTROLLER_GEN := $(GOBIN)/controller-gen
SETUP_ENVTEST  := $(GOBIN)/setup-envtest
CRDOC          := $(GOBIN)/crdoc
ENVTEST_K8S    ?= 1.36.x
# `make envtest` runs the suite once. A scheduled workflow reruns it with
# ENVTEST_COUNT=3 so that a test which cannot run twice in one process is caught,
# which a single run never shows (#32). The default timeout is go test's own.
ENVTEST_COUNT   ?= 1
ENVTEST_TIMEOUT ?= 10m
CLUSTER        ?= assayd-local

# Tool versions are pinned here, not floated with @latest. A build whose output
# depends on when it ran is not reproducible, and `make verify` would fail for
# whoever happened to pick up a new controller-gen first. CI proved the older
# assumption wrong on its first run: the tools were present on one laptop and
# absent everywhere else.
CONTROLLER_TOOLS_VERSION ?= v0.21.0
ENVTEST_VERSION          ?= release-0.24
# crdoc renders the FIELD TABLES of docs/reference/crd-agent.md — names, types,
# required, defaults, doc text and the nested-type sections. Pinned like the
# rest: `make verify` compares the committed reference against a fresh one, so a
# generator that floated would fail the gate for whoever picked up a new version
# first.
#
# It is NOT relied on for the CEL rules, and an earlier version of this comment
# said it was chosen for them. It renders a Validations block for an
# object-typed field and none for an array-item schema or the root, so it
# published 6 of this CRD's 9 rule-carrying nodes and said nothing about the
# rest — the same silent dropping of x-kubernetes-validations that ruled out
# elastic/crd-ref-docs. internal/refgen/crdrules.go reads every rule out of the
# CRD itself and refuses to write the page if one is missing.
#
# Apache-2.0. Its own `--version` self-reports v0.6.2 at this module version;
# the module version here is what is pinned.
#
# COST, and why it is paid this way. This is a `pkg@version` install outside the
# main module, so its dependency graph is in neither go.mod nor go.sum, and
# setup-go's module cache does not key on it and does not re-save on a hit —
# roughly 16 s and 176 MB on every CI run (measured by the independent review of
# PR #59).
#
# Go 1.24's `tool` directive would fix that and was measured instead of assumed:
# `go get -tool fybrik.io/crdoc@v0.6.4` in an empty module pulls 226 modules and
# 556 go.sum lines, and pins OLDER versions of five libraries this repository
# already depends on at newer ones — sigs.k8s.io/yaml v1.3.0 against our v1.6.0,
# go-logr v1.3.0 against v1.4.3, structured-merge-diff v4 against v6. Merging
# those graphs means minimum version selection builds crdoc against libraries it
# was never tested with, to make a docs generator cheaper. Not worth it.
#
# So ci.yml caches the BINARY instead, keyed on this version, which is what
# `make print-crdoc-version` exists for. The $(CRDOC) rule below is a file
# target, so a restored binary skips the install entirely. Bump this version and
# the cache key changes with it.
CRDOC_VERSION            ?= v0.6.4

.PHONY: help
help: ## show targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

## ---------- tools ----------
# Installed on demand and pinned. Every target that needs a tool depends on it,
# so a fresh clone or a fresh CI runner works without a setup document nobody
# reads.
.PHONY: tools
tools: $(CONTROLLER_GEN) $(SETUP_ENVTEST) $(CRDOC) ## install the pinned build tools

$(CONTROLLER_GEN):
	GOBIN=$(GOBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

$(SETUP_ENVTEST):
	GOBIN=$(GOBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)

$(CRDOC):
	GOBIN=$(GOBIN) go install fybrik.io/crdoc@$(CRDOC_VERSION)

## ---------- generate ----------
.PHONY: generate manifests reference
generate: $(CONTROLLER_GEN) ## deepcopy funcs
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths=./api/...
manifests: $(CONTROLLER_GEN) ## CRDs + RBAC
	$(CONTROLLER_GEN) crd rbac:roleName=assayd-operator paths=./... output:crd:artifacts:config=config/crd output:rbac:artifacts:config=config/rbac

# The public reference under docs/reference/: the CRD schema, the chart values,
# and the operator's condition and reason vocabulary — derived from config/crd,
# charts/assayd and internal/controller respectively, never hand-written. This
# repository has already shipped two hand-kept copies of one document that
# drifted apart (docs/architecture.md and docs/architecture.html); a reference on
# a public website is the same liability with a larger blast radius.
#
# It depends on `manifests`: the CRD page is rendered from config/crd, so
# `make reference` alone must not be able to publish a schema the cluster does
# not enforce. `verify` therefore does NOT run `manifests` itself — it lets this
# prerequisite do it, which is one controller-gen run for the whole check rather
# than two.
reference: $(CRDOC) manifests ## docs/reference/ — generated from the CRD, the chart and the code
	PATH="$(GOBIN):$$PATH" go run ./cmd/refgen -root . -out docs/reference

.PHONY: print-crdoc-version
print-crdoc-version: ## the pinned crdoc version, for CI to key a cache on
	@echo $(CRDOC_VERSION)

## ---------- the loop ----------
.PHONY: fmt vet unit envtest docs conformance conformance-cluster chart chart-conform release-workflow test race cover e2e verify
fmt: ; go fmt ./...
vet: ; go vet ./...
unit: ## pure logic, no cluster
	go test ./internal/... ./api/... -count=1
race: ## unit tests under the race detector (required evidence for review)
	go test -race ./internal/... ./api/... -count=1
envtest: $(SETUP_ENVTEST) ## reconcile behaviour against a real API server
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S) -p path)" go test ./test/envtest/... -count=$(ENVTEST_COUNT) -timeout=$(ENVTEST_TIMEOUT)
cover: ## coverage over changed packages
	go test ./internal/... ./api/... -coverprofile=cover.out -count=1 && go tool cover -func=cover.out | tail -1
docs: ## a superseded guarantee must not survive in the text implementers build from
	go test ./test/docs/... -count=1

conformance: ## the dependency contract our design asserts, read from the pinned artifact
	go test ./test/conformance/... -count=1

conformance-cluster: ## the same contract, measured against a real gateway (provisions and tears down)
	./hack/conformance-cluster.sh

chart: ## render the chart and hold it to the doctrine (pods, stateful deps, RBAC)
	helm lint charts/assayd
	go test ./test/chart/... -count=1

release-workflow: ## a dispatch publishes only the tag it was started from; nothing publishes after a refusal
	go test ./test/release/... -count=1

chart-conform: ## validate rendered manifests against the k8s schemas we support
	@for v in 1.34.0 1.35.0 1.36.0; do \
		echo "==> kubeconform $$v"; \
		helm template assayd charts/assayd | kubeconform -strict -summary \
			-kubernetes-version $$v -ignore-missing-schemas || exit 1; \
	done

test: fmt vet unit docs conformance envtest chart release-workflow ## the pre-commit gate
e2e: ## real cluster path on k3d (design 07's matrix, locally)
	./hack/e2e.sh
verify: ## what CI runs — generation must be reproducible
# `git diff` cannot see an untracked file, so a newly generated CRD that nobody
# committed would pass this gate silently. --porcelain reports both.
	@$(MAKE) generate
# The generated reference is held to the same bar as the generated CRD. A
# generator nobody runs is worse than no generator: its output reads as current
# and is not. Regenerating here makes a stale docs/reference/ fail CI in the
# same breath as a stale config/crd/.
#
# `reference` depends on `manifests`, so this line regenerates the CRD too and
# the copies below take its output. Running `manifests` separately as well ran
# controller-gen twice for one check.
	@$(MAKE) reference
	@cp config/crd/assayd.dev_agents.yaml charts/assayd/crds/assayd.dev_agents.yaml
	@sed -n '/^rules:/,$$p' config/rbac/role.yaml > charts/assayd/files/operator-rules.yaml
# go.mod is generated too, by a tool with an opinion. Nothing ran tidy, and
# go.yaml.in/yaml/v3 sat marked `// indirect` while two files imported it
# directly — harmless today, and the kind of untruth a reader of go.mod would
# reason from.
	@go mod tidy
	@out="$$(git status --porcelain -- api config charts docs/reference go.mod go.sum)"; \
	if [ -n "$$out" ]; then \
		echo "generated files are stale or uncommitted — run 'make generate manifests reference' and commit:"; \
		echo "$$out"; \
		exit 1; \
	fi
	@echo "generation is reproducible and committed"

## ---------- local cluster ----------
.PHONY: cluster-up cluster-down install-crds
cluster-up: ## k3d cluster matching the local profile
	k3d cluster list | grep -q '^$(CLUSTER) ' || k3d cluster create $(CLUSTER) --agents 1 --wait
cluster-down: ; -k3d cluster delete $(CLUSTER)
install-crds: manifests ## apply CRDs to the current context
	kubectl apply -f config/crd

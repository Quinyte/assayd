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
# crdoc renders the CRD half of docs/reference/. Pinned like the rest: `make
# verify` compares the committed reference against a fresh one, so a generator
# that floated would fail the gate for whoever happened to pick up a new version
# first. Chosen over elastic/crd-ref-docs, which is better maintained and reads
# the Go types directly but DROPS x-kubernetes-validations — and this CRD's
# load-bearing constraints are CEL. Apache-2.0. Its own `--version` self-reports
# v0.6.2 at this module version; the module version here is what is pinned.
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
# It depends on manifests: the CRD page is rendered from config/crd, so
# generating against a stale CRD would publish a schema the cluster does not
# enforce.
reference: $(CRDOC) manifests ## docs/reference/ — generated from the CRD, the chart and the code
	PATH="$(GOBIN):$$PATH" go run ./cmd/refgen -root . -out docs/reference

## ---------- the loop ----------
.PHONY: fmt vet unit envtest docs conformance conformance-cluster chart chart-conform test race cover e2e verify
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

chart-conform: ## validate rendered manifests against the k8s schemas we support
	@for v in 1.34.0 1.35.0 1.36.0; do \
		echo "==> kubeconform $$v"; \
		helm template assayd charts/assayd | kubeconform -strict -summary \
			-kubernetes-version $$v -ignore-missing-schemas || exit 1; \
	done

test: fmt vet unit docs conformance envtest chart ## the pre-commit gate
e2e: ## real cluster path on k3d (design 07's matrix, locally)
	./hack/e2e.sh
verify: ## what CI runs — generation must be reproducible
# `git diff` cannot see an untracked file, so a newly generated CRD that nobody
# committed would pass this gate silently. --porcelain reports both.
	@$(MAKE) generate manifests
	@cp config/crd/assayd.dev_agents.yaml charts/assayd/crds/assayd.dev_agents.yaml
	@sed -n '/^rules:/,$$p' config/rbac/role.yaml > charts/assayd/files/operator-rules.yaml
# The generated reference is held to the same bar as the generated CRD. A
# generator nobody runs is worse than no generator: its output reads as current
# and is not. Regenerating here makes a stale docs/reference/ fail CI in the
# same breath as a stale config/crd/.
	@$(MAKE) reference
	@out="$$(git status --porcelain -- api config charts docs/reference)"; \
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

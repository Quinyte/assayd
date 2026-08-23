# plume — the loop: nothing merges without green tests and a critic PASS.
SHELL := /bin/bash
GOBIN := $(shell go env GOPATH)/bin
CONTROLLER_GEN := $(GOBIN)/controller-gen
SETUP_ENVTEST  := $(GOBIN)/setup-envtest
ENVTEST_K8S    ?= 1.36.x
CLUSTER        ?= plume-local

.PHONY: help
help: ## show targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

## ---------- generate ----------
.PHONY: generate manifests
generate: ## deepcopy funcs
	$(CONTROLLER_GEN) object paths=./api/...
manifests: ## CRDs + RBAC
	$(CONTROLLER_GEN) crd rbac:roleName=plume-operator paths=./... output:crd:artifacts:config=config/crd output:rbac:artifacts:config=config/rbac

## ---------- the loop ----------
.PHONY: fmt vet unit envtest chart chart-conform test race cover e2e verify
fmt: ; go fmt ./...
vet: ; go vet ./...
unit: ## pure logic, no cluster
	go test ./internal/... ./api/... -count=1
race: ## unit tests under the race detector (required evidence for review)
	go test -race ./internal/... ./api/... -count=1
envtest: ## reconcile behaviour against a real API server
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S) -p path)" go test ./test/envtest/... -count=1
cover: ## coverage over changed packages
	go test ./internal/... ./api/... -coverprofile=cover.out -count=1 && go tool cover -func=cover.out | tail -1
chart: ## render the chart and hold it to the doctrine (pods, stateful deps, RBAC)
	helm lint charts/plume
	go test ./test/chart/... -count=1

chart-conform: ## validate rendered manifests against the k8s schemas we support
	@for v in 1.34.0 1.35.0 1.36.0; do \
		echo "==> kubeconform $$v"; \
		helm template plume charts/plume | kubeconform -strict -summary \
			-kubernetes-version $$v -ignore-missing-schemas || exit 1; \
	done

test: fmt vet unit envtest chart ## the pre-commit gate
e2e: ## real cluster path on k3d (design 07's matrix, locally)
	./hack/e2e.sh
verify: ## what CI runs — generation must be reproducible
# `git diff` cannot see an untracked file, so a newly generated CRD that nobody
# committed would pass this gate silently. --porcelain reports both.
	@$(MAKE) generate manifests
	@cp config/crd/plume.dev_agents.yaml charts/plume/crds/plume.dev_agents.yaml
	@sed -n '/^rules:/,$$p' config/rbac/role.yaml > charts/plume/files/operator-rules.yaml
	@out="$$(git status --porcelain -- api config charts)"; \
	if [ -n "$$out" ]; then \
		echo "generated files are stale or uncommitted — run 'make generate manifests' and commit:"; \
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

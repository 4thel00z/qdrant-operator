.PHONY: help dev build lint format test envtest test-integration e2e-image e2e-deploy test-e2e logo image image-multiarch push push-multiarch helm-lint helm-template helm-package install uninstall manifests crds-sync crds-install crds-uninstall clean

IMAGE_REPO ?= ghcr.io/4thel00z/qdrant-operator
IMAGE_TAG ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
VERSION ?= $(IMAGE_TAG)
NAMESPACE ?= qdrant-system
HELM_RELEASE ?= qdrant-operator
KIND_CLUSTER ?= kind
E2E_IMAGE_REPO ?= qdrant-operator
E2E_IMAGE_TAG ?= e2e

ENVTEST_K8S_VERSION ?= 1.37.0
SETUP_ENVTEST ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.3

export CGO_ENABLED := 0

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

dev: ## Run the operator locally against the current kubeconfig
	go run ./cmd/operator --logging-format text

build: ## Build the operator binary into bin/
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/qdrant-operator ./cmd/operator

lint: ## Check formatting, vet and lint the chart
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go vet -tags integration,e2e ./...
	helm lint charts/qdrant-operator

format: ## Format Go code
	gofmt -w .

envtest: ## Download the pinned envtest control plane and print its path
	@$(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path

test: ## Run unit and envtest tests
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go test ./... -count=1

test-integration: ## Run the Helm adapter integration test against the current kubeconfig
	go test -tags integration ./internal/adapters/... -count=1 -v

e2e-image: ## Build the operator image for the kind end-to-end tests
	docker build -t $(E2E_IMAGE_REPO):$(E2E_IMAGE_TAG) .

e2e-deploy: ## Load the e2e image into kind and install the chart from this checkout
	kind load docker-image $(E2E_IMAGE_REPO):$(E2E_IMAGE_TAG) --name $(KIND_CLUSTER)
	helm upgrade --install $(HELM_RELEASE) charts/qdrant-operator \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(E2E_IMAGE_REPO) \
		--set image.tag=$(E2E_IMAGE_TAG) \
		--set image.pullPolicy=Never \
		--set operator.logFormat=text \
		--wait --timeout 5m
	kubectl rollout restart deployment -n $(NAMESPACE) -l app.kubernetes.io/instance=$(HELM_RELEASE)
	kubectl rollout status deployment -n $(NAMESPACE) -l app.kubernetes.io/instance=$(HELM_RELEASE) --timeout 5m

test-e2e: ## Run the end-to-end tests against the operator deployed by e2e-deploy
	go test -tags e2e ./test/e2e/... -count=1 -v -timeout 60m

logo: ## Render PNG logos from assets/logo.svg
	rsvg-convert -w 512 -h 512 assets/logo.svg -o assets/logo.png
	rsvg-convert -w 128 -h 128 assets/logo.svg -o assets/logo-128.png

image: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE_REPO):$(IMAGE_TAG) .

image-multiarch: ## Build the multi-arch container image
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) -t $(IMAGE_REPO):$(IMAGE_TAG) .

push: ## Push the container image
	docker push $(IMAGE_REPO):$(IMAGE_TAG)

push-multiarch: ## Build and push the multi-arch container image
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) -t $(IMAGE_REPO):$(IMAGE_TAG) --push .

helm-lint: ## Lint the Helm chart
	helm lint charts/qdrant-operator

helm-template: ## Render the Helm chart
	helm template $(HELM_RELEASE) charts/qdrant-operator

helm-package: ## Package the Helm chart
	helm package charts/qdrant-operator

install: ## Install the operator via Helm
	helm upgrade --install $(HELM_RELEASE) charts/qdrant-operator \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(IMAGE_REPO) \
		--set image.tag=$(IMAGE_TAG)

uninstall: ## Uninstall the operator
	helm uninstall $(HELM_RELEASE) --namespace $(NAMESPACE)

manifests: ## Generate deepcopy methods and CRDs from api/v1alpha1 into manifests/crds and the chart
	hack/gen-crds.sh manifests/crds
	$(MAKE) crds-sync

crds-sync: ## Copy manifests/crds into the Helm chart
	cp manifests/crds/*.yaml charts/qdrant-operator/crds/

crds-install: ## Install CRDs only
	kubectl apply -f manifests/crds/

crds-uninstall: ## Uninstall CRDs (WARNING: deletes all CR instances)
	kubectl delete -f manifests/crds/

clean: ## Remove build artifacts
	rm -rf bin/ dist/ docs/book/

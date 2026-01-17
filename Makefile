.PHONY: help build push install uninstall lint test clean

# Variables
IMAGE_REPO ?= ghcr.io/qdrant/qdrant-operator
IMAGE_TAG ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
NAMESPACE ?= qdrant-system
HELM_RELEASE ?= qdrant-operator

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# Development
dev: ## Run operator locally
	uv run kopf run src/qdrant_operator/main.py --verbose

lint: ## Run linters
	uv run ruff check src tests
	uv run ruff format --check src tests
	uv run pyright src
	helm lint charts/qdrant-operator

format: ## Format code
	uv run ruff format src tests
	uv run ruff check --fix src tests

test: ## Run tests
	uv run pytest -v

test-cov: ## Run tests with coverage
	uv run pytest --cov=src --cov-report=term-missing

# Docker
build: ## Build Docker image
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

build-multiarch: ## Build multi-arch Docker image
	docker buildx build --platform linux/amd64,linux/arm64 -t $(IMAGE_REPO):$(IMAGE_TAG) .

push: ## Push Docker image
	docker push $(IMAGE_REPO):$(IMAGE_TAG)

push-multiarch: ## Build and push multi-arch Docker image
	docker buildx build --platform linux/amd64,linux/arm64 -t $(IMAGE_REPO):$(IMAGE_TAG) --push .

# Helm
helm-lint: ## Lint Helm chart
	helm lint charts/qdrant-operator

helm-template: ## Template Helm chart
	helm template $(HELM_RELEASE) charts/qdrant-operator

helm-package: ## Package Helm chart
	helm package charts/qdrant-operator

install: ## Install operator via Helm
	helm upgrade --install $(HELM_RELEASE) charts/qdrant-operator \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(IMAGE_REPO) \
		--set image.tag=$(IMAGE_TAG)

uninstall: ## Uninstall operator
	helm uninstall $(HELM_RELEASE) --namespace $(NAMESPACE)

# CRDs
crds-install: ## Install CRDs only
	kubectl apply -f manifests/crds/

crds-uninstall: ## Uninstall CRDs (WARNING: deletes all CR instances)
	kubectl delete -f manifests/crds/

# Cleanup
clean: ## Clean build artifacts
	rm -rf dist/ build/ *.egg-info .pytest_cache .mypy_cache .ruff_cache
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true

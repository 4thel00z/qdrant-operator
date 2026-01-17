FROM python:3.12-slim

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Helm 3 (multi-arch support)
ARG HELM_VERSION=v3.14.0
ARG TARGETARCH
RUN ARCH=${TARGETARCH:-amd64} && \
    curl -fsSL https://get.helm.sh/helm-${HELM_VERSION}-linux-${ARCH}.tar.gz | tar xz && \
    mv linux-${ARCH}/helm /usr/local/bin/helm && \
    rm -rf linux-${ARCH} && \
    helm version

# Install uv for fast dependency management
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# Set working directory
WORKDIR /app

# Copy project files
COPY pyproject.toml uv.lock ./
COPY src/ ./src/

# Install dependencies
RUN uv sync --frozen --no-dev

# Create non-root user
RUN useradd -m -u 1000 operator
USER operator

# Run the operator
ENTRYPOINT ["uv", "run", "kopf", "run", "src/qdrant_operator/main.py", "--verbose"]

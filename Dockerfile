FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH:-$(go env GOARCH)} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/qdrant-operator ./cmd/operator

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/qdrant-operator /qdrant-operator
EXPOSE 8080 9090
USER 65532:65532
ENTRYPOINT ["/qdrant-operator"]

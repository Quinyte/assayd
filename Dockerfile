# Two stages: build with the toolchain, ship without it.
FROM golang:1.26 AS build
WORKDIR /src
# Dependencies first, so a code change does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
# CGO off and a static binary, so the runtime image can be distroless-static.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/operator ./cmd/operator

# nonroot, not static: the operator runs as UID 65532 and the chart asserts
# runAsNonRoot, so a base image without a non-root user would fail admission.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]

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

# Pinned by digest, not by tag. `:nonroot` is rebuilt upstream, so the same
# source would otherwise produce different bytes over time — which is the exact
# property plume refuses to accept from agent images (ADR-0019). Renovate bumps
# this; a human reads the diff.
#
# nonroot rather than static: the operator runs as UID 65532 and the chart
# asserts runAsNonRoot, so a base without a non-root user fails admission.
# gcr.io/distroless/static-debian12:nonroot
FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /
COPY --from=build /out/operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]

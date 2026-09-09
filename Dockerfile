# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
# Two stages: build with the toolchain, ship without it.
# --platform=$BUILDPLATFORM pins this stage to the RUNNER's architecture, and Go
# cross-compiles to $TARGETARCH from there. Without it, buildx runs the arm64
# compile under QEMU emulation — roughly 10-20x slower, because it emulates a
# CPU to run a compiler that does not need emulating. A multi-arch release went
# from >20 minutes to about one.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
# Dependencies first, so a code change does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
# CGO off and a static binary, so the runtime image can be distroless-static.
# -trimpath strips local paths, which is a prerequisite for reproducibility.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/operator ./cmd/operator

# Pinned by digest, not by tag. `:nonroot` is rebuilt upstream, so the same
# source would otherwise produce different bytes over time — which is the exact
# property assayd refuses to accept from agent images (ADR-0019). Renovate bumps
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

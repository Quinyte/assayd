# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
# The e2e's MCP tool server: a real Streamable HTTP MCP server with two tools,
# so that ADR-0030's "one MCP tool call through the gateway" can be measured
# rather than asserted. Nothing in this repository could make an MCP call before
# it, because there was no MCP server anywhere to call.
#
# Same shape and same reasons as test/responder/Dockerfile — read that one for
# the rationale behind --platform, CGO_ENABLED=0 and the digest-pinned base.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY test/mcpserver/ test/mcpserver/
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/mcpserver ./test/mcpserver

# gcr.io/distroless/static-debian12:nonroot — pinned by digest for the reason
# ADR-0019 gives: a tag is not content.
FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /
COPY --from=build /out/mcpserver /mcpserver
# The chart asserts runAsNonRoot and the operator renders a read-only root
# filesystem with all capabilities dropped, so the fixture has to satisfy the
# same PodSecurity the real thing does — otherwise it would prove the operator
# can deploy something no real agent could be.
USER 65532:65532
ENTRYPOINT ["/mcpserver"]

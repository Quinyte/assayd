# The e2e's agent image: a real container that serves an A2A card and answers a
# task. It replaces `registry.k8s.io/pause`, which served nothing, so no test in
# this repository ever sent a request to an agent.
#
# Same shape as the operator's Dockerfile, and for the same reasons — read that
# one for the rationale behind --platform, CGO_ENABLED=0 and the digest-pinned
# base. This is a test fixture and is not published, but it is built the way a
# real agent image must be: the chart's admission rejects a tagged image, so a
# fixture built any other way could not be deployed by the thing it tests.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY test/responder/ test/responder/
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/responder ./test/responder

# gcr.io/distroless/static-debian12:nonroot — pinned by digest for the reason
# ADR-0019 gives: a tag is not content.
FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /
COPY --from=build /out/responder /responder
# The chart asserts runAsNonRoot and the operator renders a read-only root
# filesystem with all capabilities dropped, so the fixture has to satisfy the
# same PodSecurity the real thing does — otherwise it would prove the operator
# can deploy something no real agent could be.
USER 65532:65532
ENTRYPOINT ["/responder"]

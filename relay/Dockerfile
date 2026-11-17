# The localai/relay image: a gated LocalAI relay and nothing else.
#
# Build from the repository root:
#   docker build -f relay/Dockerfile -t localai/relay .
#
# The image holds one static binary (cmd/localai-relay), which has the flags of
# `local-ai p2p relay`. It has no shell, no certificates and no package
# manager. It runs as a user without privileges and keeps its key and its
# allowlist in /data.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/localai-relay ./cmd/localai-relay
COPY core/services/p2pnet ./core/services/p2pnet
COPY core/services/p2prelay ./core/services/p2prelay
COPY pkg/httpclient ./pkg/httpclient
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/localai-relay ./cmd/localai-relay
# The data folder must exist and belong to the runtime user, because a
# scratch image has no tools to create it.
RUN mkdir -p /out/data && chown 10001:10001 /out/data && chmod 0700 /out/data

FROM scratch
COPY --from=build /out/localai-relay /localai-relay
COPY --from=build --chown=10001:10001 /out/data /data
USER 10001:10001
ENV LOCALAI_RELAY_DATA=/data
VOLUME /data
EXPOSE 4001/tcp 4001/udp
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/localai-relay", "--healthcheck"]
ENTRYPOINT ["/localai-relay"]

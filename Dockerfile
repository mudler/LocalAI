ARG GO_VERSION=1.20
ARG DEBIAN_VERSION=11
ARG BUILD_TYPE=

FROM golang:$GO_VERSION as builder
WORKDIR /build
RUN apt-get update && apt-get install -y cmake
COPY . .
RUN make build

FROM debian:$DEBIAN_VERSION
COPY --from=builder /build/local-ai /usr/bin/local-ai
ENTRYPOINT [ "/usr/bin/local-ai" ]

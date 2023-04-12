ARG GO_VERSION=1.20
ARG DEBIAN_VERSION=11

FROM golang:$GO_VERSION as builder

WORKDIR /build
RUN git clone --recurse-submodules https://github.com/go-skynet/go-llama.cpp
RUN cd go-llama.cpp && make libbinding.a
COPY go.mod ./
COPY go.sum ./
RUN go mod download
RUN apt-get update
COPY . .
RUN go mod edit -replace github.com/go-skynet/go-llama.cpp=/build/go-llama.cpp
RUN C_INCLUDE_PATH=/build/go-llama.cpp LIBRARY_PATH=/build/go-llama.cpp go build -o llama-cli ./

FROM debian:$DEBIAN_VERSION
COPY --from=builder /build/llama-cli /usr/bin/llama-cli
ENTRYPOINT [ "/usr/bin/llama-cli" ]
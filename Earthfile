VERSION 0.7

go-deps:
    ARG GO_VERSION=1.20
    FROM golang:$GO_VERSION
    WORKDIR /build
    COPY go.mod ./
    COPY go.sum ./
    RUN go mod download
    RUN apt-get update
    SAVE ARTIFACT go.mod AS LOCAL go.mod
    SAVE ARTIFACT go.sum AS LOCAL go.sum

build:
    FROM +go-deps
    WORKDIR /build
    RUN git clone --recurse-submodules https://github.com/go-skynet/go-llama.cpp
    RUN cd go-llama.cpp && make libbinding.a
    COPY . .
    RUN go mod edit -replace github.com/go-skynet/go-llama.cpp=/build/go-llama.cpp
    RUN C_INCLUDE_PATH=$GOPATH/src/github.com/go-skynet/go-llama.cpp LIBRARY_PATH=$GOPATH/src/github.com/go-skynet/go-llama.cpp go build -o llama-cli ./
    SAVE ARTIFACT llama-cli AS LOCAL llama-cli

image:
    FROM +go-deps
    ARG IMAGE=alpaca-cli-nomodel
    COPY +build/llama-cli /llama-cli
    ENTRYPOINT [ "/llama-cli" ]
    SAVE IMAGE --push $IMAGE

image-all:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +image

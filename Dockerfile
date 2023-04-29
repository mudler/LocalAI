ARG GO_VERSION=1.20
ARG BUILD_TYPE=
FROM golang:$GO_VERSION
WORKDIR /build
RUN apt-get update && apt-get install -y cmake
COPY . .
RUN make prepare-sources
EXPOSE 8080
ENTRYPOINT [ "/build/entrypoint.sh" ]

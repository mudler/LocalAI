ARG GO_VERSION=1.20
ARG BUILD_TYPE=
FROM golang:$GO_VERSION
ENV REBUILD=true
WORKDIR /build
RUN apt-get update && apt-get install -y cmake curl libgomp1 libopenblas-dev libopenblas-base libopencv-dev libopencv-core-dev libopencv-core4.5 ca-certificates
COPY . .
RUN ln -s /usr/include/opencv4/opencv2/ /usr/include/opencv2
RUN make build
ENV HEALTHCHECK_ENDPOINT=http://localhost:8080/readyz
# Define the health check command
HEALTHCHECK --interval=30s --timeout=360s --retries=10 \
  CMD curl -f $HEALTHCHECK_ENDPOINT || exit 1
EXPOSE 8080
ENTRYPOINT [ "/build/entrypoint.sh" ]

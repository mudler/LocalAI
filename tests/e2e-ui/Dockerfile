ARG GO_VERSION=1.25.4
ARG PLAYWRIGHT_VERSION=v1.52.0

###################################
# Stage 1: Build React UI
###################################
FROM node:25-slim AS react-ui-builder
WORKDIR /app
COPY core/http/react-ui/package*.json ./
RUN npm install
COPY core/http/react-ui/ ./
RUN npm run build

###################################
# Stage 2: Build Go test server + mock backend
###################################
FROM ubuntu:24.04 AS go-builder

ARG GO_VERSION
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential ca-certificates curl git unzip libopus-dev pkg-config && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Go
RUN curl -L -s https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz | tar -C /usr/local -xz
ENV PATH=$PATH:/root/go/bin:/usr/local/go/bin

# Install protoc + Go gRPC tools
RUN <<EOT bash
    if [ "amd64" = "$TARGETARCH" ]; then
        curl -L -s https://github.com/protocolbuffers/protobuf/releases/download/v27.1/protoc-27.1-linux-x86_64.zip -o protoc.zip
    elif [ "arm64" = "$TARGETARCH" ]; then
        curl -L -s https://github.com/protocolbuffers/protobuf/releases/download/v27.1/protoc-27.1-linux-aarch_64.zip -o protoc.zip
    fi
    unzip -j -d /usr/local/bin protoc.zip bin/protoc && rm protoc.zip
EOT
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af

WORKDIR /build
COPY . .

# Copy pre-built React UI so it gets embedded
COPY --from=react-ui-builder /app/dist ./core/http/react-ui/dist

# Generate protobuf Go code, build mock backend and UI test server
RUN make protogen-go && \
    go build -o /out/mock-backend ./tests/e2e/mock-backend && \
    go build -o /out/ui-test-server ./tests/e2e-ui

###################################
# Stage 3: Run Playwright tests
###################################
FROM mcr.microsoft.com/playwright:${PLAYWRIGHT_VERSION}-noble

# Install runtime dependency for the Go binary (opus codec support)
RUN apt-get update && \
    apt-get install -y --no-install-recommends libopus0 && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /work

# Copy test server binaries
COPY --from=go-builder /out/ui-test-server /usr/local/bin/ui-test-server
COPY --from=go-builder /out/mock-backend /usr/local/bin/mock-backend

# Install Playwright test dependencies
COPY core/http/react-ui/package.json core/http/react-ui/playwright.config.js ./
RUN npm install

# Copy test specs
COPY core/http/react-ui/e2e ./e2e

ENV CI=true
ENV PLAYWRIGHT_EXTERNAL_SERVER=1

CMD ["bash", "-c", "\
    ui-test-server --mock-backend=/usr/local/bin/mock-backend --port=8089 & \
    for i in $(seq 1 30); do \
        curl -sf http://127.0.0.1:8089/readyz > /dev/null 2>&1 && break; \
        sleep 1; \
    done && \
    npx playwright test; \
    TEST_EXIT=$?; \
    kill %1 2>/dev/null; \
    exit $TEST_EXIT \
"]

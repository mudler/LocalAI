# A musl host for the smoke test of the portable AppImage: Alpine, which has
# no /lib64/ld-linux-x86-64.so.2. bash is what the AppImage needs from the
# host (the start script of electron-builder). The base is pinned by digest.
#   docker build -t localai-launcher-musl-smoke -f musl-smoke.Dockerfile .
FROM alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f
RUN apk add --no-cache bash

# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
#
# Sandbox host image: the host binaries that sandbox/chv needs, at pinned
# versions, and the non-root user "sandbox" (uid 1000). A product copies its
# binary in with FROM. Run it with:
#
#   docker run --device /dev/kvm --group-add "$(stat -c %g /dev/kvm)" ...
#
# The group is needed when /dev/kvm is not world-writable. Bubblewrap cannot
# make namespaces in a default container, so the image sets
# SANDBOX_NO_BWRAP=1; a product reads it and sets chv.Options.NoBwrap. The
# container is then the isolation of virtiofsd and debugfs.
#
# Boots in this container are much slower than on the host: about 10 times
# (TestGuestHardening 0.9 s on the host and 13 s in the container;
# TestConvertedImageBootsWithFileKinds 2.2 s and 25 s). The likely causes are
# Docker's default seccomp profile and the overlay /tmp. Give the tests and
# product timeouts room for a container. Cheap ways to speed boots up that
# keep the isolation: `--tmpfs /tmp:exec,mode=1777` (measured: TestInitExitFailsFast
# 9.2 s to 4.0 s, TestGuestHardening 5.9 s to 3.8 s). Optional hardening that
# still works: `--cap-drop=ALL --security-opt no-new-privileges` (both tests
# pass with it and take the same time). Do not set seccomp=unconfined for speed.

ARG DEBIAN=debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f

FROM ${DEBIAN}
ARG TARGETARCH
ARG CH_VERSION=v54.0
ARG CH_SHA256_AMD64=4f4f98a3d661a40f3e5272627cc895a1b2e525f2f6bedf5f895f9cd21ba5c65b
ARG CH_SHA256_ARM64=e8a6d9786ea678e739f1a386682846e93742dcb9c7b6e3e0c62e3bda6cff0ef3
# virtiofsd publishes a static binary for x86_64 only. On arm64 the image
# uses the Debian package (1.13.2).
ARG VIRTIOFSD_URL=https://gitlab.com/-/project/21523468/uploads/f505704014ae7a816e515f2a05a93d8b/virtiofsd-v1.14.0.zip
ARG VIRTIOFSD_SHA256=2e4fe9571f492b00baa34bc4e708e950039c5da05b830b31a8d179cb6ac8978e
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends bubblewrap ca-certificates curl e2fsprogs unzip; \
    case "${TARGETARCH}" in \
      amd64) ch=cloud-hypervisor-static; sum="${CH_SHA256_AMD64}" ;; \
      arm64) ch=cloud-hypervisor-static-aarch64; sum="${CH_SHA256_ARM64}" ;; \
      *) echo "unsupported architecture ${TARGETARCH}"; exit 1 ;; \
    esac; \
    curl -fsSLo /usr/local/bin/cloud-hypervisor "https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/${ch}"; \
    echo "${sum}  /usr/local/bin/cloud-hypervisor" | sha256sum -c -; \
    chmod 0755 /usr/local/bin/cloud-hypervisor; \
    if [ "${TARGETARCH}" = amd64 ]; then \
      curl -fsSLo /virtiofsd.zip "${VIRTIOFSD_URL}"; \
      echo "${VIRTIOFSD_SHA256}  /virtiofsd.zip" | sha256sum -c -; \
      unzip -p /virtiofsd.zip target/x86_64-unknown-linux-musl/release/virtiofsd > /usr/local/bin/virtiofsd; \
      rm /virtiofsd.zip; \
      chmod 0755 /usr/local/bin/virtiofsd; \
    else \
      apt-get install -y --no-install-recommends virtiofsd; \
      ln -s /usr/libexec/virtiofsd /usr/local/bin/virtiofsd; \
    fi; \
    apt-get purge -y curl unzip; \
    apt-get autoremove -y; \
    rm -rf /var/lib/apt/lists/*; \
    useradd --uid 1000 --create-home --shell /usr/sbin/nologin sandbox; \
    cloud-hypervisor --version; virtiofsd --version; bwrap --version; mke2fs -V; debugfs -V
ENV SANDBOX_NO_BWRAP=1
USER sandbox
WORKDIR /home/sandbox

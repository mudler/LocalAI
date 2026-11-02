# SPDX-License-Identifier: MIT
# Both stages use the same server ABI and pgvector release.
FROM pgvector/pgvector:0.8.2-pg17-bookworm@sha256:feb68f4f15446397d8cac7f4fe48fe4586de83160d1fc48b46283312d1a33966 AS build

# Match the Debian-packaged server in the pinned base, not another PG major.
RUN test "$(dpkg-query -W -f='${Version}' postgresql-17)" = '17.10-1.pgdg12+1' \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential ca-certificates curl \
        postgresql-server-dev-17=17.10-1.pgdg12+1 \
    && rm -rf /var/lib/apt/lists/*

# pg_textsearch v1.3.1, pinned to its source commit and archive checksum.
RUN curl -fsSL https://codeload.github.com/timescale/pg_textsearch/tar.gz/578ff529894992fb9e67cae4c69424e65c84868e -o /pg_textsearch.tar.gz \
    && echo '8632f91231251dc3e19395ef6a0d4d158d5f5920ba420691471771418e2a7cc7  /pg_textsearch.tar.gz' | sha256sum -c - \
    && mkdir /pg_textsearch \
    && tar -xzf /pg_textsearch.tar.gz -C /pg_textsearch --strip-components=1 \
    && test -f "$(/usr/lib/postgresql/17/bin/pg_config --includedir-server)/postgres.h" \
    && make -C /pg_textsearch -j2 PG_CONFIG=/usr/lib/postgresql/17/bin/pg_config \
    && make -C /pg_textsearch install PG_CONFIG=/usr/lib/postgresql/17/bin/pg_config DESTDIR=/extension

FROM pgvector/pgvector:0.8.2-pg17-bookworm@sha256:feb68f4f15446397d8cac7f4fe48fe4586de83160d1fc48b46283312d1a33966
COPY --from=build /extension/ /
# pg_textsearch must load at server startup, before CREATE EXTENSION.
CMD ["postgres", "-c", "shared_preload_libraries=pg_textsearch"]

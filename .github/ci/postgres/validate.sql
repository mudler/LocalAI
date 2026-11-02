-- SPDX-License-Identifier: MIT
-- Fail before the suites if this fixture loses either memory dependency.
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_textsearch;

SHOW server_version;
SHOW shared_preload_libraries;
SELECT extname, extversion FROM pg_extension ORDER BY extname;

DO $$
BEGIN
    IF current_setting('server_version_num')::integer / 10000 <> 17 THEN
        RAISE EXCEPTION 'the CI fixture requires PostgreSQL 17';
    END IF;
    IF NOT ('pg_textsearch' = ANY(string_to_array(
        replace(current_setting('shared_preload_libraries'), ' ', ''), ','))) THEN
        RAISE EXCEPTION 'pg_textsearch must be preloaded';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_extension WHERE extname = 'vector' AND extversion = '0.8.2') THEN
        RAISE EXCEPTION 'the CI fixture requires vector 0.8.2';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_extension WHERE extname = 'pg_textsearch' AND extversion = '1.3.1') THEN
        RAISE EXCEPTION 'the CI fixture requires pg_textsearch 1.3.1';
    END IF;
END
$$;

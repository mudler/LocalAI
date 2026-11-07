#!/bin/bash
#
# Assemble a backend directory for tests/e2e-backends (BACKEND_BINARY mode)
# that uses the stub engine instead of the real one:
#   strata, run.sh   the real backend binary and entry point
#   strata-engine    launcher for e2e/stub_engine.py (run.sh picks it up)
#   strata-src/      the Strata Python modules (from `make engine-source`)
#   pack/            a tiny synthetic tokenizer pack (e2e/make_pack.py)
# The Python on PATH needs the regex and jinja2 packages.

set -e

CURDIR=$(realpath "$(dirname "$(realpath "$0")")/..")
OUT="$CURDIR/package-stub"

for f in "$CURDIR/strata" "$CURDIR/strata-src/tools/strata_tokenizer.py"; do
	if [ ! -e "$f" ]; then
		echo "ERROR: $f is missing, run 'make strata engine-source' first" >&2
		exit 1
	fi
done

rm -rf "$OUT"
mkdir -p "$OUT"
cp "$CURDIR/strata" "$CURDIR/run.sh" "$OUT/"
cp -r "$CURDIR/strata-src" "$OUT/strata-src"
cp "$CURDIR/e2e/stub_engine.py" "$OUT/stub_engine.py"
printf '#!/bin/sh\nexec python3 "$(dirname "$(realpath "$0")")/stub_engine.py" "$@"\n' >"$OUT/strata-engine"
chmod +x "$OUT/run.sh" "$OUT/strata" "$OUT/strata-engine"
python3 "$CURDIR/e2e/make_pack.py" "$OUT/pack"
echo "Stub package ready: $OUT/run.sh (model dir: $OUT/pack)"

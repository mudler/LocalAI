#!/bin/bash
set -e
python3 -m unittest test_unit.py

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT
mkdir -p "$tmp_dir/bin"
cat >"$tmp_dir/bin/curl" <<'EOF'
#!/bin/bash
printf '%s\n' "${@: -1}" >"$PORTABLE_PY_URL_CAPTURE"
exit 22
EOF
chmod +x "$tmp_dir/bin/curl"

PORTABLE_PY_URL_CAPTURE="$tmp_dir/url" \
    ENV_DIR="$tmp_dir/backend" \
    PORTABLE_PYTHON=true \
    PATH="$tmp_dir/bin:$PATH" \
    bash install.sh >/dev/null 2>&1 || true

expected_url="https://github.com/astral-sh/python-build-standalone/releases/download/20250818/cpython-3.11.13+20250818-x86_64-unknown-linux-gnu-install_only.tar.gz"
actual_url=$(cat "$tmp_dir/url")
if [ "$actual_url" != "$expected_url" ]; then
    echo "unexpected portable Python URL: $actual_url" >&2
    exit 1
fi

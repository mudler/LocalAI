#!/bin/sh
# Frees disk space on a GitHub-hosted ubuntu runner before a job installs its
# own toolchain. It removes preinstalled toolchains that the add-on jobs never
# use. It keeps Go, Node.js, Python 3 from the OS and everything under
# /usr/local/bin, because setup steps and the job rely on them. The deletes run
# in parallel; the script prints df before and after.
set -u
echo "== disk before"
df -h / /mnt 2>/dev/null || df -h /
paths="
/usr/share/dotnet
/usr/local/lib/android
/opt/ghc
/usr/local/.ghcup
/usr/local/share/powershell
/usr/local/share/chromium
/opt/microsoft
/opt/az
/usr/share/swift
/usr/local/julia*
/opt/hostedtoolcache/CodeQL
/opt/hostedtoolcache/Python
/opt/hostedtoolcache/PyPy
/opt/hostedtoolcache/Ruby
/opt/hostedtoolcache/Java_*
"
for p in $paths; do
    # Unquoted on purpose: some entries are globs.
    for match in $p; do
        [ -e "$match" ] || continue
        sudo rm -rf "$match" &
    done
done
sudo docker image prune --all --force >/dev/null 2>&1 &
wait
echo "== disk after"
df -h / /mnt 2>/dev/null || df -h /

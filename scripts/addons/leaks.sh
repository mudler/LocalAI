# SPDX-License-Identifier: MIT
# Sourced by the foundation scripts. leaked_pids DIR prints the id of every
# live process, except this shell and its ancestors, whose executable, working
# directory or command line names DIR. It reads /proc, so it finds a process
# that was orphaned (parent 1) as well as a child.
leaked_pids() {
    dir=$1
    # paths-only: a process is a leak only when its executable or working
    # directory is under DIR. The command line is not read, so an unrelated
    # shell that merely names the shared scratch directory (a log tail or a
    # wait loop of another session) is never killed.
    mode=${2:-}
    skip=" $$ "
    p=$$
    while [ "$p" -gt 1 ] 2>/dev/null; do
        p=$(awk '{ s = $0; sub(/^.*\) /, "", s); split(s, f, " "); print f[2] }' "/proc/$p/stat" 2>/dev/null) || break
        [ -n "$p" ] || break
        skip="$skip$p "
    done
    for d in /proc/[0-9]*; do
        pid=${d#/proc/}
        case "$skip" in *" $pid "*) continue ;; esac
        [ -r "$d/cmdline" ] || continue
        state=$(awk '{ s = $0; sub(/^.*\) /, "", s); print substr(s, 1, 1) }' "$d/stat" 2>/dev/null) || continue
        [ "$state" != Z ] || continue
        hit=
        case "$(readlink "$d/exe" 2>/dev/null)" in "$dir"*) hit=1 ;; esac
        case "$(readlink "$d/cwd" 2>/dev/null)" in "$dir"*) hit=1 ;; esac
        [ -n "$hit" ] || [ "$mode" = paths-only ] || case "$(tr '\0' ' ' <"$d/cmdline" 2>/dev/null)" in *"$dir"*) hit=1 ;; esac
        [ -z "$hit" ] || echo "$pid"
    done
}

# reap_leaks DIR LABEL kills what leaked_pids finds, reports it on stderr and
# returns 1 when anything was found, so a leak is never silent.
reap_leaks() {
    found=$(leaked_pids "$1" "${3:-}")
    [ -n "$found" ] || return 0
    for pid in $found; do
        echo "LEAK ($2): process $pid survived: $(tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null | cut -c1-200)" >&2
    done
    # shellcheck disable=SC2086
    kill -TERM $found 2>/dev/null || true
    sleep 1
    # shellcheck disable=SC2086
    kill -KILL $found 2>/dev/null || true
    return 1
}

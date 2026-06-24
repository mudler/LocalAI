"""Parent-death watcher (best-effort backstop) for LocalAI Python backends.

LocalAI spawns each backend as a child process and, on a clean shutdown, tears
it down itself (SIGTERM -> grace -> SIGKILL). That graceful path only runs when
LocalAI receives a catchable signal and lives long enough to run its handlers.
If LocalAI is SIGKILLed (e.g. a supervising process's grace period elapses
first), that teardown never runs and this backend would be reparented to init
and linger, holding GPU/VRAM and its listen port.

The watcher here is a best-effort backstop for exactly that case: it does NOT
replace the graceful teardown, it only covers the "parent vanished without
cleaning up" path. It detects reparenting: when the process that spawned this
backend dies, the kernel reparents us to the nearest sub-reaper or to init
(PID 1), so os.getppid() stops matching the value captured at startup. This
getppid() approach is portable across Linux/macOS (unlike the Linux-only
PR_SET_PDEATHSIG), which is why it is used here, mirroring the Go backends'
pkg/grpc/parentwatch.go and the C++ backends' parent_watch.h. It is disabled on
Windows, which has no equivalent orphan-reparenting semantics.

Env vars (shared verbatim across the Go, C++ and Python backends):
  LOCALAI_BACKEND_PARENT_WATCH           enabled by default; a falsey value
                                         ("false"/"0"/"no"/"off", case-insensitive)
                                         disables it.
  LOCALAI_BACKEND_PARENT_WATCH_INTERVAL  poll interval as a Go-style duration
                                         string ("500ms", "2s", "1m") or a bare
                                         number of seconds. Defaults to 2s.
"""

import os
import sys
import threading

ENV_PARENT_WATCH = "LOCALAI_BACKEND_PARENT_WATCH"
ENV_PARENT_WATCH_INTERVAL = "LOCALAI_BACKEND_PARENT_WATCH_INTERVAL"

_DEFAULT_INTERVAL_SECONDS = 2.0

# Guard so repeated calls (e.g. get_auth_interceptors invoked more than once)
# only ever arm a single watcher thread per process.
_started = False
_started_lock = threading.Lock()


def _enabled():
    """Report whether the watcher should run in this process."""
    # Windows does not reparent orphans to a well-known init PID, so the
    # getppid() heuristic used here doesn't apply there.
    if os.name == "nt" or sys.platform.startswith("win"):
        return False
    val = os.environ.get(ENV_PARENT_WATCH, "").strip().lower()
    if val in ("false", "0", "no", "off"):
        return False
    return True


def _interval_seconds():
    """Return the configured poll interval in seconds, or the default.

    Accepts Go-style duration strings ("500ms", "2s", "1m") for cross-language
    parity, or a bare number interpreted as seconds.
    """
    raw = os.environ.get(ENV_PARENT_WATCH_INTERVAL, "").strip()
    if not raw:
        return _DEFAULT_INTERVAL_SECONDS
    # Split numeric prefix from unit suffix.
    i = 0
    while i < len(raw) and (raw[i].isdigit() or raw[i] == "." or (i == 0 and raw[i] in "+-")):
        i += 1
    if i == 0:
        return _DEFAULT_INTERVAL_SECONDS
    try:
        num = float(raw[:i])
    except ValueError:
        return _DEFAULT_INTERVAL_SECONDS
    unit = raw[i:].lower()
    if unit == "ms":
        seconds = num / 1000.0
    elif unit in ("s", ""):
        seconds = num
    elif unit == "m":
        seconds = num * 60.0
    else:
        return _DEFAULT_INTERVAL_SECONDS
    return seconds if seconds > 0 else _DEFAULT_INTERVAL_SECONDS


def _parent_died(orig_ppid):
    """Report whether this process has been reparented away from orig_ppid.

    Reparenting is the standard POSIX signal that the original parent (here, the
    LocalAI process that spawned this backend) has exited: the orphan is handed
    to the nearest sub-reaper or to init (PID 1), so os.getppid() no longer
    matches the value captured at startup.
    """
    ppid = os.getppid()
    return ppid != orig_ppid or ppid == 1


def _watch(orig_ppid, interval, on_death):
    """Poll until _parent_died reports the original parent is gone, then call
    on_death. Blocks, so run it on its own (daemon) thread."""
    import time

    while True:
        time.sleep(interval)
        if _parent_died(orig_ppid):
            on_death()
            return


def start_parent_death_watcher():
    """Install the best-effort safety net described in this module's docstring.

    No-op when disabled, on Windows, when already orphaned at startup
    (os.getppid() <= 1), or if already started. This is a backstop alongside —
    never a replacement for — LocalAI's graceful teardown.
    """
    global _started
    if not _enabled():
        return
    with _started_lock:
        if _started:
            return
        orig_ppid = os.getppid()
        # A parent of 1 (or less) at startup means we were already orphaned (or
        # launched directly under init) — there is no original parent to watch.
        if orig_ppid <= 1:
            return
        interval = _interval_seconds()

        def on_death():
            print(
                "backend parent process (pid {}) exited without stopping this "
                "backend; self-terminating to avoid orphaning".format(orig_ppid),
                file=sys.stderr,
                flush=True,
            )
            # Immediate, non-cleanup exit: this is a shutdown safety net and the
            # normal graceful path is already gone.
            os._exit(1)

        thread = threading.Thread(
            target=_watch,
            args=(orig_ppid, interval, on_death),
            name="parent-death-watcher",
            daemon=True,
        )
        thread.start()
        _started = True

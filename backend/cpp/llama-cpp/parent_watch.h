// Parent-death watcher (best-effort backstop) for the llama.cpp gRPC backend.
//
// LocalAI spawns this backend as a child process and, on a clean shutdown,
// tears it down itself (SIGTERM -> grace -> SIGKILL). That graceful path only
// runs when LocalAI receives a catchable signal and lives long enough to run
// its handlers. If LocalAI is SIGKILLed (e.g. a supervising process's grace
// period elapses first), that teardown never runs and this backend would be
// reparented to init and linger, holding VRAM and its listen port.
//
// The watcher here is a best-effort backstop for exactly that case: it does
// NOT replace the graceful teardown, it only covers the "parent vanished
// without cleaning up" path. It detects reparenting: when the process that
// spawned this backend dies, the kernel reparents us to the nearest sub-reaper
// or to init (PID 1), so getppid() stops matching the value captured at
// startup. This getppid() approach is portable across Linux/macOS (unlike the
// Linux-only PR_SET_PDEATHSIG), which is why it is used here, mirroring the Go
// backends' pkg/grpc/parentwatch.go. It is disabled on Windows, which has no
// equivalent orphan-reparenting semantics.
//
// This header is intentionally dependency-free (C++ standard library only) so
// it can be exercised by a standalone unit test (parent_watch_test.cpp) without
// building the full llama.cpp + gRPC backend.
#ifndef LLAMA_GRPC_PARENT_WATCH_H
#define LLAMA_GRPC_PARENT_WATCH_H

#include <algorithm>
#include <cctype>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <functional>
#include <string>
#include <thread>

#if !defined(_WIN32)
#include <unistd.h> // getppid(2), _exit(2)
#endif

namespace llama_grpc {

// Env var names are shared verbatim with the Go and Python backends for
// consistency across languages.
inline const char *kEnvParentWatch()         { return "LOCALAI_BACKEND_PARENT_WATCH"; }
inline const char *kEnvParentWatchInterval() { return "LOCALAI_BACKEND_PARENT_WATCH_INTERVAL"; }

// Default poll interval in milliseconds. Matches the Go side's 2 * time.Second.
inline long parent_watch_default_interval_ms() { return 2000; }

namespace detail {
inline std::string trim_lower(const std::string &in, bool lower) {
    size_t a = in.find_first_not_of(" \t\r\n");
    size_t b = in.find_last_not_of(" \t\r\n");
    if (a == std::string::npos) {
        return "";
    }
    std::string s = in.substr(a, b - a + 1);
    if (lower) {
        std::transform(s.begin(), s.end(), s.begin(),
                       [](unsigned char c) { return std::tolower(c); });
    }
    return s;
}
} // namespace detail

// parent_watch_enabled reports whether the watcher should run. Enabled by
// default; a falsey value ("false"/"0"/"no"/"off", case-insensitive) disables
// it, matching the Go implementation's exact semantics.
inline bool parent_watch_enabled() {
#if defined(_WIN32)
    return false;
#else
    const char *v = std::getenv(kEnvParentWatch());
    if (v == nullptr || v[0] == '\0') {
        return true;
    }
    const std::string s = detail::trim_lower(v, true);
    return !(s == "false" || s == "0" || s == "no" || s == "off");
#endif
}

// parent_watch_interval_ms returns the poll interval in milliseconds. Accepts
// Go-style duration strings ("500ms", "2s", "1m") for cross-language parity, or
// a bare number interpreted as seconds. Defaults to
// parent_watch_default_interval_ms().
inline long parent_watch_interval_ms() {
    const long def = parent_watch_default_interval_ms();
    const char *v = std::getenv(kEnvParentWatchInterval());
    if (v == nullptr || v[0] == '\0') {
        return def;
    }
    const std::string s = detail::trim_lower(v, false);
    if (s.empty()) {
        return def;
    }
    size_t i = 0;
    while (i < s.size() && (std::isdigit((unsigned char)s[i]) || s[i] == '.')) {
        i++;
    }
    if (i == 0) {
        return def;
    }
    double num = 0.0;
    try {
        num = std::stod(s.substr(0, i));
    } catch (...) {
        return def;
    }
    const std::string unit = s.substr(i);
    long ms;
    if (unit == "ms") {
        ms = (long)num;
    } else if (unit == "s" || unit.empty()) {
        ms = (long)(num * 1000.0);
    } else if (unit == "m") {
        ms = (long)(num * 60000.0);
    } else {
        return def; // unrecognized unit
    }
    return ms > 0 ? ms : def;
}

#if !defined(_WIN32)
// parent_died reports whether this process has been reparented away from the
// parent it had when the watcher started. Reparenting is the standard POSIX
// signal that the original parent (here, the LocalAI process that spawned this
// backend) has exited: the orphan is handed to the nearest sub-reaper or to
// init (PID 1), so getppid() no longer matches the value captured at startup.
inline bool parent_died(pid_t orig_ppid) {
    const pid_t ppid = getppid();
    return ppid != orig_ppid || ppid == 1;
}

// watch_parent_death polls until parent_died reports the original parent is
// gone, then invokes on_death. It blocks, so run it on its own thread.
inline void watch_parent_death(pid_t orig_ppid, long interval_ms,
                               const std::function<void()> &on_death) {
    for (;;) {
        std::this_thread::sleep_for(std::chrono::milliseconds(interval_ms));
        if (parent_died(orig_ppid)) {
            on_death();
            return;
        }
    }
}
#endif

// start_parent_death_watcher installs the best-effort safety net described in
// the file header on the calling backend process. It is a no-op when disabled,
// on Windows, or when the process is already orphaned at startup
// (getppid() <= 1). This is a backstop alongside — never a replacement for —
// LocalAI's graceful teardown.
inline void start_parent_death_watcher() {
#if !defined(_WIN32)
    if (!parent_watch_enabled()) {
        return;
    }
    const pid_t orig_ppid = getppid();
    // A parent of 1 (or less) at startup means we were already orphaned (or
    // launched directly under init) — there is no original parent to watch for.
    if (orig_ppid <= 1) {
        return;
    }
    const long interval_ms = parent_watch_interval_ms();
    std::thread([orig_ppid, interval_ms]() {
        watch_parent_death(orig_ppid, interval_ms, [orig_ppid]() {
            fprintf(stderr,
                    "backend parent process (pid %d) exited without stopping "
                    "this backend; self-terminating to avoid orphaning\n",
                    (int)orig_ppid);
            fflush(stderr);
            _exit(1);
        });
    }).detach();
#endif
}

} // namespace llama_grpc

#endif // LLAMA_GRPC_PARENT_WATCH_H

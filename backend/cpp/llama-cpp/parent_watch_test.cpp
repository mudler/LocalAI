// Unit tests for the parent-death watcher (parent_watch.h).
//
// Build & run standalone (C++ standard library only, no nlohmann/json needed):
//   g++ -std=c++17 -pthread parent_watch_test.cpp -o t && ./t
//
// The core test (TestDetectsReparent) builds a genuine two-level process tree
// (test -> middle -> grandchild), lets the middle process die, and asserts the
// grandchild's watch_parent_death detects the reparenting and self-terminates —
// mirroring the Go test in pkg/grpc/parentwatch_test.go, but with fork(2).
//
// On Windows this file compiles to a no-op success (the watcher is unsupported
// there), matching parent_watch.h's platform gating.

#include <cstdio>
#include <cstdlib>
#include <string>

#include "parent_watch.h"

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

// Env-parsing tests are platform-independent and always run.
static void test_env_parsing() {
    using namespace llama_grpc;

    // Interval: default when unset.
    unsetenv("LOCALAI_BACKEND_PARENT_WATCH_INTERVAL");
    check(parent_watch_interval_ms() == 2000, "interval default 2000ms");

    setenv("LOCALAI_BACKEND_PARENT_WATCH_INTERVAL", "500ms", 1);
    check(parent_watch_interval_ms() == 500, "interval 500ms");

    setenv("LOCALAI_BACKEND_PARENT_WATCH_INTERVAL", "2s", 1);
    check(parent_watch_interval_ms() == 2000, "interval 2s");

    setenv("LOCALAI_BACKEND_PARENT_WATCH_INTERVAL", "1m", 1);
    check(parent_watch_interval_ms() == 60000, "interval 1m");

    setenv("LOCALAI_BACKEND_PARENT_WATCH_INTERVAL", "3", 1); // bare number -> seconds
    check(parent_watch_interval_ms() == 3000, "interval bare 3 -> 3000ms");

    setenv("LOCALAI_BACKEND_PARENT_WATCH_INTERVAL", "garbage", 1);
    check(parent_watch_interval_ms() == 2000, "interval garbage -> default");
    unsetenv("LOCALAI_BACKEND_PARENT_WATCH_INTERVAL");

#if !defined(_WIN32)
    // Enabled semantics (POSIX only; always false on Windows).
    unsetenv("LOCALAI_BACKEND_PARENT_WATCH");
    check(parent_watch_enabled(), "enabled by default");

    for (const char *falsey : {"false", "0", "no", "off", "OFF", " False "}) {
        setenv("LOCALAI_BACKEND_PARENT_WATCH", falsey, 1);
        check(!parent_watch_enabled(), std::string("disabled by '") + falsey + "'");
    }
    setenv("LOCALAI_BACKEND_PARENT_WATCH", "true", 1);
    check(parent_watch_enabled(), "enabled by 'true'");
    setenv("LOCALAI_BACKEND_PARENT_WATCH", "1", 1);
    check(parent_watch_enabled(), "enabled by '1'");
    unsetenv("LOCALAI_BACKEND_PARENT_WATCH");
#endif
}

#if !defined(_WIN32)

#include <atomic>
#include <ctime>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

static bool file_exists(const std::string &p) {
    struct stat st;
    return ::stat(p.c_str(), &st) == 0;
}

static bool wait_for_file(const std::string &p, int timeout_ms) {
    int waited = 0;
    while (waited < timeout_ms) {
        if (file_exists(p)) {
            return true;
        }
        usleep(20 * 1000);
        waited += 20;
    }
    return false;
}

static void write_file(const std::string &p, const std::string &content) {
    FILE *f = fopen(p.c_str(), "w");
    if (f) {
        fwrite(content.data(), 1, content.size(), f);
        fclose(f);
    }
}

// Builds test -> middle -> grandchild via fork(2). The grandchild arms the REAL
// watch_parent_death against middle; middle exits, orphaning the grandchild;
// the watcher must detect the reparenting and self-terminate.
static void test_detects_reparent() {
    char tmpl[] = "/tmp/parentwatch_test_XXXXXX";
    char *dir = mkdtemp(tmpl);
    if (dir == nullptr) {
        check(false, "mkdtemp");
        return;
    }
    const std::string ready_file = std::string(dir) + "/ready";
    const std::string exited_file = std::string(dir) + "/exited";

    pid_t middle = fork();
    if (middle < 0) {
        check(false, "fork middle");
        return;
    }

    if (middle == 0) {
        // ---- middle process ----
        pid_t grandchild = fork();
        if (grandchild < 0) {
            _exit(4);
        }
        if (grandchild == 0) {
            // ---- grandchild process ----
            pid_t orig_ppid = getppid(); // == middle
            std::thread([&]() {
                llama_grpc::watch_parent_death(orig_ppid, 50 /*ms*/, [&]() {
                    write_file(exited_file, "1");
                    _exit(7);
                });
            }).detach();

            // Safety valve: never linger if something goes wrong.
            std::thread([]() {
                usleep(30 * 1000 * 1000);
                _exit(2);
            }).detach();

            // Signal readiness only after the watcher captured orig_ppid.
            write_file(ready_file, std::to_string(getpid()));
            for (;;) {
                pause();
            }
        }
        // middle: wait until grandchild is ready, then exit to orphan it.
        if (!wait_for_file(ready_file, 10000)) {
            _exit(5);
        }
        _exit(0);
    }

    // ---- test (top) process ----
    int status = 0;
    waitpid(middle, &status, 0); // reap middle only; grandchild is orphaned

    check(file_exists(ready_file), "grandchild signaled readiness");

    bool detected = wait_for_file(exited_file, 10000);
    check(detected, "watcher detected parent death and self-terminated");

    // Best-effort cleanup: kill the grandchild if it somehow survived.
    if (file_exists(ready_file)) {
        FILE *f = fopen(ready_file.c_str(), "r");
        if (f) {
            int pid = 0;
            if (fscanf(f, "%d", &pid) == 1 && pid > 1) {
                kill(pid, SIGKILL);
            }
            fclose(f);
        }
    }
    unlink(ready_file.c_str());
    unlink(exited_file.c_str());
    rmdir(dir);
}

#endif // !_WIN32

int main() {
    test_env_parsing();
#if !defined(_WIN32)
    test_detects_reparent();
#endif
    if (failures == 0) {
        fprintf(stderr, "\nAll parent_watch tests passed.\n");
        return 0;
    }
    fprintf(stderr, "\n%d parent_watch test(s) failed.\n", failures);
    return 1;
}

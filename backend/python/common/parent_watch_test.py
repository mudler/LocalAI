"""Unit tests for the parent-death watcher (parent_watch.py).

Run standalone (Python standard library only, no backend venv needed):
    python3 -m unittest parent_watch_test

The core test (test_detects_reparent) builds a genuine two-level process tree
(test -> middle -> grandchild) with os.fork, lets the middle process die, and
asserts the grandchild's parent_watch._watch detects the reparenting and
self-terminates — mirroring the Go test in pkg/grpc/parentwatch_test.go and the
C++ test in backend/cpp/llama-cpp/parent_watch_test.cpp.
"""

import os
import sys
import tempfile
import threading
import time
import unittest

import parent_watch


class TestParentWatchEnvParsing(unittest.TestCase):
    def setUp(self):
        self._saved = {
            k: os.environ.get(k)
            for k in (parent_watch.ENV_PARENT_WATCH, parent_watch.ENV_PARENT_WATCH_INTERVAL)
        }
        for k in self._saved:
            os.environ.pop(k, None)

    def tearDown(self):
        for k, v in self._saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v

    def test_interval_default(self):
        self.assertEqual(parent_watch._interval_seconds(), 2.0)

    def test_interval_units(self):
        cases = {"500ms": 0.5, "2s": 2.0, "1m": 60.0, "3": 3.0, "0.5s": 0.5}
        for raw, expected in cases.items():
            os.environ[parent_watch.ENV_PARENT_WATCH_INTERVAL] = raw
            self.assertAlmostEqual(parent_watch._interval_seconds(), expected, msg=raw)

    def test_interval_garbage_falls_back(self):
        os.environ[parent_watch.ENV_PARENT_WATCH_INTERVAL] = "garbage"
        self.assertEqual(parent_watch._interval_seconds(), 2.0)

    @unittest.skipIf(os.name == "nt" or sys.platform.startswith("win"), "POSIX only")
    def test_enabled_default(self):
        self.assertTrue(parent_watch._enabled())

    @unittest.skipIf(os.name == "nt" or sys.platform.startswith("win"), "POSIX only")
    def test_disabled_by_falsey(self):
        for val in ("false", "0", "no", "off", "OFF", " False "):
            os.environ[parent_watch.ENV_PARENT_WATCH] = val
            self.assertFalse(parent_watch._enabled(), msg=val)

    @unittest.skipIf(os.name == "nt" or sys.platform.startswith("win"), "POSIX only")
    def test_enabled_by_truthy(self):
        for val in ("true", "1", "yes", "on"):
            os.environ[parent_watch.ENV_PARENT_WATCH] = val
            self.assertTrue(parent_watch._enabled(), msg=val)


@unittest.skipIf(os.name == "nt" or sys.platform.startswith("win"), "fork/reparent is POSIX only")
class TestParentWatchReparent(unittest.TestCase):
    def _wait_for_file(self, path, timeout=10.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if os.path.exists(path):
                return True
            time.sleep(0.02)
        return False

    def test_detects_reparent(self):
        tmpdir = tempfile.mkdtemp(prefix="parentwatch_test_")
        ready_file = os.path.join(tmpdir, "ready")
        exited_file = os.path.join(tmpdir, "exited")

        middle = os.fork()
        if middle == 0:
            # ---- middle process ----
            grandchild = os.fork()
            if grandchild == 0:
                # ---- grandchild process: arm the REAL watcher against middle ----
                orig_ppid = os.getppid()

                def on_death():
                    with open(exited_file, "w") as f:
                        f.write("1")
                    os._exit(7)

                threading.Thread(
                    target=parent_watch._watch,
                    args=(orig_ppid, 0.05, on_death),
                    daemon=True,
                ).start()

                # Safety valve: never linger if something goes wrong.
                def bail():
                    time.sleep(30)
                    os._exit(2)

                threading.Thread(target=bail, daemon=True).start()

                # Signal readiness only after the watcher captured orig_ppid.
                with open(ready_file, "w") as f:
                    f.write(str(os.getpid()))
                while True:
                    time.sleep(1)
            else:
                # middle: wait until grandchild is ready, then exit to orphan it.
                if not self._wait_for_file(ready_file):
                    os._exit(5)
                os._exit(0)

        # ---- test (top) process ----
        os.waitpid(middle, 0)  # reap middle only; grandchild is orphaned

        self.assertTrue(os.path.exists(ready_file), "grandchild never signaled readiness")
        self.assertTrue(
            self._wait_for_file(exited_file),
            "watcher did not detect parent death within timeout",
        )

        # Best-effort cleanup: kill the grandchild if it somehow survived.
        try:
            with open(ready_file) as f:
                pid = int(f.read().strip())
            if pid > 1:
                os.kill(pid, 9)
        except (OSError, ValueError):
            pass
        for p in (ready_file, exited_file):
            try:
                os.remove(p)
            except OSError:
                pass
        try:
            os.rmdir(tmpdir)
        except OSError:
            pass


if __name__ == "__main__":
    unittest.main()

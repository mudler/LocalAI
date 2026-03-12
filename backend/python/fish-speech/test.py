"""
A test script to test the gRPC service
"""
import signal
import threading
import unittest
import subprocess
import time
import os
import sys
import tempfile
import backend_pb2
import backend_pb2_grpc

import grpc

BACKEND_LOG = "/tmp/fish-speech-backend.log"


def _dump_backend_log():
    """Print backend log — call before exiting so CI always shows it."""
    if os.path.exists(BACKEND_LOG):
        with open(BACKEND_LOG, "r") as f:
            contents = f.read()
        if contents:
            print("=== Backend Log ===", file=sys.stderr, flush=True)
            print(contents, file=sys.stderr, flush=True)


def _sigterm_handler(signum, frame):
    """Handle SIGTERM so the backend log is printed before exit."""
    print(f"\nReceived signal {signum}, dumping backend log before exit...",
          file=sys.stderr, flush=True)
    _dump_backend_log()
    sys.exit(143)


signal.signal(signal.SIGTERM, _sigterm_handler)


def _tail_log(path, stop_event, interval=10):
    """Background thread that periodically prints new lines from the backend log."""
    pos = 0
    while not stop_event.is_set():
        stop_event.wait(interval)
        try:
            with open(path, "r") as f:
                f.seek(pos)
                new = f.read()
                if new:
                    print(f"[backend log] {new}", file=sys.stderr, end="", flush=True)
                pos = f.tell()
        except FileNotFoundError:
            pass


class TestBackendServicer(unittest.TestCase):
    """
    TestBackendServicer is the class that tests the gRPC service
    """
    def setUp(self):
        """
        This method sets up the gRPC service by starting the server
        """
        print("Starting backend server...", file=sys.stderr, flush=True)
        self.backend_log = open(BACKEND_LOG, "w")
        self.service = subprocess.Popen(
            ["python3", "backend.py", "--addr", "localhost:50051"],
            stdout=self.backend_log,
            stderr=self.backend_log,
        )

        # Start tailing backend log so CI sees progress in real time
        self._log_stop = threading.Event()
        self._log_thread = threading.Thread(
            target=_tail_log, args=(BACKEND_LOG, self._log_stop), daemon=True
        )
        self._log_thread.start()

        # Poll for readiness instead of a fixed sleep
        print("Waiting for backend to be ready...", file=sys.stderr, flush=True)
        max_wait = 60
        start = time.time()
        ready = False
        while time.time() - start < max_wait:
            try:
                with grpc.insecure_channel("localhost:50051") as channel:
                    stub = backend_pb2_grpc.BackendStub(channel)
                    resp = stub.Health(backend_pb2.HealthMessage(), timeout=2.0)
                    if resp.message:
                        ready = True
                        break
            except Exception:
                pass
            # Check if process died
            if self.service.poll() is not None:
                self.fail(f"Backend process exited early with code {self.service.returncode}")
            time.sleep(2)

        elapsed = time.time() - start
        if not ready:
            self.fail(f"Backend not ready after {max_wait}s")
        print(f"Backend ready after {elapsed:.1f}s", file=sys.stderr, flush=True)

    def tearDown(self) -> None:
        """
        This method tears down the gRPC service by terminating the server
        """
        self._log_stop.set()
        self._log_thread.join(timeout=2)
        self.service.terminate()
        try:
            self.service.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.service.kill()
            self.service.wait()
        self.backend_log.close()
        _dump_backend_log()

    def test_tts(self):
        """
        This method tests if the TTS generation works successfully
        """
        with grpc.insecure_channel("localhost:50051") as channel:
            stub = backend_pb2_grpc.BackendStub(channel)
            # Limit max_new_tokens for CPU testing (generation is very slow on CPU)
            print("Loading model fishaudio/s2-pro...", file=sys.stderr, flush=True)
            load_start = time.time()
            response = stub.LoadModel(
                backend_pb2.ModelOptions(
                    Model="fishaudio/s2-pro",
                    Options=["max_new_tokens:50"],
                ),
                timeout=1800.0
            )
            print(
                f"LoadModel response: success={response.success}, "
                f"message={response.message}, "
                f"took {time.time() - load_start:.1f}s",
                file=sys.stderr, flush=True
            )
            self.assertTrue(response.success, f"LoadModel failed: {response.message}")

            # Create temporary output file
            with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as tmp_file:
                output_path = tmp_file.name

            tts_request = backend_pb2.TTSRequest(
                text="Hi.",
                dst=output_path
            )
            # Allow up to 10 minutes for TTS generation on CPU
            print("Starting TTS generation...", file=sys.stderr, flush=True)
            tts_start = time.time()
            tts_response = stub.TTS(tts_request, timeout=600.0)
            print(
                f"TTS response: success={tts_response.success}, "
                f"took {time.time() - tts_start:.1f}s",
                file=sys.stderr, flush=True
            )
            self.assertIsNotNone(tts_response)
            self.assertTrue(tts_response.success)

            # Verify output file exists and is not empty
            self.assertTrue(os.path.exists(output_path))
            file_size = os.path.getsize(output_path)
            print(f"Output file size: {file_size} bytes", file=sys.stderr, flush=True)
            self.assertGreater(file_size, 0)

            # Cleanup
            os.unlink(output_path)


if __name__ == "__main__":
    unittest.main()

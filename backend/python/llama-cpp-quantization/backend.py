#!/usr/bin/env python3
"""
llama.cpp quantization backend for LocalAI.

Downloads HuggingFace models, converts them to GGUF format using
convert_hf_to_gguf.py, and quantizes using llama-quantize.
"""
import argparse
import os
import queue
import re
import signal
import subprocess
import sys
import threading
import time
from concurrent import futures

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors

import backend_pb2
import backend_pb2_grpc

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '4'))


class ActiveJob:
    """Tracks a running quantization job."""
    def __init__(self, job_id):
        self.job_id = job_id
        self.progress_queue = queue.Queue()
        self.stop_event = threading.Event()
        self.thread = None
        self.process = None  # subprocess handle for killing


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self):
        self.jobs = {}  # job_id -> ActiveJob

    def Health(self, request, context):
        return backend_pb2.Reply(message=b"OK")

    def LoadModel(self, request, context):
        """Accept LoadModel — actual work happens in StartQuantization."""
        return backend_pb2.Result(success=True, message="OK")

    def StartQuantization(self, request, context):
        job_id = request.job_id
        if job_id in self.jobs:
            return backend_pb2.QuantizationJobResult(
                job_id=job_id,
                success=False,
                message=f"Job {job_id} already exists",
            )

        job = ActiveJob(job_id)
        self.jobs[job_id] = job

        job.thread = threading.Thread(
            target=self._do_quantization,
            args=(job, request),
            daemon=True,
        )
        job.thread.start()

        return backend_pb2.QuantizationJobResult(
            job_id=job_id,
            success=True,
            message="Quantization job started",
        )

    def _send_progress(self, job, status, message, progress_percent=0.0, output_file="", extra_metrics=None):
        update = backend_pb2.QuantizationProgressUpdate(
            job_id=job.job_id,
            progress_percent=progress_percent,
            status=status,
            message=message,
            output_file=output_file,
            extra_metrics=extra_metrics or {},
        )
        job.progress_queue.put(update)

    def _do_quantization(self, job, request):
        try:
            model = request.model
            quant_type = request.quantization_type or "q4_k_m"
            output_dir = request.output_dir
            extra_options = dict(request.extra_options) if request.extra_options else {}

            os.makedirs(output_dir, exist_ok=True)

            if job.stop_event.is_set():
                self._send_progress(job, "stopped", "Job stopped before starting")
                return

            # Step 1: Download / resolve model
            self._send_progress(job, "downloading", f"Resolving model: {model}", progress_percent=0.0)

            model_path = self._resolve_model(job, model, output_dir, extra_options)
            if model_path is None:
                return  # error already sent

            if job.stop_event.is_set():
                self._send_progress(job, "stopped", "Job stopped during download")
                return

            # Step 2: Convert to f16 GGUF
            self._send_progress(job, "converting", "Converting model to GGUF (f16)...", progress_percent=30.0)

            f16_gguf_path = os.path.join(output_dir, "model-f16.gguf")
            if not self._convert_to_gguf(job, model_path, f16_gguf_path, extra_options):
                return  # error already sent

            if job.stop_event.is_set():
                self._send_progress(job, "stopped", "Job stopped during conversion")
                return

            # Step 3: Quantize
            # If the user requested f16, skip quantization — the f16 GGUF is the final output
            if quant_type.lower() in ("f16", "fp16"):
                output_file = f16_gguf_path
                self._send_progress(
                    job, "completed",
                    f"Model converted to f16 GGUF: {output_file}",
                    progress_percent=100.0,
                    output_file=output_file,
                    extra_metrics=self._file_metrics(output_file),
                )
                return

            output_file = os.path.join(output_dir, f"model-{quant_type}.gguf")
            self._send_progress(job, "quantizing", f"Quantizing to {quant_type}...", progress_percent=50.0)

            if not self._quantize(job, f16_gguf_path, output_file, quant_type):
                return  # error already sent

            # Clean up f16 intermediate file to save disk space
            try:
                os.remove(f16_gguf_path)
            except OSError:
                pass

            self._send_progress(
                job, "completed",
                f"Quantization complete: {quant_type}",
                progress_percent=100.0,
                output_file=output_file,
                extra_metrics=self._file_metrics(output_file),
            )

        except Exception as e:
            self._send_progress(job, "failed", f"Quantization failed: {str(e)}")

    def _resolve_model(self, job, model, output_dir, extra_options):
        """Download model from HuggingFace or return local path."""
        # If it's a local path that exists, use it directly
        if os.path.isdir(model):
            return model

        # If it looks like a GGUF file path, use it directly
        if os.path.isfile(model) and model.endswith(".gguf"):
            return model

        # Download from HuggingFace
        try:
            from huggingface_hub import snapshot_download

            hf_token = extra_options.get("hf_token") or os.environ.get("HF_TOKEN")
            cache_dir = os.path.join(output_dir, "hf_cache")

            self._send_progress(job, "downloading", f"Downloading {model} from HuggingFace...", progress_percent=5.0)

            local_path = snapshot_download(
                repo_id=model,
                cache_dir=cache_dir,
                token=hf_token,
                ignore_patterns=["*.md", "*.txt", "LICENSE*", ".gitattributes"],
            )

            self._send_progress(job, "downloading", f"Downloaded {model}", progress_percent=25.0)
            return local_path

        except Exception as e:
            error_msg = str(e)
            if "gated" in error_msg.lower() or "access" in error_msg.lower():
                self._send_progress(
                    job, "failed",
                    f"Access denied for {model}. This model may be gated — "
                    f"please accept the license at https://huggingface.co/{model} "
                    f"and provide your HF token in extra_options.",
                )
            else:
                self._send_progress(job, "failed", f"Failed to download model: {error_msg}")
            return None

    def _convert_to_gguf(self, job, model_path, output_path, extra_options):
        """Convert HF model to f16 GGUF using convert_hf_to_gguf.py."""
        # If the model_path is already a GGUF file, just use it as-is
        if isinstance(model_path, str) and model_path.endswith(".gguf"):
            # Copy or symlink the GGUF file
            import shutil
            shutil.copy2(model_path, output_path)
            return True

        # Find convert_hf_to_gguf.py
        convert_script = self._find_convert_script()
        if convert_script is None:
            self._send_progress(job, "failed", "convert_hf_to_gguf.py not found. Install it via the backend's install.sh.")
            return False

        cmd = [
            sys.executable, convert_script,
            model_path,
            "--outfile", output_path,
            "--outtype", "f16",
        ]

        self._send_progress(job, "converting", "Running convert_hf_to_gguf.py...", progress_percent=35.0)

        try:
            process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
            )
            job.process = process

            for line in process.stdout:
                line = line.strip()
                if line:
                    self._send_progress(job, "converting", line, progress_percent=40.0)
                if job.stop_event.is_set():
                    process.kill()
                    self._send_progress(job, "stopped", "Job stopped during conversion")
                    return False

            process.wait()
            job.process = None

            if process.returncode != 0:
                self._send_progress(job, "failed", f"convert_hf_to_gguf.py failed with exit code {process.returncode}")
                return False

            return True

        except Exception as e:
            self._send_progress(job, "failed", f"Conversion failed: {str(e)}")
            return False

    def _quantize(self, job, input_path, output_path, quant_type):
        """Quantize a GGUF file using llama-quantize."""
        quantize_bin = self._find_quantize_binary()
        if quantize_bin is None:
            self._send_progress(job, "failed", "llama-quantize binary not found. Ensure it is installed and in PATH.")
            return False

        cmd = [quantize_bin, input_path, output_path, quant_type]

        self._send_progress(job, "quantizing", f"Running llama-quantize ({quant_type})...", progress_percent=55.0)

        try:
            process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
            )
            job.process = process

            for line in process.stdout:
                line = line.strip()
                if line:
                    # Try to parse progress from llama-quantize output
                    progress = self._parse_quantize_progress(line)
                    pct = 55.0 + (progress * 0.40) if progress else 60.0
                    self._send_progress(job, "quantizing", line, progress_percent=pct)
                if job.stop_event.is_set():
                    process.kill()
                    self._send_progress(job, "stopped", "Job stopped during quantization")
                    return False

            process.wait()
            job.process = None

            if process.returncode != 0:
                self._send_progress(job, "failed", f"llama-quantize failed with exit code {process.returncode}")
                return False

            return True

        except Exception as e:
            self._send_progress(job, "failed", f"Quantization failed: {str(e)}")
            return False

    def _parse_quantize_progress(self, line):
        """Try to parse a progress percentage from llama-quantize output."""
        # llama-quantize typically outputs lines like:
        # [  123/ 1234] quantizing blk.0.attn_k.weight ...
        match = re.search(r'\[\s*(\d+)\s*/\s*(\d+)\]', line)
        if match:
            current = int(match.group(1))
            total = int(match.group(2))
            if total > 0:
                return current / total
        return None

    def _find_convert_script(self):
        """Find convert_hf_to_gguf.py in known locations."""
        candidates = [
            # Same directory as this backend
            os.path.join(os.path.dirname(__file__), "convert_hf_to_gguf.py"),
            # Installed via install.sh
            os.path.join(os.path.dirname(os.path.abspath(__file__)), "convert_hf_to_gguf.py"),
        ]

        # Also check if it's on PATH
        import shutil
        path_script = shutil.which("convert_hf_to_gguf.py")
        if path_script:
            candidates.append(path_script)

        for candidate in candidates:
            if os.path.isfile(candidate):
                return candidate
        return None

    def _find_quantize_binary(self):
        """Find llama-quantize binary."""
        import shutil

        # Check common names on PATH
        for name in ["llama-quantize", "quantize"]:
            path = shutil.which(name)
            if path:
                return path

        # Check in the backend directory (built by install.sh)
        backend_dir = os.path.dirname(os.path.abspath(__file__))
        for name in ["llama-quantize", "quantize"]:
            candidate = os.path.join(backend_dir, name)
            if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                return candidate

        return None

    def _file_metrics(self, filepath):
        """Return file size metrics."""
        try:
            size_bytes = os.path.getsize(filepath)
            return {"file_size_mb": size_bytes / (1024 * 1024)}
        except OSError:
            return {}

    def QuantizationProgress(self, request, context):
        job_id = request.job_id
        job = self.jobs.get(job_id)
        if job is None:
            context.abort(grpc.StatusCode.NOT_FOUND, f"Job {job_id} not found")
            return

        while True:
            try:
                update = job.progress_queue.get(timeout=1.0)
                yield update
                # If this is a terminal status, stop streaming
                if update.status in ("completed", "failed", "stopped"):
                    break
            except queue.Empty:
                # Check if the thread is still alive
                if job.thread and not job.thread.is_alive():
                    # Thread finished but no terminal update — drain queue
                    while not job.progress_queue.empty():
                        update = job.progress_queue.get_nowait()
                        yield update
                    break
                # Check if client disconnected
                if context.is_active() is False:
                    break

    def StopQuantization(self, request, context):
        job_id = request.job_id
        job = self.jobs.get(job_id)
        if job is None:
            return backend_pb2.Result(success=False, message=f"Job {job_id} not found")

        job.stop_event.set()
        if job.process:
            try:
                job.process.kill()
            except OSError:
                pass

        return backend_pb2.Result(success=True, message="Stop signal sent")


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print(f"Quantization backend listening on {address}", file=sys.stderr, flush=True)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="llama.cpp quantization gRPC backend")
    parser.add_argument("--addr", default="localhost:50051", help="gRPC server address")
    args = parser.parse_args()

    signal.signal(signal.SIGINT, lambda sig, frame: sys.exit(0))
    serve(args.addr)

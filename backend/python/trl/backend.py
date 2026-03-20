#!/usr/bin/env python3
"""
TRL fine-tuning backend for LocalAI.

Supports all TRL training methods (SFT, DPO, GRPO, RLOO, Reward, KTO, ORPO)
using standard HuggingFace transformers + PEFT. Works on both CPU and GPU.
"""
import argparse
import json
import os
import queue
import signal
import sys
import threading
import time
import uuid
from concurrent import futures

import grpc
import backend_pb2
import backend_pb2_grpc

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


class ProgressCallback:
    """HuggingFace TrainerCallback that pushes progress updates to a queue."""

    def __init__(self, job_id, progress_queue, total_epochs):
        self.job_id = job_id
        self.progress_queue = progress_queue
        self.total_epochs = total_epochs

    def get_callback(self):
        from transformers import TrainerCallback

        parent = self

        class _Callback(TrainerCallback):
            def on_log(self, args, state, control, logs=None, **kwargs):
                if logs is None:
                    return
                total_steps = state.max_steps if state.max_steps > 0 else 0
                progress = (state.global_step / total_steps * 100) if total_steps > 0 else 0
                eta = 0.0
                if state.global_step > 0 and total_steps > 0:
                    elapsed = time.time() - state.logging_steps  # approximate
                    remaining_steps = total_steps - state.global_step
                    if state.global_step > 1:
                        eta = remaining_steps * (elapsed / state.global_step)

                extra_metrics = {}
                for k, v in logs.items():
                    if isinstance(v, (int, float)) and k not in ('loss', 'learning_rate', 'epoch', 'grad_norm', 'eval_loss'):
                        extra_metrics[k] = float(v)

                update = backend_pb2.FineTuneProgressUpdate(
                    job_id=parent.job_id,
                    current_step=state.global_step,
                    total_steps=total_steps,
                    current_epoch=float(logs.get('epoch', 0)),
                    total_epochs=float(parent.total_epochs),
                    loss=float(logs.get('loss', 0)),
                    learning_rate=float(logs.get('learning_rate', 0)),
                    grad_norm=float(logs.get('grad_norm', 0)),
                    eval_loss=float(logs.get('eval_loss', 0)),
                    eta_seconds=float(eta),
                    progress_percent=float(progress),
                    status="training",
                    extra_metrics=extra_metrics,
                )
                parent.progress_queue.put(update)

            def on_save(self, args, state, control, **kwargs):
                checkpoint_path = os.path.join(args.output_dir, f"checkpoint-{state.global_step}")
                update = backend_pb2.FineTuneProgressUpdate(
                    job_id=parent.job_id,
                    current_step=state.global_step,
                    status="saving",
                    message=f"Checkpoint saved at step {state.global_step}",
                    checkpoint_path=checkpoint_path,
                )
                parent.progress_queue.put(update)

            def on_train_end(self, args, state, control, **kwargs):
                update = backend_pb2.FineTuneProgressUpdate(
                    job_id=parent.job_id,
                    current_step=state.global_step,
                    total_steps=state.max_steps,
                    progress_percent=100.0,
                    status="completed",
                    message="Training completed",
                )
                parent.progress_queue.put(update)

        return _Callback()


class ActiveJob:
    """Represents an active fine-tuning job."""

    def __init__(self, job_id):
        self.job_id = job_id
        self.progress_queue = queue.Queue()
        self.trainer = None
        self.thread = None
        self.model = None
        self.tokenizer = None
        self.error = None
        self.completed = False
        self.stopped = False


def _is_gated_repo_error(exc):
    """Check if an exception is caused by a gated HuggingFace repo requiring authentication."""
    try:
        from huggingface_hub.utils import GatedRepoError
        if isinstance(exc, GatedRepoError):
            return True
    except ImportError:
        pass
    msg = str(exc).lower()
    if "gated repo" in msg or "access to model" in msg:
        return True
    if hasattr(exc, 'response') and hasattr(exc.response, 'status_code'):
        if exc.response.status_code in (401, 403):
            return True
    return False


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self):
        self.active_job = None

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        """Accept LoadModel — actual model loading happens in StartFineTune."""
        return backend_pb2.Result(success=True, message="OK")

    def StartFineTune(self, request, context):
        if self.active_job is not None and not self.active_job.completed:
            return backend_pb2.FineTuneJobResult(
                job_id="",
                success=False,
                message="A fine-tuning job is already running",
            )

        job_id = request.job_id if request.job_id else str(uuid.uuid4())
        job = ActiveJob(job_id)
        self.active_job = job

        # Start training in background thread
        thread = threading.Thread(target=self._run_training, args=(request, job), daemon=True)
        job.thread = thread
        thread.start()

        return backend_pb2.FineTuneJobResult(
            job_id=job_id,
            success=True,
            message="Fine-tuning job started",
        )

    def _run_training(self, request, job):
        try:
            self._do_training(request, job)
        except Exception as e:
            if _is_gated_repo_error(e):
                msg = (f"Model '{request.model}' is a gated HuggingFace repo and requires authentication. "
                       "Pass 'hf_token' in extra_options or set the HF_TOKEN environment variable.")
            else:
                msg = f"Training failed: {e}"
            job.error = msg
            job.completed = True
            update = backend_pb2.FineTuneProgressUpdate(
                job_id=job.job_id,
                status="failed",
                message=msg,
            )
            job.progress_queue.put(update)
            # Send sentinel
            job.progress_queue.put(None)

    def _do_training(self, request, job):
        import torch
        from transformers import AutoModelForCausalLM, AutoTokenizer
        from datasets import load_dataset, Dataset

        extra = dict(request.extra_options)
        training_method = request.training_method or "sft"
        training_type = request.training_type or "lora"

        # Send loading status
        job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
            job_id=job.job_id, status="loading_model", message=f"Loading model {request.model}",
        ))

        # Determine device and dtype
        device_map = "auto" if torch.cuda.is_available() else "cpu"
        dtype = torch.float32 if not torch.cuda.is_available() else torch.bfloat16

        # HuggingFace token for gated repos (from extra_options or HF_TOKEN env)
        hf_token = extra.get("hf_token") or os.environ.get("HF_TOKEN")

        # Load model
        model_kwargs = {"device_map": device_map, "torch_dtype": dtype}
        if hf_token:
            model_kwargs["token"] = hf_token
        if extra.get("trust_remote_code", "false").lower() == "true":
            model_kwargs["trust_remote_code"] = True
        if extra.get("load_in_4bit", "false").lower() == "true" and torch.cuda.is_available():
            from transformers import BitsAndBytesConfig
            model_kwargs["quantization_config"] = BitsAndBytesConfig(load_in_4bit=True)

        model = AutoModelForCausalLM.from_pretrained(request.model, **model_kwargs)
        tokenizer = AutoTokenizer.from_pretrained(request.model, token=hf_token)
        if tokenizer.pad_token is None:
            tokenizer.pad_token = tokenizer.eos_token

        job.model = model
        job.tokenizer = tokenizer

        # Apply LoRA if requested
        if training_type == "lora":
            from peft import LoraConfig, get_peft_model
            lora_r = request.adapter_rank if request.adapter_rank > 0 else 16
            lora_alpha = request.adapter_alpha if request.adapter_alpha > 0 else 16
            lora_dropout = request.adapter_dropout if request.adapter_dropout > 0 else 0.0

            target_modules = list(request.target_modules) if request.target_modules else None
            peft_config = LoraConfig(
                r=lora_r,
                lora_alpha=lora_alpha,
                lora_dropout=lora_dropout,
                target_modules=target_modules or "all-linear",
                bias="none",
                task_type="CAUSAL_LM",
            )
            model = get_peft_model(model, peft_config)

        # Load dataset
        job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
            job_id=job.job_id, status="loading_dataset", message="Loading dataset",
        ))

        dataset_split = request.dataset_split or "train"
        if os.path.exists(request.dataset_source):
            if request.dataset_source.endswith('.json') or request.dataset_source.endswith('.jsonl'):
                dataset = load_dataset("json", data_files=request.dataset_source, split=dataset_split)
            elif request.dataset_source.endswith('.csv'):
                dataset = load_dataset("csv", data_files=request.dataset_source, split=dataset_split)
            else:
                dataset = load_dataset(request.dataset_source, split=dataset_split)
        else:
            dataset = load_dataset(request.dataset_source, split=dataset_split)

        # Training config
        output_dir = request.output_dir or f"./output-{job.job_id}"
        num_epochs = request.num_epochs if request.num_epochs > 0 else 3
        batch_size = request.batch_size if request.batch_size > 0 else 2
        lr = request.learning_rate if request.learning_rate > 0 else 2e-4
        grad_accum = request.gradient_accumulation_steps if request.gradient_accumulation_steps > 0 else 4
        warmup_steps = request.warmup_steps if request.warmup_steps > 0 else 5
        weight_decay = request.weight_decay if request.weight_decay > 0 else 0.01
        max_steps = request.max_steps if request.max_steps > 0 else -1
        save_steps = request.save_steps if request.save_steps > 0 else 500
        seed = request.seed if request.seed > 0 else 3407
        optimizer = request.optimizer or "adamw_torch"

        # Checkpoint save controls
        save_total_limit = int(extra.get("save_total_limit", "0")) or None  # 0 = unlimited
        save_strategy = extra.get("save_strategy", "steps")  # steps, epoch, no

        # CPU vs GPU training args (can be overridden via extra_options)
        use_cpu = not torch.cuda.is_available()
        common_train_kwargs = {}
        if use_cpu:
            common_train_kwargs["use_cpu"] = True
            common_train_kwargs["fp16"] = False
            common_train_kwargs["bf16"] = False
            common_train_kwargs["gradient_checkpointing"] = False
        else:
            common_train_kwargs["bf16"] = True
            common_train_kwargs["gradient_checkpointing"] = request.gradient_checkpointing

        # Allow extra_options to override training kwargs
        for flag in ("use_cpu", "bf16", "fp16", "gradient_checkpointing"):
            if flag in extra:
                common_train_kwargs[flag] = extra[flag].lower() == "true"

        # Create progress callback
        progress_cb = ProgressCallback(job.job_id, job.progress_queue, num_epochs)

        # Build save kwargs (shared across all methods)
        _save_kwargs = {}
        if save_strategy == "steps" and save_steps > 0:
            _save_kwargs["save_steps"] = save_steps
            _save_kwargs["save_strategy"] = "steps"
        elif save_strategy == "epoch":
            _save_kwargs["save_strategy"] = "epoch"
        elif save_strategy == "no":
            _save_kwargs["save_strategy"] = "no"
        else:
            _save_kwargs["save_steps"] = save_steps
            _save_kwargs["save_strategy"] = "steps"
        if save_total_limit:
            _save_kwargs["save_total_limit"] = save_total_limit

        # Common training arguments shared by all methods
        _common_args = dict(
            output_dir=output_dir,
            num_train_epochs=num_epochs,
            per_device_train_batch_size=batch_size,
            learning_rate=lr,
            gradient_accumulation_steps=grad_accum,
            warmup_steps=warmup_steps,
            weight_decay=weight_decay,
            max_steps=max_steps,
            seed=seed,
            optim=optimizer,
            logging_steps=1,
            report_to="none",
            **_save_kwargs,
            **common_train_kwargs,
        )

        # Select trainer based on training method
        if training_method == "sft":
            from trl import SFTTrainer, SFTConfig

            max_length = int(extra.get("max_seq_length", "512"))
            packing = extra.get("packing", "false").lower() == "true"

            training_args = SFTConfig(
                max_length=max_length,
                packing=packing,
                **_common_args,
            )

            trainer = SFTTrainer(
                model=model,
                args=training_args,
                train_dataset=dataset,
                processing_class=tokenizer,
                callbacks=[progress_cb.get_callback()],
            )

        elif training_method == "dpo":
            from trl import DPOTrainer, DPOConfig

            beta = float(extra.get("beta", "0.1"))
            loss_type = extra.get("loss_type", "sigmoid")
            max_length = int(extra.get("max_length", "512"))

            training_args = DPOConfig(
                beta=beta,
                loss_type=loss_type,
                max_length=max_length,
                **_common_args,
            )

            trainer = DPOTrainer(
                model=model,
                args=training_args,
                train_dataset=dataset,
                processing_class=tokenizer,
                callbacks=[progress_cb.get_callback()],
            )

        elif training_method == "grpo":
            from trl import GRPOTrainer, GRPOConfig

            num_generations = int(extra.get("num_generations", "4"))
            max_completion_length = int(extra.get("max_completion_length", "256"))

            training_args = GRPOConfig(
                num_generations=num_generations,
                max_completion_length=max_completion_length,
                **_common_args,
            )

            # GRPO requires reward functions passed via extra_options as a JSON list
            from reward_functions import build_reward_functions

            reward_funcs = []
            if extra.get("reward_funcs"):
                reward_funcs = build_reward_functions(extra["reward_funcs"])

            if not reward_funcs:
                raise ValueError(
                    "GRPO requires at least one reward function. "
                    "Specify reward_functions in the request or "
                    "reward_funcs in extra_options."
                )

            trainer = GRPOTrainer(
                model=model,
                args=training_args,
                train_dataset=dataset,
                processing_class=tokenizer,
                reward_funcs=reward_funcs,
                callbacks=[progress_cb.get_callback()],
            )

        elif training_method == "orpo":
            from trl import ORPOTrainer, ORPOConfig

            beta = float(extra.get("beta", "0.1"))
            max_length = int(extra.get("max_length", "512"))

            training_args = ORPOConfig(
                beta=beta,
                max_length=max_length,
                **_common_args,
            )

            trainer = ORPOTrainer(
                model=model,
                args=training_args,
                train_dataset=dataset,
                processing_class=tokenizer,
                callbacks=[progress_cb.get_callback()],
            )

        elif training_method == "kto":
            from trl import KTOTrainer, KTOConfig

            beta = float(extra.get("beta", "0.1"))
            max_length = int(extra.get("max_length", "512"))

            training_args = KTOConfig(
                beta=beta,
                max_length=max_length,
                **_common_args,
            )

            trainer = KTOTrainer(
                model=model,
                args=training_args,
                train_dataset=dataset,
                processing_class=tokenizer,
                callbacks=[progress_cb.get_callback()],
            )

        elif training_method == "rloo":
            from trl import RLOOTrainer, RLOOConfig

            num_generations = int(extra.get("num_generations", "4"))
            max_completion_length = int(extra.get("max_completion_length", "256"))

            training_args = RLOOConfig(
                num_generations=num_generations,
                max_new_tokens=max_completion_length,
                **_common_args,
            )

            trainer = RLOOTrainer(
                model=model,
                args=training_args,
                train_dataset=dataset,
                processing_class=tokenizer,
                callbacks=[progress_cb.get_callback()],
            )

        elif training_method == "reward":
            from trl import RewardTrainer, RewardConfig

            max_length = int(extra.get("max_length", "512"))

            training_args = RewardConfig(
                max_length=max_length,
                **_common_args,
            )

            trainer = RewardTrainer(
                model=model,
                args=training_args,
                train_dataset=dataset,
                processing_class=tokenizer,
                callbacks=[progress_cb.get_callback()],
            )

        else:
            raise ValueError(f"Unsupported training method: {training_method}. "
                             "Supported: sft, dpo, grpo, orpo, kto, rloo, reward")

        job.trainer = trainer

        # Start training
        job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
            job_id=job.job_id, status="training", message="Training started",
        ))

        resume_ckpt = request.resume_from_checkpoint if request.resume_from_checkpoint else None
        trainer.train(resume_from_checkpoint=resume_ckpt)

        # Save final model
        trainer.save_model(output_dir)
        if tokenizer:
            tokenizer.save_pretrained(output_dir)

        job.completed = True
        # Sentinel to signal stream end
        job.progress_queue.put(None)

    def FineTuneProgress(self, request, context):
        if self.active_job is None or self.active_job.job_id != request.job_id:
            context.set_code(grpc.StatusCode.NOT_FOUND)
            context.set_details(f"Job {request.job_id} not found")
            return

        job = self.active_job
        while True:
            try:
                update = job.progress_queue.get(timeout=1.0)
                if update is None:
                    break
                yield update
                if update.status in ("completed", "failed", "stopped"):
                    break
            except queue.Empty:
                if job.completed or job.stopped:
                    break
                if not context.is_active():
                    break
                continue

    def StopFineTune(self, request, context):
        # No-op: stopping is handled by killing the backend process from Go.
        # This stub remains to satisfy the proto-generated gRPC interface.
        return backend_pb2.Result(success=True, message="No-op (process kill used instead)")

    def ListCheckpoints(self, request, context):
        output_dir = request.output_dir
        if not os.path.isdir(output_dir):
            return backend_pb2.ListCheckpointsResponse(checkpoints=[])

        checkpoints = []
        for entry in sorted(os.listdir(output_dir)):
            if entry.startswith("checkpoint-"):
                ckpt_path = os.path.join(output_dir, entry)
                if not os.path.isdir(ckpt_path):
                    continue
                step = 0
                try:
                    step = int(entry.split("-")[1])
                except (IndexError, ValueError):
                    pass

                # Try to read trainer_state.json for metadata
                loss = 0.0
                epoch = 0.0
                state_file = os.path.join(ckpt_path, "trainer_state.json")
                if os.path.exists(state_file):
                    try:
                        with open(state_file) as f:
                            state = json.load(f)
                        if state.get("log_history"):
                            last_log = state["log_history"][-1]
                            loss = last_log.get("loss", 0.0)
                            epoch = last_log.get("epoch", 0.0)
                    except Exception:
                        pass

                created_at = time.strftime(
                    "%Y-%m-%dT%H:%M:%SZ",
                    time.gmtime(os.path.getmtime(ckpt_path)),
                )

                checkpoints.append(backend_pb2.CheckpointInfo(
                    path=ckpt_path,
                    step=step,
                    epoch=float(epoch),
                    loss=float(loss),
                    created_at=created_at,
                ))

        return backend_pb2.ListCheckpointsResponse(checkpoints=checkpoints)

    def ExportModel(self, request, context):
        export_format = request.export_format or "lora"
        output_path = request.output_path
        checkpoint_path = request.checkpoint_path

        # Extract HF token for gated model access
        extra = dict(request.extra_options) if request.extra_options else {}
        hf_token = extra.get("hf_token") or os.environ.get("HF_TOKEN")

        if not checkpoint_path or not os.path.isdir(checkpoint_path):
            return backend_pb2.Result(success=False, message=f"Checkpoint not found: {checkpoint_path}")

        os.makedirs(output_path, exist_ok=True)

        try:
            if export_format == "lora":
                # Just copy the adapter files
                import shutil
                for f in os.listdir(checkpoint_path):
                    src = os.path.join(checkpoint_path, f)
                    dst = os.path.join(output_path, f)
                    if os.path.isfile(src):
                        shutil.copy2(src, dst)

            elif export_format in ("merged_16bit", "merged_4bit"):
                import torch
                from transformers import AutoModelForCausalLM, AutoTokenizer
                from peft import PeftModel

                base_model_name = request.model
                if not base_model_name:
                    return backend_pb2.Result(success=False, message="Base model name required for merge export")

                dtype = torch.float16 if export_format == "merged_16bit" else torch.float32
                base_model = AutoModelForCausalLM.from_pretrained(base_model_name, torch_dtype=dtype, token=hf_token)
                model = PeftModel.from_pretrained(base_model, checkpoint_path)
                merged = model.merge_and_unload()
                merged.save_pretrained(output_path)

                tokenizer = AutoTokenizer.from_pretrained(base_model_name, token=hf_token)
                tokenizer.save_pretrained(output_path)

            elif export_format == "gguf":
                import torch
                import subprocess
                import shutil
                from transformers import AutoModelForCausalLM, AutoTokenizer
                from peft import PeftModel

                base_model_name = request.model
                if not base_model_name:
                    return backend_pb2.Result(success=False, message="Base model name required for GGUF export")

                # Step 1: Merge LoRA into base model
                merge_dir = os.path.join(output_path, "_hf_merged")
                os.makedirs(merge_dir, exist_ok=True)

                base_model = AutoModelForCausalLM.from_pretrained(base_model_name, torch_dtype=torch.float16, token=hf_token)
                model = PeftModel.from_pretrained(base_model, checkpoint_path)
                merged = model.merge_and_unload()
                merged.save_pretrained(merge_dir)

                tokenizer = AutoTokenizer.from_pretrained(base_model_name, token=hf_token)
                tokenizer.save_pretrained(merge_dir)

                # Ensure tokenizer.model (SentencePiece) is present in merge_dir.
                # Gemma models need this file for GGUF conversion to use the
                # SentencePiece path; without it, the script falls back to BPE
                # handling which fails on unrecognized pre-tokenizer hashes.
                sp_model_path = os.path.join(merge_dir, "tokenizer.model")
                if not os.path.exists(sp_model_path):
                    sp_copied = False
                    # Method 1: Load the slow tokenizer which keeps the SP model file
                    try:
                        slow_tok = AutoTokenizer.from_pretrained(base_model_name, use_fast=False, token=hf_token)
                        if hasattr(slow_tok, 'vocab_file') and slow_tok.vocab_file and os.path.exists(slow_tok.vocab_file):
                            import shutil as _shutil
                            _shutil.copy2(slow_tok.vocab_file, sp_model_path)
                            sp_copied = True
                            print(f"Copied tokenizer.model from slow tokenizer cache")
                    except Exception as e:
                        print(f"Slow tokenizer method failed: {e}")
                    # Method 2: Download from HF hub
                    if not sp_copied:
                        try:
                            from huggingface_hub import hf_hub_download
                            cached_sp = hf_hub_download(repo_id=base_model_name, filename="tokenizer.model", token=hf_token)
                            import shutil as _shutil
                            _shutil.copy2(cached_sp, sp_model_path)
                            sp_copied = True
                            print(f"Copied tokenizer.model from HF hub")
                        except Exception as e:
                            print(f"HF hub download method failed: {e}")
                    if not sp_copied:
                        print(f"WARNING: Could not obtain tokenizer.model for {base_model_name}. "
                              "GGUF conversion may fail for SentencePiece models.")

                # Free GPU memory before conversion
                del merged, model, base_model
                if torch.cuda.is_available():
                    torch.cuda.empty_cache()

                # Step 2: Convert to GGUF using convert_hf_to_gguf.py
                quant = request.quantization_method or "auto"
                outtype_map = {"f16": "f16", "f32": "f32", "bf16": "bf16", "q8_0": "q8_0", "auto": "auto"}
                outtype = outtype_map.get(quant, "f16")

                gguf_filename = f"{os.path.basename(output_path)}-{outtype}.gguf"
                gguf_path = os.path.join(output_path, gguf_filename)

                script_dir = os.path.dirname(os.path.abspath(__file__))
                convert_script = os.path.join(script_dir, "convert_hf_to_gguf.py")
                if not os.path.exists(convert_script):
                    return backend_pb2.Result(success=False,
                        message="convert_hf_to_gguf.py not found. Install the GGUF conversion tools.")

                # Log merge_dir contents for debugging conversion issues
                merge_files = os.listdir(merge_dir) if os.path.isdir(merge_dir) else []
                print(f"Merge dir contents: {merge_files}", flush=True)

                env = os.environ.copy()
                env["NO_LOCAL_GGUF"] = "1"
                cmd = [sys.executable, convert_script, merge_dir, "--outtype", outtype, "--outfile", gguf_path]
                conv_result = subprocess.run(cmd, capture_output=True, text=True, timeout=3600, env=env)
                if conv_result.returncode != 0:
                    diag = f"stdout: {conv_result.stdout[-300:]}\nstderr: {conv_result.stderr[-500:]}"
                    return backend_pb2.Result(success=False,
                        message=f"GGUF conversion failed: {diag}")

                # Clean up intermediate merged model
                shutil.rmtree(merge_dir, ignore_errors=True)
            else:
                return backend_pb2.Result(success=False, message=f"Unsupported export format: {export_format}")

        except Exception as e:
            if _is_gated_repo_error(e):
                return backend_pb2.Result(success=False,
                    message=f"Model '{request.model}' is a gated HuggingFace repo and requires authentication. "
                            "Pass 'hf_token' in extra_options or set the HF_TOKEN environment variable.")
            return backend_pb2.Result(success=False, message=f"Export failed: {e}")

        return backend_pb2.Result(success=True, message=f"Model exported to {output_path}")


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ],
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print(f"TRL fine-tuning backend listening on {address}", file=sys.stderr, flush=True)

    # Handle graceful shutdown
    def stop(signum, frame):
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="TRL fine-tuning gRPC backend")
    parser.add_argument("--addr", default="localhost:50051", help="gRPC server address")
    args = parser.parse_args()
    serve(args.addr)

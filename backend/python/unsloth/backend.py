#!/usr/bin/env python3
"""
Unsloth fine-tuning backend for LocalAI.

Implements the TrainStream gRPC RPC to fine-tune LLMs using Unsloth
with QLoRA, LoRA, or full fine-tuning. Streams progress updates back
to the caller.
"""
from concurrent import futures

import argparse
import signal
import sys
import os
import json
import traceback
import asyncio

import backend_pb2
import backend_pb2_grpc

import grpc

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


def detect_hardware():
    """Detect available hardware and return device info."""
    import torch

    if torch.cuda.is_available():
        device_name = torch.cuda.get_device_name(0)
        vram_gb = torch.cuda.get_device_properties(0).total_mem / (1024**3)
        return {
            "device": "cuda",
            "device_name": device_name,
            "vram_gb": round(vram_gb, 1),
            "cuda_version": torch.version.cuda,
        }

    if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
        return {"device": "mps", "device_name": "Apple Silicon (MPS)"}

    return {"device": "cpu", "device_name": "CPU"}


def load_dataset_for_training(dataset_path, options):
    """Load a dataset from a local path or HuggingFace hub."""
    from datasets import load_dataset

    dataset_format = options.get("dataset_format", "auto")
    dataset_split = options.get("dataset_split", "train")
    dataset_text_field = options.get("dataset_text_field", "text")

    # Local file
    if os.path.exists(dataset_path):
        ext = os.path.splitext(dataset_path)[1].lower()
        if ext == ".jsonl" or ext == ".json":
            ds = load_dataset("json", data_files=dataset_path, split=dataset_split)
        elif ext == ".csv":
            ds = load_dataset("csv", data_files=dataset_path, split=dataset_split)
        elif ext == ".parquet":
            ds = load_dataset("parquet", data_files=dataset_path, split=dataset_split)
        else:
            ds = load_dataset("json", data_files=dataset_path, split=dataset_split)
    else:
        # HuggingFace dataset
        ds = load_dataset(dataset_path, split=dataset_split)

    # If dataset has chat format (conversations/messages column), handle it
    if dataset_format == "chatml" or "conversations" in ds.column_names:
        return ds, "chatml"
    if "messages" in ds.column_names:
        return ds, "messages"

    # If we have a text field, use it directly
    if dataset_text_field in ds.column_names:
        return ds, "text"

    # For instruction datasets with input/output/instruction columns
    if "instruction" in ds.column_names:
        return ds, "alpaca"

    return ds, "text"


def format_dataset(ds, fmt, tokenizer, dataset_text_field="text", max_seq_length=2048):
    """Format a dataset for SFT training based on its format type."""
    from unsloth.chat_templates import get_chat_template, standardize_sharegpt

    if fmt == "chatml" or fmt == "messages":
        tokenizer = get_chat_template(
            tokenizer,
            chat_template="chatml",
        )
        col = "conversations" if "conversations" in ds.column_names else "messages"
        ds = standardize_sharegpt(ds)

        def apply_template(examples):
            convos = examples[col]
            texts = [
                tokenizer.apply_chat_template(
                    convo, tokenize=False, add_generation_prompt=False
                )
                for convo in convos
            ]
            return {"text": texts}

        ds = ds.map(apply_template, batched=True)
        return ds, tokenizer

    if fmt == "alpaca":
        from unsloth.chat_templates import get_chat_template

        tokenizer = get_chat_template(tokenizer, chat_template="alpaca")

        def format_alpaca(examples):
            instructions = examples.get("instruction", [])
            inputs = examples.get("input", [""] * len(instructions))
            outputs = examples.get("output", [])
            texts = []
            for inst, inp, out in zip(instructions, inputs, outputs):
                if inp:
                    text = f"### Instruction:\n{inst}\n\n### Input:\n{inp}\n\n### Response:\n{out}"
                else:
                    text = f"### Instruction:\n{inst}\n\n### Response:\n{out}"
                texts.append(text)
            return {"text": texts}

        ds = ds.map(format_alpaca, batched=True)
        return ds, tokenizer

    # Plain text format — already has the text field
    return ds, tokenizer


class BackendServicer(backend_pb2_grpc.BackendServicer):
    """gRPC servicer for Unsloth fine-tuning."""

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", "utf-8"))

    def LoadModel(self, request, context):
        # Not used for fine-tuning; training is self-contained via TrainStream
        return backend_pb2.Result(
            message="Unsloth backend: use TrainStream for fine-tuning",
            success=True,
        )

    def TrainStream(self, request, context):
        """
        Run a fine-tuning job and stream progress updates.

        Expects a TrainRequest with model, dataset, output_dir, and
        hyperparameters. Yields TrainResponse messages with status,
        progress, loss, etc.
        """
        try:
            yield from self._run_training(request, context)
        except Exception as e:
            traceback.print_exc(file=sys.stderr)
            yield backend_pb2.TrainResponse(
                status=backend_pb2.TrainResponse.FAILED,
                error=str(e),
                message=f"Training failed: {e}",
            )

    def _run_training(self, request, context):
        # -- Parse options --
        options = dict(request.options) if request.options else {}

        yield backend_pb2.TrainResponse(
            status=backend_pb2.TrainResponse.STARTING,
            message="Detecting hardware...",
        )

        hw = detect_hardware()
        print(f"[unsloth] Hardware: {hw}", file=sys.stderr)

        yield backend_pb2.TrainResponse(
            status=backend_pb2.TrainResponse.STARTING,
            message=f"Hardware: {hw['device_name']}. Loading model...",
        )

        # -- Determine parameters --
        model_name = request.model
        max_seq_length = request.max_seq_length if request.max_seq_length > 0 else 2048
        epochs = request.epochs if request.epochs > 0 else 1
        batch_size = request.batch_size if request.batch_size > 0 else 4
        learning_rate = request.learning_rate if request.learning_rate > 0 else 2e-4
        lora_rank = request.lora_rank if request.lora_rank > 0 else 16
        lora_alpha = request.lora_alpha if request.lora_alpha > 0 else 16
        lora_dropout = request.lora_dropout if request.lora_dropout > 0 else 0.0
        quantization = request.quantization or "4bit"
        output_dir = request.output_dir or "/tmp/unsloth-output"

        gradient_accumulation_steps = int(options.get("gradient_accumulation_steps", "4"))
        warmup_steps = int(options.get("warmup_steps", "5"))
        weight_decay = float(options.get("weight_decay", "0.01"))
        lr_scheduler = options.get("lr_scheduler_type", "linear")
        save_steps = int(options.get("save_steps", "0"))
        logging_steps = int(options.get("logging_steps", "1"))
        dataset_text_field = options.get("dataset_text_field", "text")
        training_type = options.get("training_type", "lora")  # lora, qlora, full
        export_gguf = options.get("export_gguf", "false").lower() == "true"
        gguf_quantization = options.get("gguf_quantization", "q4_k_m")

        # Determine load_in_4bit based on quantization and training type
        load_in_4bit = quantization == "4bit" or training_type == "qlora"

        # Target modules for LoRA
        if request.target_modules:
            target_modules = list(request.target_modules)
        else:
            target_modules = [
                "q_proj", "k_proj", "v_proj", "o_proj",
                "gate_proj", "up_proj", "down_proj",
            ]

        # -- Load model with Unsloth --
        from unsloth import FastLanguageModel

        model, tokenizer = FastLanguageModel.from_pretrained(
            model_name=model_name,
            max_seq_length=max_seq_length,
            dtype=None,  # Auto-detect
            load_in_4bit=load_in_4bit,
        )

        yield backend_pb2.TrainResponse(
            status=backend_pb2.TrainResponse.STARTING,
            message="Model loaded. Applying LoRA adapters...",
        )

        # -- Apply LoRA / QLoRA --
        if training_type in ("lora", "qlora"):
            model = FastLanguageModel.get_peft_model(
                model,
                r=lora_rank,
                target_modules=target_modules,
                lora_alpha=lora_alpha,
                lora_dropout=lora_dropout,
                bias="none",
                use_gradient_checkpointing="unsloth",
                random_state=3407,
            )

        yield backend_pb2.TrainResponse(
            status=backend_pb2.TrainResponse.STARTING,
            message="LoRA applied. Loading dataset...",
        )

        # -- Load and format dataset --
        ds, ds_format = load_dataset_for_training(request.dataset, options)
        ds, tokenizer = format_dataset(
            ds, ds_format, tokenizer,
            dataset_text_field=dataset_text_field,
            max_seq_length=max_seq_length,
        )

        print(f"[unsloth] Dataset loaded: {len(ds)} samples, format={ds_format}", file=sys.stderr)

        yield backend_pb2.TrainResponse(
            status=backend_pb2.TrainResponse.STARTING,
            message=f"Dataset loaded ({len(ds)} samples). Starting training...",
        )

        # -- Setup trainer --
        from trl import SFTTrainer
        from transformers import TrainingArguments, TrainerCallback

        # Estimate total steps
        steps_per_epoch = max(1, len(ds) // (batch_size * gradient_accumulation_steps))
        total_steps = steps_per_epoch * epochs

        # Custom callback to stream progress via gRPC
        progress_state = {"responses": []}

        class ProgressCallback(TrainerCallback):
            def on_log(self, args, state, control, logs=None, **kwargs):
                if logs is None:
                    return
                current_step = state.global_step
                current_epoch = int(state.epoch) if state.epoch else 0
                loss = logs.get("loss", 0.0)
                lr = logs.get("learning_rate", 0.0)
                progress = (current_step / total_steps * 100) if total_steps > 0 else 0

                resp = backend_pb2.TrainResponse(
                    status=backend_pb2.TrainResponse.RUNNING,
                    progress=min(progress, 99.9),
                    current_epoch=current_epoch,
                    total_epochs=epochs,
                    current_step=current_step,
                    total_steps=total_steps,
                    loss=loss,
                    learning_rate_current=lr,
                    message=f"Step {current_step}/{total_steps} | Loss: {loss:.4f}",
                )
                progress_state["responses"].append(resp)

        training_args = TrainingArguments(
            per_device_train_batch_size=batch_size,
            gradient_accumulation_steps=gradient_accumulation_steps,
            warmup_steps=warmup_steps,
            num_train_epochs=epochs,
            learning_rate=learning_rate,
            fp16=hw["device"] == "cuda",
            bf16=False,
            logging_steps=logging_steps,
            optim="adamw_8bit",
            weight_decay=weight_decay,
            lr_scheduler_type=lr_scheduler,
            seed=3407,
            output_dir=output_dir,
            save_steps=save_steps if save_steps > 0 else total_steps,
            report_to="none",
        )

        trainer = SFTTrainer(
            model=model,
            tokenizer=tokenizer,
            train_dataset=ds,
            dataset_text_field="text",
            max_seq_length=max_seq_length,
            dataset_num_proc=2,
            args=training_args,
            callbacks=[ProgressCallback()],
        )

        # -- Run training in a thread so we can yield progress --
        import threading

        train_result = {"error": None, "stats": None}

        def do_train():
            try:
                stats = trainer.train()
                train_result["stats"] = stats
            except Exception as e:
                train_result["error"] = e

        train_thread = threading.Thread(target=do_train, daemon=True)
        train_thread.start()

        # Yield progress responses as they arrive
        import time
        last_idx = 0
        while train_thread.is_alive():
            time.sleep(0.5)
            responses = progress_state["responses"]
            while last_idx < len(responses):
                yield responses[last_idx]
                last_idx += 1

        # Drain remaining responses
        responses = progress_state["responses"]
        while last_idx < len(responses):
            yield responses[last_idx]
            last_idx += 1

        train_thread.join()

        if train_result["error"] is not None:
            raise train_result["error"]

        # -- Save model --
        yield backend_pb2.TrainResponse(
            status=backend_pb2.TrainResponse.RUNNING,
            progress=95.0,
            message="Training complete. Saving model...",
        )

        model.save_pretrained(output_dir)
        tokenizer.save_pretrained(output_dir)

        final_output = output_dir

        # -- Optional GGUF export --
        if export_gguf:
            yield backend_pb2.TrainResponse(
                status=backend_pb2.TrainResponse.RUNNING,
                progress=97.0,
                message=f"Exporting to GGUF ({gguf_quantization})...",
            )

            gguf_output = os.path.join(output_dir, "gguf")
            try:
                model.save_pretrained_gguf(
                    gguf_output,
                    tokenizer,
                    quantization_method=gguf_quantization,
                )
                final_output = gguf_output
                print(f"[unsloth] GGUF exported to {gguf_output}", file=sys.stderr)
            except Exception as e:
                print(f"[unsloth] GGUF export failed: {e}", file=sys.stderr)
                yield backend_pb2.TrainResponse(
                    status=backend_pb2.TrainResponse.RUNNING,
                    progress=98.0,
                    message=f"GGUF export failed (model still saved as safetensors): {e}",
                )

        # -- Done --
        yield backend_pb2.TrainResponse(
            status=backend_pb2.TrainResponse.COMPLETED,
            progress=100.0,
            current_epoch=epochs,
            total_epochs=epochs,
            current_step=total_steps,
            total_steps=total_steps,
            message="Fine-tuning completed successfully",
            output_path=final_output,
        )

        print(f"[unsloth] Training complete. Output: {final_output}", file=sys.stderr)


async def serve(address):
    server = grpc.aio.server(
        migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_message_length", 50 * 1024 * 1024),
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),
        ],
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)

    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, lambda: asyncio.ensure_future(server.stop(5)))

    await server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)
    await server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()

    asyncio.run(serve(args.addr))

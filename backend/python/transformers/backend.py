#!/usr/bin/env python3
"""
Extra gRPC server for HuggingFace AutoModel models.
"""
from concurrent import futures

import argparse
import signal
import sys
import os
from threading import Thread
import asyncio

import time
import backend_pb2
import backend_pb2_grpc

import grpc
import torch
import torch.cuda


XPU=os.environ.get("XPU", "0") == "1"
import transformers as transformers_module
from transformers import AutoTokenizer, AutoModel, AutoProcessor, set_seed, TextIteratorStreamer, StoppingCriteriaList, StopStringCriteria
from scipy.io import wavfile
from sentence_transformers import SentenceTransformer

# Backward-compat aliases for model types
TYPE_ALIASES = {"Mamba": "MambaForCausalLM"}

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


def mean_pooling(model_output, attention_mask):
    """
    Mean pooling to get sentence embeddings. See:
    https://huggingface.co/sentence-transformers/paraphrase-distilroberta-base-v1
    """
    token_embeddings = model_output[0]
    input_mask_expanded = attention_mask.unsqueeze(-1).expand(token_embeddings.size()).float()
    sum_embeddings = torch.sum(token_embeddings * input_mask_expanded, 1) # Sum columns
    sum_mask = torch.clamp(input_mask_expanded.sum(1), min=1e-9)
    return sum_embeddings / sum_mask

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer for the backend service.

    This class implements the gRPC methods for the backend service, including Health, LoadModel, and Embedding.
    """
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        model_name = request.Model

        # Check to see if the Model exists in the filesystem already.
        if os.path.exists(request.ModelFile):
            model_name = request.ModelFile

        compute = torch.float16
        if request.F16Memory == True:
            compute=torch.bfloat16

        self.CUDA = torch.cuda.is_available()
        self.OV=False
        self.GenericTTS=False
        self.SentenceTransformer = False
        self.processor = None

        device_map="cpu"
        mps_available = hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
        if mps_available:
            device_map = "mps"
        quantization = None
        autoTokenizer = True

        # Parse options from request.Options
        self.options = {}
        options = request.Options

        # The options are a list of strings in this form optname:optvalue
        # We are storing all the options in a dict so we can use it later when generating
        # Example options: ["max_new_tokens:3072", "guidance_scale:3.0", "temperature:1.8", "top_p:0.90", "top_k:45"]
        for opt in options:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            # if value is a number, convert it to the appropriate type
            try:
                if "." in value:
                    value = float(value)
                else:
                    value = int(value)
            except ValueError:
                # Keep as string if conversion fails
                pass
            self.options[key] = value

        print(f"Parsed options: {self.options}", file=sys.stderr)

        if self.CUDA:
            from transformers import BitsAndBytesConfig
            if request.MainGPU:
                device_map=request.MainGPU
            else:
                device_map="cuda:0"
            if request.Quantization == "bnb_4bit":
                quantization = BitsAndBytesConfig(
                    load_in_4bit = True,
                    bnb_4bit_compute_dtype = compute,
                    bnb_4bit_quant_type = "nf4",
                    bnb_4bit_use_double_quant = True,
                    load_in_8bit = False,
                )
            elif request.Quantization == "bnb_8bit":
                quantization = BitsAndBytesConfig(
                    load_in_4bit=False,
                    bnb_4bit_compute_dtype = None,
                    load_in_8bit=True,
                )

        try:
            if XPU and request.Type == "AutoModelForCausalLM":
                import intel_extension_for_pytorch as ipex
                from intel_extension_for_transformers.transformers.modeling import AutoModelForCausalLM

                device_map="xpu"
                compute=torch.float16
                if request.Quantization == "xpu_4bit":
                    xpu_4bit = True
                    xpu_8bit = False
                elif request.Quantization == "xpu_8bit":
                    xpu_4bit = False
                    xpu_8bit = True
                else:
                    xpu_4bit = False
                    xpu_8bit = False
                self.model = AutoModelForCausalLM.from_pretrained(model_name,
                                                                  trust_remote_code=request.TrustRemoteCode,
                                                                  device_map=device_map,
                                                                  load_in_4bit=xpu_4bit,
                                                                  load_in_8bit=xpu_8bit,
                                                                  torch_dtype=compute)
            elif request.Type == "OVModelForCausalLM":
                from optimum.intel.openvino import OVModelForCausalLM
                from openvino.runtime import Core

                if request.MainGPU:
                    device_map=request.MainGPU
                else:
                    device_map="AUTO"
                    devices = Core().available_devices
                    if "GPU" in " ".join(devices):
                        device_map="AUTO:GPU"
                if "CPU" or "NPU" in device_map:
                    if "-CPU" or "-NPU" not in device_map:
                        ovconfig={"PERFORMANCE_HINT": "CUMULATIVE_THROUGHPUT"}
                else:
                    ovconfig={"PERFORMANCE_HINT": "CUMULATIVE_THROUGHPUT","GPU_DISABLE_WINOGRAD_CONVOLUTION": "YES"}
                self.model = OVModelForCausalLM.from_pretrained(model_name,
                                                                compile=True,
                                                                trust_remote_code=request.TrustRemoteCode,
                                                                ov_config=ovconfig,
                                                                device=device_map)
                self.OV = True
            elif request.Type == "OVModelForFeatureExtraction":
                from optimum.intel.openvino import OVModelForFeatureExtraction
                from openvino.runtime import Core

                if request.MainGPU:
                    device_map=request.MainGPU
                else:
                    device_map="AUTO"
                    devices = Core().available_devices
                    if "GPU" in " ".join(devices):
                        device_map="AUTO:GPU"
                if "CPU" or "NPU" in device_map:
                    if "-CPU" or "-NPU" not in device_map:
                        ovconfig={"PERFORMANCE_HINT": "CUMULATIVE_THROUGHPUT"}
                else:
                    ovconfig={"PERFORMANCE_HINT": "CUMULATIVE_THROUGHPUT","GPU_DISABLE_WINOGRAD_CONVOLUTION": "YES"}
                self.model = OVModelForFeatureExtraction.from_pretrained(model_name,
                                                                compile=True,
                                                                trust_remote_code=request.TrustRemoteCode,
                                                                ov_config=ovconfig,
                                                                export=True,
                                                                device=device_map)
                self.OV = True
            elif request.Type == "SentenceTransformer":
                autoTokenizer = False
                self.model = SentenceTransformer(model_name, trust_remote_code=request.TrustRemoteCode)
                self.SentenceTransformer = True
            else:
                # Generic: dynamically resolve model class from transformers
                model_type = TYPE_ALIASES.get(request.Type, request.Type)
                ModelClass = AutoModel  # default
                if model_type and hasattr(transformers_module, model_type):
                    ModelClass = getattr(transformers_module, model_type)
                    print(f"Using model class: {model_type}", file=sys.stderr)
                else:
                    print(f"Using default AutoModel (type={request.Type!r})", file=sys.stderr)

                self.model = ModelClass.from_pretrained(
                    model_name,
                    trust_remote_code=request.TrustRemoteCode,
                    quantization_config=quantization,
                    device_map=device_map,
                    torch_dtype=compute,
                )

                # Try to load a processor (needed for TTS/audio models)
                try:
                    self.processor = AutoProcessor.from_pretrained(
                        model_name,
                        trust_remote_code=request.TrustRemoteCode,
                    )
                    self.GenericTTS = True
                    print(f"Loaded processor for {model_name}", file=sys.stderr)
                except Exception:
                    self.processor = None

            if request.ContextSize > 0:
                self.max_tokens = request.ContextSize
            elif hasattr(self.model, 'config') and hasattr(self.model.config, 'max_position_embeddings'):
                self.max_tokens = self.model.config.max_position_embeddings
            else:
                self.max_tokens = self.options.get("max_new_tokens", 512)

            if autoTokenizer:
                self.tokenizer = AutoTokenizer.from_pretrained(model_name)
                self.XPU = False

                if XPU and self.OV == False:
                    self.XPU = True
                    try:
                        print("Optimizing model", model_name, "to XPU.", file=sys.stderr)
                        self.model = ipex.optimize_transformers(self.model, inplace=True, dtype=torch.float16, device="xpu")
                    except Exception as err:
                        print("Not using XPU:", err, file=sys.stderr)

        except Exception as err:
            print("Error:", err, file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Embedding(self, request, context):
        set_seed(request.Seed)
        # Tokenize input
        max_length = 512
        if request.Tokens != 0:
            max_length = request.Tokens

        embeds = None

        if self.SentenceTransformer:
            print("Calculated embeddings for: " + request.Embeddings, file=sys.stderr)
            embeds = self.model.encode(request.Embeddings)
        else:
            encoded_input = self.tokenizer(request.Embeddings, padding=True, truncation=True, max_length=max_length, return_tensors="pt")

            # Create word embeddings
            if self.CUDA:
                encoded_input = encoded_input.to("cuda")

            with torch.no_grad():
                model_output = self.model(**encoded_input)

            # Pool to get sentence embeddings; i.e. generate one 1024 vector for the entire sentence
            sentence_embeddings = mean_pooling(model_output, encoded_input['attention_mask'])
            embeds = sentence_embeddings[0]
        return backend_pb2.EmbeddingResult(embeddings=embeds)

    async def _predict(self, request, context, streaming=False):
        set_seed(request.Seed)
        if request.TopP < 0 or request.TopP > 1:
            request.TopP = 1

        if request.TopK <= 0:
            request.TopK = 50

        if request.Temperature > 0 :
            sample=True
        else:
            sample=False
            request.TopP == None
            request.TopK == None
            request.Temperature == None

        prompt = request.Prompt
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            prompt = self.tokenizer.apply_chat_template(request.Messages, tokenize=False, add_generation_prompt=True)

        inputs = self.tokenizer(prompt, return_tensors="pt")

        if request.Tokens > 0:
            max_tokens = request.Tokens
        else:
            max_tokens = self.max_tokens - inputs["input_ids"].size()[inputs["input_ids"].dim()-1]

        if self.CUDA:
            inputs = inputs.to("cuda")
        if XPU and self.OV == False:
            inputs = inputs.to("xpu")
            streaming = False

        criteria=[]
        if request.StopPrompts:
            criteria = StoppingCriteriaList(
                [
                    StopStringCriteria(tokenizer=self.tokenizer, stop_strings=request.StopPrompts),
                ]
            )

        if streaming:
            streamer=TextIteratorStreamer(self.tokenizer,
                                        skip_prompt=True,
                                        skip_special_tokens=True)
            config=dict(inputs,
                        max_new_tokens=max_tokens,
                        temperature=request.Temperature,
                        top_p=request.TopP,
                        top_k=request.TopK,
                        do_sample=sample,
                        attention_mask=inputs["attention_mask"],
                        eos_token_id=self.tokenizer.eos_token_id,
                        pad_token_id=self.tokenizer.eos_token_id,
                        streamer=streamer,
                        stopping_criteria=criteria,
                        use_cache=True,
                        )
            thread=Thread(target=self.model.generate, kwargs=config)
            thread.start()
            generated_text = ""
            try:
                for new_text in streamer:
                    generated_text += new_text
                    yield backend_pb2.Reply(message=bytes(new_text, encoding='utf-8'))
            finally:
                thread.join()
        else:
            if XPU and self.OV == False:
                outputs = self.model.generate(inputs["input_ids"],
                                    max_new_tokens=max_tokens,
                                    temperature=request.Temperature,
                                    top_p=request.TopP,
                                    top_k=request.TopK,
                                    do_sample=sample,
                                    pad_token=self.tokenizer.eos_token_id)
            else:
                outputs = self.model.generate(**inputs,
                        max_new_tokens=max_tokens,
                        temperature=request.Temperature,
                        top_p=request.TopP,
                        top_k=request.TopK,
                        do_sample=sample,
                        eos_token_id=self.tokenizer.eos_token_id,
                        pad_token_id=self.tokenizer.eos_token_id,
                        stopping_criteria=criteria,
                        use_cache=True,
                        )
            generated_text = self.tokenizer.batch_decode(outputs[:, inputs["input_ids"].shape[1]:], skip_special_tokens=True)[0]

        if streaming:
            return

        yield backend_pb2.Reply(message=bytes(generated_text, encoding='utf-8'))

    async def Predict(self, request, context):
        gen = self._predict(request, context, streaming=False)
        res = await gen.__anext__()
        return res

    async def PredictStream(self, request, context):
        iterations = self._predict(request, context, streaming=True)
        try:
            async for iteration in iterations:
                yield iteration
        finally:
            await iterations.aclose()

    def SoundGeneration(self, request, context):
        model_name = request.model
        try:
            if self.processor is None:
                if model_name == "":
                    return backend_pb2.Result(success=False, message="request.model is required")
                self.processor = AutoProcessor.from_pretrained(model_name)
            if self.model is None:
                if model_name == "":
                    return backend_pb2.Result(success=False, message="request.model is required")
                # Dynamically resolve model class if configured, otherwise default to MusicgenForConditionalGeneration
                model_type = self.options.get("model_type", "MusicgenForConditionalGeneration")
                ModelClass = getattr(transformers_module, model_type)
                self.model = ModelClass.from_pretrained(model_name)
            inputs = None
            if request.text == "":
                inputs = self.model.get_unconditional_inputs(num_samples=1)
            elif request.HasField('src'):
                sample_rate, wsamples = wavfile.read('path_to_your_file.wav')

                if request.HasField('src_divisor'):
                    wsamples = wsamples[: len(wsamples) // request.src_divisor]

                inputs = self.processor(
                    audio=wsamples,
                    sampling_rate=sample_rate,
                    text=[request.text],
                    padding=True,
                    return_tensors="pt",
                )
            else:
                inputs = self.processor(
                    text=[request.text],
                    padding=True,
                    return_tensors="pt",
                )

            if request.HasField('duration'):
                tokens = int(request.duration * 51.2) # 256 tokens = 5 seconds, therefore 51.2 tokens is one second
            guidance = self.options.get("guidance_scale", 3.0)
            if request.HasField('temperature'):
                guidance = request.temperature
            dosample = self.options.get("do_sample", True)
            if request.HasField('sample'):
                dosample = request.sample
            audio_values = self.model.generate(**inputs, do_sample=dosample, guidance_scale=guidance, max_new_tokens=self.max_tokens)
            print("[transformers] SoundGeneration generated!", file=sys.stderr)

            # Save audio output
            if hasattr(self.processor, 'save_audio'):
                if hasattr(self.processor, 'batch_decode'):
                    try:
                        audio_values = self.processor.batch_decode(audio_values)
                    except Exception:
                        pass
                self.processor.save_audio(audio_values, request.dst)
            else:
                sampling_rate = self.model.config.audio_encoder.sampling_rate
                wavfile.write(request.dst, rate=sampling_rate, data=audio_values[0, 0].numpy())

            print("[transformers] SoundGeneration saved to", request.dst, file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

    def TTS(self, request, context):
        try:
            text = request.text
            print(f"[transformers] TTS generating for text: {text[:100]}...", file=sys.stderr)

            # Build inputs based on processor capabilities
            if request.voice and os.path.exists(request.voice):
                # Voice cloning: use chat template with reference audio
                chat_template = [{
                    "role": "0",
                    "content": [
                        {"type": "text", "text": text},
                        {"type": "audio", "path": request.voice},
                    ],
                }]
                inputs = self.processor.apply_chat_template(
                    chat_template, tokenize=True, return_dict=True,
                ).to(self.model.device, self.model.dtype)
            elif hasattr(self.processor, 'apply_chat_template'):
                # Models that use chat template format (VibeVoice, CSM, etc.)
                chat_template = [{"role": "0", "content": [{"type": "text", "text": text}]}]
                try:
                    inputs = self.processor.apply_chat_template(
                        chat_template, tokenize=True, return_dict=True,
                    ).to(self.model.device, self.model.dtype)
                except Exception:
                    # Fallback if chat template fails (not all processors support it)
                    inputs = self.processor(text=[text], padding=True, return_tensors="pt")
                    if self.CUDA:
                        inputs = inputs.to("cuda")
            else:
                # Direct processor call (Musicgen, etc.)
                inputs = self.processor(text=[text], padding=True, return_tensors="pt")
                if self.CUDA:
                    inputs = inputs.to("cuda")

            # Build generation kwargs from self.options
            gen_kwargs = {**inputs, "max_new_tokens": self.max_tokens}
            for key in ["guidance_scale", "temperature", "top_p", "top_k", "do_sample"]:
                if key in self.options:
                    gen_kwargs[key] = self.options[key]

            # Add noise scheduler if configured (e.g., for VibeVoice)
            noise_scheduler_type = self.options.get("noise_scheduler", None)
            if noise_scheduler_type:
                import diffusers
                SchedulerClass = getattr(diffusers, noise_scheduler_type)
                scheduler_kwargs = {}
                for key in ["beta_schedule", "prediction_type"]:
                    if key in self.options:
                        scheduler_kwargs[key] = self.options[key]
                gen_kwargs["noise_scheduler"] = SchedulerClass(**scheduler_kwargs)

            # Generate audio
            audio = self.model.generate(**gen_kwargs)
            print("[transformers] TTS generated!", file=sys.stderr)

            # Save audio output
            if hasattr(self.processor, 'save_audio'):
                if hasattr(self.processor, 'batch_decode'):
                    try:
                        audio = self.processor.batch_decode(audio)
                    except Exception:
                        pass
                self.processor.save_audio(audio, request.dst)
            else:
                sampling_rate = self.model.config.audio_encoder.sampling_rate
                wavfile.write(request.dst, rate=sampling_rate, data=audio[0, 0].numpy())

            print("[transformers] TTS saved to", request.dst, file=sys.stderr)

        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

async def serve(address):
    # Start asyncio gRPC server
    server = grpc.aio.server(migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_send_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),  # 50MB
        ])
    # Add the servicer to the server
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    # Bind the server to the address
    server.add_insecure_port(address)

    # Gracefully shutdown the server on SIGTERM or SIGINT
    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(
            sig, lambda: asyncio.ensure_future(server.stop(5))
        )

    # Start the server
    await server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)
    # Wait for the server to be terminated
    await server.wait_for_termination()

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()

    asyncio.run(serve(args.addr))

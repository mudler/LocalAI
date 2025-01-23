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
from transformers import AutoTokenizer, AutoModel, set_seed, TextIteratorStreamer, StoppingCriteriaList, StopStringCriteria, MambaConfig, MambaForCausalLM
from transformers import AutoProcessor, MusicgenForConditionalGeneration
from scipy.io import wavfile
import outetts
from sentence_transformers import SentenceTransformer


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
        """
        A gRPC method that returns the health status of the backend service.

        Args:
            request: A HealthRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Reply object that contains the health status of the backend service.
        """
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        """
        A gRPC method that loads a model into memory.

        Args:
            request: A LoadModelRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Result object that contains the result of the LoadModel operation.
        """

        model_name = request.Model
        
        # Check to see if the Model exists in the filesystem already.
        if os.path.exists(request.ModelFile):
            model_name = request.ModelFile

        compute = torch.float16
        if request.F16Memory == True:
            compute=torch.bfloat16

        self.CUDA = torch.cuda.is_available()
        self.OV=False
        self.OuteTTS=False
        self.SentenceTransformer = False

        device_map="cpu"

        quantization = None
        autoTokenizer = True

        if self.CUDA:
            from transformers import BitsAndBytesConfig, AutoModelForCausalLM
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
            if request.Type == "AutoModelForCausalLM":
                if XPU:
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
                                                                      use_safetensors=True,
                                                                      device_map=device_map, 
                                                                      load_in_4bit=xpu_4bit, 
                                                                      load_in_8bit=xpu_8bit, 
                                                                      torch_dtype=compute)
                else:
                    self.model = AutoModelForCausalLM.from_pretrained(model_name, 
                                                                      trust_remote_code=request.TrustRemoteCode, 
                                                                      use_safetensors=True, 
                                                                      quantization_config=quantization, 
                                                                      device_map=device_map, 
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
                # While working on a fine tuned model, inference may give an inaccuracy and performance drop on GPU if winograd convolutions are selected. 
                # https://docs.openvino.ai/2024/openvino-workflow/running-inference/inference-devices-and-modes/gpu-device.html
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
                # While working on a fine tuned model, inference may give an inaccuracy and performance drop on GPU if winograd convolutions are selected. 
                # https://docs.openvino.ai/2024/openvino-workflow/running-inference/inference-devices-and-modes/gpu-device.html
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
            elif request.Type == "MusicgenForConditionalGeneration":
                autoTokenizer = False
                self.processor = AutoProcessor.from_pretrained(model_name)
                self.model = MusicgenForConditionalGeneration.from_pretrained(model_name)
            elif request.Type == "OuteTTS":
                autoTokenizer = False
                options = request.Options
                MODELNAME = "OuteAI/OuteTTS-0.3-1B"
                TOKENIZER = "OuteAI/OuteTTS-0.3-1B"
                VERSION = "0.3"
                SPEAKER = "en_male_1"
                for opt in options:
                    if opt.startswith("tokenizer:"):
                        TOKENIZER = opt.split(":")[1]
                        break
                    if opt.startswith("version:"):
                        VERSION = opt.split(":")[1]
                        break
                    if opt.startswith("speaker:"):
                        SPEAKER = opt.split(":")[1]
                        break
                
                if model_name != "":
                    MODELNAME = model_name

                # Configure the model
                model_config = outetts.HFModelConfig_v2(
                    model_path=MODELNAME,
                    tokenizer_path=TOKENIZER
                )
                # Initialize the interface
                self.interface = outetts.InterfaceHF(model_version=VERSION, cfg=model_config)
                self.OuteTTS = True

                self.interface.print_default_speakers()
                if request.AudioPath:
                    if os.path.isabs(request.AudioPath):
                        self.AudioPath = request.AudioPath
                    else:
                        self.AudioPath = os.path.join(request.ModelPath, request.AudioPath)
                    self.speaker = self.interface.create_speaker(audio_path=self.AudioPath)
                else:
                    self.speaker = self.interface.load_default_speaker(name=SPEAKER)               
            elif request.Type == "SentenceTransformer":
                autoTokenizer = False
                self.model = SentenceTransformer(model_name, trust_remote_code=request.TrustRemoteCode)
                self.SentenceTransformer = True
            elif request.Type == "Mamba":
                autoTokenizer = False
                self.tokenizer = AutoTokenizer.from_pretrained(model_name)
                self.model = MambaForCausalLM.from_pretrained(model_name)
            else:
                print("Automodel", file=sys.stderr)
                self.model = AutoModel.from_pretrained(model_name, 
                                                       trust_remote_code=request.TrustRemoteCode,  
                                                       use_safetensors=True,  
                                                       quantization_config=quantization, 
                                                       device_map=device_map, 
                                                       torch_dtype=compute)
            if request.ContextSize > 0:
                self.max_tokens = request.ContextSize
            elif hasattr(self.model, 'config') and hasattr(self.model.config, 'max_position_embeddings'):
                self.max_tokens = self.model.config.max_position_embeddings
            else:
                self.max_tokens = 512
 
            if autoTokenizer:
                self.tokenizer = AutoTokenizer.from_pretrained(model_name, use_safetensors=True)
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
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Embedding(self, request, context):
        """
        A gRPC method that calculates embeddings for a given sentence.

        Args:
            request: An EmbeddingRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            An EmbeddingResult object that contains the calculated embeddings.
        """

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
        """
        Generates text based on the given prompt and sampling parameters.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        gen = self._predict(request, context, streaming=False)
        res = await gen.__anext__()
        return res

    async def PredictStream(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters, and streams the results.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The predict stream result.
        """
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
                self.model = MusicgenForConditionalGeneration.from_pretrained(model_name)
            inputs = None
            if request.text == "":
                inputs = self.model.get_unconditional_inputs(num_samples=1)
            elif request.HasField('src'):
                # TODO SECURITY CODE GOES HERE LOL
                # WHO KNOWS IF THIS WORKS???
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
            
            tokens = 256
            if request.HasField('duration'):
                tokens = int(request.duration * 51.2) # 256 tokens = 5 seconds, therefore 51.2 tokens is one second
            guidance = 3.0
            if request.HasField('temperature'):
                guidance = request.temperature
            dosample = True
            if request.HasField('sample'):
                dosample = request.sample
            audio_values = self.model.generate(**inputs, do_sample=dosample, guidance_scale=guidance, max_new_tokens=tokens)
            print("[transformers-musicgen] SoundGeneration generated!", file=sys.stderr)
            sampling_rate = self.model.config.audio_encoder.sampling_rate
            wavfile.write(request.dst, rate=sampling_rate, data=audio_values[0, 0].numpy())
            print("[transformers-musicgen] SoundGeneration saved to", request.dst, file=sys.stderr)
            print("[transformers-musicgen] SoundGeneration for", file=sys.stderr)
            print("[transformers-musicgen] SoundGeneration requested tokens", tokens, file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

    def OuteTTS(self, request, context):
        try:
            print("[OuteTTS] generating TTS", file=sys.stderr)
            gen_cfg = outetts.GenerationConfig(
                text="Speech synthesis is the artificial production of human speech.",
                temperature=0.1,
                repetition_penalty=1.1,
                max_length=self.max_tokens,
                speaker=self.speaker,
                # voice_characteristics="upbeat enthusiasm, friendliness, clarity, professionalism, and trustworthiness"
            )
            output = self.interface.generate(config=gen_cfg)
            print("[OuteTTS] Generated TTS", file=sys.stderr)
            output.save(request.dst)
            print("[OuteTTS] TTS done", file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

# The TTS endpoint is older, and provides fewer features, but exists for compatibility reasons
    def TTS(self, request, context):
        if self.OuteTTS:
            return self.OuteTTS(request, context)

        model_name = request.model
        try:
            if self.processor is None:
                if model_name == "":
                    return backend_pb2.Result(success=False, message="request.model is required")
                self.processor = AutoProcessor.from_pretrained(model_name)
            if self.model is None:
                if model_name == "":
                    return backend_pb2.Result(success=False, message="request.model is required")
                self.model = MusicgenForConditionalGeneration.from_pretrained(model_name)
            inputs = self.processor(
                text=[request.text],
                padding=True,
                return_tensors="pt",
            )
            tokens = self.max_tokens # No good place to set the "length" in TTS, so use 10s as a sane default
            audio_values = self.model.generate(**inputs, max_new_tokens=tokens)
            print("[transformers-musicgen] TTS generated!", file=sys.stderr)
            sampling_rate = self.model.config.audio_encoder.sampling_rate
            wavfile.write(request.dst, rate=sampling_rate, data=audio_values[0, 0].numpy())
            print("[transformers-musicgen] TTS saved to", request.dst, file=sys.stderr)
            print("[transformers-musicgen] TTS for", file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

async def serve(address):
    # Start asyncio gRPC server
    server = grpc.aio.server(migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
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

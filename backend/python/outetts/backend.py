#!/usr/bin/env python3
"""
gRPC server for OuteTTS (OuteAI TTS) models.
"""
from concurrent import futures

import argparse
import signal
import sys
import os
import asyncio

import backend_pb2
import backend_pb2_grpc

import grpc
import outetts

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        model_name = request.Model
        if os.path.exists(request.ModelFile):
            model_name = request.ModelFile

        self.options = {}
        for opt in request.Options:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            try:
                if "." in value:
                    value = float(value)
                else:
                    value = int(value)
            except ValueError:
                pass
            self.options[key] = value

        MODELNAME = "OuteAI/OuteTTS-0.3-1B"
        TOKENIZER = "OuteAI/OuteTTS-0.3-1B"
        VERSION = "0.3"
        SPEAKER = "en_male_1"
        for opt in request.Options:
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

        try:
            model_config = outetts.HFModelConfig_v2(
                model_path=MODELNAME,
                tokenizer_path=TOKENIZER
            )
            self.interface = outetts.InterfaceHF(model_version=VERSION, cfg=model_config)

            self.interface.print_default_speakers()
            if request.AudioPath:
                if os.path.isabs(request.AudioPath):
                    self.AudioPath = request.AudioPath
                else:
                    self.AudioPath = os.path.join(request.ModelPath, request.AudioPath)
                self.speaker = self.interface.create_speaker(audio_path=self.AudioPath)
            else:
                self.speaker = self.interface.load_default_speaker(name=SPEAKER)

            if request.ContextSize > 0:
                self.max_tokens = request.ContextSize
            else:
                self.max_tokens = self.options.get("max_new_tokens", 512)

        except Exception as err:
            print("Error:", err, file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        try:
            text = request.text if request.text else "Speech synthesis is the artificial production of human speech."
            print("[OuteTTS] generating TTS", file=sys.stderr)
            gen_cfg = outetts.GenerationConfig(
                text=text,
                temperature=self.options.get("temperature", 0.1),
                repetition_penalty=self.options.get("repetition_penalty", 1.1),
                max_length=self.max_tokens,
                speaker=self.speaker,
            )
            output = self.interface.generate(config=gen_cfg)
            print("[OuteTTS] Generated TTS", file=sys.stderr)
            output.save(request.dst)
            print("[OuteTTS] TTS done", file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)


async def serve(address):
    server = grpc.aio.server(
        migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ])
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)

    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(
            sig, lambda: asyncio.ensure_future(server.stop(5))
        )

    await server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)
    await server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the OuteTTS gRPC server.")
    parser.add_argument("--addr", default="localhost:50051", help="The address to bind the server to.")
    args = parser.parse_args()
    asyncio.run(serve(args.addr))

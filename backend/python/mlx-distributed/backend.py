#!/usr/bin/env python3
"""
MLX Distributed Inference Backend for LocalAI.

Two startup modes:

1. Server mode (started by LocalAI automatically):
   run.sh --addr localhost:50051
   Distributed config comes from LoadModel options or env vars.

2. Worker mode (started by CLI for remote ranks):
   run.sh --worker --hostfile hosts.json --rank 1 --backend ring
   Enters a loop waiting for commands from rank 0.
"""
import asyncio
from concurrent import futures
import argparse
import json
import os
import signal
import sys
import tempfile

import grpc

import backend_pb2
import backend_pb2_grpc

MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


def mlx_distributed_init(rank, hostfile, backend="ring", coordinator=None):
    """Initialize MLX distributed runtime.

    Ring: MLX_HOSTFILE points to a JSON array of "ip:port" strings. Each rank
    binds to its own entry (hostfile[rank]) and connects to neighbors for the
    ring pipeline.

    JACCL: MLX_IBV_DEVICES points to a JSON 2D matrix of RDMA device names.
    MLX_JACCL_COORDINATOR is rank 0's ip:port where it runs a TCP service that
    helps all ranks establish RDMA connections.
    """
    import mlx.core as mx

    if backend == "ring":
        os.environ["MLX_HOSTFILE"] = hostfile
        os.environ["MLX_RANK"] = str(rank)
        os.environ["MLX_RING_VERBOSE"] = "1"
        return mx.distributed.init(backend="ring", strict=True)
    elif backend == "jaccl":
        os.environ["MLX_IBV_DEVICES"] = hostfile
        os.environ["MLX_RANK"] = str(rank)
        if coordinator:
            os.environ["MLX_JACCL_COORDINATOR"] = coordinator
        return mx.distributed.init(backend="jaccl", strict=True)
    else:
        raise ValueError(f"Unknown backend: {backend}")


def is_float(s):
    try:
        float(s)
        return True
    except ValueError:
        return False


def is_int(s):
    try:
        int(s)
        return True
    except ValueError:
        return False


def parse_options(options):
    """Parse key:value option strings into a dict."""
    result = {}
    for opt in options:
        if ":" not in opt:
            continue
        key, value = opt.split(":", 1)
        if is_float(value):
            value = float(value)
        elif is_int(value):
            value = int(value)
        elif value.lower() in ["true", "false"]:
            value = value.lower() == "true"
        result[key] = value
    return result


class BackendServicer(backend_pb2_grpc.BackendServicer):
    """gRPC servicer for distributed MLX inference (runs on rank 0).

    When started by LocalAI (server mode), distributed init happens at
    LoadModel time using config from model options or environment variables.
    """

    def __init__(self):
        self.group = None
        self.dist_backend = None
        self.model = None
        self.tokenizer = None
        self.coordinator = None
        self.options = {}

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    async def LoadModel(self, request, context):
        try:
            import mlx.core as mx
            from mlx_lm import load
            from coordinator import DistributedCoordinator, CMD_LOAD_MODEL
            from sharding import pipeline_auto_parallel

            print(f"[Rank 0] Loading model: {request.Model}", file=sys.stderr)

            self.options = parse_options(request.Options)

            # Get distributed config from model options, falling back to env vars.
            # If neither is set, run as single-node (no distributed).
            hostfile = self.options.get("hostfile", os.environ.get("MLX_DISTRIBUTED_HOSTFILE", ""))
            dist_backend = str(self.options.get("distributed_backend",
                               os.environ.get("MLX_DISTRIBUTED_BACKEND", "ring")))
            # JACCL coordinator: rank 0 reads from env (set by CLI --coordinator).
            # Not in model options — rank 0 is the coordinator, workers get
            # the address via their own --coordinator CLI flag.
            jaccl_coordinator = os.environ.get("MLX_JACCL_COORDINATOR", "")

            if hostfile:
                print(f"[Rank 0] Initializing distributed: backend={dist_backend}, hostfile={hostfile}", file=sys.stderr)
                self.dist_backend = dist_backend
                self.group = mlx_distributed_init(
                    rank=0,
                    hostfile=hostfile,
                    backend=dist_backend,
                    coordinator=jaccl_coordinator or None,
                )
                self.coordinator = DistributedCoordinator(self.group)
                self.coordinator.broadcast_command(CMD_LOAD_MODEL)
                self.coordinator.broadcast_model_name(request.Model)
            else:
                print("[Rank 0] No hostfile configured, running single-node", file=sys.stderr)

            tokenizer_config = {}
            if request.TrustRemoteCode or self.options.get("trust_remote_code", False):
                tokenizer_config["trust_remote_code"] = True

            if tokenizer_config:
                self.model, self.tokenizer = load(request.Model, tokenizer_config=tokenizer_config)
            else:
                self.model, self.tokenizer = load(request.Model)

            if self.group is not None:
                self.model = pipeline_auto_parallel(self.model, self.group)
                print(f"[Rank 0] Model loaded and sharded across {self.group.size()} ranks", file=sys.stderr)
            else:
                print("[Rank 0] Model loaded (single-node)", file=sys.stderr)

        except Exception as err:
            print(f"[Rank 0] Error loading model: {err}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Error loading model: {err}")

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    async def Predict(self, request, context):
        try:
            import mlx.core as mx
            from mlx_lm import stream_generate
            from mlx_lm.sample_utils import make_sampler
            from coordinator import CMD_GENERATE

            prompt_text = self._prepare_prompt(request)
            tokens = self.tokenizer.encode(prompt_text)
            if hasattr(tokens, 'tolist'):
                tokens = tokens.tolist()

            if self.coordinator:
                self.coordinator.broadcast_command(CMD_GENERATE, len(tokens))
                self.coordinator.broadcast_tokens(tokens)

            max_tokens, sampler_params = self._build_generation_params(request)

            if self.coordinator:
                gen_params = self.coordinator.broadcast_generation_params(
                    max_tokens=max_tokens,
                    temperature=sampler_params.get('temp', 0.6),
                    top_p=sampler_params.get('top_p', 1.0),
                )
                max_tokens = gen_params["max_tokens"]

            sampler = make_sampler(**sampler_params)

            generated = []
            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=tokens,
                max_tokens=max_tokens,
                sampler=sampler,
            ):
                generated.append(response.text)

            return backend_pb2.Reply(message=bytes(''.join(generated), encoding='utf-8'))

        except Exception as e:
            print(f"[Rank 0] Error in Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    async def PredictStream(self, request, context):
        try:
            import mlx.core as mx
            from mlx_lm import stream_generate
            from mlx_lm.sample_utils import make_sampler
            from coordinator import CMD_GENERATE

            prompt_text = self._prepare_prompt(request)
            tokens = self.tokenizer.encode(prompt_text)
            if hasattr(tokens, 'tolist'):
                tokens = tokens.tolist()

            if self.coordinator:
                self.coordinator.broadcast_command(CMD_GENERATE, len(tokens))
                self.coordinator.broadcast_tokens(tokens)

            max_tokens, sampler_params = self._build_generation_params(request, default_max_tokens=512)

            if self.coordinator:
                gen_params = self.coordinator.broadcast_generation_params(
                    max_tokens=max_tokens,
                    temperature=sampler_params.get('temp', 0.6),
                    top_p=sampler_params.get('top_p', 1.0),
                )
                max_tokens = gen_params["max_tokens"]

            sampler = make_sampler(**sampler_params)

            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=tokens,
                max_tokens=max_tokens,
                sampler=sampler,
            ):
                yield backend_pb2.Reply(message=bytes(response.text, encoding='utf-8'))

        except Exception as e:
            print(f"[Rank 0] Error in PredictStream: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Streaming failed: {str(e)}")
            yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    def _prepare_prompt(self, request):
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            messages = [{"role": msg.role, "content": msg.content} for msg in request.Messages]
            return self.tokenizer.apply_chat_template(
                messages, tokenize=False, add_generation_prompt=True
            )
        return request.Prompt

    def _build_generation_params(self, request, default_max_tokens=200):
        max_tokens = getattr(request, 'Tokens', default_max_tokens)
        if max_tokens == 0:
            max_tokens = default_max_tokens

        temp = getattr(request, 'Temperature', 0.0)
        if temp == 0.0:
            temp = 0.6

        top_p = getattr(request, 'TopP', 0.0)
        if top_p == 0.0:
            top_p = 1.0

        sampler_params = {
            'temp': temp,
            'top_p': top_p,
            'min_p': getattr(request, 'MinP', 0.0),
            'top_k': getattr(request, 'TopK', 0),
            'xtc_threshold': 0.0,
            'xtc_probability': 0.0,
        }

        seed = getattr(request, 'Seed', 0)
        if seed != 0:
            import mlx.core as mx
            mx.random.seed(seed)

        if hasattr(self, 'options'):
            if 'max_tokens' in self.options:
                max_tokens = self.options['max_tokens']
            option_mapping = {
                'temp': 'temp', 'temperature': 'temp',
                'top_p': 'top_p', 'min_p': 'min_p', 'top_k': 'top_k',
            }
            for opt_key, param_key in option_mapping.items():
                if opt_key in self.options:
                    sampler_params[param_key] = self.options[opt_key]

        xtc_special_tokens = []
        if hasattr(self.tokenizer, 'eos_token_id') and self.tokenizer.eos_token_id is not None:
            xtc_special_tokens = [self.tokenizer.eos_token_id]
        sampler_params['xtc_special_tokens'] = xtc_special_tokens

        return max_tokens, sampler_params


def run_worker(group):
    """Worker loop for ranks > 0.  Waits for commands from rank 0."""
    from mlx_lm import load, stream_generate
    from mlx_lm.sample_utils import make_sampler
    from coordinator import DistributedCoordinator, CMD_LOAD_MODEL, CMD_GENERATE, CMD_SHUTDOWN
    from sharding import pipeline_auto_parallel
    import mlx.core as mx

    coordinator = DistributedCoordinator(group)
    model = None
    tokenizer = None

    print(f"[Rank {group.rank()}] Worker started, waiting for commands...", file=sys.stderr)

    while True:
        cmd, payload_size = coordinator.wait_for_command()

        if cmd == CMD_LOAD_MODEL:
            model_name = coordinator.broadcast_model_name()
            print(f"[Rank {group.rank()}] Loading model: {model_name}", file=sys.stderr)
            model, tokenizer = load(model_name)
            model = pipeline_auto_parallel(model, group)
            print(f"[Rank {group.rank()}] Model loaded and sharded", file=sys.stderr)

        elif cmd == CMD_GENERATE:
            if model is None:
                print(f"[Rank {group.rank()}] No model loaded, skipping generate", file=sys.stderr)
                continue

            token_count = coordinator.broadcast_token_count(payload_size)
            tokens_array = coordinator.broadcast_tokens([0] * token_count)
            tokens = tokens_array.tolist()

            gen_params = coordinator.broadcast_generation_params()

            sampler = make_sampler(
                temp=gen_params["temperature"],
                top_p=gen_params["top_p"],
            )

            for _ in stream_generate(
                model, tokenizer,
                prompt=tokens,
                max_tokens=gen_params["max_tokens"],
                sampler=sampler,
            ):
                pass

        elif cmd == CMD_SHUTDOWN:
            print(f"[Rank {group.rank()}] Shutting down", file=sys.stderr)
            break


async def serve(address):
    server = grpc.aio.server(
        migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ],
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)

    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, lambda: asyncio.ensure_future(server.stop(5)))

    await server.start()
    print(f"[Rank 0] gRPC server listening on {address}", file=sys.stderr)
    await server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="MLX Distributed Backend")
    parser.add_argument("--addr", default="localhost:50051",
                        help="gRPC listen address (used by LocalAI to send requests)")
    parser.add_argument("--worker", action="store_true",
                        help="Run in worker mode (for remote ranks started by CLI)")
    parser.add_argument("--backend", default="ring", choices=["ring", "jaccl"],
                        help="ring = TCP pipeline parallelism, jaccl = RDMA tensor parallelism")
    parser.add_argument("--hostfile", default=None,
                        help="Path to hostfile JSON (required for --worker mode)")
    parser.add_argument("--rank", type=int, default=0,
                        help="Rank of this process (0 = server, >0 = worker)")
    parser.add_argument("--coordinator", default=None,
                        help="JACCL coordinator ip:port (jaccl backend only)")
    args = parser.parse_args()

    if args.worker:
        if not args.hostfile:
            print("Error: --hostfile is required in worker mode", file=sys.stderr)
            sys.exit(1)
        group = mlx_distributed_init(args.rank, args.hostfile, args.backend, args.coordinator)
        run_worker(group)
    else:
        # Server mode: started by LocalAI with just --addr.
        # Distributed init deferred to LoadModel (reads config from model options/env vars).
        asyncio.run(serve(args.addr))

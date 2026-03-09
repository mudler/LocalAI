"""
Distributed coordination using MLX distributed primitives.

Rank 0 broadcasts commands and tokens to all ranks via all_sum/all_gather.
Worker ranks wait in a loop for commands from rank 0.
"""
import json
import struct

import mlx.core as mx


CMD_IDLE = 0
CMD_GENERATE = 1
CMD_LOAD_MODEL = 2
CMD_SHUTDOWN = -1


class DistributedCoordinator:
    def __init__(self, group):
        self.group = group
        self.rank = group.rank()
        self.world_size = group.size()

    def broadcast_command(self, cmd, payload_size=0):
        """Rank 0 broadcasts a command to all ranks.

        Uses all_sum with only rank 0 providing non-zero values so every
        rank receives the same command array.
        """
        if self.rank == 0:
            cmd_array = mx.array([cmd, payload_size], dtype=mx.int32)
        else:
            cmd_array = mx.zeros((2,), dtype=mx.int32)
        result = mx.distributed.all_sum(cmd_array, group=self.group)
        mx.eval(result)
        return int(result[0].item()), int(result[1].item())

    def broadcast_tokens(self, tokens):
        """Broadcast input token ids from rank 0 to all ranks.

        Rank 0 provides the real token array; other ranks provide zeros of the
        same shape.  ``all_sum`` ensures every rank ends up with identical data.
        """
        if self.rank == 0:
            token_array = mx.array(tokens, dtype=mx.int32)
        else:
            token_array = mx.zeros((len(tokens),), dtype=mx.int32)
        result = mx.distributed.all_sum(token_array, group=self.group)
        mx.eval(result)
        return result

    def broadcast_token_count(self, count):
        """Broadcast the number of tokens so workers can prepare a buffer."""
        if self.rank == 0:
            count_array = mx.array([count], dtype=mx.int32)
        else:
            count_array = mx.zeros((1,), dtype=mx.int32)
        result = mx.distributed.all_sum(count_array, group=self.group)
        mx.eval(result)
        return int(result[0].item())

    def broadcast_generation_params(self, max_tokens=200, temperature=0.6, top_p=1.0):
        """Broadcast generation parameters from rank 0."""
        if self.rank == 0:
            params = mx.array([max_tokens, temperature, top_p], dtype=mx.float32)
        else:
            params = mx.zeros((3,), dtype=mx.float32)
        result = mx.distributed.all_sum(params, group=self.group)
        mx.eval(result)
        return {
            "max_tokens": int(result[0].item()),
            "temperature": float(result[1].item()),
            "top_p": float(result[2].item()),
        }

    def wait_for_command(self):
        """Worker ranks block here until rank 0 broadcasts a command."""
        return self.broadcast_command(CMD_IDLE, 0)

    def broadcast_model_name(self, model_name=""):
        """Broadcast model name string from rank 0 to all ranks.

        Encodes the model name as int32 codepoints so it can travel via
        all_sum.
        """
        if self.rank == 0:
            encoded = [ord(c) for c in model_name]
            # First broadcast the length
            length = self.broadcast_token_count(len(encoded))
            if length > 0:
                name_array = mx.array(encoded, dtype=mx.int32)
                result = mx.distributed.all_sum(name_array, group=self.group)
                mx.eval(result)
                return model_name
            return ""
        else:
            length = self.broadcast_token_count(0)
            if length > 0:
                name_array = mx.zeros((length,), dtype=mx.int32)
                result = mx.distributed.all_sum(name_array, group=self.group)
                mx.eval(result)
                return "".join(chr(int(c.item())) for c in result)
            return ""

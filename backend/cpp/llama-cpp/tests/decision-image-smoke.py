# SPDX-License-Identifier: MIT
"""Direct RPC only; public API/router end-to-end tests are a separate gate."""
import argparse
import json
import math
import grpc
import backend_pb2 as pb
import backend_pb2_grpc as rpc

p = argparse.ArgumentParser()
p.add_argument('--address', default='127.0.0.1:50061')
p.add_argument('--model', required=True)
p.add_argument('--projector', required=True)
p.add_argument('--fixtures', required=True)
a = p.parse_args()
f = json.load(open(a.fixtures))
channel = grpc.insecure_channel(a.address, options=[('grpc.max_send_message_length', 20 << 20)])
grpc.channel_ready_future(channel).result(timeout=20)
s = rpc.BackendStub(channel)

def load(projector):
    r = s.LoadModel(pb.ModelOptions(ModelFile=a.model, MMProj=projector,
        ContextSize=8192, NBatch=512, Threads=4, NGPULayers=0,
        Options=['parallel:1']), timeout=600)
    assert r.success, r
    print('LOAD', 'vision' if projector else 'no projector', 'PASS', flush=True)

def body(image):
    return {'state': {}, 'images': [image], 'questions': {'color': {
        'type': 'choice', 'instructions': 'What is the dominant color of the image?',
        'criteria': {'red': None, 'blue': None}}}}

def score(b):
    return s.Score(pb.ScoreRequest(question_type='systemone', prompt=json.dumps(b)), timeout=600)

def reject(b, code):
    try:
        score(b)
        raise AssertionError('request unexpectedly accepted')
    except grpc.RpcError as e:
        assert e.code() == code, (e.code(), e.details())
        print('REJECT', code.name, e.details(), flush=True)

load('')
reject(body(f['red']), grpc.StatusCode.UNIMPLEMENTED)
for k in ['dimension', 'pixels']:
    reject(body(f[k]), grpc.StatusCode.RESOURCE_EXHAUSTED)
for k in ['bomb', 'truncated', 'bad_crc']:
    reject(body(f[k]), grpc.StatusCode.INVALID_ARGUMENT)
reject(body('data:image/png;base64,AB=='), grpc.StatusCode.INVALID_ARGUMENT)
reject(body('https://example.invalid/a.png'), grpc.StatusCode.INVALID_ARGUMENT)
reject({'state': 'x' * (64 << 10)}, grpc.StatusCode.RESOURCE_EXHAUSTED)
reject({'state': 'x' * (16 << 20)}, grpc.StatusCode.RESOURCE_EXHAUSTED)
load(a.projector)
results = {}
for color in ['red', 'blue']:
    r = json.loads(score(body(f[color])).response_json)
    print('IMAGE', color, json.dumps(r), flush=True)
    assert r['usage']['input_tokens'] > 0 and r['usage']['output_tokens'] == 0, r
    probs = r['answers']['color']['probabilities']
    assert all(math.isfinite(v) for v in probs.values()) and abs(sum(probs.values())-1)<1e-4, r
    results[color] = probs
assert results['red']['red'] > results['blue']['red'], results
assert results['blue']['blue'] > results['red']['blue'], results
print('CONTRASTING IMAGE EXECUTION PASS', flush=True)

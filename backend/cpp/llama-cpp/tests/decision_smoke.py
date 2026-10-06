# SPDX-License-Identifier: MIT
# Requires generated backend_pb2{,_grpc}.py on PYTHONPATH and a running backend.
import argparse
import json, grpc, os, concurrent.futures
import tempfile
from pathlib import Path
parser = argparse.ArgumentParser()
parser.add_argument('--address', default='127.0.0.1:50051')
parser.add_argument('--model', required=True)
args = parser.parse_args()
import backend_pb2 as pb
import backend_pb2_grpc as rpc
channel=grpc.insecure_channel(args.address)
grpc.channel_ready_future(channel).result(timeout=10)
s=rpc.BackendStub(channel)
r=s.LoadModel(pb.ModelOptions(ModelFile=os.path.abspath(args.model),ContextSize=1024,NBatch=512,Threads=2,NGPULayers=0,Options=['parallel:2']),timeout=120)
assert r.success,r
print('LOAD PASS',flush=True)
body={'model':'tinylaya','state':'I was charged twice for my order last week and nobody has replied.','questions':{
'route':{'type':'choice','instructions':'Which team should handle this?','criteria':{'billing':'payments and refunds','shipping':None,'technical':None}},
'urgency':{'type':'score','instructions':'How urgent is this?','criteria':['can wait','this week','today','right now']},
'angry':{'type':'noul','instructions':'Is the customer angry?'}}}
def run():
 r=json.loads(s.Score(pb.ScoreRequest(question_type='systemone',prompt=json.dumps(body)),timeout=60).response_json)
 assert r['usage']['input_tokens']>0 and r['usage']['output_tokens']==0,r
 a=r['answers']; assert set(a)==set(body['questions']),r
 assert 0<=a['angry']['noul']<=1,r
 for k in ['route','urgency']: assert abs(sum(a[k]['probabilities'].values())-1)<1e-4,r
 assert a['route']['choice']==max(a['route']['probabilities'],key=a['route']['probabilities'].get),r
 assert abs(a['urgency']['score']-sum(int(k)*v for k,v in a['urgency']['probabilities'].items()))<1e-4,r
 return r
print('MULTIQUESTION',json.dumps(run()),flush=True)
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool: list(pool.map(lambda _:run(),range(4)))
print('CONCURRENCY PASS',flush=True)
for payload in ['{',json.dumps({'state':'x','questions':{}})]:
 try:s.Score(pb.ScoreRequest(question_type='systemone',prompt=payload),timeout=30);raise AssertionError('expected invalid')
 except grpc.RpcError as e:assert e.code()==grpc.StatusCode.INVALID_ARGUMENT,e
print('INVALID PASS',flush=True)
f=s.Score.future(pb.ScoreRequest(question_type='systemone',prompt=json.dumps(body)),timeout=60);f.cancel()
try:f.result();raise AssertionError('expected cancelled')
except grpc.FutureCancelledError:pass
run();print('CANCEL AND RECOVERY PASS',flush=True)
try:s.Score(pb.ScoreRequest(prompt='hello',candidates=['world']),timeout=30);raise AssertionError('score unexpectedly enabled')
except grpc.RpcError as e:assert e.code()==grpc.StatusCode.FAILED_PRECONDITION,e
print('PLAIN SCORE DISABLED GUARD PASS',flush=True)

r=s.LoadModel(pb.ModelOptions(ModelFile=os.path.abspath(args.model),ContextSize=1024,NBatch=512,Threads=2,EnableScore=True,Options=['parallel:2']),timeout=120)
assert r.success,r
r=s.Score(pb.ScoreRequest(prompt='Hello',candidates=[' world',' there']),timeout=30)
assert len(r.candidates)==2 and all(c.num_tokens>0 for c in r.candidates),r
assert all(__import__('math').isfinite(c.log_prob) for c in r.candidates),r
print('PLAIN SCORE ENABLED PASS',flush=True)

# This fixture is an encoder: keep embeddings enabled when removing decision
# metadata, otherwise upstream warmup can exceed the one-slot output budget.
data = Path(args.model).read_bytes()
assert data.count(b'.decision.type') == 1, 'expected the tinylaya test fixture'
with tempfile.TemporaryDirectory() as directory:
 model = Path(directory) / 'no-decision.gguf'
 model.write_bytes(data.replace(b'.decision.type', b'.disabled.type'))
 r=s.LoadModel(pb.ModelOptions(ModelFile=str(model),ContextSize=1024,NBatch=512,Threads=2,Embeddings=True),timeout=120)
 assert r.success,r
 try:
  s.Score(pb.ScoreRequest(question_type='systemone',prompt=json.dumps(body)),timeout=30)
  raise AssertionError('missing decision metadata accepted')
 except grpc.RpcError as e:
  assert e.code()==grpc.StatusCode.UNIMPLEMENTED,e
 print('MISSING DECISION METADATA PASS',flush=True)

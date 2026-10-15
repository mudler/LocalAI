#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Native multimodal embedding probe against a pinned llama.cpp build.

Runs the fixed acceptance matrix of test/multimodal/acceptance-contract.json
through the pinned llama-server /v1/embeddings path with an approved model and
projector, then writes a machine-readable report. It never downloads or builds
anything and never searches for a substitute artifact. Any missing artifact,
runtime failure or unsupported operation yields a BLOCKED report and exit 2.

Environment (explicit approved paths):
  MM_MODEL, MM_PROJECTOR   GGUF model and matching projector
  MM_UPSTREAM_SOURCE       checkout of the pinned llama.cpp revision
  MM_BUILD_DIR             CMake build dir containing bin/llama-server
  MM_DECODER_DIR           directory containing ffmpeg and ffprobe
  MM_APPROVAL_RECORD       JSON approval and storage record from the root
  MM_PROBE_WORKDIR         scratch directory (default .sdd-work/task7)

Preprocessing, fixed before any retrieval score was computed
(version mm-v1-f4-tsms-a16k-mono):
  text   bare UTF-8, no prefix, no template
  image  fixture PNG bytes unchanged
  audio  ffmpeg -ac 1 -ar 16000 -c:a pcm_s16le WAV, complete first track
  video  "Video:\\n"; per selected frame "Timestamp: <ms> ms\\n" then the
         decoded frame as PNG; then "Audio track:\\n" and the audio when present.
         Sample times 0, 250, 500 ... ms while < stream duration; the first
         decoded frame at or after each time; a frame is never repeated;
         timestamps are elapsed ms from the first presentation timestamp.
"""
import base64
import hashlib
import json
import os
from fractions import Fraction
from pathlib import Path
import socket
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

HERE = Path(__file__).resolve().parent
BASE = HERE.parent
ROOT = BASE.parents[1]
sys.path.insert(0, str(BASE))
import check_native_reports as checker  # noqa: E402

PIN = checker.PIN
SERVER_ARGS = ['--embeddings', '-c', '16384', '-b', '16384', '-ub', '16384', '-np', '1', '--no-warmup']
POOLING = {0: 'none', 1: 'mean', 2: 'cls', 3: 'last', 4: 'rank'}


class Blocked(Exception):
    pass


def sha(data):
    return hashlib.sha256(data).hexdigest()


def file_sha(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def need_env(name):
    value = os.environ.get(name)
    if not value:
        raise Blocked('required environment variable %s is not set' % name)
    return value


def need_file(path, what):
    if not Path(path).is_file():
        raise Blocked('%s is missing' % what)
    return Path(path)


def run(cmd, **kw):
    result = subprocess.run(cmd, capture_output=True, **kw)
    if result.returncode != 0:
        raise Blocked('command failed: %s: %s' % (Path(cmd[0]).name, result.stderr.decode(errors='replace')[-300:]))
    return result.stdout


def round_half_up_ms(value):
    return int((value * 1000 + Fraction(1, 2)) // 1)


class Decoder:
    def __init__(self, directory, workdir):
        self.ffmpeg = str(need_file(Path(directory) / 'ffmpeg', 'ffmpeg'))
        self.ffprobe = str(need_file(Path(directory) / 'ffprobe', 'ffprobe'))
        self.workdir = workdir
        self.versions = {}
        for name, binary in (('ffmpeg', self.ffmpeg), ('ffprobe', self.ffprobe)):
            self.versions[name] = run([binary, '-version']).decode().splitlines()[0]

    def probe_streams(self, path):
        info = json.loads(run([self.ffprobe, '-v', 'error', '-show_streams', '-of', 'json', str(path)]))
        return info['streams']

    def video_frames(self, path):
        """Return (width, height, duration_ms, source_frames_ms, rgb frames in presentation order)."""
        streams = self.probe_streams(path)
        videos = [s for s in streams if s['codec_type'] == 'video']
        if len(videos) != 1:
            raise Blocked('expected exactly one video stream in %s' % Path(path).name)
        v = videos[0]
        tb = Fraction(v['time_base'])
        raw = json.loads(run([self.ffprobe, '-v', 'error', '-select_streams', 'v:0', '-show_entries', 'frame=best_effort_timestamp',
                              '-of', 'json', str(path)]))['frames']
        stamps = sorted(int(f['best_effort_timestamp']) for f in raw)
        first = stamps[0]
        source = [round_half_up_ms((s - first) * tb) for s in stamps]
        duration_ms = round_half_up_ms(Fraction(v['duration']))
        width, height = v['width'], v['height']
        data = run([self.ffmpeg, '-v', 'error', '-nostdin', '-i', str(path), '-map', '0:v:0', '-fps_mode', 'passthrough',
                    '-f', 'rawvideo', '-pix_fmt', 'rgb24', '-'])
        size = width * height * 3
        if len(data) != size * len(source):
            raise Blocked('decoded frame count differs from probed frame count in %s' % Path(path).name)
        frames = [data[i * size:(i + 1) * size] for i in range(len(source))]
        return width, height, duration_ms, source, frames

    def audio_tracks(self, path):
        return [s for s in self.probe_streams(path) if s['codec_type'] == 'audio']

    def normalized_wav(self, path, tag):
        """First audio track, complete, as 16 kHz mono signed 16-bit WAV."""
        out = Path(self.workdir) / ('norm-%s.wav' % tag)
        run([self.ffmpeg, '-v', 'error', '-nostdin', '-y', '-i', str(path), '-map', '0:a:0', '-vn', '-ac', '1', '-ar', '16000',
             '-c:a', 'pcm_s16le', '-f', 'wav', str(out)])
        data = out.read_bytes()
        samples = (len(data) - 44) // 2
        return data, samples


def png_bytes(width, height, rgb):
    from PIL import Image
    import io
    buffer = io.BytesIO()
    Image.frombytes('RGB', (width, height), rgb).save(buffer, format='PNG', compress_level=6)
    return buffer.getvalue()


def data_uri(mime, payload):
    return 'data:%s;base64,%s' % (mime, base64.b64encode(payload).decode())


class Request:
    """Ordered parts of one input item plus the evidence the checker needs."""

    def __init__(self, form='content'):
        self.parts = []      # evidence records
        self.api = []        # OpenAI content parts
        self.video = None
        self.form = form

    def text(self, value):
        self.parts.append({'kind': 'text', 'text': value, 'sha256': sha(value.encode())})
        self.api.append({'type': 'text', 'text': value})

    def image(self, fixture, payload, source_sha, frame_ms=None):
        record = {'kind': 'image', 'fixture': fixture, 'sha256': sha(payload)}
        if frame_ms is None:
            record['source_sha256'] = source_sha
        else:
            record['frame_ms'] = frame_ms
        self.parts.append(record)
        self.api.append({'type': 'image_url', 'image_url': {'url': data_uri('image/png', payload)}})

    def audio(self, fixture, wav, source_sha):
        self.parts.append({'kind': 'audio', 'fixture': fixture, 'source_sha256': source_sha, 'sha256': sha(wav)})
        self.api.append({'type': 'input_audio', 'input_audio': {'data': base64.b64encode(wav).decode(), 'format': 'wav'}})

    def body(self):
        if self.form == 'plain':
            return self.parts[0]['text']
        return {'content': self.api}


class Probe:
    def __init__(self, contract, manifest, workdir):
        self.contract, self.manifest, self.workdir = contract, manifest, workdir
        self.fixture_dir = BASE / 'fixtures'
        self.sha = {f['id']: f['sha256'] for f in manifest['fixtures']}
        self.paths = {f['id']: self.fixture_dir / f['path'] for f in manifest['fixtures']}
        self.semantic = {f['id']: f['semantic'] for f in manifest['fixtures'] if f['id'].startswith('text-')}
        self.decoder = None
        self.video_cache = {}
        self.audio_cache = {}

    def fixture_bytes(self, fid):
        data = self.paths[fid].read_bytes()
        if sha(data) != self.sha[fid]:
            raise Blocked('fixture %s differs from the manifest checksum' % fid)
        return data

    def add_fixture(self, req, fid):
        kind = fid.split('-')[0]
        if kind == 'text':
            req.text(self.fixture_bytes(fid).decode('utf-8'))
        elif kind == 'image':
            req.image(fid, self.fixture_bytes(fid), self.sha[fid])
        elif kind == 'audio':
            req.audio(fid, self.wav(fid), self.sha[fid])
        else:
            self.add_video(req, fid)

    def wav(self, fid):
        if fid not in self.audio_cache:
            self.fixture_bytes(fid)
            data, _ = self.decoder.normalized_wav(self.paths[fid], fid)
            self.audio_cache[fid] = data
        return self.audio_cache[fid]

    def add_video(self, req, fid):
        self.fixture_bytes(fid)
        if fid not in self.video_cache:
            width, height, duration, source, frames = self.decoder.video_frames(self.paths[fid])
            times, selected = checker.expected_selection(source, duration)
            chosen = []
            for ms in selected:
                chosen.append((ms, png_bytes(width, height, frames[source.index(ms)])))
            tracks = self.decoder.audio_tracks(self.paths[fid])
            if len(tracks) > 1:
                raise Blocked('video %s has more than one audio track' % fid)
            wav, samples = (self.decoder.normalized_wav(self.paths[fid], fid) if tracks else (None, 0))
            self.video_cache[fid] = (duration, source, times, chosen, wav, samples)
        duration, source, times, chosen, wav, samples = self.video_cache[fid]
        req.text('Video:\n')
        for ms, png in chosen:
            req.text('Timestamp: %d ms\n' % ms)
            req.image(None, png, None, frame_ms=ms)
        if wav is not None:
            req.text('Audio track:\n')
            req.audio(fid, wav, self.sha[fid])
        req.video = {'fixture': fid, 'source_sha256': self.sha[fid], 'duration_ms': duration, 'source_frames_ms': source,
                     'sample_times_ms': times, 'selected_frames_ms': [ms for ms, _ in chosen],
                     'frame_sha256': [sha(png) for _, png in chosen],
                     'audio_pcm_sha256': sha(wav) if wav is not None else None, 'audio_samples': samples}

    def build_requests(self):
        c, forms = self.contract, self.contract['input_forms']
        reqs = {}

        def make(key, fids=(), text_before=None, form='content', plain=None):
            req = Request(form)
            if plain is not None:
                req.text(plain)
            if text_before is not None:
                req.text(text_before)
            for fid in fids:
                self.add_fixture(req, fid)
            reqs[key] = req
            return req

        make('plain-text', form='plain', plain=forms['plain-text'])
        make('structured-text', plain=forms['structured-text'][0]['text'])
        for form in ('image', 'audio', 'silent-video', 'audiovisual-video'):
            make(form, forms[form])
        make('mixed-item', forms['mixed-item'])
        for fid in self.sha:
            make(fid, [fid], form='plain' if fid.startswith('text-') else 'content')
        mixed = forms['mixed-item']
        for name in ('image', 'audio', 'video'):
            make('mixed-without-' + name, [f for f in mixed if f.split('-')[0] != name])
        pairs = c['pairs']
        for pair, short in (('media-change', 'media-change'), ('soundtrack-change', 'soundtrack-change')):
            make(short + '-left', [pairs[pair]['left']], text_before=pairs[pair]['surrounding_text'])
            make(short + '-right', [pairs[pair]['right']], text_before=pairs[pair]['surrounding_text'])
        for q in c['queries']:
            if q['query_text'] is not None:
                make('query:' + q['id'], form='plain', plain=q['query_text'])
            else:
                make('query:' + q['id'], [q['query_fixture']])
        return reqs


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def http(url, payload=None, timeout=600):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data, {'Content-Type': 'application/json'} if data else {})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as exc:
        raise Blocked('server rejected a request: HTTP %d %s' % (exc.code, exc.read().decode(errors='replace')[:300]))
    except (urllib.error.URLError, OSError) as exc:
        raise Blocked('server request failed: %s' % exc)


class Server:
    def __init__(self, binary, model, projector, libdir, log):
        self.port = free_port()
        env = dict(os.environ, LD_LIBRARY_PATH=str(libdir))
        self.log = open(log, 'wb')
        self.proc = subprocess.Popen([str(binary), '-m', str(model), '--mmproj', str(projector), '--host', '127.0.0.1',
                                      '--port', str(self.port)] + SERVER_ARGS, stdout=self.log, stderr=subprocess.STDOUT, env=env)
        self.url = 'http://127.0.0.1:%d' % self.port

    def wait(self, seconds=600):
        deadline = time.time() + seconds
        while time.time() < deadline:
            if self.proc.poll() is not None:
                raise Blocked('server exited during startup with status %s' % self.proc.returncode)
            try:
                with urllib.request.urlopen(self.url + '/health', timeout=2) as resp:
                    if resp.status == 200:
                        return
            except (urllib.error.URLError, OSError):
                pass
            time.sleep(1)
        raise Blocked('server did not become ready')

    def stop(self):
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(30)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()
        self.log.close()


def read_gguf(source, path):
    sys.path.insert(0, str(Path(source) / 'gguf-py'))
    from gguf import GGUFReader
    reader = GGUFReader(str(path))
    values = {}
    for key, field in reader.fields.items():
        if key.startswith('tokenizer.ggml.tokens') or key.endswith('merges') or 'token_type' in key or 'scores' in key:
            continue
        try:
            values[key] = field.contents()
        except Exception:
            pass
    return values


def hardware():
    model = 'unknown'
    for line in Path('/proc/cpuinfo').read_text().splitlines():
        if line.startswith('model name'):
            model = line.split(':', 1)[1].strip()
            break
    mem = 0
    for line in Path('/proc/meminfo').read_text().splitlines():
        if line.startswith('MemTotal'):
            mem = int(line.split()[1]) * 1024
            break
    uname = os.uname()
    return {'cpu_model': model, 'cores': os.cpu_count(), 'ram_bytes': mem, 'os': '%s %s %s' % (uname.sysname, uname.release, uname.machine),
            'accelerator': 'none used (CPU build)'}


def vector_entry(values):
    raw = struct.pack('<%df' % len(values), *values)
    return {'dimensions': len(values), 'dtype': 'float32-le', 'sha256': sha(raw), 'base64': base64.b64encode(raw).decode()}


def embed(server, body):
    resp = http(server.url + '/v1/embeddings', {'model': 'probe', 'input': body, 'encoding_format': 'float'})
    data = resp.get('data')
    if not isinstance(data, list) or not data:
        raise Blocked('embedding response without data')
    return data, resp.get('usage', {})


def main():
    out = Path(os.environ.get('MM_PROBE_OUT', ROOT / '.sdd-work/task7/native-report.json'))
    workdir = Path(os.environ.get('MM_PROBE_WORKDIR', ROOT / '.sdd-work/task7'))
    workdir.mkdir(parents=True, exist_ok=True)
    contract = json.loads((BASE / 'acceptance-contract.json').read_text())
    manifest = json.loads((BASE / 'fixtures/manifest.json').read_text())
    started = time.time()
    server = None
    command = 'python3 test/multimodal/native_probe/probe.py'
    try:
        model = need_file(need_env('MM_MODEL'), 'MM_MODEL artifact')
        projector = need_file(need_env('MM_PROJECTOR'), 'MM_PROJECTOR artifact')
        source = Path(need_env('MM_UPSTREAM_SOURCE'))
        build = Path(need_env('MM_BUILD_DIR'))
        decoder_dir = need_env('MM_DECODER_DIR')
        approval = json.loads(need_file(need_env('MM_APPROVAL_RECORD'), 'approval record').read_text())
        for field in ('recorded_by', 'approved_actions', 'storage'):
            if not approval.get(field):
                raise Blocked('approval record lacks ' + field)
        head = run(['git', '-C', str(source), 'rev-parse', 'HEAD']).decode().strip()
        if head != PIN:
            raise Blocked('upstream checkout is %s, not the pinned revision' % head)
        binary = need_file(build / 'bin/llama-server', 'pinned llama-server binary')
        cache = (build / 'CMakeCache.txt').read_text() if (build / 'CMakeCache.txt').is_file() else ''
        if 'CMAKE_HOME_DIRECTORY:INTERNAL=' + str(source) not in cache:
            raise Blocked('build directory was not configured from the pinned source checkout')
        version = subprocess.run([str(binary), '--version'], capture_output=True, text=True, env=dict(os.environ, LD_LIBRARY_PATH=str(build / 'bin')))
        version_text = (version.stdout + version.stderr).strip().splitlines()
        version_line = next((l for l in version_text if l.startswith('version:')), '')
        if PIN[:7] not in version_line:
            raise Blocked('server binary does not report the pinned commit')
        decoder = Decoder(decoder_dir, workdir)
        probe = Probe(contract, manifest, workdir)
        probe.decoder = decoder
        model_sha, projector_sha = file_sha(model), file_sha(projector)
        meta = read_gguf(source, model)
        pmeta = read_gguf(source, projector)
        arch = meta['general.architecture']
        pooling = POOLING.get(int(meta.get(arch + '.pooling_type', -1)))
        if pooling is None:
            raise Blocked('pooling type absent from model metadata')
        reqs = probe.build_requests()
        server = Server(binary, model, projector, build / 'bin', workdir / 'server.log')
        server.wait()
        props = http(server.url + '/props')
        modalities = props.get('modalities', {})
        flags = {k: bool(modalities.get(k)) for k in ('vision', 'audio', 'video')}
        missing = [k for k, v in flags.items() if not v]
        if missing:
            raise Blocked('server reports missing modalities: ' + ', '.join(missing))
        items, dims = {}, set()
        for key, req in reqs.items():
            data, usage = embed(server, [req.body()] if req.form != 'plain' else [req.body()])
            if len(data) != 1:
                raise Blocked('item %s did not return exactly one vector' % key)
            values = [float(v) for v in data[0]['embedding']]
            dims.add(len(values))
            items[key] = {'parts': req.parts, 'tokens': int(usage.get('prompt_tokens', 0)), 'input_form': req.form,
                          'model_sha256': model_sha, 'projector_sha256': projector_sha, 'pooling': pooling,
                          'preprocessing_version': contract['preprocessing']['version'], 'vector': vector_entry(values)}
            if req.video is not None:
                items[key]['video'] = req.video
        if len(dims) != 1:
            raise Blocked('inconsistent embedding dimensions: %s' % sorted(dims))
        dimension = dims.pop()
        # Special tokens per item: usage of a bare text minus its raw token count.
        sample = probe.semantic['text-bell']
        raw_tokens = len(http(server.url + '/tokenize', {'content': sample, 'add_special': False})['tokens'])
        special = items['text-bell']['tokens'] - raw_tokens
        # Supplementary, non-gating: ordered mixed batch and repeatability.
        batch = contract['ordered_mixed_batch']
        order = [batch['items'][i] for i in batch['permutation']]
        data, usage = embed(server, [reqs[k].body() for k in order])
        batch_report = {'order': order, 'indexes': [d.get('index') for d in data], 'dimensions': [len(d['embedding']) for d in data],
                        'cosine_to_single': []}
        for k, d in zip(order, data):
            single = checker.decode_vector(items[k], dimension, k)
            batch_report['cosine_to_single'].append(checker.cosine(single, [float(v) for v in d['embedding']]))
        repeat = {}
        for a, b in (('image', 'image-bell'), ('audio', 'audio-bell'), ('audiovisual-video', 'video-bell'), ('plain-text', 'text-bell')):
            repeat[a + '~' + b] = items[a]['vector']['sha256'] == items[b]['vector']['sha256']
        server.stop()
        server_log_sha = file_sha(workdir / 'server.log')
        license_review = approval.get('license_review', {})
        report = {
            'schema_version': 1, 'kind': 'multimodal-native-proof-report', 'status': 'PASS',
            'contract_version': contract['contract_version'], 'blockers': [], 'failures': [],
            'command': command, 'exit_status': 0, 'duration_seconds': round(time.time() - started, 1),
            'identity': {'model_identity': approval.get('model_identity', 'unrecorded'), 'model_sha256': model_sha,
                         'projector_identity': approval.get('projector_identity', 'unrecorded'), 'projector_sha256': projector_sha,
                         'license': {'model_gguf_general.license': meta.get('general.license'),
                                     'projector_gguf_general.license': pmeta.get('general.license'), 'review': license_review},
                         'architecture': arch, 'projector_types': {k: pmeta.get(k) for k in ('clip.vision.projector_type', 'clip.audio.projector_type')},
                         'pooling': pooling, 'dimensions': dimension, 'normalization': 'server default (L2, embd_normalize=2)',
                         'preprocessing_version': contract['preprocessing']['version'], 'special_tokens_per_item': special},
            'source': {'native_revision': PIN, 'upstream_head': head, 'backend_binary_sha256': file_sha(binary),
                       'backend_version': version_line,
                       'libraries_sha256': {p.name: file_sha(p) for p in sorted((build / 'bin').glob('lib*.so.*')) if p.is_file() and not p.is_symlink()},
                       'build_type': next((l.split('=', 1)[1] for l in cache.splitlines() if l.startswith('CMAKE_BUILD_TYPE:')), 'unknown')},
            'decoder': {'versions': decoder.versions,
                        'config': {'role': 'probe-side only', 'audio_rate_hz': 16000, 'audio_channels': 1, 'audio_sample_format': 's16le',
                                   'audio_container': 'wav', 'video_frame_encoding': 'PNG rgb24 native resolution',
                                   'sampling_fps': 4, 'timestamp_syntax': 'Timestamp: <integer> ms\\n'}},
            'hardware': hardware(),
            'approval': approval,
            'server': {'modalities': flags, 'context': props.get('default_generation_settings', {}).get('n_ctx'),
                       'instance': str(uuid.uuid4()),
                       'arguments': ['llama-server', '-m', '$MM_MODEL', '--mmproj', '$MM_PROJECTOR'] + SERVER_ARGS,
                       'log_sha256': server_log_sha},
            'fixtures': dict(probe.sha), 'items': items, 'counts': {}, 'cases': [], 'similarities': {}, 'margins': {},
            'supplementary': {'ordered-mixed-batch': batch_report, 'repeatability_bit_identical': repeat},
        }
        result = checker.evaluate(report, contract, manifest)
        report['cases'] = [{'id': k, 'status': v['status'], 'detail': v['detail']} for k, v in result['cases'].items()]
        report['similarities'], report['margins'] = result['similarities'], result['margins']
        failed = sorted(c['id'] for c in report['cases'] if c['status'] != 'PASS')
        report['status'] = 'FAIL' if failed else 'PASS'
        report['failures'] = failed
        n = len(report['cases'])
        report['counts'] = {'expected': n, 'executed': n, 'passed': n - len(failed), 'failed': len(failed)}
        outcome = checker.validate_native_report(report, contract, manifest)
        out.write_text(json.dumps(report, separators=(',', ':')) + '\n')
        print('native proof outcome: %s (%d failed cases); report: %s' % (outcome, len(failed), out.name))
        return 0 if outcome == 'PASS' else 3
    except (Blocked, ValueError, KeyError, OSError, subprocess.SubprocessError) as exc:
        if server is not None:
            server.stop()
        report = {'schema_version': 1, 'kind': 'multimodal-native-proof-report', 'status': 'BLOCKED',
                  'contract_version': contract['contract_version'], 'blockers': [str(exc) or type(exc).__name__],
                  'command': command, 'exit_status': 2}
        out.write_text(json.dumps(report, indent=2) + '\n')
        print('native proof outcome: BLOCKED: %s' % exc, file=sys.stderr)
        return 2
    finally:
        if server is not None:
            server.stop()


if __name__ == '__main__':
    sys.exit(main())

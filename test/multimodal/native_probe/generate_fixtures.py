#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Deterministically synthesize the owned multimodal acceptance fixtures.

Everything is drawn or synthesized from code with fixed seeds: clip-art style
renders (PIL) and procedural audio (numpy). No third-party content is used.
The fixtures are created once from semantic intent. They must never be
redrawn or tuned after embedding scores have been seen.

Usage: generate_fixtures.py [--out DIR] [--manifest FILE] [--ffmpeg PATH]
"""
import argparse
import hashlib
import json
import subprocess
import wave
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw

SIZE = 224
SCALE = 4
FPS = 10
SECONDS = 2
FRAMES = FPS * SECONDS
RATE = 16000
HERE = Path(__file__).resolve().parent

SEMANTIC = {
    'bell': 'A hand rings a brass bell.',
    'drum': 'A hand strikes a small drum.',
    'water': 'Water pours from a jug into a glass.',
    'typing': 'Hands type on a computer keyboard.',
}
SKIN = (240, 187, 150)
SKIN_SHADE = (214, 150, 115)
BRASS = (222, 168, 40)
BRASS_DARK = (160, 110, 20)
BG = (236, 242, 248)


def canvas():
    img = Image.new('RGB', (SIZE * SCALE, SIZE * SCALE), BG)
    return img, ImageDraw.Draw(img)


def S(*values):
    return [int(round(v * SCALE)) for v in values]


def finish(img):
    return img.resize((SIZE, SIZE), Image.LANCZOS)


def hand(d, cx, cy, w=46, h=34, angle_dx=0):
    # Simple clip-art hand: palm block with four finger bumps and a thumb.
    d.rounded_rectangle(S(cx - w / 2, cy - h / 2, cx + w / 2, cy + h / 2), radius=SCALE * 10, fill=SKIN, outline=SKIN_SHADE, width=SCALE * 2)
    for i in range(4):
        fx = cx - w / 2 + 6 + i * (w - 12) / 3
        d.rounded_rectangle(S(fx - 5, cy - h / 2 - 14, fx + 5, cy - h / 2 + 8), radius=SCALE * 5, fill=SKIN, outline=SKIN_SHADE, width=SCALE * 2)
    d.ellipse(S(cx - w / 2 - 12, cy - 4, cx - w / 2 + 8, cy + 16), fill=SKIN, outline=SKIN_SHADE, width=SCALE * 2)


def draw_bell(t):
    img, d = canvas()
    swing = np.sin(2 * np.pi * t * 2) * 14
    # Hand holding a loop of cord at the top.
    hand(d, 112, 36)
    cx = 112 + swing
    d.line(S(112, 52, cx, 78), fill=(90, 60, 30), width=SCALE * 4)
    # Bell body: dome plus flared rim.
    d.pieslice(S(cx - 52, 74, cx + 52, 190), 180, 360, fill=BRASS, outline=BRASS_DARK, width=SCALE * 3)
    d.rectangle(S(cx - 52, 132, cx + 52, 146), fill=BRASS, outline=BRASS_DARK, width=SCALE * 3)
    d.polygon(S(cx - 52, 132, cx - 70, 164, cx + 70, 164, cx + 52, 132), fill=BRASS, outline=BRASS_DARK, width=SCALE * 3)
    d.ellipse(S(cx - 70, 156, cx + 70, 172), fill=BRASS_DARK)
    d.ellipse(S(cx - 12, 168, cx + 12, 192), fill=(110, 80, 40))
    d.arc(S(cx - 90, 100, cx - 70, 150), 120, 240, fill=(120, 120, 140), width=SCALE * 3)
    d.arc(S(cx + 70, 100, cx + 90, 150), 300, 60, fill=(120, 120, 140), width=SCALE * 3)
    return finish(img)


def draw_drum(t):
    img, d = canvas()
    # Small drum: cylinder with red sides, white head, zigzag tension cords.
    d.rectangle(S(52, 120, 172, 180), fill=(190, 40, 50), outline=(110, 20, 30), width=SCALE * 3)
    d.ellipse(S(52, 168, 172, 192), fill=(190, 40, 50), outline=(110, 20, 30), width=SCALE * 3)
    d.ellipse(S(52, 100, 172, 140), fill=(250, 250, 245), outline=(110, 20, 30), width=SCALE * 3)
    for i in range(6):
        x = 60 + i * 20
        d.line(S(x, 134, x + 10, 176), fill=(230, 200, 150), width=SCALE * 2)
        d.line(S(x + 10, 134, x, 176), fill=(230, 200, 150), width=SCALE * 2)
    # Hand holds a stick that rises and falls onto the drum head.
    lift = (0.5 + 0.5 * np.cos(2 * np.pi * t * 4)) * 46
    tipy = 100 - lift
    hand(d, 150, tipy - 28, w=40, h=30)
    d.line(S(150, tipy - 14, 100, tipy + 8), fill=(180, 130, 70), width=SCALE * 6)
    d.ellipse(S(94, tipy + 2, 108, tipy + 16), fill=(120, 80, 40))
    return finish(img)


def draw_water(t):
    img, d = canvas()
    # Glass on the right, filling over time.
    level = 0.15 + 0.7 * t
    gx0, gx1, gy0, gy1 = 130, 190, 112, 200
    top = gy1 - (gy1 - gy0) * level
    d.rectangle(S(gx0 + 2, top, gx1 - 2, gy1 - 3), fill=(70, 150, 230))
    d.polygon(S(gx0, gy0, gx0 + 6, gy1, gx1 - 6, gy1, gx1, gy0), outline=(90, 110, 130), width=SCALE * 3)
    # Jug on the left, tilted, with a stream falling into the glass.
    d.polygon(S(28, 40, 96, 40, 112, 110, 44, 124), fill=(190, 215, 240), outline=(90, 110, 130), width=SCALE * 3)
    d.polygon(S(28, 40, 96, 40, 100, 62, 32, 62), fill=(70, 150, 230))
    d.arc(S(10, 52, 44, 100), 90, 270, fill=(90, 110, 130), width=SCALE * 4)
    d.polygon(S(100, 104, 118, 104, 124, 118, 112, 118), fill=(190, 215, 240), outline=(90, 110, 130), width=SCALE * 2)
    d.rectangle(S(114, 116, 122, top), fill=(70, 150, 230))
    for i in range(3):
        y = 120 + ((t * 300 + i * 25) % 70)
        d.ellipse(S(112, y, 124, y + 8), fill=(160, 205, 250))
    return finish(img)


def draw_typing(t):
    img, d = canvas()
    # Laptop screen on top, keyboard below, two hands over the keys.
    d.rounded_rectangle(S(40, 14, 184, 90), radius=SCALE * 6, fill=(40, 48, 60), outline=(20, 24, 30), width=SCALE * 3)
    d.rectangle(S(50, 24, 174, 80), fill=(170, 205, 240))
    for i in range(4):
        d.line(S(58, 34 + i * 12, 58 + 30 + (i * 23) % 70, 34 + i * 12), fill=(60, 80, 110), width=SCALE * 3)
    d.polygon(S(28, 150, 196, 150, 210, 206, 14, 206), fill=(70, 76, 88), outline=(30, 34, 40), width=SCALE * 3)
    pressed = int(t * 18) % 12
    for r in range(3):
        for c in range(10):
            x = 34 + c * 15.6 + r * 4
            y = 156 + r * 16
            on = (r * 10 + c) % 12 == pressed
            d.rounded_rectangle(S(x, y, x + 13, y + 12), radius=SCALE * 2, fill=(255, 220, 120) if on else (225, 228, 235))
    d.rounded_rectangle(S(60, 190, 164, 202), radius=SCALE * 2, fill=(225, 228, 235))
    # Hands hover and tap alternately.
    tap = np.sin(2 * np.pi * t * 5)
    hand(d, 78, 128 + 6 * max(tap, 0), w=48, h=30)
    hand(d, 148, 128 + 6 * max(-tap, 0), w=48, h=30)
    return finish(img)


DRAW = {'bell': draw_bell, 'drum': draw_drum, 'water': draw_water, 'typing': draw_typing}
# Still image: one fixed instant of the same animation, chosen up front.
STILL_T = 0.35


def envelope_decay(n, rate):
    return np.exp(-np.arange(n) / (rate * 1.0))


def synth_bell(rng):
    n = RATE * SECONDS
    out = np.zeros(n)
    ratios = [1.0, 2.0, 2.76, 5.4, 8.93, 11.34]
    amps = [1.0, 0.6, 0.5, 0.3, 0.2, 0.1]
    decays = [0.9, 0.7, 0.5, 0.3, 0.2, 0.12]
    for start in (0.0, 0.95):
        s = int(start * RATE)
        m = n - s
        tt = np.arange(m) / RATE
        for r, a, dec in zip(ratios, amps, decays):
            ph = rng.uniform(0, 2 * np.pi)
            out[s:] += a * np.sin(2 * np.pi * 520.0 * r * tt + ph) * np.exp(-tt / dec) * (1 - np.exp(-tt / 0.002))
    return out


def synth_drum(rng):
    n = RATE * SECONDS
    out = np.zeros(n)
    for start in (0.0, 0.5, 1.0, 1.5):
        s = int(start * RATE)
        m = min(int(0.4 * RATE), n - s)
        tt = np.arange(m) / RATE
        freq = 60 + 130 * np.exp(-tt / 0.04)
        phase = 2 * np.pi * np.cumsum(freq) / RATE
        thump = np.sin(phase) * np.exp(-tt / 0.09)
        noise = rng.standard_normal(m) * np.exp(-tt / 0.02) * 0.4
        out[s:s + m] += thump + noise
    return out


def bandpass(x, lo, hi):
    spec = np.fft.rfft(x)
    f = np.fft.rfftfreq(len(x), 1 / RATE)
    spec[(f < lo) | (f > hi)] = 0
    return np.fft.irfft(spec, len(x))


def synth_water(rng):
    n = RATE * SECONDS
    base = bandpass(rng.standard_normal(n), 300, 3000)
    tt = np.arange(n) / RATE
    base *= 0.35 + 0.25 * np.sin(2 * np.pi * 3 * tt) ** 2
    out = base / np.max(np.abs(base))
    for _ in range(40):
        s = rng.integers(0, n - int(0.08 * RATE))
        m = int(rng.uniform(0.02, 0.06) * RATE)
        t2 = np.arange(m) / RATE
        f0 = rng.uniform(400, 900)
        f = f0 * (1 + 2.0 * t2 / t2[-1])
        out[s:s + m] += 0.6 * np.sin(2 * np.pi * np.cumsum(f) / RATE) * np.exp(-t2 / 0.015)
    return out


def synth_typing(rng):
    n = RATE * SECONDS
    out = np.zeros(n)
    pos = 0.05
    while pos < SECONDS - 0.05:
        s = int(pos * RATE)
        m = int(0.03 * RATE)
        tt = np.arange(m) / RATE
        click = bandpass(rng.standard_normal(m), 1500, 6000) * np.exp(-tt / 0.004)
        thock = np.sin(2 * np.pi * rng.uniform(150, 250) * tt) * np.exp(-tt / 0.01) * 0.6
        out[s:s + m] += (click * 2.0 + thock) * rng.uniform(0.6, 1.0)
        pos += rng.uniform(0.08, 0.22)
    return out


SYNTH = {'bell': synth_bell, 'drum': synth_drum, 'water': synth_water, 'typing': synth_typing}
SEEDS = {'bell': 11, 'drum': 22, 'water': 33, 'typing': 44}


def write_wav(path, samples):
    peak = np.max(np.abs(samples))
    pcm = np.int16(np.clip(samples / peak * 0.8, -1, 1) * 32767)
    with wave.open(str(path), 'wb') as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(pcm.tobytes())


def run(cmd, data=None):
    subprocess.run(cmd, input=data, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def encode_video(ffmpeg, name, out, wav):
    frames = b''.join(np.asarray(DRAW[name](i / (FRAMES - 1))).tobytes() for i in range(FRAMES))
    cmd = [ffmpeg, '-y', '-nostdin', '-f', 'rawvideo', '-pix_fmt', 'rgb24', '-s', f'{SIZE}x{SIZE}', '-r', str(FPS), '-i', 'pipe:0',
           '-i', str(wav), '-map', '0:v:0', '-map', '1:a:0', '-c:v', 'libx264', '-preset', 'veryslow', '-crf', '23',
           '-pix_fmt', 'yuv420p', '-threads', '1', '-c:a', 'aac', '-b:a', '64k', '-ac', '1', '-t', str(SECONDS),
           '-fflags', '+bitexact', '-flags:v', '+bitexact', '-flags:a', '+bitexact', '-map_metadata', '-1',
           '-movflags', '+faststart', str(out)]
    run(cmd, frames)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', type=Path, default=HERE.parent / 'fixtures')
    parser.add_argument('--manifest', type=Path, default=HERE.parent / 'fixtures/manifest.json')
    parser.add_argument('--ffmpeg', default='ffmpeg')
    args = parser.parse_args()
    out = args.out
    out.mkdir(parents=True, exist_ok=True)
    for name in SEMANTIC:
        (out / f'text-{name}.txt').write_text(SEMANTIC[name], encoding='utf-8')
        DRAW[name](STILL_T).save(out / f'image-{name}.png', optimize=False, compress_level=9)
        write_wav(out / f'audio-{name}.wav', SYNTH[name](np.random.default_rng(SEEDS[name])))
        encode_video(args.ffmpeg, name, out / f'video-{name}.mp4', out / f'audio-{name}.wav')
    # Same encoded video stream as video-bell: no soundtrack, and a different one.
    base = ['-fflags', '+bitexact', '-flags:v', '+bitexact', '-flags:a', '+bitexact', '-map_metadata', '-1', '-movflags', '+faststart']
    run([args.ffmpeg, '-y', '-nostdin', '-i', str(out / 'video-bell.mp4'), '-map', '0:v:0', '-c:v', 'copy', '-an'] + base + [str(out / 'video-bell-silent.mp4')])
    run([args.ffmpeg, '-y', '-nostdin', '-i', str(out / 'video-bell.mp4'), '-i', str(out / 'audio-drum.wav'),
         '-map', '0:v:0', '-map', '1:a:0', '-c:v', 'copy', '-c:a', 'aac', '-b:a', '64k', '-ac', '1', '-t', str(SECONDS)] + base +
        [str(out / 'video-bell-drum-track.mp4')])
    manifest = json.loads(args.manifest.read_text())
    for fixture in manifest['fixtures']:
        data = (out / fixture['path']).read_bytes()
        fixture['sha256'] = hashlib.sha256(data).hexdigest()
        fixture['status'] = 'materialized'
        fixture['license'] = 'MIT (owned fixture generated by test/multimodal/native_probe/generate_fixtures.py; no third-party content)'
        fixture['ownership'] = 'LocalAI contributor-created; deterministic procedural render/synthesis'
    manifest['status'] = 'materialized'
    manifest['materialization_policy'] = ('Fixtures are produced once by test/multimodal/native_probe/generate_fixtures.py from semantic '
                                          'intent and are never redrawn or tuned after embedding scores. Checksums are of the committed bytes.')
    args.manifest.write_text(json.dumps(manifest, indent=2) + '\n')


if __name__ == '__main__':
    main()

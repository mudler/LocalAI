---
name: assemble-media
description: Use to join, trim, mix, subtitle or inspect generated media, make frames and masks for the next generation, or make a simple video from pictures, text or a chart, with ffmpeg, sox, ImageMagick or Pillow.
sandbox: media
---

# Assemble generated media

Generate the pieces with the LocalAI tools, then rework them with the shell tools of the media sandbox: `ffmpeg`, `ffprobe`, `sox`, `magick` and Python with Pillow. There is no network. Work in the folder where the pieces are.

## Find the files

A generation tool returns a `ref`, a path such as `/workspace/home/generated/clip-1a2b3c4d.mp4`. Use that exact path; do not guess names. List what exists with `ls -lt /workspace/home/generated | head`. Write results next to the inputs, with new names (never overwrite a piece), then give the user the output path. A file in that folder can be passed to a tool as an argument (`start_image`, `image`, `mask`, `audio`).

## Loop

1. Generate one piece. Do not generate all pieces before checking the first.
2. Inspect it: `ffprobe` for the facts, one frame grab for the picture (below). Look at the PNG.
3. If it is wrong (size, length, no audio, bad frame), say so and fix the prompt or the command. Do not retry in a cycle.
4. Assemble with the recipes below, then inspect the result the same way.

## Inspect

```sh
cd /workspace/home/generated
ffprobe -v error -show_entries format=duration:stream=index,codec_type,codec_name,width,height,r_frame_rate,sample_rate,channels -of default=nw=1 a.mp4
ffmpeg -y -v error -ss 1 -i a.mp4 -frames:v 1 a-frame.png
ffmpeg -hide_banner -nostats -i voice.wav -af volumedetect -f null - 2>&1 | grep -E 'mean_volume|max_volume'
```

Look at a frame PNG with the file tools, or describe it with `describe_image`.

## Join clips

Same codec, size and frame rate (clips of one model and settings), no re-encode:

```sh
printf "file '%s'\n" a.mp4 a.mp4 > list.txt
ffmpeg -y -v error -f concat -safe 0 -i list.txt -c copy joined.mp4
```

Different size, frame rate, codec, or some clips without audio: write this script once, then run `sh /tmp/vconcat.sh out.mp4 1280 720 24 a.mp4 b.mp4`. It scales each clip to fit with black bars, adds silence to clips with no audio, and joins them.

```sh
cat > /tmp/vconcat.sh <<'EOF2'
#!/bin/sh
# usage: vconcat.sh out.mp4 WIDTH HEIGHT FPS clip...
set -e
out=$1; w=$2; h=$3; fps=$4; shift 4
vf="scale=$w:$h:force_original_aspect_ratio=decrease,pad=$w:$h:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=$fps,format=yuv420p"
enc="-c:v libx264 -preset veryfast -crf 20 -c:a aac -ar 48000 -ac 2"
dir=$(mktemp -d); : > "$dir/list.txt"; i=0
for f in "$@"; do
  i=$((i+1)); n="$dir/$i.mp4"
  if [ -n "$(ffprobe -v error -select_streams a -show_entries stream=index -of csv=p=0 "$f")" ]; then
    ffmpeg -y -v error -i "$f" -vf "$vf" $enc "$n"
  else
    ffmpeg -y -v error -i "$f" -f lavfi -i anullsrc=r=48000:cl=stereo -map 0:v -map 1:a -shortest -vf "$vf" $enc "$n"
  fi
  echo "file '$n'" >> "$dir/list.txt"
done
ffmpeg -y -v error -f concat -safe 0 -i "$dir/list.txt" -c copy "$out"
rm -rf "$dir"
EOF2
sh /tmp/vconcat.sh joined2.mp4 1280 720 24 a.mp4 b.mp4
```

## Audio track

```sh
# replace or add the audio of a video; the result ends with the shorter input
ffmpeg -y -v error -i a.mp4 -i music.wav -map 0:v -map 1:a -c:v copy -c:a aac -shortest withaudio.mp4
# audio shorter than the video: pad it with silence to the video length
ffmpeg -y -v error -i a.mp4 -i voice.wav -map 0:v -map 1:a -c:v copy -c:a aac -af apad -shortest withvoice.mp4
```

Voice over music: the music ducks under the voice. To start the voice later, write `[1:a]adelay=1500|1500,apad,...` (milliseconds). `apad` keeps the music playing after the voice ends.

```sh
ffmpeg -y -v error -i music.wav -i voice.wav -filter_complex \
 "[1:a]apad,asplit=2[v][sc];[0:a][sc]sidechaincompress=threshold=0.03:ratio=8:attack=20:release=400[d];[d][v]amix=inputs=2:duration=first:normalize=0[a]" \
 -map "[a]" mix-raw.wav
```

Normalise loudness to -16 LUFS in two passes. A single `loudnorm` pass levels quiet parts up and undoes the ducking. Save this script once, then run `sh /tmp/lnorm.sh mix-raw.wav mix.wav`:

```sh
cat > /tmp/lnorm.sh <<'EOF2'
#!/bin/sh
# usage: lnorm.sh in out   (audio or video with audio in; wav out)
set -e
t="I=-16:TP=-1.5:LRA=11"
m=$(ffmpeg -hide_banner -nostats -i "$1" -af "loudnorm=$t:print_format=json" -f null - 2>&1)
g() { echo "$m" | grep "\"$1\"" | sed 's/.*: "\(.*\)".*/\1/'; }
ffmpeg -y -v error -i "$1" -af "loudnorm=$t:measured_I=$(g input_i):measured_TP=$(g input_tp):measured_LRA=$(g input_lra):measured_thresh=$(g input_thresh):offset=$(g target_offset):linear=true" -ar 44100 "$2"
EOF2
sh /tmp/lnorm.sh mix-raw.wav mix.wav
```

Put the mix on a video with the first command of this section. `sox` is shorter for plain audio work:

```sh
sox voice.wav music.wav both.wav          # one after the other
sox voice.wav fast.wav tempo 1.25         # faster, same pitch
sox voice.wav quiet.wav norm -3           # peak at -3 dB
```

Convert: `ffmpeg -y -v error -i voice.wav -c:a libmp3lame -q:a 2 voice.mp3`, or `-c:a libopus voice.opus`, or `-ar 16000 -ac 1 voice16k.wav` for speech models.

## Trim, fade, speed

```sh
# seconds 1 to 4, with 0.5 s fades in and out (times in the fades are relative to the cut)
ffmpeg -y -v error -ss 1 -to 4 -i a.mp4 -vf "fade=t=in:d=0.5,fade=t=out:st=2.5:d=0.5" -af "afade=t=in:d=0.5,afade=t=out:st=2.5:d=0.5" -c:v libx264 -preset veryfast -crf 20 cut.mp4
# 1.5x faster
ffmpeg -y -v error -i a.mp4 -vf setpts=PTS/1.5 -af atempo=1.5 fast.mp4
```

## Resize and crop

```sh
ffmpeg -y -v error -i a.mp4 -vf "scale=640:-2" -c:v libx264 -preset veryfast small.mp4      # keep the ratio, even height
ffmpeg -y -v error -i a.mp4 -vf "crop=ih*9/16:ih,scale=360:640" -c:v libx264 -preset veryfast vertical.mp4   # centre crop to 9:16
magick photo.png -resize 512x512^ -gravity center -extent 512x512 square.png                # image: fill and centre crop
```

## Contact sheet, GIF, still to clip

```sh
ffmpeg -y -v error -i a.mp4 -vf "fps=1,scale=160:-1,tile=5x1" -frames:v 1 sheet.png
ffmpeg -y -v error -i a.mp4 -vf "fps=8,scale=240:-1:flags=lanczos,split[a][b];[a]palettegen[p];[b][p]paletteuse" a.gif
ffmpeg -y -v error -loop 1 -i photo.png -t 3 -vf "fps=24,format=yuv420p" -c:v libx264 still.mp4
```

Look at `sheet.png` before you assemble.

## Subtitles

Write an SRT file, then burn it in or add it as a switchable track:

```sh
printf '1\n00:00:00,200 --> 00:00:02,000\nHello there\n\n2\n00:00:02,200 --> 00:00:04,000\nSecond line\n' > subs.srt
ffmpeg -y -v error -i a.mp4 -vf "subtitles=subs.srt:force_style='FontSize=24'" -c:v libx264 -preset veryfast burned.mp4
ffmpeg -y -v error -i a.mp4 -i subs.srt -map 0 -map 1 -c copy -c:s mov_text soft.mp4
```

Burning in needs a system font. If `fc-list` prints nothing, the text does not appear (ffmpeg only warns "Failed to load fonctconfig fonts"): use the soft track instead.

## Frames and masks for the next generation

```sh
ffmpeg -y -v error -i a.mp4 -frames:v 1 first.png                         # first frame
ffmpeg -y -v error -sseof -1 -i a.mp4 -update 1 -frames:v 1 last.png      # last frame (continue a video: pass it as start_image)
ffmpeg -y -v error -i a.mp4 -vf fps=2 f%02d.png                           # 2 frames per second, as ref_images
```

A mask for `generate_image` with `image`: same size as the image, white where it must be repainted, black elsewhere. Read the size first, then draw (ImageMagick `ellipse` takes centre, radii, then `0,360`):

```sh
magick identify -format '%wx%h\n' photo.png
magick -size 640x360 xc:black -fill white -draw "rectangle 200,100 440,260" mask-rect.png
magick -size 640x360 xc:black -fill white -draw "ellipse 320,180 120,80 0,360" mask-ellipse.png
```

Pillow does the same:

```sh
python3 -c "
from PIL import Image, ImageDraw
m = Image.new('L', Image.open('photo.png').size, 0)
ImageDraw.Draw(m).ellipse((200, 100, 440, 260), fill=255)
m.save('mask-pil.png')"
```

## Make a video from frames

No renderer is needed for simple videos: draw each frame with Pillow, pipe the raw frames into `ffmpeg`, or use `ffmpeg` filters alone. Fonts: `/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf` (check with `ls`; matplotlib also carries DejaVu in `matplotlib.get_data_path() + '/fonts/ttf'`). ImageMagick text needs `-font <that path>`.

Write this helper once (`/tmp/fx.py`). `render` pipes frames (`-f rawvideo -pix_fmt rgb24 -s WxH -r FPS -i -`) into `libx264`. `frame(t)` returns a Pillow image of the video size; `t` is in seconds.

```sh
cat > /tmp/fx.py <<'EOF2'
import os, subprocess
from PIL import Image, ImageDraw, ImageFont
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
if not os.path.exists(FONT):
    import matplotlib; FONT = matplotlib.get_data_path() + "/fonts/ttf/DejaVuSans-Bold.ttf"

def lerp(a, b, t): return a + (b - a) * t
def clamp(t): return min(1.0, max(0.0, t))
def ease(t): t = clamp(t); return t * t * (3 - 2 * t)      # ease in and out, 0..1
def key(t, pts):                                            # pts: [(time, value), ...], eased between keys
    if t <= pts[0][0]: return pts[0][1]
    for (t0, v0), (t1, v1) in zip(pts, pts[1:]):
        if t <= t1: return lerp(v0, v1, ease((t - t0) / (t1 - t0)))
    return pts[-1][1]

def render(path, w, h, fps, seconds, frame):
    p = subprocess.Popen(["ffmpeg", "-y", "-v", "error", "-f", "rawvideo", "-pix_fmt", "rgb24",
        "-s", f"{w}x{h}", "-r", str(fps), "-i", "-", "-c:v", "libx264", "-preset", "veryfast",
        "-crf", "20", "-pix_fmt", "yuv420p", path], stdin=subprocess.PIPE)
    for i in range(round(seconds * fps)):
        p.stdin.write(frame(i / fps).convert("RGB").tobytes())
    p.stdin.close(); p.wait()

def text(img, s, size, y=0.5, fill=(255, 255, 255)):       # centred line; y is a fraction of the height
    ImageDraw.Draw(img).text((img.width / 2, img.height * y), s, font=ImageFont.truetype(FONT, size), fill=fill, anchor="mm")
    return img

def cover(img, w, h):                                       # fill w x h, centre crop
    s = max(w / img.width, h / img.height)
    img = img.resize((round(img.width * s) + 1, round(img.height * s) + 1), Image.LANCZOS)
    x, y = (img.width - w) // 2, (img.height - h) // 2
    return img.crop((x, y, x + w, y + h))

def move(img, w, h, z, px=0.5, py=0.5):                     # zoom z >= 1, pan (px, py) in 0..1; img is w x h
    cw, ch = w / z, h / z
    x, y = (w - cw) * px, (h - ch) * py
    return img.resize((w, h), Image.BILINEAR, box=(x, y, x + cw, y + ch))
EOF2
```

Title card with fade, 5 s, on a gradient or on a picture. Use `W=1080 H=1920` in the environment for 9:16.

```sh
cat > /tmp/title.py <<'EOF2'
# usage: python3 /tmp/title.py out.mp4 "Title" "Subtitle" [background.png]
import os, sys, numpy as np
from PIL import Image
from fx import *
W, H, FPS = int(os.environ.get("W", 1920)), int(os.environ.get("H", 1080)), 30
k = W / 1920                                    # scale the type with the width
out, title, sub = sys.argv[1:4]
if len(sys.argv) > 4:                           # a generated picture, darkened so the text reads
    bg = Image.blend(cover(Image.open(sys.argv[4]).convert("RGB"), W, H), Image.new("RGB", (W, H)), 0.45)
else:
    y = np.linspace(0, 1, H)[:, None, None]; x = np.linspace(0, 1, W)[None, :, None]
    c = np.array([20, 30, 80]) * (1 - y) + np.array([180, 60, 120]) * y * x
    bg = Image.fromarray(np.broadcast_to(c, (H, W, 3)).astype("uint8"))
full = text(text(bg.copy(), title, int(140 * k)), sub, int(60 * k), y=0.5 + 120 * k / H)
black = Image.new("RGB", (W, H))
render(out, W, H, FPS, 5, lambda t: Image.blend(black, full, key(t, [(0, 0), (1, 1), (4, 1), (5, 0)])))
EOF2
PYTHONPATH=/tmp python3 /tmp/title.py /workspace/home/generated/title.mp4 "Hello, world" "a title card" /workspace/home/generated/a.png
```

Slideshow with a slow zoom and pan on each picture and a 1 s crossfade. `python3 /tmp/slideshow.py out.mp4 SECONDS a.png b.png c.png` (total length; any number of pictures).

```sh
cat > /tmp/slideshow.py <<'EOF2'
import os, sys
from PIL import Image
from fx import *
W, H, FPS, XF = int(os.environ.get("W", 1920)), int(os.environ.get("H", 1080)), 30, 1.0
out, total, files = sys.argv[1], float(sys.argv[2]), sys.argv[3:]
imgs = [cover(Image.open(f).convert("RGB"), W, H) for f in files]
n = len(imgs); S = (total + (n - 1) * XF) / n          # seconds per slide; XF of it is shared with the next
def shot(i, t):                                         # slow zoom in, pan left or right
    u = ease(t / S); px = lerp(0.3, 0.7, u) if i % 2 == 0 else lerp(0.7, 0.3, u)
    return move(imgs[i], W, H, lerp(1.0, 1.2, u), px)
def frame(t):
    i = min(int(t // (S - XF)), n - 1); lt = t - i * (S - XF)
    if i > 0 and lt < XF:                               # crossfade from the previous slide
        return Image.blend(shot(i - 1, lt + S - XF), shot(i, lt), lt / XF)
    return shot(i, lt)
render(out, W, H, FPS, total, frame)
EOF2
PYTHONPATH=/tmp python3 /tmp/slideshow.py /workspace/home/generated/slides.mp4 10 /workspace/home/generated/a.png /workspace/home/generated/b.png /workspace/home/generated/c.png
```

Speed (1080p, 30 fps, measured on a fast host; the sandbox may be slower): a 5 s title card renders in about 2 s, a 10 s slideshow of three pictures in about 7 s. These loops are fast because each frame is one Pillow call on a prepared image. Per-pixel `numpy` maths on every frame costs about 40 ms a frame (5 s in 6 s). Never loop over pixels in Python. If a job is slower, draw at 1280x720, or use the `ffmpeg` filters below.

A chart that draws itself, with matplotlib (`Agg` is already the backend). `figsize` times `dpi` is the video size; use `figsize=(10.8, 19.2)` for 9:16. 6 s at 720p takes about 3 s.

```sh
cat > /tmp/chart.py <<'EOF2'
import sys, numpy as np, matplotlib.pyplot as plt
from matplotlib.animation import FFMpegWriter
x = np.linspace(0, 10, 300); y = np.sin(x) * np.exp(-x / 8)
fig, ax = plt.subplots(figsize=(12.8, 7.2), dpi=100)    # 1280x720
ax.set(xlim=(0, 10), ylim=(-1.1, 1.1), title="Damped wave", xlabel="time (s)")
(line,) = ax.plot([], [], lw=3)
w = FFMpegWriter(fps=30, codec="libx264", extra_args=["-pix_fmt", "yuv420p", "-preset", "veryfast"])
with w.saving(fig, sys.argv[1], dpi=100):
    for i in range(180):                                # 180 frames = 6 s
        n = max(2, int(300 * (i + 1) / 180)); line.set_data(x[:n], y[:n]); w.grab_frame()
EOF2
python3 /tmp/chart.py /workspace/home/generated/chart.mp4
```

`ffmpeg` alone is simpler for these jobs:

```sh
F=/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf
# zoom into the centre of one picture, 150 frames = 5 s (a picture of 2000 px or more keeps it smooth)
ffmpeg -y -v error -i a.png -vf "zoompan=z='min(1+0.04*on/30,1.2)':x='(iw-iw/zoom)/2':y='(ih-ih/zoom)/2':d=150:s=1920x1080:fps=30,format=yuv420p" -c:v libx264 -preset veryfast -crf 20 zoom.mp4
# crossfade two pictures: 4 s each, 1 s fade, offset = 4 - 1, result 7 s
ffmpeg -y -v error -loop 1 -t 4 -i a.png -loop 1 -t 4 -i b.png -filter_complex "[0]scale=1920:1080:force_original_aspect_ratio=increase,crop=1920:1080,fps=30,format=yuv420p[a];[1]scale=1920:1080:force_original_aspect_ratio=increase,crop=1920:1080,fps=30,format=yuv420p[b];[a][b]xfade=transition=fade:duration=1:offset=3" -c:v libx264 -preset veryfast -crf 20 xfade.mp4
# text with fade in and out on a generated gradient (r=30 sets the frame rate)
ffmpeg -y -v error -f lavfi -i "gradients=s=1920x1080:r=30:d=5:c0=0x142050:c1=0xb43c78:x0=0:y0=0:x1=1920:y1=1080,format=yuv420p" -vf "drawtext=fontfile=$F:text='Hello, world':fontsize=140:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2:alpha='if(lt(t,1),t,if(lt(t,4),1,5-t))'" -c:v libx264 -preset veryfast -crf 20 dtext.mp4
```

For 9:16, crop before `zoompan` (`crop=ih*9/16:ih,zoompan=...:s=1080x1920`), or change the sizes in `xfade` to 1080:1920. Add `xfade=transition=` values such as `slideleft`, `wipeleft`, `dissolve` for other moves. `drawtext` needs the font file: without `fontfile=` it may fail with no system font.

SVG frames: `rsvg-convert` draws them (text needs `font-family="DejaVu Sans"`). `-w 1920` scales a 16:9 SVG to 1080p. `magick x.svg out.png` works too.

```sh
for i in 0 1 2; do rsvg-convert -w 1920 -o f_0$i.png f$i.svg; done      # or pipe: rsvg-convert ... | ffmpeg -f image2pipe -framerate 5 -i - ...
ffmpeg -y -v error -framerate 5 -i f_%02d.png -c:v libx264 -pix_fmt yuv420p svg.mp4
```

Finish: add a voice or music with the commands in "Audio track" (`-af apad -shortest` keeps the video length). Then check the result: size, frame count, length, and one frame to look at.

```sh
ffprobe -v error -count_frames -show_entries format=duration:stream=codec_name,width,height,nb_read_frames -of default=nw=1 slides.mp4
ffmpeg -y -v error -ss 3.5 -i slides.mp4 -frames:v 1 slides-frame.png      # a time inside a crossfade shows the blend
```

Look at the PNG. For 1080p keep `1920x1080`; for 9:16 use `1080x1920`; keep even sizes.

## When it fails

- `Unknown encoder` or `libass` errors: a tool of the image is missing. Say so; do not try to install it.
- `Invalid data found` or an empty output: the input is not finished or not a media file. Run `ffprobe` on it.
- Odd width or height with `libx264`: use `-2` in `scale` (`scale=640:-2`).
- `Output file does not contain any stream`: a `-map` names a stream the input lacks. Check with `ffprobe`.
- Audio and video drift or the output is too short: `-shortest` cut it. Check durations with `ffprobe`.
- Concat with `-c copy` jumps or freezes: the clips differ. Use the script above.
- Long jobs: add `-preset veryfast`, work on a short test cut first. A command that runs for minutes may time out; run it again on a smaller input.

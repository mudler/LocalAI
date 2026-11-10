---
name: assemble-media
description: Use to join, trim, mix, subtitle or inspect generated media, or make frames and masks for the next generation, with ffmpeg, sox or ImageMagick.
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

## When it fails

- `Unknown encoder` or `libass` errors: a tool of the image is missing. Say so; do not try to install it.
- `Invalid data found` or an empty output: the input is not finished or not a media file. Run `ffprobe` on it.
- Odd width or height with `libx264`: use `-2` in `scale` (`scale=640:-2`).
- `Output file does not contain any stream`: a `-map` names a stream the input lacks. Check with `ffprobe`.
- Audio and video drift or the output is too short: `-shortest` cut it. Check durations with `ffprobe`.
- Concat with `-c copy` jumps or freezes: the clips differ. Use the script above.
- Long jobs: add `-preset veryfast`, work on a short test cut first. A command that runs for minutes may time out; run it again on a smaller input.

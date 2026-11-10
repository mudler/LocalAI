---
name: fframes-video
description: Use to make a video, title card or motion graphic in code with fframes, and to check and render it to MP4.
sandbox: video
---

# Videos in code with fframes

Adapted for the LocalAI sandbox from the `fframes-video` skill of [fframes](https://github.com/dmtrKovalenko/fframes) (MIT licence, copyright Dmitriy Kovalenko). The upstream skill has more: design, 3D, audio and captions references.

fframes renders video from a Rust crate. A video is a struct whose `render_frame` returns an SVG tree for each frame (`svgr!` macro: SVG with `{rust expressions}`). Scenes split the timeline, `timeline!` animates values, ffmpeg encodes. Every project has a command line for rendering and for checking frames as files.

The `video` sandbox profile has Rust, `cargo fframes` and ffmpeg installed. The sandbox has no GPU: always use the CPU backend. It is about 10 times slower than the GPU one and has no preview window. Keep videos short (5 to 15 s), at 24 or 30 fps, and check with frames and draft renders before the final render. A first build compiles all dependencies and takes minutes; run it in the background. If the build cannot fetch crates, tell the user the sandbox network preset must allow crates.io. Do not install packages.

## Plan first

Ask for what matters and is missing: where it plays (format and length), the one thing the viewer must remember, tone, and assets. Never invent product facts, numbers or quotes. Give each scene a reason tied to the subject. Avoid template leftovers (the starter text, colours, font), the same fade-up on everything, and static scenes where one thing appears and nothing else moves. Write on-screen text as a person would say it, and keep it short.

Do not write tests. Do not cut, retime or re-encode media with ffmpeg scripts to build the video: express it in the crate. Reading files to measure them with `ffprobe` is fine.

## Create a project

```sh
cargo fframes new my-video --backend cpu --format landscape --fps 30 --yes
cd my-video
cargo build --release > build.log 2>&1 &
```

Options: `--template single-scene|multi-scene`, `--format landscape|portrait|square|uhd`, `--fps`, `--title`, `--dir`. `single-scene` (default) suits any format. `multi-scene` has two scenes and takes only `landscape` and `uhd`. Always pass `--yes`. The project is `src/lib.rs` (the video), `src/main.rs` (the command line), `media/` (fonts, images and audio, compiled into the binary; one starter font) and a `README.md` with the commands.

The template code is a placeholder that shows the API: replace its layout, colours, font and text. Read `src/lib.rs` before you change it. Put every font file in `media/` and refer to it by family name with a numeric `font-weight`.

## Write the scene

```rust
impl Video for MyVideo<'_> {
    const FPS: usize = 30; const WIDTH: usize = 1920; const HEIGHT: usize = 1080;
    fn duration(&self) -> Duration<'_> { Duration::Auto }            // sum of the scenes
    fn audio(&self) -> AudioMap<'_> { AudioMap::none() }
    fn define_scenes(&self) -> Scenes<'_> { Scenes::from(vec![&self.intro as &dyn Scene, &self.main]) }
    fn render_frame<'a>(&'a self, frame: Frame, ctx: &FFramesContext<'a, '_>) -> Svgr<'a> {
        fframes::svgr!(<svg xmlns="http://www.w3.org/2000/svg" width={Self::WIDTH} height={Self::HEIGHT}>
            <rect width={Self::WIDTH} height={Self::HEIGHT} fill={BACKGROUND} />
            {ctx.render_scenes(&frame)}
        </svg>)
    }
}
```

- One scene per idea, 2 to 6 s. Inside a scene `frame.seconds()` counts from the scene start.
- Animate with `frame.animate(fframes::timeline!(...))` in `svgr!` attributes. Easings go inline, for example `Easing::Spring { mass: 1.0, stiffness: 180.0, damping: 20.0 }`; leave the end time off a spring so it settles.
- Markup without `{}` is cached across frames. Put animated values on a wrapping `<g transform={..} opacity={..}>`.
- `render_frame` runs for every frame on several threads: no panics, file reads or heavy work in it. Prepare data in the constructor or a `OnceLock`.
- Measure text, do not guess: `frame.text_width`, `frame.text_fit(.., TextOverflow::Ellipsis)`, `frame.text_break_lines`.
- Sound goes in `fn audio` with `AudioMap::from([AudioTrack::new("music.mp3", Second(0.)..Eof).gain_db(-18.).fade_in(1.).fade_out(2.), ...])`, files in `media/`. Use `.voice()` on speech and `.duck_under_voice()` on music. Make the music, voice and effects first with the LocalAI tools (`generate_sound`, `speak`), save them in `media/`, then rebuild.
- Titles 96 to 140 px and body 44 to 60 px at 1920x1080; margins 8 to 10% of the width; elements enter in 300 to 600 ms and leave faster.

## Check, draft, render

Run these in the project folder with `cargo run --release -- <command>`, or the binary in `target/release/`. Keep `--release`: debug builds are much slower. After a source or `media/` change, build again.

```sh
F() { cargo run --release -- "$@"; }
F timeline                      # scenes, durations, audio tracks
F inspect                       # missing media or fonts, cut-off text, panics; exit code 2 on errors
F strip Intro -n 8              # contact sheet strip.png
F frame 2s,Intro@end            # full-size PNGs in frames/
F frame 3s --crop 1100,520,240,120   # sharp zoom on a detail
F render --draft -o /workspace/home/generated/my-video-draft.mp4   # half resolution, fast preset
F render -o /workspace/home/generated/my-video.mp4                 # the final video
```

- `F inspect` first, and fix everything it reports. A warning only on a scene's first frames is usually an entrance.
- Open `strip.png` and the `frame` PNGs with the file tools and look at them before you say anything looks good. Judge small details on a `--crop`, not on a scaled-down frame.
- `F strip 2s,5s,9s` shows chosen times. `F onion "Intro@0..Intro@1s" -n 6` shows the path of a movement.
- `F render Intro --draft` encodes one scene. A range is `Intro`, `10s..20s`, `#3` (scene index), `Intro@50%`.
- `F audio analyze` shows loudness (about -14 LUFS for web video, true peak below -1 dBTP) and silence.
- `F inspect --pops` before you finish: it warns about parts that change in one frame between still frames.
- Add `--json` to parse output. If a frame panics, run again with `RUST_BACKTRACE=1`.
- Do not use `preview`: it needs a window and a GPU.

When done, confirm the file: `ffprobe -v error -show_entries stream=codec_type,width,height,nb_frames,duration /workspace/home/generated/my-video.mp4`, and give the user the path.

## When it fails

- Build fails in `ffmpeg-sys-fframes` or on a missing library: the image is missing a build tool. Say so; do not install.
- Text is wrong or missing: `inspect` reports "No match for ... font-family". Add the font file to `media/` and use its exact family name.
- A render takes too long: shorten the video, lower the fps or the format, render a draft or one scene.
- If fframes cannot express what was asked, tell the user what it cannot do and the closest result. Do not work around it with another tool.

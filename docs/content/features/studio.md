+++
disableToc = false
title = "Studio"
weight = 49
url = "/features/studio/"
+++

Studio is the part of the web UI where you make images, video, 3D objects, speech, sound and transformed audio, and find what you made before. It opens on a front page with a prompt box and your own work. Each type still has its own workspace page (Images, Video, 3D, TTS, Sound, Transform, Diarization) where a run happens.

## Make something

Type what you want in the box under **What do you want to make?**, or pick a type first. The box suggests a type from your words ("Sounds like Video") and never switches by itself. The suggestion is a short list of keywords in the browser. No model is called.

| Key | Action |
|---|---|
| `/` | Focus the prompt box |
| `Alt+1` to `Alt+7` | Pick a type, in the order of the chips |
| `Ctrl+Enter` (`Cmd+Enter`) | Generate |
| `Alt+Enter` | Take the suggested type |
| `Esc` | Close the details in a lineage view |

**Generate** opens the workspace for the chosen type with the prompt, the model, and the options that workspace has already filled in: size and count for Images, size for Video. The run itself happens on the workspace page, as before. The front page does not run anything.

3D, Transform and Diarization start from a file. Use the **Start from** list to pick one of your earlier results as the file, or add a file on the next page.

### A type with no model

A type with no installed model is a dashed chip. Picking it shows one model from the gallery, its download size and memory need where the gallery knows them, the memory free now, and an **Install** button. Nothing installs until you press it, and the words you typed stay in the box while it installs. When the gallery gives no size, the note says so.

## Your work

**Your work** lists the results this browser has a record of, newest first, with filters for each type and counts. Each tile shows a thumbnail (a waveform drawing for audio, one bar per speaker for diarization), the prompt, the model and the age. The star marks a favourite.

Results made from each other stack into one project tile. Open a tile to see the lineage.

### Where the history is kept

The history lives in your browser, not on the server. Each workspace writes its own list when a result is produced: images, video, TTS, sound and audio transform in browser storage (up to 100 entries each), 3D in IndexedDB (up to 20 entries, with the model file), and diarization in browser storage with the file name, the model and the speaker count only. It never keeps the recording or the transcript. A prompt longer than 2000 characters is cut when it is stored. Favourites are a list of up to 500 ids.

Results made before this version have no link to the result they came from, so they appear as single tiles. The files themselves are on the server; if the server has cleaned its output folder, the tile shows the type icon instead of a picture.

**Clear history** removes all of these lists and the favourites from this browser after a confirmation. It does not delete files on the server.

## Lineage

When a workspace is opened from a result (for example **Animate** on a picture), the new result records the id of the result it came from and how: take, animate, to 3D, variation, transform or who spoke. The lineage view draws those links as a board: prompts or files on the left, results next to them, the path through the selected result drawn heavier, and one dashed suggested next step chosen from the installed models.

The dock under the board acts on the selected result:

- **Run as a new take** opens the same workspace with the prompt, model and size filled in. It is disabled when the original input was not kept (3D, diarization).
- **Branch from here** opens a draft: choose the next step, edit the words, and **Open in** the workspace with the result as its starting point.

A step is listed but disabled, with the reason, when the destination page cannot start from that kind of result yet. Today a video cannot start a sound and a 3D object cannot start a clip.

Arrow keys walk the board, `B` branches and `Esc` closes the draft, then the details, then the view.

## The workspace page

All seven workspaces share one layout, under a row of tabs, one per type:

- **Compose card.** Optional sources as dashed chips (a start image, reference images, an end image, avatar audio), or a drop area where the run cannot start without a file (a recording for Diarization, audio for Transform, a picture for 3D). Then the prompt, with starters while it is empty, a model chip, the essential options as chips (size and count, duration and frame rate, voice, mode), and an **Advanced** fold that names what is inside when it is closed. Below that: the memory the model needs and whether it fits, when the gallery knows it, and one button. When the button cannot run, the reason is next to it. With no model for the type, the install note from the front page shows in the card.
- **Run area.** While a request is out, a job card shows the model and options and the time that has passed. The server reports no phase and no percentage on these endpoints, so the bar is indeterminate. A failed run shows what the server said, says the prompt and settings are still there, and offers **Try again**. A finished run shows the result in a viewer for its type (picture grid, video player, waveform player, 3D viewer, spectrograms, or a timeline of speakers) with a toolbar:
  - **Favourite**, the same list the front page keeps.
  - **Download**.
  - **Use in** lists where the result can go next. A step is disabled, with the reason, when the destination cannot start from it, or when no model for it is installed it says so.
  - **Re-run with edits** puts the result's values back in the form. The fields you then change are outlined and listed under **Changed from this take**, and the button reads **Run again**. It is disabled when the original input was not kept (3D, diarization).
  - **Lineage** opens the lineage view of the front page for this result.
- **Recent results.** A strip of the results of this type from the same history, with an All and a Favourites filter. Click a tile to show it above, and click it again to go back to the latest.

Diarization lists who spoke when as one lane per speaker, the talk time of each speaker, and the segments with their text when the model returned it. **RTTM**, **SRT** (only when there is text) and **JSON** are built in the browser from the result in hand.

## What a workspace accepts from the front page

The workspaces read these query parameters, so a link of your own works too: `prompt`, `model`, `size`, `n` (count), `from` (the id of the result it starts from) and `edge` (how). For example `/app/studio/video?prompt=Slow%20push-in&from=<id>&edge=animate`.

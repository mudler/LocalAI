# qwen3-tts

Qwen3-TTS through `qwen3-tts-cpp`: 24 kHz mono, 11 languages. Ids seen: `qwen3-tts-cpp` (base), `qwen3-tts-cpp-1.7b-base`, `qwen3-tts-cpp-1.7b-customvoice`, `qwen3-tts-cpp-1.7b-voicedesign`. The variant decides how you choose the voice.

## Good at
- Short to medium text in English, Chinese and 9 more languages.
- Voice from a description (voicedesign), a named speaker (customvoice), or a clip (base).

## Prompt shape
Write the text as it is said. Spell out numbers and dates for a fixed reading. Set `language`.
- voicedesign: `instructions` is required: attributes such as `female, young adult, calm and clear, moderate pitch, slow pace`. No `voice`.
- customvoice: `voice` is a speaker: serena, vivian, uncle_fu, ryan, aiden, ono_anna, sohee, eric, dylan. `instructions` sets the tone.
- base: `voice` is a profile from `voice_profile` (clone needs a confirmed clip and its transcript).

## Example prompts
Tested, 24 kHz mono, not listened:
- voicedesign, `The meeting starts at 3:30 PM on March 5th. Please bring the printed report.` with the instructions above (4.8 s).
- customvoice, `ryan`, `Your order has shipped. It will arrive on Friday.`, instructions `cheerful, a little fast` (2.9 s).

## Parameters to touch
`text`, `voice`, `language`, `instructions`. `speed`: unverified for this backend.

## Known failure cases
- voicedesign without `instructions` is rejected.
- A base model with no `voice` has no speaker: unverified.
- Whether `instructions` change a customvoice speaker audibly: not listened.

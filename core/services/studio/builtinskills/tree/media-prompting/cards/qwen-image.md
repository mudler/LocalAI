# qwen-image

Qwen-Image family. Ids seen: `qwen-image`, `qwen-image-2.1`, `qwen-image-2.1-q4_k-ggml`, `Qwen-Image-2.1-Turbo-Q8_0`.

## Good at
- Text in the picture, English and Chinese.
- Long, descriptive prompts with a stated light.

## Prompt shape
Subject, setting, style, light. Then each text to render in double quotes, with colour and place. Write the prompt in the language of the text. Upstream appends `, Ultra HD, 4K, cinematic composition.`

## Example prompts
Tested (1024x1024, steps 25, seed 42; text correct):
- `A hand-painted wooden shop sign above a bakery door, the sign reads "FRESH BREAD" in bold cream letters on dark green, morning light from the left, shallow depth of field, warm street scene`

Tested (1344x768, steps 25; all characters correct):
- `一张咖啡馆的黑板菜单特写，黑板上用白色粉笔写着“今日特饮：桂花拿铁 18元”，旁边画着一只小小的咖啡杯，木质桌面，柔和的午后自然光，浅景深`

## Parameters to touch
- `size`: multiples of 32. Upstream ratios: 1328x1328, 1664x928 (16:9), 928x1664, 1472x1140 (4:3).
- `steps`: full model 25 to 50. A `Turbo` id: 8, tested.
- Guidance is set in the model file; the tool has no cfg argument. `negative_prompt`: skip it.

## Known failure cases
- Turbo, same prompt and seed: text correct, picture darker, cream turned yellow-green. Name colours strictly.
- Languages other than English and Chinese, and long text blocks: unverified.

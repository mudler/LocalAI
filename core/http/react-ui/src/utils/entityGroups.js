// Grouping for the rails on Models Explore and Backends Catalog.
//
// The gallery does not tag entries with the use-case keys the filter chips
// send. Those keys (`chat`, `tts`, `transcript`, …) are a server-side
// vocabulary the handler maps onto entries; what an entry actually carries is
// free-form and inconsistent: models come back tagged `llm`, `gguf`, `vision`,
// `coding`, and backends `LLM`, `text-to-text`, `audio-transcription`, `CUDA`.
// An earlier version of this matched on the filter keys and put all 1,595
// models into "Everything else", which is how this file came to exist.
//
// Order is specific before general, and that is load-bearing. A vision model is
// tagged `llm` as well as `vision`, so testing text first would swallow it.
const GROUPS = [
  {
    id: 'audio',
    labelKey: 'groups.audio',
    icon: 'fa-wave-square',
    tags: ['tts', 'stt', 'asr', 'transcript', 'transcription', 'audio-transcription',
      'speech', 'speech-to-text', 'text-to-speech', 'audio', 'voice', 'voice-cloning',
      'whisper', 'diarization', 'sound', 'music', 'vad'],
    backends: ['whisper', 'parakeet', 'kokoro', 'bark', 'piper', 'vibevoice', 'qwentts',
      'crispasr', 'moss-transcribe', 'omnivoice', 'ced', 'silero'],
  },
  {
    id: 'visual',
    labelKey: 'groups.visual',
    icon: 'fa-image',
    tags: ['image', 'image-generation', 'text-to-image', 'video', 'text-to-video', '3d',
      'sd', 'diffusion', 'stable-diffusion', 'flux'],
    backends: ['stablediffusion', 'diffusers', 'flux'],
  },
  {
    id: 'vision',
    labelKey: 'groups.vision',
    icon: 'fa-eye',
    tags: ['vision', 'multimodal', 'vlm', 'image-to-text', 'detection', 'ocr'],
    backends: [],
  },
  {
    id: 'text',
    labelKey: 'groups.text',
    icon: 'fa-brain',
    tags: ['llm', 'text-to-text', 'text-generation', 'chat', 'completion', 'coding',
      'reasoning', 'thinking', 'agent', 'tool-use', 'embeddings', 'rerank'],
    backends: ['llama', 'vllm', 'sglang', 'ds4', 'bonsai', 'transformers', 'exllama',
      'mlx', 'rerankers', 'bert'],
  },
  { id: 'other', labelKey: 'groups.other', icon: 'fa-cube', tags: [], backends: [] },
]

export const ENTITY_GROUPS = GROUPS

// Tags are matched case-insensitively because the two galleries disagree on
// case for the same concept (`llm` on models, `LLM` on backends).
export function groupForEntity({ tags, backend, name } = {}) {
  const owned = new Set((tags || []).map(t => String(t).toLowerCase()))
  for (const g of GROUPS) {
    if (g.tags.some(tag => owned.has(tag))) return g
  }
  // Nothing matched, so fall back to what runs it. A backend named `whisper`
  // is a speech backend whatever its tags say, and for the backend gallery the
  // entry's own name is that signal.
  const hint = String(backend || name || '').toLowerCase()
  if (hint) {
    for (const g of GROUPS) {
      if (g.backends.some(b => hint.includes(b))) return g
    }
  }
  return GROUPS[GROUPS.length - 1]
}

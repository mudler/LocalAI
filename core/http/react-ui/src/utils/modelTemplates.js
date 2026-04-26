// Model templates for the "Add Model" create flow.
// Each template pre-populates the Model Editor with relevant fields.

const MODEL_TEMPLATES = [
  {
    id: 'other',
    label: 'Other',
    icon: 'fa-file-alt',
    description: 'Blank configuration — add any fields you need',
    fields: {
      'name': '',
    },
  },
  {
    id: 'pipeline',
    label: 'Voice Pipeline',
    icon: 'fa-diagram-project',
    description: 'Real-time voice pipeline combining VAD, transcription, LLM, and TTS models',
    fields: {
      'name': '',
      'pipeline.vad': '',
      'pipeline.transcription': '',
      'pipeline.llm': '',
      'pipeline.tts': '',
      'tts.voice': '',
    },
  },
  {
    id: 'llm',
    label: 'LLM',
    icon: 'fa-brain',
    description: 'Language model for chat and text completion',
    fields: {
      'name': '',
      'backend': '',
      'parameters.model': '',
      'context_size': 0,
    },
  },
  {
    id: 'tts',
    label: 'TTS',
    icon: 'fa-volume-up',
    description: 'Text-to-speech model for voice synthesis',
    fields: {
      'name': '',
      'backend': '',
      'parameters.model': '',
      'tts.voice': '',
    },
  },
  {
    id: 'image',
    label: 'Image Generation',
    icon: 'fa-image',
    description: 'Image generation model using diffusers or other backends',
    fields: {
      'name': '',
      'backend': 'diffusers',
      'parameters.model': '',
      'diffusers.pipeline_type': '',
      'diffusers.cuda': false,
    },
  },
  {
    id: 'embedding',
    label: 'Embedding',
    icon: 'fa-vector-square',
    description: 'Embedding model for text vectorization',
    fields: {
      'name': '',
      'backend': '',
      'parameters.model': '',
      'embeddings': true,
    },
  },
]

export default MODEL_TEMPLATES

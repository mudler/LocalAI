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
  {
    id: 'cloud-proxy-openai',
    label: 'OpenAI Cloud Proxy',
    icon: 'fa-cloud',
    description: 'Forward chat completions to OpenAI or any OpenAI-compatible provider; PII redaction runs in flight',
    // known_usecases is pre-seeded with chat so the proxy model
    // surfaces in places that filter by capability — model pickers
    // for chat, router fallback dropdowns, etc. Backends without an
    // explicit usecase list are filtered out of those selectors.
    fields: {
      'name': '',
      'backend': 'cloud-proxy',
      'known_usecases': ['chat'],
      'proxy.mode': 'passthrough',
      'proxy.provider': 'openai',
      'proxy.upstream_url': 'https://api.openai.com/v1/chat/completions',
      'proxy.api_key_env': 'OPENAI_API_KEY',
      'proxy.upstream_model': '',
      'proxy.request_timeout_seconds': 120,
      'pii.enabled': true,
    },
  },
  {
    id: 'cloud-proxy-anthropic',
    label: 'Anthropic Cloud Proxy',
    icon: 'fa-cloud',
    description: 'Forward chat completions to Anthropic via translate mode (OpenAI ↔ Messages); tool_use blocks and usage tokens survive the round-trip. PII redaction runs in flight.',
    fields: {
      'name': '',
      'backend': 'cloud-proxy',
      'known_usecases': ['chat'],
      // translate mode targets Anthropic's native /v1/messages and
      // converts request/response between OpenAI Chat Completions and
      // Anthropic Messages so the LocalAI chat UI keeps speaking
      // OpenAI. passthrough would only work against Anthropic's
      // /v1/chat/completions OpenAI-compat endpoint and loses
      // tool_use semantics.
      'proxy.mode': 'translate',
      'proxy.provider': 'anthropic',
      'proxy.upstream_url': 'https://api.anthropic.com/v1/messages',
      'proxy.api_key_env': 'ANTHROPIC_API_KEY',
      'proxy.upstream_model': '',
      'proxy.request_timeout_seconds': 300,
      'pii.enabled': true,
    },
  },
  {
    id: 'router',
    label: 'Routing Model',
    icon: 'fa-route',
    description: 'Score-classifier router with three example policies and two candidates. Fill in the classifier_model (Arch-Router-1.5B recommended), the per-candidate downstream models, and the fallback. The L2 embedding cache is opt-in via the Routing section.',
    fields: {
      'name': 'smart-router',
      'router.classifier': 'score',
      'router.classifier_model': '',
      'router.fallback': '',
      'router.activation_threshold': 0.40,
      'router.policies': [
        { label: 'code-generation', description: 'writing, debugging, reading, or explaining code in any programming language' },
        { label: 'casual-chat', description: 'small talk, greetings, jokes, or general conversation with no specific task' },
        { label: 'math-reasoning', description: 'arithmetic, equations, percentage calculations, or step-by-step word problems' },
      ],
      'router.candidates': [
        { model: '', labels: ['casual-chat'] },
        { model: '', labels: ['code-generation', 'casual-chat', 'math-reasoning'] },
      ],
    },
  },
  {
    id: 'mitm',
    label: 'MITM Intercept',
    icon: 'fa-shield-halved',
    description: 'Bind a hostname to this config for the cloudproxy MITM listener. PII filtering and pattern overrides flow from this config when the host is intercepted.',
    // The mitm- name prefix is a convention, not a contract — the
    // dispatcher looks up by host, not name. Prefixing keeps the
    // config out of the way of callable model names so a chat client
    // accidentally requesting "anthropic" doesn't hit a backendless
    // intercept config.
    //
    // pii.patterns is pre-seeded with an empty list so the override
    // editor is visible by default — admins typically want to tighten
    // a couple of pattern actions when intercepting a cloud provider.
    // An empty list serializes out and the redactor ignores it.
    fields: {
      'name': 'mitm-anthropic',
      'mitm.hosts': ['api.anthropic.com'],
      'pii.enabled': true,
      'pii.patterns': [],
    },
  },
]

export default MODEL_TEMPLATES

export const API_CONFIG = {
  endpoints: {
    // Operations
    operations: '/api/operations',
    cancelOperation: (jobID) => `/api/operations/${jobID}/cancel`,

    // Models gallery
    models: '/api/models',
    installModel: (id) => `/api/models/install/${id}`,
    deleteModel: (id) => `/api/models/delete/${id}`,
    modelConfig: (id) => `/api/models/config/${id}`,
    modelConfigJson: (name) => `/api/models/config-json/${name}`,
    modelJob: (uid) => `/api/models/job/${uid}`,

    // Backends gallery
    backends: '/api/backends',
    installBackend: (id) => `/api/backends/install/${id}`,
    deleteBackend: (id) => `/api/backends/delete/${id}`,
    installExternalBackend: '/api/backends/install-external',
    backendJob: (uid) => `/api/backends/job/${uid}`,
    deleteInstalledBackend: (name) => `/api/backends/system/delete/${name}`,

    // Resources
    resources: '/api/resources',

    // Settings
    settings: '/api/settings',

    // Traces
    traces: '/api/traces',
    clearTraces: '/api/traces/clear',
    backendTraces: '/api/backend-traces',
    clearBackendTraces: '/api/backend-traces/clear',

    // P2P
    p2pWorkers: '/api/p2p/workers',
    p2pFederation: '/api/p2p/federation',
    p2pStats: '/api/p2p/stats',
    p2pToken: '/api/p2p/token',

    // Agent jobs
    agentTasks: '/api/agent/tasks',
    agentTask: (id) => `/api/agent/tasks/${id}`,
    executeAgentTask: (name) => `/api/agent/tasks/${name}/execute`,
    agentJobs: '/api/agent/jobs',
    agentJob: (id) => `/api/agent/jobs/${id}`,
    cancelAgentJob: (id) => `/api/agent/jobs/${id}/cancel`,
    executeAgentJob: '/api/agent/jobs/execute',

    // OpenAI-compatible endpoints
    chatCompletions: '/v1/chat/completions',
    mcpChatCompletions: '/v1/mcp/chat/completions',
    mcpServers: (model) => `/v1/mcp/servers/${model}`,
    mcpPrompts: (model) => `/v1/mcp/prompts/${model}`,
    mcpGetPrompt: (model, prompt) => `/v1/mcp/prompts/${model}/${encodeURIComponent(prompt)}`,
    mcpResources: (model) => `/v1/mcp/resources/${model}`,
    mcpReadResource: (model) => `/v1/mcp/resources/${model}/read`,
    completions: '/v1/completions',
    imageGenerations: '/v1/images/generations',
    audioSpeech: '/v1/audio/speech',
    audioTranscriptions: '/v1/audio/transcriptions',
    soundGeneration: '/v1/sound-generation',
    embeddings: '/v1/embeddings',
    modelsList: '/v1/models',
    modelsCapabilities: '/api/models/capabilities',

    // LocalAI-specific
    tts: '/tts',
    video: '/video',
    backendMonitor: '/backend/monitor',
    backendShutdown: '/backend/shutdown',
    modelsApply: '/models/apply',
    modelsDelete: (name) => `/models/delete/${name}`,
    modelsAvailable: '/models/available',
    modelsGalleries: '/models/galleries',
    modelsReload: '/models/reload',
    modelsImportUri: '/models/import-uri',
    modelsImport: '/models/import',
    modelsJobStatus: (uid) => `/models/jobs/${uid}`,
    modelEditGet: (name) => `/api/models/edit/${name}`,
    modelEdit: (name) => `/models/edit/${name}`,
    backendsAvailable: '/backends/available',
    backendsInstalled: '/backends',
    version: '/version',
    system: '/system',
    corsProxy: '/api/cors-proxy',
  },
}

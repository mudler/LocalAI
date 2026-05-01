export const API_CONFIG = {
  endpoints: {
    // Operations
    operations: '/api/operations',
    cancelOperation: (jobID) => `/api/operations/${jobID}/cancel`,
    dismissOperation: (jobID) => `/api/operations/${jobID}/dismiss`,

    // Models gallery
    models: '/api/models',
    installModel: (id) => `/api/models/install/${id}`,
    deleteModel: (id) => `/api/models/delete/${id}`,
    modelConfig: (id) => `/api/models/config/${id}`,
    modelConfigJson: (name) => `/api/models/config-json/${name}`,
    configMetadata: '/api/models/config-metadata',
    configAutocomplete: (provider) => `/api/models/config-metadata/autocomplete/${encodeURIComponent(provider)}`,
    configPatch: (name) => `/api/models/config-json/${encodeURIComponent(name)}`,
    modelJob: (uid) => `/api/models/job/${uid}`,

    // Backends gallery
    backends: '/api/backends',
    installBackend: (id) => `/api/backends/install/${id}`,
    deleteBackend: (id) => `/api/backends/delete/${id}`,
    installExternalBackend: '/api/backends/install-external',
    backendJob: (uid) => `/api/backends/job/${uid}`,
    deleteInstalledBackend: (name) => `/api/backends/system/delete/${name}`,
    backendsUpgrades: '/api/backends/upgrades',
    backendsUpgradesCheck: '/api/backends/upgrades/check',
    upgradeBackend: (name) => `/api/backends/upgrade/${name}`,

    // Resources
    resources: '/api/resources',

    // Settings
    settings: '/api/settings',

    // Traces
    traces: '/api/traces',
    clearTraces: '/api/traces/clear',
    backendTraces: '/api/backend-traces',
    clearBackendTraces: '/api/backend-traces/clear',

    // Backend Logs
    backendLogs: '/api/backend-logs',
    backendLogsModel: (modelId) => `/api/backend-logs/${encodeURIComponent(modelId)}`,
    clearBackendLogs: (modelId) => `/api/backend-logs/${encodeURIComponent(modelId)}/clear`,

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
    audioTransformations: '/audio/transformations',
    audioTransformStream: '/audio/transformations/stream',
    soundGeneration: '/v1/sound-generation',
    embeddings: '/v1/embeddings',

    // Face biometrics
    faceVerify: '/v1/face/verify',
    faceAnalyze: '/v1/face/analyze',
    faceEmbed: '/v1/face/embed',
    faceRegister: '/v1/face/register',
    faceIdentify: '/v1/face/identify',
    faceForget: '/v1/face/forget',

    // Voice biometrics
    voiceVerify: '/v1/voice/verify',
    voiceAnalyze: '/v1/voice/analyze',
    voiceEmbed: '/v1/voice/embed',
    voiceRegister: '/v1/voice/register',
    voiceIdentify: '/v1/voice/identify',
    voiceForget: '/v1/voice/forget',

    modelsList: '/v1/models',
    modelsCapabilities: '/api/models/capabilities',

    // Realtime / WebRTC
    realtimeCalls: '/v1/realtime/calls',
    pipelineModels: '/api/pipeline-models',

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
    vramEstimate: '/api/models/vram-estimate',
    modelsJobStatus: (uid) => `/models/jobs/${uid}`,
    modelEditGet: (name) => `/api/models/edit/${name}`,
    modelEdit: (name) => `/models/edit/${name}`,
    modelToggleState: (name, action) => `/models/toggle-state/${name}/${action}`,
    modelTogglePinned: (name, action) => `/models/toggle-pinned/${name}/${action}`,
    backendsAvailable: '/backends/available',
    backendsKnown: '/backends/known',
    backendsInstalled: '/backends',
    version: '/version',
    system: '/system',
    corsProxy: '/api/cors-proxy',

    // Nodes (distributed)
    nodes: '/api/nodes',
    node: (id) => `/api/nodes/${id}`,
    nodeModels: (id) => `/api/nodes/${id}/models`,
    nodeDrain: (id) => `/api/nodes/${id}/drain`,
    nodeResume: (id) => `/api/nodes/${id}/resume`,
    nodeApprove: (id) => `/api/nodes/${id}/approve`,
    nodeHeartbeat: (id) => `/api/nodes/${id}/heartbeat`,
    nodeBackends: (id) => `/api/nodes/${id}/backends`,
    nodeBackendsInstall: (id) => `/api/nodes/${id}/backends/install`,
    nodeBackendsDelete: (id) => `/api/nodes/${id}/backends/delete`,
    nodeBackendLogs: (id) => `/api/nodes/${id}/backend-logs`,
    nodeBackendLogsModel: (id, modelId) => `/api/nodes/${id}/backend-logs/${encodeURIComponent(modelId)}`,
    nodeModelsUnload: (id) => `/api/nodes/${id}/models/unload`,
    nodeLabels: (id) => `/api/nodes/${id}/labels`,
    nodeLabelKey: (id, key) => `/api/nodes/${id}/labels/${key}`,
    nodeMaxReplicasPerModel: (id) => `/api/nodes/${id}/max-replicas-per-model`,
    nodesScheduling: '/api/nodes/scheduling',
    nodesSchedulingModel: (model) => `/api/nodes/scheduling/${encodeURIComponent(model)}`,
  },
}

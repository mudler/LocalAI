// The core routes the Voice add-on uses, reached with the signed-in user's own
// session (sdk.core.fetch). The shapes are the ones of
// core/http/react-ui/src/utils/api.js; only the transport changed.
import { createIdentityApi } from '../../../internal/identity-ui/src/registry.js'

const enc = encodeURIComponent

export function createApi(ctx) {
  const { core, json } = ctx
  const query = (params = {}) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
    const s = q.toString()
    return s ? `?${s}` : ''
  }
  return {
    // /v1/voice/* and /v1/face/*: verify, analyze, embed, register, identify, forget.
    identity: createIdentityApi(ctx),
    voiceProfiles: {
      list: () => json('/api/voice-profiles'),
      create: (formData) => json('/api/voice-profiles', { method: 'POST', body: formData }),
      remove: (id) => json(`/api/voice-profiles/${enc(id)}`, { method: 'DELETE' }),
      // Used as <audio src>, so the browser sends the session cookie itself and
      // sdk.core.fetch is not involved; under a path prefix the prefix is added.
      audioUrl: (id) => `${ctx.basePath || ''}/api/voice-profiles/${enc(id)}/audio`,
    },
    models: {
      listCapabilities: () => json('/api/models/capabilities'),
      // The gallery list, as modelsApi.list builds it: API_CONFIG.endpoints.models
      // is '/api/models' and empty parameters are left out (utils/api.js buildUrl).
      list: (params) => json(`/api/models${query(params)}`),
      // Starts a gallery install (modelsApi.install: POST installModel(id)).
      install: (id) => json(`/api/models/install/${enc(id)}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }),
    },
    // The running installs, polled by the library while one is in progress.
    operations: {
      list: () => json('/api/operations'),
    },
    raw: core,
  }
}

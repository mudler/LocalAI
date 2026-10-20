// The core routes the Face add-on uses, reached with the signed-in user's own
// session (sdk.core.fetch). Paths and bodies are the ones of faceApi in
// core/http/react-ui/src/utils/api.js; only the transport changed. register
// takes { model, name, img, labels }, identify { model, img, ... }.
import { createIdentityApi } from '../../../internal/identity-ui/src/registry.js'

export function createApi(ctx) {
  const { json } = ctx
  const query = (params = {}) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
    const s = q.toString()
    return s ? `?${s}` : ''
  }
  // /v1/voice/* and /v1/face/*: the shared views read both kinds; the page only
  // uses the face one. A failed call keeps its status (a 404 on forget reads as
  // already gone).
  const identity = createIdentityApi(ctx)
  return {
    identity,
    face: identity.face,
    models: {
      listCapabilities: () => json('/api/models/capabilities'),
      // The gallery list, as modelsApi.list builds it: empty parameters are left out.
      list: (params) => json(`/api/models${query(params)}`),
      // Starts a gallery install (modelsApi.install).
      install: (id) => json(`/api/models/install/${encodeURIComponent(id)}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }),
    },
    // The running installs, polled while one is in progress.
    operations: {
      list: () => json('/api/operations'),
    },
    raw: ctx.core,
  }
}

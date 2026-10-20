// The core routes the quantization add-on uses, reached with the signed-in
// user's own session (sdk.core.fetch). Paths and bodies are the ones of
// quantizationApi and resourcesApi in core/http/react-ui/src/utils/api.js; only
// the transport changed.
export function createApi(ctx) {
  const { json } = ctx
  const enc = encodeURIComponent
  const post = (path, data) => json(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) })

  return {
    quantize: {
      listBackends: () => json('/api/quantization/backends'),
      startJob: (data) => post('/api/quantization/jobs', data),
      listJobs: () => json('/api/quantization/jobs'),
      getJob: (id) => json(`/api/quantization/jobs/${enc(id)}`),
      stopJob: (id) => json(`/api/quantization/jobs/${enc(id)}/stop`, { method: 'POST' }),
      deleteJob: (id) => json(`/api/quantization/jobs/${enc(id)}`, { method: 'DELETE' }),
      importModel: (id, data) => post(`/api/quantization/jobs/${enc(id)}/import`, data),
      // The response is a stream of events; the caller reads it with readSse
      // and aborts the signal to close it.
      progress: (id, signal) => ctx.core.fetch(`/api/quantization/jobs/${enc(id)}/progress`, { signal }),
    },
    // The one request the page makes for the machine facts of the checks.
    resources: {
      get: () => json('/api/resources'),
    },
    // The link the browser follows to download the result of a job.
    downloadHref: (id) => `${ctx.basePath}/api/quantization/jobs/${enc(id)}/download`,
  }
}

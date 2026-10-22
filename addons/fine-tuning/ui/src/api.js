// The core routes the fine-tuning add-on uses, reached with the signed-in
// user's own session (sdk.core.fetch). Paths and bodies are the ones of
// fineTuneApi and resourcesApi in core/http/react-ui/src/utils/api.js; only the
// transport changed.
export function createApi(ctx) {
  const { json } = ctx
  const enc = encodeURIComponent
  const post = (path, data) => json(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) })

  return {
    fineTune: {
      listBackends: () => json('/api/fine-tuning/backends'),
      startJob: (data) => post('/api/fine-tuning/jobs', data),
      listJobs: () => json('/api/fine-tuning/jobs'),
      getJob: (id) => json(`/api/fine-tuning/jobs/${enc(id)}`),
      stopJob: (id, saveCheckpoint) => json(`/api/fine-tuning/jobs/${enc(id)}/stop?save_checkpoint=${saveCheckpoint ? 'true' : 'false'}`, { method: 'POST' }),
      deleteJob: (id) => json(`/api/fine-tuning/jobs/${enc(id)}`, { method: 'DELETE' }),
      listCheckpoints: (id) => json(`/api/fine-tuning/jobs/${enc(id)}/checkpoints`),
      exportModel: (id, data) => post(`/api/fine-tuning/jobs/${enc(id)}/export`, data),
      // The browser sets the multipart boundary, so no Content-Type is given.
      uploadDataset: (file) => {
        const formData = new FormData()
        formData.append('file', file)
        return json('/api/fine-tuning/datasets', { method: 'POST', body: formData })
      },
      // The response is a stream of events; the caller reads it with readSse
      // and aborts the signal to close it.
      progress: (id, signal) => ctx.core.fetch(`/api/fine-tuning/jobs/${enc(id)}/progress`, { signal }),
    },
    // The one request useResources makes: the machine facts for the checks.
    resources: {
      get: () => json('/api/resources'),
    },
    // The link the browser follows to download the archive of a job.
    downloadHref: (id) => `${ctx.basePath}/api/fine-tuning/jobs/${enc(id)}/download`,
  }
}

// The first-run model recommendation, as GET /api/models/recommended answers it.
// The server picks the models, the window each one installs with and the fit
// verdict; this file only shapes that answer for the screen and builds the
// install request, so no screen keeps a copy of the rules.

// splitRecommended turns the server answer into what a screen draws: one
// primary choice and the alternatives. It never throws on a short or absent
// answer, so a screen can call it before the data has arrived.
export function splitRecommended(data) {
  const models = Array.isArray(data?.models) ? data.models : []
  return {
    tier: data?.tier || 'cpu',
    detected: data?.hardware?.detected !== false,
    scope: data?.hardware?.scope || 'local',
    nodeName: data?.hardware?.node_name || '',
    nothingFits: !!data?.nothing_fits,
    primary: models[0] || null,
    alternatives: models.slice(1),
  }
}

// windowLabel writes a context window as the short form people know:
// 32768 is "32K". A window the server could not compute (0) has no label.
export function windowLabel(tokens) {
  if (!Number.isFinite(tokens) || tokens <= 0) return ''
  if (tokens < 1024) return String(tokens)
  const k = tokens / 1024
  return `${Number.isInteger(k) ? k : k.toFixed(1)}K`
}

// installRequest is the argument list of modelsApi.install for a recommended
// model. The model is pinned to its own gallery entry, so what installs is the
// build whose memory was estimated and not a larger variant the host could
// also run. The window goes along, so the installed config carries it; when
// the server gave none, nothing is sent and the loader chooses.
export function installRequest(model) {
  return [model.id, model.id, model.context_size > 0 ? model.context_size : undefined]
}

// fitTone maps a fit verdict to a StatusChip tone.
export function fitTone(fit) {
  switch (fit) {
    case 'fits': return 'ok'
    case 'tight': return 'warn'
    case 'does_not_fit': return 'error'
    default: return 'idle'
  }
}

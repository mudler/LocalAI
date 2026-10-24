// Pure of React and of the network: the caller hands in the runner and the clients.
// The two steps of Install and turn on. `run` is the hook's own action runner,
// so each step has its own operation identity and keeps the unknown-outcome
// rule; `mark` receives where the flow is. Pure of React, so it is tested
// with a fake runner.
export async function runInstallAndTurnOn({ run, api, adapter, mark }, entry, { skipInstall = false } = {}) {
  if (!skipInstall) {
    mark({ step: 'install' })
    const installed = await run(`install:${entry.key}`, {
      addonId: entry.id,
      submit: (operationId, signal) => adapter.submitLifecycle({ operationId, action: 'install', addonId: entry.id, sourceId: entry.sourceId, version: entry.version }, signal),
      verify: async (signal) => (await api.installed(signal)).some(a => a.id === entry.id),
    })
    if (!installed) return null
    if (installed.outcome !== 'completed') { mark({ step: 'failed', failedStep: 'install', outcome: installed.outcome, error: installed.error }); return installed }
  }
  mark({ step: 'turn-on' })
  // Generation 0 sets no precondition: the add-on was just installed.
  const on = await run(`enable:${entry.id}:0`, {
    addonId: entry.id,
    submit: (operationId, signal) => adapter.submitLifecycle({ operationId, action: 'enable', addonId: entry.id, generation: 0 }, signal),
    verify: async (signal) => (await api.installed(signal)).some(a => a.id === entry.id && a.enabled === true),
  })
  if (!on) return null
  mark(on.outcome === 'completed' ? { step: 'done' } : { step: 'failed', failedStep: 'turn-on', outcome: on.outcome, error: on.error })
  return on
}

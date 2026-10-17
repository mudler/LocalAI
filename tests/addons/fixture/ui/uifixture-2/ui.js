// UI runtime fixture, version 2: the package an update installs.
const events = (window.__uiFixture = window.__uiFixture || { events: [] }).events
export function createAddon(sdk) {
  const h = sdk.React.createElement
  events.push('create-v2:' + sdk.generation)
  return {
    Component: () => h('section', null, h('h2', { 'data-testid': 'fx-title' }, 'Fixture version 2'), h('p', { 'data-testid': 'fx-generation' }, 'generation ' + sdk.generation + ' version 2')),
    chatActions: [],
    dispose() { events.push('dispose-v2:' + sdk.generation) },
  }
}

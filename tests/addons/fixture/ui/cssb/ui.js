// A second add-on whose style sheet uses the same class names as the first.
export function createAddon(sdk) {
  const h = sdk.React.createElement
  return {
    Component: () => h('section', null, h('h2', { className: 'title', 'data-testid': 'cssb-title' }, 'Second add-on')),
    dispose() {},
  }
}

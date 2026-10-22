// UI runtime fixture, version 1. Hand-written in the compiled form an add-on
// build produces: classic JSX turned into factory-scoped React.createElement
// calls over the React the host SDK hands in. There is no bare `react` import
// and no bundler at test time.
const events = (window.__uiFixture = window.__uiFixture || { events: [] }).events
const log = (name) => { events.push(name) }

export function createAddon(sdk) {
  const React = sdk.React
  const h = React.createElement
  const { Button, Stack, Text, Badge } = sdk.ui
  const generation = sdk.generation
  log('create:' + generation)
  sdk.signal.addEventListener('abort', () => log('abort:' + generation))
  sdk.onDispose(() => log('sdk-dispose:' + generation))
  sdk.locale.define('en', { greeting: 'Hello {{name}}' })
  sdk.locale.define('it', { greeting: 'Ciao {{name}}' })

  function Home(props) {
    const [count, setCount] = React.useState(0)
    const [draft, setDraft] = React.useState('')
    const [result, setResult] = React.useState('idle')
    React.useEffect(() => { log('home-mounted:' + generation); return () => log('home-unmounted:' + generation) }, [])
    const onInput = (event) => {
      setDraft(event.target.value)
      sdk.markDirty(event.target.value !== '')
    }
    const read = async () => {
      setResult('pending')
      try {
        const out = await sdk.api.read({ arguments: { q: draft } })
        setResult('ok:' + JSON.stringify(out))
      } catch (error) {
        setResult('error:' + (error && (error.kind || error.name)) + ':' + (error && error.status))
      }
    }
    return h('section', { className: 'fx-home' },
      h('h2', { className: 'title', 'data-testid': 'fx-title' }, sdk.locale.t('greeting', 'Hello {{name}}', { name: 'fixture' })),
      h('p', { 'data-testid': 'fx-where' }, 'page=' + props.page + ' path=' + props.path),
      h('p', { 'data-testid': 'fx-generation' }, 'generation ' + generation + ' version 1'),
      h(Stack, null,
        h(Badge, { tone: 'healthy' }, 'hooks ' + count),
        h(Button, { onClick: () => setCount((n) => n + 1) }, 'Increment'),
        h('label', null, 'Draft ', h('input', { 'data-testid': 'fx-draft', value: draft, onChange: onInput })),
        h(Button, { onClick: read }, 'Read'),
        h(Button, { onClick: () => { sdk.markDirty(false); setDraft('') } }, 'Save draft'),
        h(Button, { onClick: () => sdk.navigation.navigate('dialog/opened?from=home') }, 'Go to dialog'),
        h(Button, { onClick: () => { try { sdk.navigation.navigate('/app/settings') } catch (e) { setResult('refused:' + e.code) } } }, 'Leave'),
        h('p', { 'data-testid': 'fx-result' }, result),
        h(sdk.Icon, { name: 'table', title: 'table' })));
  }

  function Boom() {
    throw new Error('fixture render failure')
  }

  function Dialog(props) {
    return h('section', null,
      h('h2', { className: 'title' }, 'Dialog page'),
      h('p', { 'data-testid': 'fx-where' }, 'page=' + props.page + ' path=' + props.path),
      h(Button, {
        onClick: () => sdk.layers.openDialog({
          title: 'Fixture dialog',
          render: ({ close }) => h('div', null, h('p', null, 'Opened by the add-on.'), h(Button, { onClick: close }, 'Done')),
        }),
      }, 'Open dialog'),
      h(Button, {
        onClick: () => sdk.layers.openDialog({
          title: 'Broken dialog',
          render: () => { throw new Error('fixture layer failure') },
        }),
      }, 'Open broken dialog'),
      h(Button, {
        onClick: () => sdk.layers.openPanel({
          title: 'Broken panel',
          render: () => { throw new Error('fixture panel failure') },
        }),
      }, 'Open broken panel'))
  }

  function Root(props) {
    if (props.page === 'boom') return h(Boom)
    if (props.page === 'dialog') return h(Dialog, props)
    return h(Home, props)
  }

  return {
    Component: Root,
    chatActions: [{
      id: 'summarize', label: 'Summarize conversation', icon: 'sparkles',
      open: (context) => { log('chat-action:' + JSON.stringify(context)) },
    }],
    dispose() { log('dispose:' + generation) },
  }
}

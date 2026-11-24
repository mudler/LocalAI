import test from 'node:test'
import assert from 'node:assert/strict'
import { React, renderToString, makeSdk } from './host.mjs'
import { setSdk } from '../src/runtime.js'

// The router stand-in, driven the way a page uses it: hooks inside a
// RouteProvider, a Link, and the calls that reach the shell.
async function setup(addonId, pages) {
  const host = await makeSdk({ addonId, pages })
  setSdk(host.sdk)
  const router = await import('../src/modules/react-router-dom.js')
  return { ...host, router }
}

function capture(router, route, element) {
  const seen = {}
  function Probe() {
    seen.navigate = router.useNavigate()
    seen.params = router.useParams()
    seen.search = router.useSearchParams()
    seen.location = router.useLocation()
    seen.outlet = router.useOutletContext()
    return element ? element(router) : null
  }
  const html = renderToString(React.createElement(router.RouteProvider, { value: route }, React.createElement(Probe)))
  return { seen, html }
}

test('useParams and useSearchParams read the route the shell passed', async () => {
  const { router } = await setup('studio', ['studio'])
  const { seen } = capture(router, { page: 'studio', path: 'images', search: 'model=a%2Fb&work=w1' })
  assert.deepEqual(seen.params, { tab: 'images', model: 'a/b' })
  assert.equal(seen.search[0].get('work'), 'w1')
  assert.equal(seen.location.search, '?model=a%2Fb&work=w1')
  assert.equal(seen.location.pathname, '/app/addons/studio/studio/images')
})

test('navigate to an address of the add-on stays inside it, with the model in the query', async () => {
  const { router, navigations } = await setup('studio', ['studio'])
  const { seen } = capture(router, { page: 'studio', path: '', search: '' })
  seen.navigate('/app/studio/images/some%2Fmodel')
  seen.navigate('/app/studio', { replace: true })
  assert.deepEqual(navigations, [
    ['/app/addons/studio/studio/images?model=some%2Fmodel', { replace: false }],
    ['/app/addons/studio/studio', { replace: true }],
  ])
})

test('navigate to a core page, or to the other add-on, opens it, and a bad address throws', async () => {
  const { router, navigations } = await setup('talk', ['talk'])
  const { seen } = capture(router, { page: 'talk', path: '', search: '' })
  seen.navigate('/app/models')
  seen.navigate('/app/model-editor/x', { state: { from: '/app/addons/talk/talk', fromLabel: 'Talk' } })
  seen.navigate('/app/studio/diarization')
  assert.deepEqual(navigations, [
    ['/app/models', { replace: false }],
    ['/app/model-editor/x', { replace: false, state: { from: '/app/addons/talk/talk', fromLabel: 'Talk' } }],
    ['/app/addons/studio/studio/diarization', { replace: false }],
  ])
  assert.throws(() => seen.navigate('/api/models'), /Cannot open/)
})

test('setSearchParams keeps the page and the tab and replaces the query', async () => {
  const { router, navigations } = await setup('studio', ['studio'])
  const { seen } = capture(router, { page: 'studio', path: 'tts', search: 'a=1' })
  seen.search[1]({ work: 'w2' })
  seen.search[1]({})
  seen.search[1]((prev) => ({ a: prev.get('a'), b: '2' }), { replace: true })
  assert.deepEqual(navigations, [
    ['/app/addons/studio/studio/tts?work=w2', { replace: false }],
    ['/app/addons/studio/studio/tts', { replace: false }],
    ['/app/addons/studio/studio/tts?a=1&b=2', { replace: true }],
  ])
})

test('addToast of the outlet context reaches the SDK notices, and an unknown kind is an info', async () => {
  const { router, notices } = await setup('studio', ['studio'])
  const { seen } = capture(router, { page: 'studio', path: '', search: '' })
  seen.outlet.addToast('saved', 'success')
  seen.outlet.addToast('careful', 'warning')
  seen.outlet.addToast('plain')
  seen.outlet.addToast('odd', 'weird')
  assert.deepEqual(notices, [['success', 'saved'], ['warning', 'careful'], ['info', 'plain'], ['info', 'odd']])
})

test('a Link draws a real address and navigates on a plain click only', async () => {
  const { router, navigations } = await setup('studio', ['studio'])
  const { html, seen } = capture(router, { page: 'studio', path: '', search: '' }, (r) => React.createElement(r.Link, { to: '/app/studio/images/m', className: 'ws-link' }, 'Open'))
  assert.match(html, /<a [^>]*href="\/app\/addons\/studio\/studio\/images\?model=m"[^>]*class="ws-link"[^>]*>Open<\/a>|<a [^>]*class="ws-link"[^>]*href="\/app\/addons\/studio\/studio\/images\?model=m"[^>]*>Open<\/a>/)
  assert.ok(seen)
  // The click handler, taken from the element the Link makes.
  const link = router.Link({ to: '/app/models', children: 'x' })
  const plain = { defaultPrevented: false, button: 0, preventDefault() { this.defaultPrevented = true } }
  link.props.onClick(plain)
  assert.equal(plain.defaultPrevented, true)
  link.props.onClick({ ...plain, defaultPrevented: false, ctrlKey: true, preventDefault() { throw new Error('a modified click must open in a tab') } })
  assert.deepEqual(navigations, [['/app/models', { replace: false }]])
})

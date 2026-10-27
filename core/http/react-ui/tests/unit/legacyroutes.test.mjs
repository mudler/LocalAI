import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { addonPageAddress, chatsRedirect, STUDIO_TABS, studioAddress, studioRedirect, talkAddress, MIGRATION_ROUTES } from '../../src/utils/legacyRoutes.js'

const here = dirname(fileURLToPath(import.meta.url))
const router = readFileSync(join(here, '../../src/router.jsx'), 'utf8')

test('studioRedirect opens the Studio add-on and keeps the model, the query and the hash', () => {
  // A model id may hold "/" or ":", which an add-on path segment cannot, so it travels as ?model=.
  assert.equal(studioRedirect('image', 'stable/diffusion', '?x=1', '#top'), '/app/addons/studio/studio/images?x=1&model=stable%2Fdiffusion#top')
  assert.equal(studioRedirect('3d', undefined, '', ''), '/app/addons/studio/studio/threed')
  assert.equal(studioRedirect('tts', 'piper', '?voice=a', ''), '/app/addons/studio/studio/tts?voice=a&model=piper')
})

test('studioAddress and talkAddress build the add-on page addresses', () => {
  assert.equal(studioAddress(), '/app/addons/studio/studio')
  assert.equal(studioAddress({ tab: 'images', model: 'a b', search: '?work=1' }), '/app/addons/studio/studio/images?work=1&model=a+b')
  assert.equal(studioAddress({ search: '?work=w%201' }), '/app/addons/studio/studio?work=w+1')
  assert.equal(talkAddress(), '/app/addons/talk/talk')
  assert.equal(talkAddress({ search: '?model=x', hash: '#h' }), '/app/addons/talk/talk?model=x#h')
})

test('every standalone generator route maps to a Studio tab', () => {
  assert.deepEqual(Object.keys(STUDIO_TABS).sort(), ['3d', 'diarization', 'image', 'sound', 'transform', 'tts', 'video'])
})

test('every moved row targets what studioRedirect builds for its own generator', () => {
  const moved = MIGRATION_ROUTES.filter(r => r.kind === 'studio-tab')
  assert.equal(moved.length, 7)
  for (const r of moved) {
    const key = r.from.replace('/app/', '')
    assert.equal(r.to, studioRedirect(key), `${r.from} targets ${r.to}`)
  }
})

test('each moved route has its own redirect entry, plain and with a model, bound to its own generator', () => {
  const lines = router.split('\n')
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'studio-tab')) {
    const key = r.from.replace('/app/', '')
    for (const path of [key, `${key}/:model`]) {
      const entry = lines.find(l => l.includes(`{ path: '${path}', element:`))
      assert.ok(entry, `router.jsx has no entry for path ${path}`)
      assert.ok(entry.includes(`<StudioTabRedirect legacy="${key}" />`), `entry for ${path} is not bound to legacy="${key}": ${entry.trim()}`)
    }
  }
})

test('the Studio and Talk routes redirect to their add-ons, with or without a tab and a model', () => {
  for (const path of ["path: 'studio'", "path: 'studio/:tab'", "path: 'studio/:tab/:model'"]) {
    assert.ok(router.includes(`{ ${path}, element: <StudioRedirect /> }`), `no StudioRedirect for ${path}`)
  }
  assert.ok(router.includes("{ path: 'talk', element: <TalkRedirect /> }"))
  assert.ok(MIGRATION_ROUTES.some(r => r.from === '/app/studio' && r.to === '/app/addons/studio/studio' && r.kind === 'addon-page'))
  assert.ok(MIGRATION_ROUTES.some(r => r.from === '/app/talk' && r.to === '/app/addons/talk/talk' && r.kind === 'addon-page'))
})

test('the router imports no Studio or Talk page: both are add-ons', () => {
  assert.ok(!/pages\/(Studio|Talk|ImageGen|VideoGen|ThreeDGen|TTS|Sound|Diarization|AudioTransform)'/.test(router))
})

test('the same-path routes need no redirect', () => {
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'same')) assert.equal(r.from, r.to)
})

test('moved routes redirect to their add-on page, with no built-in page behind them', () => {
  // Studio and Talk have their own redirects (see above).
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'addon-page' && !['/app/studio', '/app/talk'].includes(x.from))) {
    const [, , , addon, ...rest] = r.to.split('/')
    const page = rest.join('/')
    assert.ok(router.includes(`<AddonRedirect addon="${addon}" page="${page}" />`) || router.includes(`<AddonRedirect addon="${addon}" page="${page}" withModel />`), `router.jsx does not redirect ${r.from}`)
  }
  assert.ok(!router.includes('MovedPage'))
  assert.ok(!/pages\/(FaceRecognition|VoiceRecognition|VoiceLibrary|VoiceProfileCreate|FineTune|Quantize)'/.test(router))
})

test('addonPageAddress keeps the page path, the model, the query and the hash', () => {
  assert.equal(addonPageAddress({ addon: 'voice', page: 'voice/recognition' }), '/app/addons/voice/voice/recognition')
  assert.equal(addonPageAddress({ addon: 'face', page: 'recognition', model: 'org/name', search: '?x=1', hash: '#h' }), '/app/addons/face/recognition?x=1&model=org%2Fname#h')
})

test('a moved route with a :model segment carries the model as ?model=, not in the path', () => {
  assert.ok(router.includes('withModel'), 'the model routes must use withModel')
  assert.ok(!/\/app\/addons\/\$\{addon\}\/\$\{page\}\/\$\{model\}/.test(router), 'the model must not be appended to the path')
})

test('the face route and the face model route both open the Face add-on, the model as ?model=', () => {
  assert.ok(MIGRATION_ROUTES.some(r => r.from === '/app/face' && r.to === '/app/addons/face/recognition' && r.kind === 'addon-page'))
  const lines = router.split('\n')
  const plain = lines.find(l => l.includes("{ path: 'face', element:"))
  const withModel = lines.find(l => l.includes("{ path: 'face/:model', element:"))
  assert.ok(plain?.includes('<AddonRedirect addon="face" page="recognition" />'), 'face does not redirect')
  assert.ok(withModel?.includes('<AddonRedirect addon="face" page="recognition" withModel />'), 'face/:model does not redirect with withModel')
})

test('the Build hub has no Faces tab', async () => {
  const { buildHub } = await import('../../src/components/hub/hubConfig.js')
  assert.ok(!buildHub.tabs.some(t => t.id === 'faces' || t.id === 'voices'))
})

test('the fine-tune and quantize routes redirect to their add-on pages', () => {
  const lines = router.split('\n')
  const rows = [
    { from: '/app/fine-tune', to: '/app/addons/fine-tuning/jobs', addon: 'fine-tuning', tool: 'fineTune', feature: 'fine_tuning', path: 'fine-tune' },
    { from: '/app/quantize', to: '/app/addons/quantization/jobs', addon: 'quantization', tool: 'quantize', feature: 'quantization', path: 'quantize' },
  ]
  for (const r of rows) {
    assert.ok(MIGRATION_ROUTES.some(m => m.from === r.from && m.to === r.to && m.kind === 'addon-page'), `no row for ${r.from}`)
    const entry = lines.find(l => l.includes(`{ path: '${r.path}', element:`))
    assert.ok(entry?.includes(`<AddonRedirect addon="${r.addon}" page="jobs" />`), `${r.path} does not redirect`)
  }
})

test('the Build hub has no fine-tune or quantize tab', async () => {
  const { buildHub } = await import('../../src/components/hub/hubConfig.js')
  assert.ok(!buildHub.tabs.some(t => t.id === 'fine-tune' || t.id === 'quantize'))
})

test('the Build overview keeps the fine-tune and quantize cards, and links the agents, triggers and skills of the chats', async () => {
  const { buildOverviewTools } = await import('../../src/components/hub/hubConfig.js')
  assert.deepEqual(buildOverviewTools.map(t => [t.id, t.items[0].path]), [
    ['agents', '/app/chats/agents'], ['triggers', '/app/chats/triggers'], ['skills', '/app/chats/skills'],
    ['fine-tune', '/app/fine-tune'], ['quantize', '/app/quantize'],
  ])
})

test('the voice routes open the tabs of the one Voice page', () => {
  const to = (from) => MIGRATION_ROUTES.find(r => r.from === from)?.to
  assert.equal(to('/app/voice'), '/app/addons/voice/voice/recognition')
  assert.equal(to('/app/voice-library'), '/app/addons/voice/voice/library')
  assert.equal(to('/app/voice-library/new'), '/app/addons/voice/voice/library/new')
})

test('chatsRedirect opens the matching page of the chats and keeps the query and the hash', () => {
  assert.equal(chatsRedirect('/app/chat'), '/app/chats')
  assert.equal(chatsRedirect('/app/chat/'), '/app/chats')
  assert.equal(chatsRedirect('/app/chat/llama', '?x=1', '#top'), '/app/chats?x=1&model=llama#top')
  // A model id may hold "/" or ":", so it travels as ?model= and is decoded first.
  assert.equal(chatsRedirect('/app/chat/org%2Fname%3Aq4'), '/app/chats?model=org%2Fname%3Aq4')
  assert.equal(chatsRedirect('/app/agents'), '/app/chats/agents')
  assert.equal(chatsRedirect('/app/agents/Helper/edit', '?a=b'), '/app/chats/agents?a=b')
  assert.equal(chatsRedirect('/app/agents/new'), '/app/chats/agents')
  assert.equal(chatsRedirect('/app/agent-jobs'), '/app/chats/triggers')
  assert.equal(chatsRedirect('/app/agent-jobs/jobs/j1'), '/app/chats/triggers')
  assert.equal(chatsRedirect('/app/skills'), '/app/chats/skills')
  assert.equal(chatsRedirect('/app/skills/edit/web'), '/app/chats/skills')
})

test('every old chat, agent, job and skill route redirects, and no built-in page is left behind', () => {
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'chats')) {
    assert.equal(chatsRedirect(r.from), r.to, r.from)
  }
  for (const path of ["'chat'", "'chat/:model'", "'agents'", "'agents/*'", "'skills'", "'skills/*'", "'agent-jobs'", "'agent-jobs/*'"]) {
    const entry = router.split('\n').find(l => l.includes(`{ path: ${path}, element:`))
    assert.ok(entry?.includes('<ChatsRedirect />'), `no ChatsRedirect for ${path}`)
  }
  assert.ok(!/pages\/(Chat|Agents|AgentCreate|AgentPage|AgentRun|AgentStatus|Skills|SkillEdit|AgentJobs|AgentTaskDetails|AgentJobDetails)'/.test(router))
})

test('chatsRedirect keeps a model name that is not valid percent-encoding as typed', () => {
  assert.equal(chatsRedirect('/app/chat/100%'), '/app/chats?model=100%25')
  assert.equal(chatsRedirect('/app/chat/a%zzb'), '/app/chats?model=a%25zzb')
})

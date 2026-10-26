// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'

// Run the real streaming hook with synchronous hook slots. Browser-only imports
// and effects are replaced; fetch still returns real ReadableStream responses.
function mount() {
  const slots = []
  let cursor = 0
  let now = 1000
  let streams = []
  const context = vm.createContext({
    useState(initial) {
      const i = cursor++
      if (!(i in slots)) slots[i] = typeof initial === 'function' ? initial() : initial
      return [slots[i], value => { slots[i] = typeof value === 'function' ? value(slots[i]) : value }]
    },
    useRef(initial) {
      const i = cursor++
      return slots[i] ??= { current: initial }
    },
    useCallback: fn => fn, useEffect() {}, useDebouncedEffect() {},
    useContext: () => null, RegistryContext: {}, useAuth: () => ({ user: null }),
    chatsStorageKey: () => 'chats', readChatStore: () => null,
    effectiveSystemPrompt: text => text, toolAddons: () => [],
    addonRequestMetadata: () => ({}), chatHeaders: () => ({}), storedResult: () => null,
    API_CONFIG: { endpoints: { chatCompletions: '/v1/chat/completions' } }, apiUrl: path => path,
    Date: class extends Date { static now() { return now } },
    Math, generateId: () => 'chat1', AbortController, TextDecoder, TextDecoderStream, setTimeout, clearTimeout,
    fetch: async () => {
      const next = streams.shift()
      now += next.duration
      if (next.error) throw next.error
      return new Response(next.events.map(e => `data: ${JSON.stringify(e)}\n\n`).join('') + 'data: [DONE]\n\n')
    },
  })
  const source = readFileSync(new URL('../../src/hooks/useChat.js', import.meta.url), 'utf8')
  vm.runInContext(source.replace(/^import .*\n/gm, '').replace('export function useChat', 'function useChat'), context)
  const render = () => { cursor = 0; return context.useChat('test-model') }
  return {
    render,
    async send(responses, options = {}) {
      streams = responses
      await render().sendMessage('hello', [], options)
      return render()
    },
  }
}

const delta = value => ({ choices: [{ delta: value }] })
const usage = count => ({ choices: [], usage: { completion_tokens: count } })

test('chat throughput: multi-token chunks use reported totals including reasoning', async () => {
  const state = await mount().send([{ duration: 2000, events: [
    delta({ reasoning: 'many reasoning tokens' }), delta({ content: 'many answer tokens' }), usage(100),
  ] }])
  assert.equal(state.tokensPerSecond, 50)
  assert.equal(state.throughputIsEstimated, false)
})

test('chat throughput: missing usage uses a character estimate independent of chunk boundaries', async () => {
  for (const content of [['12345678'], ['1', '234', '5678']]) {
    const state = await mount().send([{ duration: 2000, events: content.map(text => delta({ content: text })) }])
    assert.equal(state.tokensPerSecond, 1)
    assert.equal(state.throughputIsEstimated, true)
  }
})

test('chat throughput: zero usage is valid and repeated totals are not added', async () => {
  for (const count of [0, 100]) {
    const state = await mount().send([{ duration: 2000, events: [delta({ content: 'text' }), usage(count), usage(count)] }])
    assert.equal(state.tokensPerSecond, count / 2)
    assert.equal(state.throughputIsEstimated, false)
  }
})

test('chat throughput: tool turns sum completion totals across the full elapsed time', async () => {
  const state = await mount().send([
    { duration: 2000, events: [delta({ tool_calls: [{ index: 0, id: 't1', type: 'function', function: { name: 'lookup', arguments: '{}' } }] }),
      { choices: [{ delta: {}, finish_reason: 'tool_calls' }] }, usage(20)] },
    { duration: 2000, events: [delta({ content: 'done' }), usage(100)] },
  ], { isClientTool: () => true, executeTool: async () => 'result' })
  assert.equal(state.tokensPerSecond, 30)
  assert.equal(state.throughputIsEstimated, false)
})

test('chat throughput: a later turn without usage cannot reuse an earlier total', async () => {
  const state = await mount().send([
    { duration: 2000, events: [delta({ tool_calls: [{ index: 0, id: 't1', type: 'function', function: { name: 'lookup', arguments: '{}' } }] }),
      { choices: [{ delta: {}, finish_reason: 'tool_calls' }] }, usage(20)] },
    { duration: 2000, events: [delta({ content: '1234567890123456' })] },
  ], { isClientTool: () => true, executeTool: async () => 'result' })
  assert.equal(state.tokensPerSecond, 1)
  assert.equal(state.throughputIsEstimated, true)
})

test('chat throughput: failed and aborted requests never show reported throughput', async () => {
  for (const name of ['Error', 'AbortError']) {
    const error = new Error('request interrupted')
    error.name = name
    const state = await mount().send([{ duration: 2000, error }])
    assert.equal(state.throughputIsEstimated, true)
  }
})

test('chat throughput: zero elapsed time does not produce an infinite rate', async () => {
  const state = await mount().send([{ duration: 0, events: [delta({ content: 'answer' }), usage(100)] }])
  assert.equal(state.tokensPerSecond, null)
})

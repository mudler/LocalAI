// The status line under the chat composer: which one state it shows, and the
// figures of its context meter. Pure functions, so the rules are tested on
// their own (tests/unit/chatfoot.test.mjs).

// Every state the line can show. `busy` states have a spinner and mean a turn
// is on its way; the others are still.
//   tone   idle | accent | warn | error
export const FOOT_STATES = {
  ready: { tone: 'idle', busy: false, icon: null },
  nomodel: { tone: 'idle', busy: false, icon: null },
  sending: { tone: 'accent', busy: true, icon: 'spinner' },
  loading: { tone: 'accent', busy: true, icon: 'spinner' },
  thinking: { tone: 'accent', busy: true, icon: 'spinner' },
  tool: { tone: 'accent', busy: true, icon: 'spinner' },
  answering: { tone: 'accent', busy: true, icon: 'spinner' },
  waiting: { tone: 'warn', busy: false, icon: 'clock' },
  stopped: { tone: 'idle', busy: false, icon: 'stop' },
  failed: { tone: 'error', busy: false, icon: 'alert-circle' },
  interrupted: { tone: 'warn', busy: false, icon: 'warning' },
  // The tool loop reached its step limit: the work may not be finished, and
  // Continue lets it go on.
  paused: { tone: 'warn', busy: false, icon: 'pause' },
  // A context command the person ran (/compact, /clear) is on its way.
  compacting: { tone: 'accent', busy: true, icon: 'spinner' },
  clearing: { tone: 'accent', busy: true, icon: 'spinner' },
}

import { toolLabel } from './toolLabels.js'

// The short name of a tool for the line: the last part of a namespaced id
// ("addon__sandbox__bash" is "bash").
export function toolShortName(name) {
  const parts = String(name || '').split('__').filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
}

// The one state of the line.
//
//   activeId  the open chat, or null for a new chat
//   noModel   no chat model is installed
//   running   the server says a turn runs
//   sending   the page is sending a message and no turn runs yet
//   lastTurn  how the last turn ended: '', done, stopped, failed, interrupted,
//             paused (the step limit)
//   pending   the messages that wait for the model
//   live      the live turn: { text, reasoning, tools, phase }; phase is the
//             kind of the last streamed event (text, reasoning, tool, after-tool)
//   warm      the chat's model is loaded
//   command   a context command on its way: 'compacting' or 'clearing'
//
// A running turn always wins. With no turn running a failure comes first, then
// an interruption, then messages waiting for Send now, then a pause at the step
// limit, then a stop.
export function chatFootState({ activeId, noModel, running, sending, lastTurn, pending, live, warm, command } = {}) {
  const make = (key, extra = {}) => ({ key, ...FOOT_STATES[key], ...extra })
  if (!running && (command === 'compacting' || command === 'clearing')) return make(command)
  if (running) {
    const tools = live?.tools || []
    const open = [...tools].reverse().find((tl) => tl.state === 'running')
    const phase = live?.phase || ''
    // A tool that has started and not ended holds the turn, whatever came before.
    if (open) {
      const label = toolLabel(open.name, open.summary, '')
      return make('tool', { tool: toolShortName(open.name), verb: label.verb, arg: label.arg })
    }
    if (phase === 'text') return make('answering')
    if (phase === 'reasoning' || phase === 'after-tool') return make('thinking')
    if (!warm && !live?.text && !live?.reasoning && tools.length === 0) return make('loading')
    return make('thinking')
  }
  if (sending) return make('sending')
  if (noModel && !activeId) return make('nomodel')
  if (!activeId) return make('ready')
  if (lastTurn === 'failed') return make('failed')
  if (lastTurn === 'interrupted') return make('interrupted')
  if ((pending?.length || 0) > 0) return make('waiting', { count: pending.length })
  if (lastTurn === 'paused') return make('paused')
  if (lastTurn === 'stopped') return make('stopped')
  return make('ready')
}

// A speed for the line: whole numbers from 10 tok/s, one decimal below. The
// line does not jitter on the tenths of a fast model.
export function speedValue(n) {
  if (!(n > 0)) return null
  return n >= 10 ? Math.round(n) : Math.round(n * 10) / 10
}

// The speed the page estimates while a request streams: the streamed pieces
// per second since the first piece of this request. The server sends the
// measured figure only when the request ends. Null until there is enough to
// say (at least 4 pieces over at least a second).
//   pace  { reqAt, lastAt, reqChunks } (times in ms)
export function liveRate(pace) {
  if (!pace || !pace.reqAt || !pace.lastAt || pace.reqChunks < 4) return null
  const sec = (pace.lastAt - pace.reqAt) / 1000
  if (sec < 1) return null
  return (pace.reqChunks - 1) / sec
}

// What the meter says about speed now.
//   live      the page's estimate (liveRate) while a request streams, or null
//   measured  tokens_per_second of the last request, or 0
//   running   a turn runs
// Returns null when there is nothing to say, else
//   { value, approx (an estimate), stale (the turn has ended) }.
export function speedView({ live, measured, running }) {
  if (running && live > 0) return { value: speedValue(live), approx: true, stale: false }
  const m = speedValue(measured)
  if (m === null) return null
  return { value: m, approx: false, stale: !running }
}

// The average measured speed of the requests the page has seen in this chat
// (the last 10). Null with fewer than two figures: one figure is not an
// average.
export function averageSpeed(history) {
  const speeds = (history || []).map((u) => u?.tokens_per_second).filter((s) => s > 0).slice(-10)
  if (speeds.length < 2) return null
  return { value: speedValue(speeds.reduce((a, b) => a + b, 0) / speeds.length), count: speeds.length }
}

// Keeps the last 10 usage readings.
export function pushUsage(history, usage) {
  return [...(history || []), usage].slice(-10)
}

// Whether the keyboard hint shows: the composer has focus and is empty, and
// the chat is idle (while a turn runs, Enter queues a message instead).
export function showHint({ focused, empty, state }) {
  return Boolean(focused && empty && state && (state.key === 'ready' || state.key === 'stopped'))
}

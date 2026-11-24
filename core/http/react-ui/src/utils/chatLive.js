// The live turn of a chat and how it merges with the transcript.
//
// A running turn shows its tool calls from the events. The transcript is the
// record and is read again only at some moments (a turn starts or ends, a
// waiting message is delivered, the stream reconnects). Both are drawn as one
// flat list with stable keys, so a tool call is the same element in both: the
// frame of an MCP App is not unmounted (and so not reloaded) when the live card
// gives way to the transcript line of the same call.
//
// A call is named by the sequence number of its transcript entry. The server
// puts it on the tool_end event.

// A turn is a list of rounds: the model thinks, may say a word about what it is
// about to do, then calls a tool, and again. The round that is still open has
// its reasoning and text in live.reasoning and live.text. A tool_start closes
// it: what was said moves into live.beats, with the number of calls that came
// before it (at), so the order of thoughts, notes and calls can be drawn again
// from the three lists. Text after the last call is the answer.
export const emptyLive = () => ({ text: '', reasoning: '', tools: [], beats: [], openAt: 0, ended: false, endedAt: 0 })

const stamp = (now) => (Number.isFinite(now) ? now : Date.now())

// Closes the open round into a beat. A round with nothing in it leaves no beat.
function closeRound(live, now) {
  const reasoning = (live.reasoning || '').trim()
  const note = (live.text || '').trim()
  if (!reasoning && !note) return { ...live, reasoning: '', text: '', openAt: 0 }
  const beat = { reasoning, note, at: live.tools.length, start: live.openAt || 0, end: stamp(now) }
  return { ...live, beats: [...(live.beats || []), beat], reasoning: '', text: '', openAt: 0 }
}

// A piece of reasoning. Reasoning that arrives after prose in the same round
// means the prose was not the answer: the round closes and a new one opens.
export function reasoningDelta(live, text, now) {
  if (!text) return live
  const l = live.text ? closeRound(live, now) : live
  return { ...l, reasoning: l.reasoning + text, openAt: l.openAt || stamp(now) }
}

// A piece of prose. It is the answer until a tool call follows it.
export function textDelta(live, text, now) {
  if (!text) return live
  return { ...live, text: live.text + text, openAt: live.openAt || stamp(now) }
}

// The key of a tool call, in the live turn and in the transcript.
export const toolKey = (seq) => `tool-${seq}`

export function toolStart(live, tool, now) {
  const l = closeRound(live, now)
  return {
    ...l,
    tools: [...l.tools, { name: tool?.name || '', summary: tool?.summary || '', state: 'running', appState: '', startedAt: stamp(now), endedAt: 0 }],
  }
}

// An app whose card can be read: the call worked, has an app and has a transcript entry.
export const liveCardWanted = (tool) => Boolean(tool && tool.ok && tool.app && tool.seq > 0)

// Puts the card that asks to run a command on this machine on the latest
// running bash or job call. The answer goes to chatId. The card of a job start
// says that the approval also covers the input of the job.
export function consentAsk(live, chatId, id, command) {
  const tools = [...live.tools]
  for (let i = tools.length - 1; i >= 0; i--) {
    if (tools[i].state === 'running' && (tools[i].name === 'bash' || tools[i].name === 'job')) {
      tools[i] = { ...tools[i], consent: { chatId, id, command: command || '', job: tools[i].name === 'job' } }
      return { ...live, tools }
    }
  }
  return live
}

// Marks the latest running call of that name as ended.
export function toolEnd(live, tool, now) {
  const tools = [...live.tools]
  const name = tool?.name || ''
  for (let i = tools.length - 1; i >= 0; i--) {
    if (tools[i].state === 'running' && tools[i].name === name) {
      const hasApp = Boolean(tool?.ok && tool?.app)
      tools[i] = {
        ...tools[i],
        consent: undefined,
        state: tool?.ok ? 'done' : 'failed',
        endedAt: stamp(now),
        output: tool?.output || '',
        seq: tool?.seq || 0,
        appRef: hasApp ? tool.app : undefined,
        appState: hasApp ? (liveCardWanted(tool) ? 'loading' : 'failed') : '',
      }
      break
    }
  }
  return { ...live, tools }
}

// Records the card read for the call with this seq, or its failure (card null).
// It returns the same object when no call of the turn has that seq.
export function cardResult(live, seq, card) {
  const i = live.tools.findIndex((t) => t.seq === seq && t.appState === 'loading')
  if (i < 0) return live
  const tools = [...live.tools]
  tools[i] = card ? { ...tools[i], app: card, appState: 'ready' } : { ...tools[i], app: undefined, appState: 'failed' }
  return { ...live, tools }
}

// The app of the transcript line with this seq, from lines read with
// GET .../messages?after=<seq-1>&limit=1.
export function cardFromMessages(messages, seq) {
  const m = (messages || []).find((x) => x.seq === seq)
  return m?.app || null
}

// The key of a transcript line.
const messageKey = (m) => (m.role === 'tool' ? toolKey(m.seq) : m.seq)

// One flat list to draw: the transcript lines, then what only the live turn has.
// A live call that the transcript already holds is left out, so it shows once,
// as the transcript line that has the same key. Live calls without an app are
// grouped in runs; a call with an app is an item of its own.
export function conversationItems(messages, live) {
  const items = messages.map((m) => ({ key: messageKey(m), type: 'message', m }))
  const stored = new Set(messages.filter((m) => m.role === 'tool').map((m) => m.seq))
  if (live.reasoning) items.push({ key: 'live-reasoning', type: 'reasoning', text: live.reasoning })
  let run = null
  live.tools.forEach((tool, i) => {
    if (tool.seq && stored.has(tool.seq)) return
    if (tool.appRef) {
      run = null
      items.push({ key: tool.seq ? toolKey(tool.seq) : `live-tool-${i}`, type: 'app', tool })
      return
    }
    if (!run) {
      run = { key: `live-tools-${i}`, type: 'tools', tools: [] }
      items.push(run)
    }
    run.tools.push(tool)
  })
  if (live.text) items.push({ key: 'live-text', type: 'text', text: live.text })
  return items
}

// Calls in a row, with no thought or note between them, group from this many.
export const GROUP_MIN = 3

// The turn in the order it happened: thoughts, notes and calls, then the open
// thought. Calls with an app are rows of their own (threadRows) and are left
// out. The keys are stable: the open round's thought and text have the keys
// that the beat gets when the round closes, so the text that was the answer so
// far is the same element when it becomes a note.
export function liveSteps(live) {
  const beats = live?.beats || []
  const tools = live?.tools || []
  const steps = []
  let b = 0
  const beatsUpTo = (n) => {
    while (b < beats.length && beats[b].at <= n) {
      const beat = beats[b]
      if (beat.reasoning) steps.push({ kind: 'thought', key: `h${b}`, text: beat.reasoning, ms: beat.start ? beat.end - beat.start : null })
      if (beat.note) steps.push({ kind: 'note', key: `t${b}`, text: beat.note })
      b++
    }
  }
  tools.forEach((tool, i) => {
    beatsUpTo(i)
    if (!tool.appRef) steps.push({ kind: 'tool', key: `l${i}`, tool, ms: tool.endedAt ? tool.endedAt - tool.startedAt : null })
  })
  beatsUpTo(Infinity)
  if (live?.reasoning) steps.push({ kind: 'thought', key: `h${beats.length}`, text: live.reasoning, open: !live.text })
  return { steps, answer: live?.text || '', answerKey: `t${beats.length}` }
}

// Runs of calls become one group from GROUP_MIN calls. A step of kind tool
// needs only a tool with a state (running, done or failed) and ms.
export function groupSteps(steps, min = GROUP_MIN) {
  const out = []
  let run = []
  const flush = () => {
    if (run.length >= min) out.push({ kind: 'group', key: `g-${run[0].key}`, tools: run })
    else out.push(...run)
    run = []
  }
  for (const step of steps) {
    if (step.kind === 'tool') run.push(step)
    else { flush(); out.push(step) }
  }
  flush()
  return out
}

// What a group says of its calls: how many, how many failed, whether one still
// runs (and which) and the time from the first start to the last end, when all
// are known.
export function groupFacts(tools) {
  const list = tools || []
  const failed = list.filter((s) => s.tool.state === 'failed').length
  let active = -1
  list.forEach((s, i) => { if (s.tool.state === 'running') active = i })
  const ms = list.map((s) => s.ms)
  const total = active < 0 && ms.length > 0 && ms.every((x) => Number.isFinite(x)) ? ms.reduce((a, x) => a + x, 0) : null
  return { count: list.length, failed, active, total }
}

// The line a thought shows when it is folded: one line of its words, without
// Markdown marks, that ends at a sentence or a word. An ellipsis marks a cut
// that really took words away from that line. The rest is one click away.
// A thought that is still arriving shows its latest line (tail), so the line
// moves with the model.
const GIST_MAX = 140
const GIST_MIN = 40

function plain(line) {
  return String(line)
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}(#{1,6}|>|[-*+]|\d+[.)])\s+/, '')
    .replace(/(\*\*|__|\*|`)/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

export function thoughtGist(text, { tail = false, max = GIST_MAX } = {}) {
  const lines = String(text || '').split('\n').map(plain).filter(Boolean)
  const whole = lines.length === 0 ? '' : (tail ? lines[lines.length - 1] : lines[0])
  const rest = lines.length > 1
  if (whole.length <= max) return { text: whole, cut: false, more: rest }
  if (tail) {
    // The end of the line: start at a word, so the first word is whole.
    const from = whole.slice(whole.length - max)
    const sp = from.indexOf(' ')
    return { text: `…${(sp >= 0 && sp < 24 ? from.slice(sp + 1) : from).trim()}`, cut: true, more: true }
  }
  const head = whole.slice(0, max)
  let end = -1
  for (const m of head.matchAll(/[.!?](?=\s)/g)) if (m.index + 1 >= GIST_MIN) end = m.index + 1
  if (end > 0) return { text: head.slice(0, end), cut: false, more: true }
  const sp = head.lastIndexOf(' ')
  const words = (sp >= GIST_MIN ? head.slice(0, sp) : head).replace(/[\s,;:(\-–—]+$/, '')
  return { text: `${words}…`, cut: true, more: true }
}

// How long the live turn takes to fold into its line, in ms. The record waits
// that long, so the fold is seen once and the swap changes nothing.
export function foldMs() {
  const reduced = typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  return reduced ? 0 : 320
}

// What the line of a finished live turn says: its steps (the thoughts and the
// calls, which is what the list under the line shows) and the time from the
// first start to the last end, when both are known.
export function turnFacts(live) {
  const { steps } = liveSteps(live)
  const thoughts = steps.filter((s) => s.kind === 'thought').length
  const calls = steps.filter((s) => s.kind === 'tool').length
  const starts = [...(live?.beats || []).map((b) => b.start), ...(live?.tools || []).map((x) => x.startedAt), live?.reasoning ? live.openAt : 0].filter(Boolean)
  const ends = [...(live?.beats || []).map((b) => b.end), ...(live?.tools || []).map((x) => x.endedAt), live?.reasoning ? live.endedAt : 0].filter(Boolean)
  const ms = starts.length > 0 && ends.length > 0 ? Math.max(...ends) - Math.min(...starts) : null
  return { steps: thoughts + calls, thoughts, calls, ms }
}

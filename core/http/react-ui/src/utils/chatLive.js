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

export const emptyLive = () => ({ text: '', reasoning: '', tools: [] })

// The key of a tool call, in the live turn and in the transcript.
export const toolKey = (seq) => `tool-${seq}`

export function toolStart(live, tool) {
  return {
    ...live,
    tools: [...live.tools, { name: tool?.name || '', summary: tool?.summary || '', state: 'running', appState: '' }],
  }
}

// An app whose card can be read: the call worked, has an app and has a transcript entry.
export const liveCardWanted = (tool) => Boolean(tool && tool.ok && tool.app && tool.seq > 0)

// Puts the card that asks to run a command on this machine on the latest
// running bash call. The answer goes to chatId.
export function consentAsk(live, chatId, id, command) {
  const tools = [...live.tools]
  for (let i = tools.length - 1; i >= 0; i--) {
    if (tools[i].state === 'running' && tools[i].name === 'bash') {
      tools[i] = { ...tools[i], consent: { chatId, id, command: command || '' } }
      return { ...live, tools }
    }
  }
  return live
}

// Marks the latest running call of that name as ended.
export function toolEnd(live, tool) {
  const tools = [...live.tools]
  const name = tool?.name || ''
  for (let i = tools.length - 1; i >= 0; i--) {
    if (tools[i].state === 'running' && tools[i].name === name) {
      const hasApp = Boolean(tool?.ok && tool?.app)
      tools[i] = {
        ...tools[i],
        consent: undefined,
        state: tool?.ok ? 'done' : 'failed',
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

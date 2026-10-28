// The thread of the chat page, built from the transcript the server keeps and
// the live turn (see chatLive.js). The page draws the rows in one list:
//
//   msg       a message of the person or the model; a model message carries
//             the reasoning and tool lines that came just before it
//   activity  reasoning and tool lines that no model message follows yet
//   app       a tool call that has an MCP App, on a row of its own
//
// A tool call with an app keeps the key chatLive gives it (tool-<seq>) in the
// transcript and in the live turn, so its frame is not reloaded when the turn
// ends and the transcript takes the call over.

import { conversationItems, toolKey } from './chatLive.js'

const isActivity = (role) => role === 'reasoning' || role === 'tool'

// Rows of the transcript and of the live turn's apps. The live reasoning, the
// running tool calls and the live text are not rows: the page shows them in the
// one streaming turn at the end (see streamingParts).
export function threadRows(messages, live) {
  const rows = []
  let buf = []
  const flush = (key) => {
    if (buf.length > 0) rows.push({ kind: 'activity', key: `a-${key}`, items: buf })
    buf = []
  }
  for (const item of conversationItems(messages || [], live)) {
    if (item.type === 'app') {
      flush(item.key)
      rows.push({ kind: 'app', key: item.key, live: true, tool: item.tool })
      continue
    }
    if (item.type !== 'message') continue
    const m = item.m
    if (m.role === 'tool' && m.app) {
      flush(item.key)
      rows.push({ kind: 'app', key: toolKey(m.seq), live: false, m })
      continue
    }
    if (isActivity(m.role)) {
      buf.push(m)
      continue
    }
    if (m.role === 'assistant') {
      rows.push({ kind: 'msg', key: `m-${m.seq}`, msg: m, activity: buf.length > 0 ? buf : null })
      buf = []
      continue
    }
    if (m.role === 'user') {
      flush(m.seq)
      rows.push({ kind: 'msg', key: `m-${m.seq}`, msg: m, activity: null })
    }
    // checkpoints are for the sandbox's undo, not for reading
  }
  flush('end')
  return rows
}

// What the streaming turn shows: the reasoning and text so far and the tool
// calls of the turn that have no app (an app is a row, see threadRows). A call
// is a tool_call while it runs and a tool_result once it ended.
export function streamingParts(live) {
  const tools = (live?.tools || [])
    .filter((tool) => !tool.appRef)
    .map((tool) => ({
      type: tool.state === 'running' ? 'tool_call' : 'tool_result',
      name: tool.name,
      summary: tool.summary || '',
      result: tool.output || '',
      failed: tool.state === 'failed',
    }))
  return { reasoning: live?.reasoning || '', text: live?.text || '', tools }
}

// The name and the rest of the first line of a tool entry ("name: summary").
export function toolLine(text) {
  const first = String(text || '').split('\n')[0]
  const at = first.indexOf(':')
  if (at < 0) return { name: first.trim(), summary: '' }
  return { name: first.slice(0, at).trim(), summary: first.slice(at + 1).trim() }
}

// What the transcript stored after the first line of a tool entry.
export function toolOutput(text) {
  const s = String(text || '')
  const nl = s.indexOf('\n')
  return nl < 0 ? '' : s.slice(nl + 1)
}

// The text of the last message the person sent, for the composer's Up key.
export function lastUserText(messages) {
  for (let i = (messages || []).length - 1; i >= 0; i--) {
    if (messages[i].role === 'user') return messages[i].text || ''
  }
  return ''
}

// The chat as Markdown: its title, model and date, then every message. Tool
// lines show their first line only.
export function chatMarkdown(chat, messages) {
  const created = chat?.created ? new Date(chat.created) : null
  let md = `# ${chat?.title || 'Chat'}\n\n`
  md += `Model: ${chat?.model || 'Unknown'}\n`
  if (created && !Number.isNaN(created.getTime())) md += `Date: ${created.toLocaleString()}\n`
  md += '\n---\n\n'
  for (const m of messages || []) {
    if (m.role === 'user') md += `## User\n\n${m.text || ''}\n\n`
    else if (m.role === 'assistant') md += `## Assistant\n\n${m.text || ''}\n\n`
    else if (m.role === 'reasoning') md += `<details><summary>Thinking</summary>\n\n${m.text || ''}\n\n</details>\n\n`
    else if (m.role === 'tool') md += `> Tool: ${String(m.text || '').split('\n')[0]}\n\n`
  }
  return md
}

// A file name for the Markdown export of a chat.
export function markdownFileName(title) {
  return `${String(title || 'chat').replace(/[^a-zA-Z0-9]/g, '_')}.md`
}

// The chats of the server in the shape the conversations menu reads.
export function menuChats(conversations) {
  return (conversations || []).map((c) => ({
    id: c.id,
    name: c.title || '',
    model: c.model || '',
    running: Boolean(c.running),
    origin: c.origin || '',
    agentId: c.agent_id || '',
    assistant: Boolean(c.assistant),
    createdAt: Date.parse(c.created) || 0,
    updatedAt: Date.parse(c.updated) || Date.parse(c.created) || 0,
  }))
}

// The model's messages in the shape the canvas reads (utils/artifacts.js): a
// list where a message sits at the index of its seq, so an artifact's
// messageIndex and the data-index of the message on the page are the same
// number. forEach skips the holes.
export function artifactSource(messages) {
  const out = []
  for (const m of messages || []) {
    if (m.role === 'assistant') out[m.seq] = { role: 'assistant', content: m.text || '' }
  }
  return out
}

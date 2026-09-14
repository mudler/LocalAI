// SPDX-License-Identifier: MIT
export function validParticipants(participants) {
  const names = participants.map(p => p.name.trim().toLowerCase())
  return participants.length >= 2 && participants.length <= 6 &&
    participants.every(p => p.model && p.name.trim()) && new Set(names).size === names.length
}

export function buildMessages(objective, participant, transcript) {
  // A shared transcript is quoted user context, never this model's assistant history.
  const history = transcript.filter(e => e.status === 'complete').map(e =>
    `${e.name}${e.model ? ` (${e.model})` : ''}:\n${e.content}`
  ).join('\n\n')
  return [
    { role: 'system', content: `You are ${participant.name} (${participant.model}) in a moderated group conversation. Speak only for yourself. Other speakers' contributions are context.\n\nModerator objective:\n${objective || 'Discuss the shared conversation.'}` },
    { role: 'user', content: `${history || 'No contributions yet.'}\n\nIt is now ${participant.name}'s turn. Contribute to the discussion.` },
  ]
}

export async function readCompletion(stream, signal, onContent = () => {}) {
  signal.throwIfAborted()
  if (!stream) throw new Error('The model returned no stream.')
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  let buffer = '', content = '', done = false
  const abort = () => { void reader.cancel().catch(() => {}) }
  signal.addEventListener('abort', abort, { once: true })
  try {
    while (!done) {
      signal.throwIfAborted()
      const chunk = await reader.read()
      signal.throwIfAborted()
      buffer += chunk.done ? decoder.decode() : decoder.decode(chunk.value, { stream: true })
      // Normalize after buffering so a CRLF split across chunks remains intact.
      let match
      while ((match = /\r?\n\r?\n/.exec(buffer))) {
        const frame = buffer.slice(0, match.index)
        buffer = buffer.slice(match.index + match[0].length)
        const data = frame.split(/\r?\n/).filter(l => l.startsWith('data:')).map(l => l.slice(5).trimStart()).join('\n')
        if (!data) continue
        if (data === '[DONE]') { done = true; break }
        let payload
        try { payload = JSON.parse(data) } catch { throw new Error('Malformed model stream.') }
        if (payload.error) throw new Error(payload.error.message || String(payload.error))
        const choice = payload.choices?.[0]
        if (choice?.finish_reason && !['stop', 'length'].includes(choice.finish_reason)) throw new Error(`The model stopped: ${choice.finish_reason}`)
        if (typeof choice?.delta?.content === 'string') {
          content += choice.delta.content
          onContent(content)
        }
        if (choice?.finish_reason === 'length') throw new Error('The model reached its output limit. The response is incomplete.')
      }
      if (chunk.done && !done) throw new Error('The model stream ended before completion.')
    }
    if (!content.trim()) throw new Error('The model returned no text.')
    return content
  } finally {
    signal.removeEventListener('abort', abort)
    void reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export class GroupRun {
  constructor(provider) { this.provider = provider; this.controller = null }
  get running() { return this.controller !== null }
  stop() { this.controller?.abort() }

  async run({ participants, rounds = 1, speaker, objective = '', transcript, onUpdate = () => {} }) {
    if (this.running) throw new Error('A run is already active.')
    if (!validParticipants(participants)) throw new Error('Choose 2–6 participants with unique, nonempty names.')
    if (!Number.isInteger(rounds) || rounds < 1 || rounds > 10) throw new Error('Choose 1–10 rounds.')
    if (speaker !== undefined && (!Number.isInteger(speaker) || !participants[speaker])) throw new Error('Unknown participant.')
    const controller = new AbortController()
    this.controller = controller
    const { signal } = controller
    let entries = transcript.map(e => ({ ...e }))
    const order = speaker === undefined ? Array.from({ length: rounds }, () => participants).flat() : [participants[speaker]]
    const publish = () => onUpdate(entries.map(e => ({ ...e })))
    try {
      for (const participant of order) {
        signal.throwIfAborted()
        const messages = buildMessages(objective, participant, entries)
        const entry = { ...participant, content: '', status: 'streaming' }
        entries.push(entry)
        publish()
        try {
          const stream = await this.provider({ model: participant.model, messages, stream: true }, signal)
          signal.throwIfAborted()
          entry.content = await readCompletion(stream, signal, content => { entry.content = content; publish() })
          signal.throwIfAborted()
          entry.status = 'complete'
          publish()
        } catch (error) {
          entry.status = 'incomplete'
          publish()
          throw error
        }
      }
      return entries
    } finally {
      this.controller = null
    }
  }
}

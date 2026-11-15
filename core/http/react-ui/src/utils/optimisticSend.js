// The message of the person on its way to the server. It is drawn the moment
// it is sent, from the text and the files, and the server's copy takes its
// row (same key, so the element stays) when the transcript has it.

let counter = 0

// An entry of a send. chatId is null for the first message of a new chat,
// until the chat exists. base is how many messages of the person the
// transcript had when it was sent: the server's copy is the one after them.
export function newOptimistic({ chatId, text, files = [], base = 0 }) {
  counter += 1
  return {
    key: `m-sending-${counter}`,
    chatId: chatId || null,
    text,
    files: files.map(f => ({ name: f.name, size: f.size || 0, type: f.type || '' })),
    base,
    seq: 0, // the line of the transcript that took the row, once it has one
    status: 'sending', // 'sending', then 'sent' or 'failed'
  }
}

const userLines = (list) => list.filter(m => m.role === 'user')

// The seq of the server's copy of the message, or 0 while the transcript has
// none yet.
export function adoptedSeq(entry, list) {
  if (!entry) return 0
  if (entry.seq) return entry.seq
  const lines = userLines(list || [])
  return lines.length > entry.base ? lines[entry.base].seq : 0
}

// The transcript with the message in it: the entry's own line while the
// server has none, the server's line with the entry's row key after.
export function withOptimistic(messages, entry, chatId) {
  const list = messages || []
  if (!entry || (entry.chatId || null) !== (chatId || null)) return list
  const seq = adoptedSeq(entry, list)
  if (seq) {
    return list.map(m => (m.seq === seq && m.role === 'user' ? { ...m, rowKey: entry.key, sending: false } : m))
  }
  if (entry.status === 'sent') return list
  return [...list, {
    seq: 0,
    turn: 0,
    role: 'user',
    text: entry.text,
    attachments: entry.files.map(f => ({ path: '', name: f.name, size: f.size, mime: f.type })),
    rowKey: entry.key,
    sending: entry.status === 'sending',
    failed: entry.status === 'failed',
  }]
}

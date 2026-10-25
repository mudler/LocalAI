// Conversations on the Home page: read from the same browser storage the Chat
// page writes (see useChat and chatStore), grouped by day for the "Jump back
// in" list. Every function takes the storage key of the signed-in account
// (chatsStorageKey); without one it reads the browser-only key.
//
// Home only reads, resumes and deletes. It never writes a chat's content, so a
// stored conversation keeps the exact shape Chat expects.

import { CHATS_STORAGE_KEY, readChatStore, writeChatStore } from './chatStore'

const DAY_MS = 24 * 60 * 60 * 1000

// A message body is a string, or a list of blocks ({type:'text'|'image_url'|...}).
function messageText(message) {
  const content = message?.content
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content.filter(b => b?.type === 'text' || typeof b?.text === 'string').map(b => b.text || '').join(' ')
  }
  return ''
}

function messageHasImage(message) {
  return Array.isArray(message?.content) && message.content.some(b => b?.type === 'image_url' || b?.type === 'image')
}

// Reasoning markers are not part of what the reader wants in a one-line preview.
const THINK_RE = /<thinking>[\s\S]*?<\/thinking>|<think>[\s\S]*?<\/think>|<\|channel>thought[\s\S]*?<channel\|>/g

function oneLine(text, max = 140) {
  const flat = String(text || '').replace(THINK_RE, ' ').replace(/\s+/g, ' ').trim()
  return flat.length > max ? flat.slice(0, max - 1) + '…' : flat
}

// Title: the chat's name unless it is still the placeholder, then its first
// user message.
function titleOf(chat) {
  const name = (chat.name || '').trim()
  if (name && name !== 'New Chat') return name
  const first = (chat.history || []).find(m => m.role === 'user')
  return oneLine(messageText(first), 80) || name || 'New Chat'
}

// The line under the title is the last thing the model said; when the model
// has not answered yet, the last thing that was asked.
function previewOf(chat) {
  const history = chat.history || []
  for (let i = history.length - 1; i >= 0; i--) {
    if (history[i].role === 'assistant') {
      const text = oneLine(messageText(history[i]))
      if (text) return text
    }
  }
  for (let i = history.length - 1; i >= 0; i--) {
    if (history[i].role === 'user') return oneLine(messageText(history[i]))
  }
  return ''
}

// The rows of the "Jump back in" list for a set of chats: the ones that hold at
// least one message, newest first. Home reads them from storage; Chat passes
// the chats it has in memory, which are newer than storage by a moment.
export function conversationsFromChats(chats) {
  return (chats || [])
    .filter(c => c && c.id && Array.isArray(c.history) && c.history.some(m => m.role === 'user' || m.role === 'assistant'))
    .map(c => ({
      id: c.id,
      title: titleOf(c),
      preview: previewOf(c),
      model: c.model || '',
      count: c.history.filter(m => m.role === 'user' || m.role === 'assistant').length,
      hasImage: c.history.some(messageHasImage),
      updatedAt: c.updatedAt || c.createdAt || 0,
    }))
    .sort((a, b) => b.updatedAt - a.updatedAt)
}

// Chats that hold at least one message, newest first.
export function listConversations(key = CHATS_STORAGE_KEY) {
  const data = readChatStore(key)
  return data ? conversationsFromChats(data.chats) : []
}

function startOfDay(ts) {
  const d = new Date(ts)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

// Buckets: today, yesterday, earlier this week (2 to 6 days ago), older.
export function dayBucket(ts, now = Date.now()) {
  const days = Math.round((startOfDay(now) - startOfDay(ts)) / DAY_MS)
  if (days <= 0) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 7) return 'week'
  return 'older'
}

// Groups keep the input order, so a sorted list gives sorted groups.
export function groupConversations(items, now = Date.now()) {
  const groups = []
  for (const item of items) {
    const key = dayBucket(item.updatedAt, now)
    let group = groups[groups.length - 1]
    if (!group || group.key !== key) {
      group = { key, items: [] }
      groups.push(group)
    }
    group.items.push(item)
  }
  return groups
}

// Make a stored chat the one Chat opens on.
export function setActiveConversation(id, key = CHATS_STORAGE_KEY) {
  const data = readChatStore(key)
  if (!data || !data.chats.some(c => c.id === id)) return false
  return writeChatStore(key, { ...data, activeChatId: id })
}

// Remove a chat from storage. Home keeps the chat until its undo time ends
// and calls this only then, so there is nothing to put back afterwards. The
// id is queued as a pending delete: Home does not talk to the server, so the
// Chat page sends the delete the next time it syncs. Without that a synced
// chat removed here would come straight back from the server.
export function removeConversation(id, key = CHATS_STORAGE_KEY) {
  const data = readChatStore(key)
  if (!data || !data.chats.some(c => c.id === id)) return false
  const chats = data.chats.filter(c => c.id !== id)
  let activeChatId = data.activeChatId
  if (activeChatId === id) activeChatId = chats[0]?.id
  const pendingDeletes = data.pendingDeletes.includes(id) ? data.pendingDeletes : [...data.pendingDeletes, id]
  return writeChatStore(key, { ...data, chats, activeChatId, pendingDeletes })
}

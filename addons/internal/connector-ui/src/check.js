// The model of "Check it": the answer of "Try it" and the rows of "Recent
// refusals". No React, so a node test drives it. Nothing here decides who may
// do what: the page picks which kind of sender a question is about and reads
// the decision the host gave for it (the access preview), and the refusals
// are the host's own log lines, put in order.
import { cellOf } from './matrix.js'
import { hasPairing, remainingChecks } from './access.js'
import { trustOf } from './connector.js'
import { nameOf } from './people.js'

const DM = 'dm'

// The choices of the three selects of Try it, for the form as it is now.
//   who:    a person, "someone in <channel>", or anyone
//   places: a direct message, then the listed channels
// A choice has a value, the id it is about and the kind of sender the preview
// answers for. A kind that keeps one list of people and channels offers each
// entry both ways, because an ID there can be either.
export function tryChoices({ form, people, chatNames, paired = [], isChannel = () => false, mixed = false, roles = [], now = Date.now() }) {
  const allow = form?.accept_any ? [] : form?.allow || []
  const verified = new Set((paired || []).filter((p) => hasPairing([p], now)).map((p) => p.sender_id))
  const channels = mixed ? allow : allow.filter((x) => isChannel(x))
  const persons = mixed ? allow : allow.filter((x) => !isChannel(x))
  for (const id of verified) if (!persons.includes(id)) persons.push(id)
  const who = []
  for (const id of persons) who.push({ value: `person:${id}`, kind: 'person', id, name: nameOf(people, id), subject: verified.has(id) ? 'paired' : 'listed' })
  for (const id of channels) {
    who.push({ value: `channel:${id}`, kind: 'channel', id, name: chatNames?.get(id) || '', subject: trustOf(form, id) === 'act' ? 'channel_acts' : 'anyone' })
  }
  if (form?.accept_any) who.push({ value: 'anyone', kind: 'anyone', id: '', name: '', subject: 'anyone' })
  for (const r of roles || []) who.push({ value: `role:${r.id}`, kind: 'role', id: r.id, name: r.title, subject: `role:${r.id}` })
  const places = [{ value: DM, kind: 'dm', id: '', name: '' }]
  for (const id of form?.accept_any ? [] : channels) places.push({ value: `channel:${id}`, kind: 'channel', id, name: chatNames?.get(id) || '' })
  return { who, places }
}

// The place a choice of who fixes: someone in a channel writes in that channel.
export const placeFor = (who) => (who?.kind === 'channel' ? `channel:${who.id}` : '')

// The answer for one question: the verdict, the kind of sender, the sentence
// of the host and what it did not look at. view is the preview (null while it
// is not ready); the subject and the decision are the host's.
export function tryAnswer({ view, who, capability, proposalMode }) {
  if (!view || !who || !capability) return null
  const subject = (view.subjects || []).find((s) => s.id === who.subject)
  if (!subject) return { verdict: 'unknown', subject: who.subject }
  const decision = (subject.decisions || []).find((d) => d.capability === capability)
  if (!decision) return { verdict: 'unknown', subject: who.subject }
  const cell = cellOf(decision, subject, view, capability)
  const verdict = !decision.allowed ? 'refused' : cell.state === 'asks' ? 'asks' : 'allowed'
  return {
    verdict,
    subject: who.subject,
    decision,
    message: decision.allowed ? '' : decision.message || decision.reason || '',
    then: decision.allowed ? [] : remainingChecks(decision.then_check),
    fix: decision.allowed ? null : decision.fix || null,
  }
}

// ---- Recent refusals -----------------------------------------------------

// The refusals of the access log and of the sender log, newest first. An event
// of the access log names a permission and carries the host's reason; a sender
// the bot ignored (not listed, or the message did not name the bot) has only a
// code, which the page says in words. titleOf gives the title of a permission.
export function refusalRows({ recent = [], senders = [], titleOf = (x) => x, limit = 20 }) {
  const rows = []
  ;(recent || []).forEach((e, i) => {
    if (!e || e.allowed !== false || !e.capability) return
    const sender = (e.senders || [])[0] || ''
    rows.push({ key: `a${i}-${e.at}`, at: e.at, source: 'access', sender, name: e.sender_name || '', chat: e.chat || '', chatName: e.chat_name || '', action: titleOf(e.capability), code: e.code || '', reason: e.reason || '' })
  })
  ;(senders || []).forEach((s, i) => {
    if (!s || s.allowed !== false || !s.id) return
    rows.push({ key: `s${i}-${s.id}`, at: s.last || s.first || '', source: 'sender', sender: s.id, name: s.name && s.name !== s.id ? s.name : '', chat: s.chat || '', chatName: s.chat_name || '', action: '', code: s.reason || '', reason: '' })
  })
  return rows.sort((a, b) => String(b.at).localeCompare(String(a.at))).slice(0, limit)
}

// The buttons a refusal offers, as data: allow the person (when they are not on
// the list yet) and let the members of the channel act (when the refusal says
// the channel is set to answer only).
export function refusalActions(row, form) {
  const listed = (form?.allow || []).includes(row.sender)
  const out = []
  if (row.sender && !form?.accept_any && !listed) out.push({ type: 'allow', sender: row.sender })
  if (row.chat && row.code === 'channel_not_trusted' && (form?.allow || []).includes(row.chat) && trustOf(form, row.chat) !== 'act') {
    out.push({ type: 'act', chat: row.chat })
  }
  return out
}

// Pure helpers of the Contacts page. The server owns the rules; these only
// shape the form and keep the page from sending what it knows is wrong.

const BIRTHDAY = /^(\d{4}-\d{2}-\d{2}|--\d{2}-\d{2})$/

export const emptyRow = () => ({ label: '', value: '' })

export function emptyForm() {
  return { id: '', name: '', organisation: '', phones: [emptyRow()], emails: [emptyRow()], birthday: '', address: '', notes: '' }
}

// formOf fills the form from a contact of the server.
export function formOf(c) {
  const rows = (l) => (l && l.length ? l.map((e) => ({ label: e.label || '', value: e.value || '' })) : [emptyRow()])
  return { id: c.id, name: c.name || '', organisation: c.organisation || '', phones: rows(c.phones), emails: rows(c.emails), birthday: c.birthday || '', address: c.address || '', notes: c.notes || '' }
}

const entries = (rows) => rows.map((r) => ({ label: r.label.trim(), value: r.value.trim() })).filter((r) => r.value).map((r) => (r.label ? r : { value: r.value }))

// argsOf turns the form into the arguments of ui-contact-create or
// ui-contact-update. Every field is sent, so a cleared field clears the contact.
export function argsOf(f) {
  const args = { name: f.name.trim(), organisation: f.organisation.trim(), phones: entries(f.phones), emails: entries(f.emails), birthday: f.birthday.trim(), address: f.address.trim(), notes: f.notes.trim() }
  if (f.id) args.id = f.id
  return args
}

// problem is the key of the first thing wrong with the form, or ''.
export function problem(f) {
  if (!f.name.trim()) return 'contacts.form.needName'
  if (f.birthday.trim() && !BIRTHDAY.test(f.birthday.trim())) return 'contacts.form.badBirthday'
  return ''
}

// initials is the one or two letters of the avatar of a contact.
export function initials(name) {
  const w = (name || '').trim().split(/\s+/).filter(Boolean)
  if (!w.length) return '?'
  const first = Array.from(w[0])[0]
  const last = w.length > 1 ? Array.from(w[w.length - 1])[0] : ''
  return (first + last).toUpperCase()
}

// birthdayText writes a birthday for a person: the year is left out when it is
// not known. fmt formats a UTC date with Intl options.
export function birthdayText(b, fmt) {
  const m = /^(?:(\d{4})-|--)(\d{2})-(\d{2})$/.exec(b || '')
  if (!m) return b || ''
  return m[1] ? fmt({ year: 'numeric', month: 'long', day: 'numeric' }, +m[1], +m[2], +m[3]) : fmt({ month: 'long', day: 'numeric' }, 2000, +m[2], +m[3])
}

// downloadVcard offers the file to the person through a link.
export function downloadVcard(doc, text, name) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/vcard;charset=utf-8' }))
  const a = doc.createElement('a')
  a.href = url
  a.download = name
  doc.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

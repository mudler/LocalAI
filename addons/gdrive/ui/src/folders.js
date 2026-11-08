// Helpers of the Google Drive page. No React, so a node test drives them.

export const MAX_FOLDERS = 50
// A folder ID, or the word root for the top of My Drive. IDs are case
// sensitive, so the page never changes their case.
const ID = /^[A-Za-z0-9_-]{1,128}$/
const FOLDER_URL = /^https:\/\/drive\.google\.com\/drive\/(?:u\/\d+\/)?folders\/([A-Za-z0-9_-]{1,128})(?:[/?#].*)?$/

// The ID of one entry: an ID as it is, or the ID inside the address of a
// folder copied from the browser. Anything else stays as typed, so the error
// can name it.
export function folderId(entry) {
  const e = String(entry || '').trim()
  const m = FOLDER_URL.exec(e)
  return m ? m[1] : e
}

// The folders of a text box: one per line, or separated by commas. A space is
// not a separator. Blank entries and repeats are dropped.
export function parseFolders(text) {
  const seen = new Set()
  const out = []
  for (const part of String(text || '').split(/[\n\r,]+/)) {
    const id = folderId(part)
    if (!id || seen.has(id)) continue
    seen.add(id)
    out.push(id)
  }
  return out
}

// The first thing wrong with the typed list: { key, entry } or null.
export function foldersIssue(text) {
  const list = parseFolders(text)
  if (list.length > MAX_FOLDERS) return { key: 'foldersMany', entry: '' }
  const bad = list.find((f) => !ID.test(f))
  return bad === undefined ? null : { key: 'foldersShape', entry: bad.length > 60 ? `${bad.slice(0, 57)}...` : bad }
}

export const foldersProblem = (text) => foldersIssue(text)?.key || ''

// What the agent may change in Drive with this connection. The host keeps it on
// the binding and enforces it: off runs no write tool, propose stores each
// change for you to confirm, auto lets a chat of yours make it at once. Every
// change in Drive also needs the optional access that was allowed at login.
export const WRITE_MODES = ['off', 'propose', 'auto']
// The modes the page offers. Every write tool of this add-on waits for a
// person (the tools set approval always), so auto would be the same as
// propose; a connection that has it keeps it and reads as asking first.
export const OFFERED_MODES = ['off', 'propose']
export const writeModeOf = (m) => (WRITE_MODES.includes(m) ? m : 'off')

// All folders is the list *: the host holds the connection to its list and
// honors * for this kind (wildcardResources), and the add-on then reads and
// writes in every drive the login reaches, shared drives too. It is a distinct
// choice, not a folder: an empty list still means no access. A new choice
// needs the tick of a confirmation, which the host asks for as well.
export const ALL_FOLDERS = '*'
export const ALL_FOLDERS_CONFIRM = 'allow any resource'

// The fields this page adds to a binding: its form values, the request body
// they make, and whether they allow saving.
//
// all_folders is the choice All folders. all_was says the saved connection
// already had it, which the host does not ask to confirm again; all_ack is the
// tick of this edit and is never saved. The typed list is kept while the choice
// is on, so turning it off gives it back.
export const extraForm = (c) => {
  const saved = c.resources || []
  return {
    folders_text: saved.filter((f) => f !== ALL_FOLDERS).join('\n'), write_mode: writeModeOf(c.write_mode),
    all_folders: saved.includes(ALL_FOLDERS), all_was: saved.includes(ALL_FOLDERS), all_ack: false,
  }
}
export const extraBody = (form) => {
  const body = { resources: form.all_folders ? [ALL_FOLDERS] : parseFolders(form.folders_text), write_mode: writeModeOf(form.write_mode) }
  if (form.all_folders && !form.all_was && form.all_ack) body.confirm_any_resource = ALL_FOLDERS_CONFIRM
  return body
}
export const extraValid = (form) => (form.all_folders ? form.all_was || Boolean(form.all_ack) : !foldersProblem(form.folders_text))

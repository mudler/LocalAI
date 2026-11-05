// The model of the approval settings. No React, so a node test drives it. The
// host keeps the answers (user settings of the add-on) and decides every
// call; these helpers only name the keys and read an answer safely.

// The tools the owner may stop being asked for. type-text is not here on
// purpose: it types arbitrary text, so the manifest gives it no such setting
// and the host would not honor one.
export const TOOLS = ['power-action', 'send-key']

export const ASK = 'ask'
export const SKIP = 'skip'
export const CHOICES = [ASK, SKIP]

// The key of the setting for a tool, as the manifest declares it.
export const keyOf = (tool) => `approval-${tool}`

// What the host reports for the signed-in person: { tool: 'ask' | 'skip' }.
// A tool the host does not report, or an answer it does not know, is ask.
export function valuesOf(out) {
  const list = Array.isArray(out?.settings) ? out.settings : []
  const result = {}
  for (const tool of TOOLS) {
    const s = list.find((x) => x?.key === keyOf(tool))
    result[tool] = s?.value === SKIP ? SKIP : ASK
  }
  return result
}

// Whether the host declares the settings at all. An older package declares
// none, and the page then shows nothing about them.
export const declared = (out) => TOOLS.some((tool) => (Array.isArray(out?.settings) ? out.settings : []).some((x) => x?.key === keyOf(tool)))

// The rows of the connections show which tools run without asking. They read
// it from the host, so a save below must tell them to read it again.
let revision = 0
const listeners = new Set()
export const approvalRevision = () => revision
export function approvalsChanged() { revision += 1; for (const f of [...listeners]) f() }
export function onApprovalsChanged(f) { listeners.add(f); return () => listeners.delete(f) }

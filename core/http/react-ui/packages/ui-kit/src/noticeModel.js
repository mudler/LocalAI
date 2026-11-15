// The pure rules of <Notice>, so the node unit tests drive them without a
// browser. A notice says that something is off or not available, in a fixed
// order: what (title), why (reason), who can fix it (who), the fix (a button).

export const NOTICE_SEVERITIES = ['info', 'warn', 'error']

// The kit icon of each severity. A notice with its own icon (a terminal for
// commands) keeps the severity's colour.
export const NOTICE_ICONS = { info: 'info', warn: 'warning', error: 'alert-circle' }

// What a screen reader does with the notice.
//   error  role alert (assertive): something the person asked for does not work
//   warn   role status (polite): degraded, or an action is recommended
//   info   role note (not live): off by design
const NOTICE_ROLES = { error: 'alert', warn: 'status', info: 'note' }

export function noticeSeverity(value) {
  return NOTICE_SEVERITIES.includes(value) ? value : 'info'
}

export function noticeRole(severity) {
  return NOTICE_ROLES[noticeSeverity(severity)]
}

// One action of a notice, or null when it has no label or nothing to do. An
// action with `to` is a router link, with `href` an address, with `onClick` a
// button. A notice never draws a button that does nothing.
export function noticeAction(action) {
  if (!action || typeof action !== 'object' || !action.label) return null
  if (action.to === undefined && action.href === undefined && typeof action.onClick !== 'function') return null
  return action
}

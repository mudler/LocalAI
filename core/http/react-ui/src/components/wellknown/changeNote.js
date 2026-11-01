// The one place that shows a toast about a change to the user's files: "Added
// to your composer", "Task board is in Pages", "Examples hidden". It wraps the
// app's toast today, so the toast it uses can change in one file later.
//
// A toast here only says what happened outside the page the user looks at;
// every action it offers has a twin on the page (the change note in the chat,
// the switch in Account), so nobody depends on catching it.

const DURATION = 6000

// A shortcut the assistant added lights up the shortcuts row for a moment.
export const SHORTCUT_ADDED_EVENT = 'localai:shortcut-added'

// showChange shows a toast. action is { label, onClick } or nothing.
export function showChange(addToast, { message, action, testId = 'change-toast', tone = 'info', duration = DURATION }) {
  if (typeof addToast !== 'function' || !message) return null
  return addToast(message, tone, duration, { action, testId })
}

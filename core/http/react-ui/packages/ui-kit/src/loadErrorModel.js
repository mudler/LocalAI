// The pure parts of <LoadError>, so the node unit tests read them without a
// browser.

// The English words of LoadError. A translated app passes its own as the
// `labels` prop; tests/unit/uikitpackage.test.mjs keeps these equal to
// common.json.
export const LOAD_ERROR_LABELS = {
  title: 'Could not load this.',
  tryAgain: 'Try again',
  nothingChanged: 'Nothing was changed.',
  serverSaid: (message) => `The server answered \u201c${message}\u201d.`,
}

// The words of a failed request, for LoadError: the server's own message when
// the error carries one (a nested API error body), else the error's message.
export function errorMessage(error) {
  if (!error) return ''
  if (typeof error === 'string') return error
  const nested = error?.error?.message || error?.body?.error?.message
  return String(nested || error.message || '').trim()
}

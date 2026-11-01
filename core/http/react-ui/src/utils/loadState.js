// The state of one region that loads data: loading, ready or error. A page
// that keeps only `loading` and `items` cannot tell a failed load from an
// empty list, so a failure shows the empty state. With this state a failure
// is its own branch, and the region renders <LoadError> there.
//
// Each request has an id. An answer to an older request (the user retried, or
// the inputs changed) is ignored, so a slow first answer cannot overwrite a
// newer one.

export const initialLoad = { status: 'loading', data: undefined, error: null, request: 0 }

export function loadReducer(state, action) {
  switch (action.type) {
    case 'start':
      // Keep the data of a ready region while it loads again, so a refresh
      // does not blank it.
      return { ...state, status: state.status === 'ready' && action.keep ? 'ready' : 'loading', error: null, request: action.request, refreshing: state.status === 'ready' && !!action.keep }
    case 'success':
      if (action.request !== state.request) return state
      return { status: 'ready', data: action.data, error: null, request: state.request, refreshing: false }
    case 'failure':
      if (action.request !== state.request) return state
      return { ...state, status: 'error', error: action.error, refreshing: false }
    default:
      return state
  }
}

// The text of an error for a person: the server's own message when there is
// one, without a generic prefix.
export function errorMessage(error) {
  if (!error) return ''
  if (typeof error === 'string') return error
  const nested = error?.error?.message || error?.body?.error?.message
  return String(nested || error.message || '').trim()
}

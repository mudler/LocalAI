// SPDX-License-Identifier: MIT
// The login of an add-on whose connections are made with OAuth: what the page
// draws in place of the New button. The host draws the login itself
// (sdk.ui.ConnectionLogin), so the page only decides which of three things
// shows. No React and no network: the node unit tests drive it directly.

// loginPlan says what the page draws for a kind that logs in.
//   host      the host's login (needs the kind from the kinds list)
//   waiting   the kinds list has not answered yet: nothing yet
//   oldHost   the host has no login component: say so, since nothing can add
//   none      the page is not a login page, or cannot add
// `sheetAdd` is whether the New button and its token sheet are drawn: a login
// kind never has them, because it has no token to type.
export function loginPlan({ login, canAdd, sdk, kindInfo, kindsReady }) {
  const sheetAdd = Boolean(canAdd) && !login
  if (!login || !canAdd) return { show: 'none', sheetAdd }
  if (typeof sdk?.ui?.ConnectionLogin !== 'function') return { show: kindsReady ? 'oldHost' : 'waiting', sheetAdd }
  return { show: kindInfo ? 'host' : 'waiting', sheetAdd }
}

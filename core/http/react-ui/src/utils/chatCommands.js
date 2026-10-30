// Whether a chat can run commands and code, from the "commands" field of the
// chat's add-on list (GET /api/chats/:id/addons). The server computes it from
// the tools the model gets at the next turn:
//   available    a tool runs commands
//   off          the Sandbox add-on is installed but off for this chat
//   unavailable  the Sandbox add-on is installed but cannot run commands here
//   none         no add-on that runs commands is installed for the user
// An older server sends no field: then nothing is shown.

export const SANDBOX_ADDON_ID = 'sandbox'

// The notice for a state, or null when commands run (or the state is unknown).
// keys are under chats:tools.commands. link is for administrators only; a
// person who is not one is told to ask one, unless the fix is theirs.
export function commandsNotice(commands, { isAdmin = false, reason = '' } = {}) {
  switch (commands) {
    case 'off':
      return { state: 'off', textKey: 'off', link: null, askKey: null, reason: '' }
    case 'unavailable':
      return {
        state: 'unavailable',
        textKey: reason ? 'unavailableWhy' : 'unavailable',
        reason: typeof reason === 'string' ? reason.slice(0, 200) : '',
        link: isAdmin ? { to: `/app/addons?detail=${SANDBOX_ADDON_ID}`, labelKey: 'openSandbox' } : null,
        askKey: isAdmin ? null : 'askFix',
      }
    case 'none':
      return {
        state: 'none',
        textKey: 'none',
        reason: '',
        link: isAdmin ? { to: `/app/addons?tab=browse&q=${SANDBOX_ADDON_ID}`, labelKey: 'install' } : null,
        askKey: isAdmin ? null : 'askInstall',
      }
    default:
      return null
  }
}

// The notice can be hidden. What is hidden is the state, so a notice comes back
// when the reason changes (the add-on is installed but cannot run, say). The
// value lives in this browser only.
export const DISMISS_KEY = 'localai.chat.commandsNotice.hidden'

export function readHidden(storage) {
  try {
    return storage?.getItem(DISMISS_KEY) || ''
  } catch {
    return ''
  }
}

export function writeHidden(storage, state) {
  try {
    if (state) storage?.setItem(DISMISS_KEY, state)
    else storage?.removeItem(DISMISS_KEY)
  } catch {
    // Storage can be off (a private window): the notice then stays.
  }
}

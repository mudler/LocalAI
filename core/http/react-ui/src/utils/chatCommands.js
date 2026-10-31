// Whether a chat can run commands and code, from the "commands" field of the
// chat's add-on list (GET /api/chats/:id/addons). The server computes it from
// the tools the model gets at the next turn:
//   available    a tool runs commands
//   off          the Sandbox add-on is installed but off for this chat
//   unavailable  the Sandbox add-on is installed but cannot run commands here
//   none         no add-on that runs commands is installed for the user
// When commands_provider is builtin, bash runs directly on this machine (no
// Sandbox serves it); commandsState turns that into one of three states of its
// own, so the notice says that commands run here, not isolated:
//   host              no Sandbox
//   host-off          the Sandbox is off for this chat
//   host-unavailable  the Sandbox cannot run (commands_fallback_reason)
// An older server sends no field: then nothing is shown.

export const SANDBOX_ADDON_ID = 'sandbox'

// Where an administrator installs the Sandbox. The first-start wizard step
// "Run code safely" links here too.
export const SANDBOX_INSTALL_LINK = `/app/addons?tab=browse&q=${SANDBOX_ADDON_ID}`

// commandsState reads the commands fields of an add-on list as one state and
// its reason.
export function commandsState(res) {
  const state = typeof res?.commands === 'string' ? res.commands : ''
  const reason = typeof res?.commands_reason === 'string' ? res.commands_reason : ''
  if (state === 'available' && res?.commands_provider === 'builtin') {
    const why = typeof res.commands_fallback_reason === 'string' ? res.commands_fallback_reason : ''
    if (res.commands_fallback && why === 'off') return { state: 'host-off', reason: '' }
    if (res.commands_fallback) return { state: 'host-unavailable', reason: why }
    return { state: 'host', reason: '' }
  }
  return { state, reason }
}

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
        link: isAdmin ? { to: SANDBOX_INSTALL_LINK, labelKey: 'install' } : null,
        askKey: isAdmin ? null : 'askInstall',
      }
    // Commands run, but on this machine: a quiet notice that stays.
    case 'host':
      return {
        state: 'host', titleKey: 'hostTitle', textKey: 'host', reason: '', tone: 'warn',
        link: isAdmin ? { to: SANDBOX_INSTALL_LINK, labelKey: 'install' } : null,
        askKey: isAdmin ? null : 'askInstall',
      }
    case 'host-off':
      return { state: 'host-off', titleKey: 'hostTitle', textKey: 'hostOff', reason: '', tone: 'warn', link: null, askKey: null }
    case 'host-unavailable':
      return {
        state: 'host-unavailable', titleKey: 'hostTitle', textKey: reason ? 'hostUnavailableWhy' : 'hostUnavailable',
        reason: typeof reason === 'string' ? reason.slice(0, 200) : '', tone: 'warn',
        link: isAdmin ? { to: `/app/addons?detail=${SANDBOX_ADDON_ID}`, labelKey: 'openSandbox' } : null,
        askKey: isAdmin ? null : 'askFix',
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

// Who served a call of a replaceable tool (bash), from the card of its
// transcript line: { key, values } of chats:tools.provider, or null.
//   tool_provider  an add-on served it (id: the add-on)
//   host_exec      the built-in bash on this machine (id: what happened)
export function providerTag(card) {
  if (!card || typeof card !== 'object') return null
  if (card.kind === 'tool_provider' && card.id) {
    return card.id === SANDBOX_ADDON_ID ? { key: 'sandbox' } : { key: 'addon', values: { name: String(card.id) } }
  }
  if (card.kind === 'host_exec') {
    switch (card.id) {
      case 'once': return { key: 'hostOnce' }
      case 'denied': return { key: 'hostDenied' }
      case 'timeout': return { key: 'hostTimeout' }
      case 'admin': return { key: 'hostAdmin' }
      default: return { key: 'host' }
    }
  }
  return null
}

// consentExpired says whether the answer to the card of a command was refused
// because the card expired (410 consent_expired): the turn that asked has
// ended, so nobody waits on the answer and the command did not run.
export function consentExpired(e) {
  return e?.status === 410 || e?.body?.error?.type === 'consent_expired'
}

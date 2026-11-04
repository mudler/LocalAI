// Command availability and isolation come from the selected provider DTO, not
// an add-on's private discovery response or its name.

// Why the host has no execution status (execution_status.unknown_reason).
// Each code has its own sentence under chats:tools.commands.unknownBy.
const UNKNOWN_REASONS = ['owner_unreachable', 'not_reported', 'stale', 'invalid_status', 'not_selected', 'no_answer', 'reported_unknown']

// The text key of the notice of an unknown state, with the reason when known.
export const unknownTextKey = (code) => (UNKNOWN_REASONS.includes(code) ? `unknownBy.${code}` : 'unknown')
const reasonCode = (observation) => (UNKNOWN_REASONS.includes(observation?.unknown_reason) ? observation.unknown_reason : '')

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
  const observation = res?.execution_status
  // A chat that is not created yet has no status to read: nothing to warn of.
  if (observation?.unknown_reason === 'draft' && (state === 'available' || state === 'none')) return { state: state === 'none' ? 'none' : 'draft', reason: '' }
  if (state === 'available') {
    const isolation = observation?.isolation || res?.commands_isolation
    if (observation && observation.readiness !== 'available') return { state: 'unknown', reason: plainExplanation(observation.explanation), code: reasonCode(observation) }
    if (isolation === 'host') return { state: 'provider-host', reason: plainExplanation(observation?.explanation) }
    if (isolation !== 'isolated') return { state: 'unknown', reason: plainExplanation(observation?.explanation), code: reasonCode(observation) }
  }
  if (state === 'none' && observation) return { state: 'unknown', reason: plainExplanation(observation.explanation), code: reasonCode(observation) }
  if (state === 'unavailable' && (observation || res?.commands_provider)) return { state: 'provider-unavailable', reason: plainExplanation(observation?.explanation || reason) }
  return { state, reason }
}

// The notice for a state, or null when commands run (or the state is unknown).
// keys are under chats:tools.commands. link is for administrators only; a
// person who is not one is told to ask one, unless the fix is theirs.
export function commandsNotice(commands, { isAdmin = false, reason = '', code = '' } = {}) {
  switch (commands) {
    case 'provider-host':
    case 'unknown':
    case 'loading':
    case 'provider-unavailable':
    case 'switched_off':
      return { state: commands, titleKey: commands === 'provider-host' ? 'hostTitle' : 'title', textKey: commands === 'switched_off' ? 'disabled' : commands === 'provider-host' ? 'providerHost' : commands === 'provider-unavailable' ? 'providerUnavailable' : unknownTextKey(code), reason: plainExplanation(reason), tone: 'warn', link: null, askKey: null }

    case 'off':
      return { state: 'off', textKey: 'disabled', link: null, askKey: null, reason: '' }
    case 'unavailable':
      return {
        state: 'unavailable',
        textKey: 'providerUnavailable',
        reason: typeof reason === 'string' ? reason.slice(0, 200) : '',
        link: null,
        askKey: isAdmin ? null : 'askFix',
      }
    case 'none':
      return {
        state: 'none',
        textKey: 'none',
        reason: '',
        link: null,
        askKey: null,
      }
    // Actual host execution never implies that one particular add-on is the fix.
    case 'host':
    case 'host-off':
    case 'host-unavailable':
      return { state: commands, titleKey: 'hostTitle', textKey: 'providerHost', reason: plainExplanation(reason), tone: 'warn', link: null, askKey: null }
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
    return { key: 'addon', values: { name: String(card.id) } }
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

// Plain text only. React text children escape markup; never pass this to HTML/Markdown.
export function plainExplanation(value) {
  return typeof value === 'string' ? [...value.replace(/[\u0000-\u001f\u007f\u202a-\u202e\u2066-\u2069]/g, '')].slice(0, 200).join('') : ''
}

export function noticeIdentity(res, chat, actor) {
  const v = res?.execution_status || {}
  return JSON.stringify([actor, chat, res?.commands_provider, v.generation, v.incarnation, res?.commands, v.readiness, v.isolation || res?.commands_isolation, v.unknown_reason])
}

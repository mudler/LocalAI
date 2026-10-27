// What the settings section draws, made from the answer of the status tool.
// Only an administrator gets the settings: the status tool says who asked.
const OPTIONS = ['auto', 'gvisor', 'kvm', 'off', 'host']

const text = (v) => (typeof v === 'string' ? v : '')

export function settingsModel(status) {
  if (!status || typeof status !== 'object' || status.ok !== true || status.admin !== true) return { hidden: true }
  const backends = (Array.isArray(status.backends) ? status.backends : [])
    .filter(b => b && typeof b === 'object')
    .map(b => ({ name: text(b.name), usable: b.usable === true, reason: text(b.reason) }))
  const setting = OPTIONS.includes(status.setting) ? status.setting : 'auto'
  const hostNotice = status.hostNotice === true
  return {
    hidden: false,
    setting,
    active: text(status.active),
    canRun: status.canRun === true,
    options: OPTIONS.map(value => ({ value })),
    backends,
    hostNotice,
    hostConfirmed: status.hostConfirmed === true,
    // The host needs the phrase before it can be chosen; once it is the choice
    // and confirmed, there is nothing more to type.
    needsHostConfirmation: status.hostConfirmed !== true && setting !== 'host',
    note: text(status.note),
  }
}

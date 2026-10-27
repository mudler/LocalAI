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

// What the main page shows to every user, made from the answer of the status
// tool. The state is "isolated" when gVisor or a virtual machine runs the
// code, "host" when commands run on this machine, "cannot" when no backend
// works and "unknown" when the answer cannot be read. The reasons of the
// backends that do not work come only with the answer for an administrator.
export function pageStatus(status) {
  if (!status || typeof status !== 'object' || status.ok !== true) return { state: 'unknown', backend: '', reasons: [] }
  const backend = text(status.active)
  const reasons = status.admin === true && Array.isArray(status.backends)
    ? status.backends.filter(b => b && typeof b === 'object' && b.usable !== true && text(b.reason)).map(b => ({ name: text(b.name), reason: text(b.reason) }))
    : []
  if (status.canRun !== true) return { state: 'cannot', backend: '', reasons }
  if (status.hostNotice === true || backend === 'host') return { state: 'host', backend, reasons }
  return { state: 'isolated', backend, reasons }
}

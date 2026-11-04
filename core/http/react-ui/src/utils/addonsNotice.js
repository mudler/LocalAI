// Decides the Home notice for an add-on address that was opened while add-ons
// were off. `addon` is the name kept in the navigation state; `features` is the
// body of /api/features, or null when it could not be read.
//
// The navigation state survives a refresh, so it only says that add-ons were
// off once. The features say whether they still are.
//   kind 'off'          the instance switch is off
//   kind 'unreachable'  the switch is on and the owner does not answer
// With no features answer the notice stays as it was (kind 'off').
export function addonsNotice(addon, features) {
  if (!addon) return { addon: '', kind: 'off' }
  if (!features) return { addon, kind: 'off' }
  if (features.addons !== false) return { addon: '', kind: 'off' }
  return { addon, kind: features.addons_enabled === true ? 'unreachable' : 'off' }
}

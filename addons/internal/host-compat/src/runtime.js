// The one place the stand-ins of host modules find the SDK of the add-on that
// is running. An add-on calls setSdk(sdk) in createAddon, before it loads its
// page code (a dynamic import), so no stand-in reads the SDK while the bundle
// is still being evaluated.
let current = null

export function setSdk(sdk) {
  current = sdk
}

export function getSdk() {
  if (!current) throw new Error('The host SDK is not set yet')
  return current
}

// Fails early, and readably, when the host is older than the add-on needs.
export function requireHost(sdk, names) {
  const missing = names.filter((n) => {
    const [group, key] = n.split('.')
    return !sdk[group] || typeof sdk[group][key] === 'undefined'
  })
  if (missing.length > 0) throw new Error(`This add-on needs a newer LocalAI: the host has no ${missing.join(', ')}`)
}

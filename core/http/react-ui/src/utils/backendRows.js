// What a row in the Backends list says about one backend. Pure functions, so
// the rules sit in one place and the Catalog and Installed lists never describe
// the same backend two ways.

// The live operation for a backend, if any. Operations name the backend by its
// bare name or by its gallery id, so both are tried.
export function opForBackend(operations, ...names) {
  const wanted = names.filter(Boolean)
  if (!Array.isArray(operations) || wanted.length === 0) return null
  return operations.find(op => wanted.includes(op.name) || wanted.includes(op.id)) || null
}

// The state of one backend, in the order it matters to a person: something is
// happening to it now, then the last attempt failed, then it has an update,
// then it is simply there, then it is not.
export function backendState({ installed = false, upgrade = null, op = null, libraryUpdate = null } = {}) {
  if (op && !op.error) {
    return {
      kind: op.isDeletion ? 'removing' : op.isQueued ? 'queued' : 'installing',
      progress: op.progress > 0 ? Math.min(100, Math.round(op.progress)) : 0,
      cancellable: Boolean(op.cancellable),
      jobID: op.jobID,
    }
  }
  if (op && op.error) return { kind: 'failed', error: op.error, jobID: op.jobID }
  if (upgrade) {
    return { kind: 'update', from: upgrade.installed_version || '', to: upgrade.available_version || '' }
  }
  // A library backend has its own newer-build check. It says only that there is
  // one; the build is named in the detail, so `to` stays empty.
  if (libraryUpdate) return { kind: 'update', from: libraryUpdate.installedVersion || '', to: '', library: true }
  if (installed) return { kind: 'current' }
  return { kind: 'absent' }
}

// Installed backends, with those that have an update first, then by name. The
// list is short and a person scans it for what to do, so what needs a decision
// sits at the top.
export function sortInstalled(list, upgrades) {
  return [...list].sort((a, b) => {
    const ua = upgrades[a.Name] ? 0 : 1
    const ub = upgrades[b.Name] ? 0 : 1
    if (ua !== ub) return ua - ub
    return a.Name.localeCompare(b.Name)
  })
}

// The library a backend was installed from, as its metadata pins it, or null
// for a backend of a gallery, a system backend or one installed by hand.
export function libraryOrigin(backend) {
  const m = backend && backend.Metadata
  if (!m || !m.library) return null
  return { id: m.library, digest: m.digest || '', version: m.version || '' }
}

// A digest as people read it: the algorithm and the first twelve characters.
export function shortDigest(digest) {
  const d = String(digest || '')
  const [algo, hex] = d.includes(':') ? d.split(':') : ['', d]
  if (!hex) return ''
  return `${algo ? algo + ':' : ''}${hex.slice(0, 12)}`
}

// "v1.2.0" or "1.2.0" the same way, and nothing for an empty version.
export function versionLabel(version) {
  const v = String(version || '').trim()
  if (!v) return ''
  return /^v/i.test(v) ? v : `v${v}`
}

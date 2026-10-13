// The runtime manager keeps one instance of each loaded add-on UI and applies
// the registry's verdict to it. It has no React in it; the registry provider
// and the shell route read its snapshots.
//
// Instance status:
//   loading      the module is being fetched and created
//   ready        the Component and chatActions are live
//   stale        withdrawn while the user has unsaved edits: aborted and
//                frozen, nothing is dropped until the user decides
//   unavailable  withdrawn: unmounted, aborted, caches and styles cleared
//   error        could not be loaded (incompatible SDK, import failed, ...)
//
// The user's edits are never discarded by a poll: a withdrawn add-on with
// unsaved edits becomes `stale` and keeps its last rendered component until
// `reload` or `release`.

import { invalidReason } from './registryState.js'

const EMPTY_COMPONENTS = Object.freeze({})

export function createRuntimeManager({ load, onChange = () => {} } = {}) {
  const instances = new Map()
  let version = 0
  let snapshot = []

  const publish = () => {
    version++
    snapshot = Array.from(instances.values()).map(view)
    onChange(snapshot, version)
  }
  const view = (r) => ({
    id: r.id,
    generation: r.generation,
    entry: r.entry,
    status: r.status,
    reason: r.reason,
    error: r.error,
    dirty: r.dirty,
    Component: r.addon ? r.addon.component : null,
    chatActions: r.status === 'ready' && r.addon ? r.addon.chatActions : [],
    components: r.status === 'ready' && r.addon && r.addon.components ? r.addon.components : EMPTY_COMPONENTS,
    sdk: r.status === 'ready' && r.addon ? r.addon.sdk || null : null,
    version: r.token,
  })

  let tokens = 0
  const start = (app) => {
    const r = {
      id: app.id, generation: app.generation, entry: app.ui ? app.ui.entry : '', status: 'loading', reason: '', error: null,
      dirty: false, addon: null, holders: 0, token: ++tokens, controller: new AbortController(), load: null,
    }
    r.load = load(app, {
      signal: r.controller.signal,
      setDirty: (flag) => {
        if (instances.get(app.id) !== r || r.dirty === flag) return
        r.dirty = flag
        publish()
      },
    }).then((addon) => {
      if (instances.get(app.id) !== r || r.status !== 'loading') { addon.dispose(); return }
      r.addon = addon
      r.status = 'ready'
      publish()
    }, (error) => {
      if (instances.get(app.id) !== r || r.status !== 'loading') return
      r.status = 'error'
      r.error = error
      r.reason = error && error.code ? error.code : 'import_failed'
      publish()
    })
    instances.set(app.id, r)
    return r
  }

  const teardown = (r, status, reason) => {
    r.controller.abort('withdrawn')
    if (r.addon) {
      r.addon.abort?.('withdrawn')
    }
    if (status === 'unavailable') {
      r.addon?.dispose()
      r.addon = null
    }
    r.status = status
    r.reason = reason
  }

  return {
    // hold keeps the instance of `app` alive for one consumer and starts it
    // when there is none. It never replaces a withdrawn one: that is `reload`.
    hold(app) {
      let r = instances.get(app.id)
      if (!r) { r = start(app); publish() }
      r.holders++
      let released = false
      return {
        release: () => {
          if (released) return
          released = true
          const current = instances.get(app.id)
          if (!current) return
          current.holders--
          if (current.holders <= 0) {
            current.controller.abort('released')
            current.addon?.dispose()
            instances.delete(app.id)
            publish()
          }
        },
      }
    },
    // revalidate applies the apps list: every instance the list invalidates is
    // withdrawn at once, so no call and no slot outlives the owner's decision.
    revalidate(apps) {
      const byId = new Map((apps || []).map(a => [a.id, a]))
      let changed = false
      for (const r of instances.values()) {
        if (r.status !== 'loading' && r.status !== 'ready') continue
        const reason = invalidReason(byId.get(r.id), r)
        if (!reason) continue
        teardown(r, r.dirty ? 'stale' : 'unavailable', reason)
        changed = true
      }
      if (changed) publish()
    },
    // reload replaces the instance of an add-on with a fresh load of `app`.
    // The caller has asked the user first when there were unsaved edits.
    reload(app) {
      const old = instances.get(app.id)
      let holders = 0
      if (old) {
        holders = old.holders
        old.controller.abort('reload')
        old.addon?.dispose()
        instances.delete(app.id)
      }
      const r = start(app)
      r.holders = holders
      publish()
    },
    get(id) { return snapshot.find(r => r.id === id) || null },
    snapshot: () => snapshot,
    version: () => version,
    // disposeAll drops everything, for a sign-out or a user change.
    disposeAll() {
      for (const r of instances.values()) {
        r.controller.abort('closed')
        r.addon?.dispose()
      }
      instances.clear()
      publish()
    },
  }
}

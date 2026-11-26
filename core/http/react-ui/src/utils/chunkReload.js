import { lazy } from 'react'

// A deploy swaps the whole content-hashed asset set at once, so a tab holding
// an older index.html (a long-lived tab or app WebView, or a request that lands
// on a replica that has not been swapped yet, the normal state during a rolling
// update) asks for a chunk the server no longer has. The import rejects and,
// without handling, the nearest error boundary replaces the app. Reloading is
// what fixes it: index.html is served no-cache, so the reload lands on a
// self-consistent set.
//
// The timestamp guard bounds that to one reload per RELOAD_WINDOW_MS. Without
// it, a chunk that is genuinely gone rather than merely stale would reload the
// app forever, which is worse than the error screen: it never settles and never
// says why. The lazy wrappers do not narrow the failure to fetch errors: the
// message differs per browser and a missed match costs the recovery, while a
// module that throws while evaluating costs one wasted reload before the error
// surfaces anyway.
export const RELOAD_KEY = 'localai.chunkReloadedAt'
export const RELOAD_WINDOW_MS = 10_000

// The messages of a dynamic import or a Vite preload that could not fetch its
// file, in Chromium, Firefox and Safari. Used only to choose the words of the
// error screen and whether it may reload on its own.
const CHUNK_ERROR = /dynamically imported module|Importing a module script failed|Unable to preload CSS|error loading dynamically|ChunkLoadError|Loading (CSS )?chunk .* failed/i

export function isChunkLoadError(err) {
  if (!err) return false
  if (err.name === 'ChunkLoadError') return true
  return CHUNK_ERROR.test(String(err.message ?? err))
}

// The reload guard over an injected window, so the unit tests can drive it.
export function createChunkRecovery({ storage, reload, now = () => Date.now() }) {
  let reloading = false

  // Takes the one reload of the window, or says that it is spent.
  function claim() {
    try {
      const last = Number(storage().getItem(RELOAD_KEY)) || 0
      if (now() - last < RELOAD_WINDOW_MS) return false
      storage().setItem(RELOAD_KEY, String(now()))
      return true
    } catch {
      // No usable sessionStorage means no way to bound the reloads.
      return false
    }
  }

  function reloadOnce() {
    if (reloading) return true
    if (!claim()) return false
    reloading = true
    reload()
    return true
  }

  // The catch of a failed import: reload, and stay pending until the reload
  // commits. Resolving or rejecting would flash the error screen in the
  // frames before it does. A second chunk failing in the same document waits
  // for the reload already under way. When the reload is spent, the error
  // goes on to the error screen.
  function recover(err) {
    if (!reloadOnce()) throw err
    return new Promise(() => {})
  }

  // Vite fires vite:preloadError on window when it cannot preload the CSS or
  // the shared chunks of a dynamic import. Cancelling the event stops Vite from
  // throwing, so cancel it only when a reload is under way; otherwise the
  // error goes on to the import's own catch and to the error screen.
  function onPreloadError(event) {
    if (reloadOnce()) event.preventDefault()
  }

  return { claim, reloadOnce, recover, onPreloadError, get reloading() { return reloading } }
}

const recovery = createChunkRecovery({
  storage: () => window.sessionStorage,
  reload: () => window.location.reload(),
})

export const reloadOnce = () => recovery.reloadOnce()
export const recoverFromChunkError = (err) => recovery.recover(err)

export function installPreloadErrorReload(target = window) {
  target.addEventListener('vite:preloadError', recovery.onPreloadError)
}

// React.lazy for a component that is not a route page (a panel, a viewer, a
// settings block): a failed import reloads once like a page does.
export function lazyWithReload(loader) {
  return lazy(() => loader().catch(recoverFromChunkError))
}

// What the error screen says about an error: 'offline' when the device has no
// network, 'updated' when a chunk failed to load (a deploy replaced it), and
// 'generic' for anything else.
export function routeErrorKind(err, online = true) {
  if (!isChunkLoadError(err)) return 'generic'
  return online === false ? 'offline' : 'updated'
}

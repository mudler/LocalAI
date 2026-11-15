// SPDX-License-Identifier: MIT
// Sharing a connection from the page of its add-on. The host owns the share:
// the page renders the host's dialog (sdk.ui.ConnectionShareDialog) with the
// ID of the binding, and the host makes the binding shareable and calls the
// sharing routes. The page's own code never reaches them: sdk.core.fetch
// refuses the sharing routes. A host without the dialog, or one that cannot
// share (local-owner mode), shows no Share button. No React and no network of
// its own: the node unit tests drive it directly.

// SHARE_KIND is the kind of a binding in the sharing API.
export const SHARE_KIND = 'connection'

// shareAvailable says whether the page offers sharing: the host draws the
// dialog of a binding and the listing says this server shares.
export function shareAvailable(sdk, listing) {
  return Boolean(listing && listing.sharing === true && sdk && sdk.ui && typeof sdk.ui.ConnectionShareDialog === 'function')
}

// sharedOfKind keeps the bindings shared with the person that are of the
// kind of this page. A row carries the name, the state and the owner only.
export function sharedOfKind(listing, kind) {
  const rows = Array.isArray(listing?.shared) ? listing.shared : []
  return rows.filter((c) => c && typeof c.id === 'string' && c.kind === kind)
}

// Which of two controls a screen offers, so a screen never carries two for the
// same action. Pure rules, no page code.

// Sharing needs named accounts to share with. An installation with one local
// owner has none, and the listing says so; the page then has no Share button.
export function canShare(listing) {
  return Boolean(listing && listing.sharing)
}

// Where "Add rows" is offered for the open table:
//   'toolbar' the toolbar button, for a table that has rows or a filter in use
//   'empty'   the button of the empty state, for a table with columns and no rows
//   'none'    a reader, or a table with no columns to fill
export function addRowsPlace({ canWrite, columnCount, loaded, rowCount, filtered }) {
  if (!canWrite || columnCount === 0) return 'none'
  if (loaded && rowCount === 0 && !filtered) return 'empty'
  return 'toolbar'
}

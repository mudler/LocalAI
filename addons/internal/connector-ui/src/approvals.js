// The model of the approvals list. No React, so a node test drives it.

// The state of one tool as the host reported it:
//   "asks"  the owner did not relax it: every call waits for a card;
//   "runs"  it runs without asking for the owner and for paired senders;
//   "idle"  the owner relaxed it, but the write mode of the connection is Read
//           only, so nothing runs at all.
//   "other" the owner relaxed it, but the kind of sender shown here is not the
//           owner or a paired sender, so this sender is still asked.
// sender is the kind of sender the view is for ("listed", "anyone", "paired",
// "role:<id>"), or empty for the owner's own view.
export function approvalState(a, sender = '') {
  if (!a || a.skip !== true) return 'asks'
  const who = Array.isArray(a.runs_without_asking) ? a.runs_without_asking : []
  if (who.length === 0) return 'idle'
  // Only the owner and a paired sender skip the card; the host reports both
  // together, so a paired sender is the one kind of sender that is not asked.
  if (sender && !who.includes(sender)) return 'other'
  return 'runs'
}

// The tools that run without asking, in the order the host gave them.
export const runningUnasked = (list) => (Array.isArray(list) ? list.filter((a) => approvalState(a) === 'runs').map((a) => a.tool) : [])

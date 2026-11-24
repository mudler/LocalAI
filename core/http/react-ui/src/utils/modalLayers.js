// A stack of the open modal layers (a side sheet, a confirm dialog). Only the
// topmost layer may react to Escape or trap Tab, so a dialog opened over a
// sheet is closed first and the sheet stays open under it.
const stack = []

// Registers a layer and returns { isTop, pop }. Call pop() when it closes.
export function pushLayer() {
  const layer = {}
  stack.push(layer)
  return {
    isTop: () => stack[stack.length - 1] === layer,
    pop: () => {
      const i = stack.indexOf(layer)
      if (i >= 0) stack.splice(i, 1)
    },
  }
}

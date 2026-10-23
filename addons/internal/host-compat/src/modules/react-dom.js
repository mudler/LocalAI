// Stand-in for `react-dom`: only the portal, through the SDK.
import { getSdk } from '../runtime.js'

export function createPortal(children, node) {
  return getSdk().portal(children, node)
}
export default { createPortal }

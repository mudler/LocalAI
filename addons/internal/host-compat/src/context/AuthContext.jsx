import { getSdk } from '../runtime.js'

// What the pages read of the signed-in person: the role and the feature
// flags, from the SDK's viewer. The server still decides every call.
export function useAuth() {
  const { viewer } = getSdk()
  return { isAdmin: viewer.isAdmin(), hasFeature: (name) => viewer.hasFeature(name) }
}

import { createContext } from 'react'

// The registry's value: the apps list, the runtime instances and what the
// host draws from them. Provided by AddonRegistryProvider (registry.jsx).
export const RegistryContext = createContext(null)

export const EMPTY_LIST = []

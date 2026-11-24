import { createContext } from 'react'

// The signed-in person's state. It lives apart from the provider so that
// components that must work with or without a provider (the add-on proposal
// card) can read the principal without throwing.
export const AuthContext = createContext(null)

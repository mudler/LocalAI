import { createContext } from 'react'

// The header slots a hub hands to the page under its tabs (see HubHeader.jsx):
// { supporting, actions } DOM nodes and claim(delta) to say a page fills the
// supporting line.
export const HubHeaderContext = createContext(null)

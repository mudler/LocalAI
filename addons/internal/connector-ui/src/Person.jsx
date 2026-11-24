/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { createContext, useContext } from 'react'
import { nameOf } from './people'

// id -> { name, chat, chatName }, from the sender log of the open binding.
export const PeopleContext = createContext(new Map())

export const usePeople = () => useContext(PeopleContext)

// A person as inline text: the name, then the ID in the mono face.
export default function Person({ id, name = '' }) {
  const n = nameOf(usePeople(), id, name)
  return n ? <span data-testid="connector-person"><span className="cn-person__name">{n}</span> · <span className="cn-mono">{id}</span></span> : <span className="cn-mono">{id}</span>
}

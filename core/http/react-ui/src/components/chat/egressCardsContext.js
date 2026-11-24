import { createContext } from 'react'

// The network cards of the open chat by the tool line they belong to
// (tool_seq), and how to answer them. The chat page provides it; the fold of
// a run of tool lines reads it to draw each card under its bash call, so a
// reload shows the cards where they were asked.
export const EgressCardsContext = createContext({ byTool: new Map(), chatId: '', egress: null })

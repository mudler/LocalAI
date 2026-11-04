import { createContext } from 'react'

// How a card in the thread sends a message to the open chat, for the cards
// that offer to ask the assistant again. null outside the chat page.
export const ChatSendContext = createContext(null)

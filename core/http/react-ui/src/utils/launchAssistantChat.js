// Opens a fresh chat already in LocalAI Assistant ("manage") mode. Chat.jsx
// reads localai_index_chat_data on mount and enables localaiAssistant for the
// new chat. Shared by the Home CTA and the top navbar quick-jump so there is
// one definition of how the assistant is launched.
export function launchAssistantChat(navigate, model = '') {
  const chatData = {
    model: model || '',
    mcpMode: false,
    localaiAssistant: true,
    newChat: true,
  }
  try { localStorage.setItem('localai_index_chat_data', JSON.stringify(chatData)) } catch { /* ignore */ }
  try { localStorage.setItem('localai_assistant_used', '1') } catch { /* ignore */ }
  navigate('/app/chat')
}

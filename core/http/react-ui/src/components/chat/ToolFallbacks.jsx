import { useTranslation } from 'react-i18next'

// The built-in tools that an installed add-on replaces but that it cannot
// serve in this chat now (stopped, switched off here, not granted by the
// chat's agent, or unable to run), from fallbacks of GET /api/chats/{id}/tools.
// One line each says so, and whether the built-in tool serves instead.
export function ToolFallbacks({ fallbacks }) {
  const { t } = useTranslation('chats')
  if (!fallbacks || fallbacks.length === 0) return null
  return (
    <div data-testid="chat-tool-fallbacks">
      {fallbacks.map((f) => (
        <p key={f.tool} className="cmd-notice__line" data-testid={`chat-tool-fallback-${f.tool}`} data-builtin={f.builtin ? 'true' : 'false'}>
          {t(f.builtin ? 'tools.fallbackBuiltin' : 'tools.fallbackNone', { tool: f.tool, addon: f.addon_title || f.addon })}
        </p>
      ))}
    </div>
  )
}

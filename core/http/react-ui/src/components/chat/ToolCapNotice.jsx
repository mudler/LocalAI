import { useTranslation } from 'react-i18next'
import Notice from '../ui/Notice'

// Where the administrator raises the limit (Settings > Tools).
export const TOOL_CAP_SETTING = '/app/settings?group=tools#settings-field-chats_addon_tool_limit'

// What the cap on add-on tools left out of this chat, from tool_cap of
// GET /api/chats/{id}/tools and GET /api/chats/draft/tools: which add-ons lost
// tools, how many of all the tools are available, and what the person can do.
// The server sends nothing when the cap cuts nothing, and nothing is drawn.
export function ToolCapNotice({ cap, isAdmin }) {
  const { t } = useTranslation('chats')
  if (!cap || !(cap.cut > 0)) return null
  return (
    <Notice
      className="cmd-notice"
      severity="info"
      icon="plug"
      title={t('tools.toolCap.title')}
      reason={t('tools.toolCap.reason', { offered: cap.offered, total: cap.total })}
      who={t(isAdmin ? 'tools.toolCap.whoAdmin' : 'tools.toolCap.who')}
      fix={isAdmin ? { label: t('tools.toolCap.fix'), to: TOOL_CAP_SETTING, testId: 'chat-tool-cap-link' } : null}
      testId="chat-tool-cap"
    >
      <p className="cmd-notice__lost">{t('tools.toolCap.lost')}</p>
      <ul className="cmd-notice__list" data-testid="chat-tool-cap-addons">
        {(cap.addons || []).map((a) => (
          <li key={a.id} data-testid={`chat-tool-cap-addon-${a.id}`}>{t('tools.toolCap.addon', { name: a.title || a.id, count: a.cut })}</li>
        ))}
      </ul>
    </Notice>
  )
}

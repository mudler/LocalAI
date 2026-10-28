// Helpers of the Slack page. No React, so a node test drives them.

// A Slack app has two tokens. The host keeps them as one sealed secret: the
// bot token and the app-level token joined by a space. The add-on reads them
// back in any order.
export function slackSecret(form) {
  const bot = (form.bot_token || '').trim()
  const app = (form.app_token || '').trim()
  return tokenProblem(form) || !bot ? '' : `${bot} ${app}`
}

// What is wrong with the typed tokens, as a key of the page's words, or ''.
// Nothing typed is no problem: an update without tokens keeps the stored ones.
export function tokenProblem(form) {
  const bot = (form.bot_token || '').trim()
  const app = (form.app_token || '').trim()
  if (!bot && !app) return ''
  if (!bot || !app) return 'tokensBoth'
  if (!bot.startsWith('xoxb-')) return 'botShape'
  if (!app.startsWith('xapp-')) return 'appShape'
  return ''
}

// A channel ID: a public (C) or private (G) channel, or a direct conversation (D).
export const isChannel = (x) => /^[CGD][A-Z0-9]+$/.test(x)

// The two allowlists of the page: people, and channels where everyone may
// mention the bot. Both are entries of the one allowlist of the binding.
export const SLACK_LISTS = [
  { key: 'users', label: 'allowUsers', hint: 'allowUsersHint', testId: 'connector-allow', match: (x) => !isChannel(x) },
  { key: 'channels', label: 'allowChannels', hint: 'allowChannelsHint', testId: 'connector-allow-channels', match: isChannel },
]

// The manifest of a Slack app that this add-on serves: Socket Mode, the bot
// events it reads and the scopes it needs. The page shows it to paste at
// api.slack.com/apps ("From an app manifest").
export const APP_MANIFEST = `display_information:
  name: LocalAI
features:
  app_home:
    messages_tab_enabled: true
    messages_tab_read_only_enabled: false
  bot_user:
    display_name: LocalAI
    always_online: true
oauth_config:
  scopes:
    bot:
      - app_mentions:read
      - channels:history
      - groups:history
      - im:history
      - mpim:history
      - chat:write
      - files:read
      - users:read
settings:
  event_subscriptions:
    bot_events:
      - app_mention
      - message.channels
      - message.groups
      - message.im
      - message.mpim
  interactivity:
    is_enabled: false
  org_deploy_enabled: false
  socket_mode_enabled: true
  token_rotation_enabled: false
`

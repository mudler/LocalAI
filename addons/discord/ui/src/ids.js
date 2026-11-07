// Helpers of the Discord page. No React, so a node test drives them.

// A Discord ID (a snowflake): 17 to 20 digits. Anything else is not an ID the
// bot can match. Names are refused on purpose: they are not unique and they
// change hands, so an allowlist by name would not be a safe key.
export const isSnowflake = (x) => /^\d{17,20}$/.test(x)

// The entries of the allowlist that are not IDs, in the order they were added.
export function badEntries(form) {
  if (form?.accept_any) return []
  return (form?.allow || []).filter((x) => !isSnowflake(String(x).trim()))
}

// The one allowlist of the page. It holds people and channels, as bare IDs:
// the host matches the sender of a message against the person IDs, and the
// channel of the message against the channel IDs. Both are numbers, so one
// list shows them together.
// An entry that can name a channel: a number. Discord does not tell a channel ID from
// a user ID by its shape, so every number gets the choice; for a user it changes nothing.
export const isChannelId = (x) => /^[0-9]+$/.test(x)

export const DISCORD_LISTS = [
  { key: 'ids', hint: 'allowIdsHint', testId: 'connector-allow' },
]

// What is wrong with the typed bot token, as a key of the page's words, or ''.
// Nothing typed is no problem: an update without a token keeps the stored one.
// A token has no space in it, and is long. The shape is not checked further:
// Discord may change it, and the server asks Discord whether it is good.
export function tokenProblem(form) {
  const t = (form?.token || '').trim()
  if (!t) return ''
  if (/\s/.test(t)) return 'tokenSpace'
  if (t.length < 30) return 'tokenShape'
  return ''
}

// The secret the form makes: the trimmed token, '' when not typed or wrong.
export const discordSecret = (form) => (tokenProblem(form) ? '' : (form?.token || '').trim())

// The permissions the bot needs, by Discord's bit numbers. The integer is
// their sum. Administrator, Mention Everyone and Manage Messages are not in
// it, on purpose.
export const PERMISSIONS = [
  { key: 'viewChannels', bit: 10 },
  { key: 'sendMessages', bit: 11 },
  { key: 'attachFiles', bit: 15 },
  { key: 'readHistory', bit: 16 },
  { key: 'sendInThreads', bit: 38 },
]

export const PERMISSION_INTEGER = PERMISSIONS.reduce((sum, p) => sum + 2n ** BigInt(p.bit), 0n).toString()

// The link that adds the bot to a server. The application ID is the number
// from General Information of the application in the developer portal.
export function inviteUrl(applicationId) {
  const id = (applicationId || '').trim()
  if (!isSnowflake(id)) return ''
  return `https://discord.com/oauth2/authorize?client_id=${id}&scope=bot&permissions=${PERMISSION_INTEGER}`
}

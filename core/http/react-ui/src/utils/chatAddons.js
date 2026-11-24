// The add-ons a chat can offer to a model, and which of them are on for one
// chat. Pure rules, shared by the header control, the /addons command and the
// request builder, so the three never disagree.
//
// The chat keeps `addonToolIds`, the ids that are on. A chat saved before the
// list existed has only the boolean `addonTools`; it means every add-on.

// The add-ons that ship tools right now: enabled, running and with at least
// one tool in the manifest. An add-on without tools is not listed at all.
export function toolAddons(apps) {
  if (!Array.isArray(apps)) return []
  return apps
    .filter(a => a && a.enabled && a.state === 'running' && Array.isArray(a.toolDefinitions) && a.toolDefinitions.length > 0)
    .map(a => ({ id: a.id, title: a.title || a.name || a.id, icon: a.icon || '' }))
}

// The ids that are on for this chat, limited to the add-ons that can be used.
export function enabledAddonIds(chat, available) {
  const ids = (available || []).map(a => a.id)
  if (!chat) return []
  if (Array.isArray(chat.addonToolIds)) return ids.filter(id => chat.addonToolIds.includes(id))
  return chat.addonTools === true ? ids : []
}

// The chat settings that turn one add-on on or off.
export function toggleAddonSettings(chat, available, id) {
  const on = enabledAddonIds(chat, available)
  const next = on.includes(id) ? on.filter(x => x !== id) : [...on, id]
  return { addonToolIds: next, addonTools: next.length > 0 }
}

// The chat settings for the master switch and the /addons command: all on when
// none is, otherwise all off.
export function toggleAllAddonSettings(chat, available) {
  const anyOn = enabledAddonIds(chat, available).length > 0
  const next = anyOn ? [] : (available || []).map(a => a.id)
  return { addonToolIds: next, addonTools: next.length > 0 }
}

// The request metadata. Every add-on on sends the boolean alone, as before;
// some on sends it with the list; none on sends nothing.
export function addonRequestMetadata(chat, available) {
  const on = enabledAddonIds(chat, available)
  if (on.length === 0) return {}
  if (on.length === (available || []).length) return { addon_tools: 'true' }
  return { addon_tools: 'true', addon_tool_ids: on.join(',') }
}

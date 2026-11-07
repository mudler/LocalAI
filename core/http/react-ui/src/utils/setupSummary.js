// The one line a setup gets in the menu and the Manage sheet. t is the chat
// namespace translator.
export function setupSummary(setup, t) {
  const off = ['builtins_off', 'addons_off', 'mcp_off', 'skills_off'].reduce((n, k) => n + (setup?.tools?.[k]?.length || 0), 0)
  const parts = []
  if (setup?.effort) parts.push(t('setup.effort', { level: t(`settings.effort.${setup.effort}`) }))
  parts.push(off > 0 ? t('setup.toolsOff', { count: off }) : t('setup.noneOff'))
  return parts.join(' · ')
}

// The names of what the Tools badge counts as on: built-ins (named by
// nameOf), add-ons, MCP servers and connectors that are on, usable and not
// waiting for a connection. The same filter as toolsCounts in chatMCP.js.
export function toolsOnLabels({ builtins = [], addons = [], servers = [], connectors = [] }, nameOf) {
  const on = (x) => !x.off && x.usable !== false && !x.needs_connection
  return [
    ...builtins.filter(on).map(nameOf),
    ...addons.filter(on).map((a) => a.title || a.id),
    ...servers.filter(on).map((x) => x.name),
    ...connectors.filter(on).map((x) => x.name),
  ]
}

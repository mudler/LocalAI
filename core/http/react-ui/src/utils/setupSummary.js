// The one line a setup gets in the menu and the Manage sheet. t is the chat
// namespace translator.
export function setupSummary(setup, t) {
  const off = ['builtins_off', 'addons_off', 'mcp_off', 'skills_off'].reduce((n, k) => n + (setup?.tools?.[k]?.length || 0), 0)
  const parts = []
  if (setup?.effort) parts.push(t('setup.effort', { level: t(`settings.effort.${setup.effort}`) }))
  parts.push(off > 0 ? t('setup.toolsOff', { count: off }) : t('setup.noneOff'))
  return parts.join(' · ')
}

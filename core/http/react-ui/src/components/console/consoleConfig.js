// Console configuration shared by ConsoleLayout (renders the rail) and Sidebar
// (renders the single tier entry + computes its active state). Keeping the tier
// definitions in one place means the sidebar entry, its active-path matching,
// and the console rail can never drift out of sync.
//
// A console is a tier that opens a secondary rail instead of expanding inline.
// Each item carries its own gating (adminOnly / authOnly / feature /
// requiresAgentPool) so visibility matches the legacy sidebar exactly. Note
// `/api/features` only emits: agents, mcp, fine_tuning, quantization,
// distributed, localai_assistant — capability-style flags (face_recognition,
// skills, …) come from hasFeature(), not the features map.

// Recognition (Faces/Voices) lives as a GROUP inside the Build console rather
// than its own tier — it's an AI capability you build with, so keeping it next
// to Automation/Training avoids it feeling split off on its own.
export const buildConsole = {
  id: 'build',
  titleKey: 'sections.build',
  icon: 'fas fa-screwdriver-wrench',
  groups: [
    {
      titleKey: 'console.automation',
      items: [
        { path: '/app/agents', icon: 'fas fa-robot', labelKey: 'items.agents', feature: 'agents', requiresAgentPool: true },
        { path: '/app/skills', icon: 'fas fa-wand-magic-sparkles', labelKey: 'items.skills', feature: 'skills', requiresAgentPool: true },
        { path: '/app/collections', icon: 'fas fa-database', labelKey: 'items.memory', feature: 'collections', requiresAgentPool: true },
        { path: '/app/agent-jobs', icon: 'fas fa-tasks', labelKey: 'items.jobs', feature: 'mcp', requiresAgentPool: true },
      ],
    },
    {
      titleKey: 'console.training',
      items: [
        { path: '/app/fine-tune', icon: 'fas fa-graduation-cap', labelKey: 'items.fineTune', feature: 'fine_tuning' },
        { path: '/app/quantize', icon: 'fas fa-compress', labelKey: 'items.quantize', feature: 'quantization' },
      ],
    },
    {
      titleKey: 'sections.recognition',
      items: [
        { path: '/app/face', icon: 'fas fa-face-smile', labelKey: 'items.faces', feature: 'face_recognition' },
        { path: '/app/voice', icon: 'fas fa-microphone-lines', labelKey: 'items.voices', feature: 'voice_recognition' },
      ],
    },
  ],
}

// Four groups, not six. Inference and Activity were both "the runtime right
// now"; Access and System were both administration. Six headings over thirteen
// items is a list with extra steps. Nothing is removed and no gate changes —
// the heading an item sits under is the only thing that moves.
//
// Overview leads the first group so firstVisiblePath() returns it without
// needing to know it exists. Before it, opening Operate landed on Backends
// because Backends happened to be written first.
//
// `signal` names a value the rail renders beside the label (see
// OperateSummaryContext). It is orientation while inside Operate, NOT an
// alarm: the rail exists only on Operate routes and can be collapsed, which is
// precisely why the operations badge stays on the sidebar entry. Nothing
// urgent may depend on a rail signal alone.
export const operateConsole = {
  id: 'operate',
  titleKey: 'sections.operate',
  icon: 'fas fa-sliders',
  groups: [
    {
      titleKey: 'operate.runtime',
      items: [
        { path: '/app/operate', icon: 'fas fa-gauge-high', labelKey: 'items.overview', adminOnly: true, signal: 'attention' },
        { path: '/app/backends', icon: 'fas fa-server', labelKey: 'items.backends', adminOnly: true, signal: 'backends' },
        { path: '/app/voice-library', icon: 'fas fa-wave-square', labelKey: 'items.voiceLibrary', adminOnly: true },
        { path: '/app/activity', icon: 'fas fa-download', labelKey: 'items.activity', adminOnly: true, badge: 'operations', signal: 'activity' },
      ],
    },
    {
      titleKey: 'operate.cluster',
      items: [
        { path: '/app/nodes', icon: 'fas fa-network-wired', labelKey: 'items.nodes', adminOnly: true, feature: 'distributed', signal: 'nodes' },
        { path: '/app/scheduling', icon: 'fas fa-calendar-alt', labelKey: 'items.scheduling', adminOnly: true, feature: 'distributed' },
        { path: '/app/p2p', icon: 'fas fa-circle-nodes', labelKey: 'items.swarm', adminOnly: true },
      ],
    },
    {
      titleKey: 'operate.observability',
      items: [
        { path: '/app/usage', icon: 'fas fa-chart-bar', labelKey: 'items.usage', adminOnly: true, signal: 'usage' },
        { path: '/app/traces', icon: 'fas fa-chart-line', labelKey: 'items.traces', adminOnly: true, signal: 'traces' },
      ],
    },
    {
      titleKey: 'operate.administration',
      items: [
        { path: '/app/users', icon: 'fas fa-users', labelKey: 'items.users', adminOnly: true, authOnly: true },
        { path: '/app/middleware', icon: 'fas fa-shield-halved', labelKey: 'items.middleware', adminOnly: true },
        { path: '/app/settings', icon: 'fas fa-cog', labelKey: 'items.settings', adminOnly: true },
        { href: '/swagger/index.html', icon: 'fas fa-code', labelKey: 'items.api', external: true, adminOnly: true },
      ],
    },
  ],
}

export const consoles = [buildConsole, operateConsole]

// Single source of truth for item visibility — mirrors the legacy sidebar
// filterItem + section gates.
export function isConsoleItemVisible(item, { isAdmin, authEnabled, hasFeature, features }) {
  if (item.adminOnly && !isAdmin) return false
  if (item.authOnly && !authEnabled) return false
  if (item.requiresAgentPool && features.agents === false) return false
  if (item.feature && features[item.feature] === false) return false
  if (item.feature && !hasFeature(item.feature)) return false
  return true
}

// All nav paths a console owns (for the sidebar entry's active-state match).
export function consolePaths(config) {
  return config.groups.flatMap(g => g.items.filter(i => i.path).map(i => i.path))
}

// The page the tier's single sidebar entry links to: its first visible item.
// Returns null when nothing is visible (so the entry can be hidden entirely).
export function firstVisiblePath(config, auth) {
  for (const group of config.groups) {
    for (const item of group.items) {
      if (item.path && isConsoleItemVisible(item, auth)) return item.path
    }
  }
  return null
}

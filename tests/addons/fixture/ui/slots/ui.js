// Slots fixture, version 1. Hand-written in the compiled form an add-on build
// produces: no bare `react` import. It contributes to every host slot, with a
// contribution that throws and a style sheet that collides with host classes.
const state = (window.__slotsFixture = window.__slotsFixture || { events: [] })
const log = (name) => { state.events.push(name) }

export function createAddon(sdk) {
  const h = sdk.React.createElement
  const { Button } = sdk.ui
  const generation = sdk.generation
  log('create:' + generation)
  sdk.signal.addEventListener('abort', () => log('abort:' + generation))

  const props = (testid) => (p) => h('span', { className: 'fx-chip', 'data-testid': testid, 'data-props': JSON.stringify(p) }, testid)

  function NavChip() {
    const href = sdk.navigation.href('home')
    return h('a', {
      className: 'fx-chip', href, 'data-testid': 'fx-nav',
      onClick: (e) => { e.preventDefault(); sdk.navigation.navigate('home') },
    }, 'Slots home')
  }
  function SettingsCard() {
    return h('div', { className: 'fx-card', 'data-testid': 'fx-settings' },
      h('h2', null, 'Fixture setting'),
      h('p', null, 'Settings contributed by the slots fixture.'),
      h(Button, null, 'Fixture button'))
  }
  function BrokenChip() { throw new Error('fixture composer contribution failure') }
  function BrokenSection() { throw new Error('fixture settings contribution failure') }
  function ExplodePanel() { throw new Error('fixture dialog failure') }
  function InspectPanel(p) {
    return h('div', { 'data-testid': 'fx-inspect', 'data-props': JSON.stringify({ conversationId: p.conversationId, messageId: p.messageId, role: p.role }) },
      h('p', null, 'Inspecting message ' + p.messageId),
      h(Button, { onClick: p.close }, 'Done'))
  }
  function ApprovePanel(p) {
    return h('div', { 'data-testid': 'fx-approve' },
      h('p', null, 'This add-on asks the host to show a request.'),
      h(Button, { onClick: () => { p.close(); sdk.approvals.present('op-fixture-1') } }, 'Show the request'))
  }
  function PingCard(p) {
    return h('div', { className: 'fx-card', 'data-testid': 'fx-card' },
      h('p', null, 'Card for ' + p.tool + ': ' + p.effects.count + ' changes'),
      h(Button, { onClick: () => log('card-clicked') }, 'Card button'))
  }
  function SummaryResult(p) {
    const ctx = p.result && p.result.arguments && p.result.arguments.context
    return h('div', { className: 'fx-card', 'data-testid': 'fx-result-card' },
      h('p', null, 'Summary of ' + (ctx && ctx.conversationId)),
      h('p', null, 'tool ' + p.tool))
  }
  function Home() { return h('section', null, h('h2', null, 'Slots home')) }

  return {
    Component: Home,
    components: {
      ComposerChip: props('fx-composer-chip'),
      MessageChip: props('fx-message-chip'),
      NavChip, SettingsCard, BrokenChip, BrokenSection, ExplodePanel, InspectPanel, ApprovePanel, PingCard, SummaryResult,
      // Not in the manifest: the host must never draw these.
      Rogue: () => h('span', { 'data-testid': 'fx-rogue' }, 'ROGUE'),
    },
    registerSlot() { log('register-slot-called') },
    dispose() { log('dispose:' + generation) },
  }
}

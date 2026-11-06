/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// One connection as a row, the way the MCP connectors page draws its rows: a
// letter tile, the name and a state chip, one line of facts, and on the right
// the one action the state asks for, the switch that applies at once and a
// menu. The row body opens the pane beside the list.
import ActionMenu from '../../host-compat/src/components/ActionMenu'
import StatusChip from '../../host-compat/src/components/ui/StatusChip'
import { Switch } from './controls'

const CHIP_TONE = { running: 'ok', ready: 'ok', off: 'idle', attention: 'warn' }

// A hue from the id, so two connections never share a tile colour by chance.
const hueOf = (text) => [...String(text)].reduce((n, ch) => (n * 31 + ch.codePointAt(0)) % 12, 7)

export function Tile({ c }) {
  const name = (c.name || c.id || '?').trim()
  return <span className="cn-tile" data-hue={hueOf(c.id || name)} aria-hidden="true">{[...name][0]?.toUpperCase()}</span>
}

export function StateChip({ state, label, title }) {
  return <StatusChip tone={CHIP_TONE[state] || 'idle'} title={title} testId="connector-state">{label}</StatusChip>
}

export default function ConnectionRow({ c, state, attention, fix, facts, line, selected, testing, busy, messaging, t, s, onOpen, onToggle, onTest, onFix, onEdit, onDelete, onShare }) {
  const stateText = s(`conn.state.${state}`)
  const items = [
    { key: 'edit', icon: 'pencil', label: t('editButton'), onClick: (trigger) => onEdit(c, trigger) },
    // Only where the host can share the binding (see share.js).
    { key: 'share', icon: 'share', label: s('share.label'), onClick: () => onShare(c), hidden: !onShare },
    { divider: true, key: 'divider' },
    { key: 'delete', icon: 'trash', label: t('delete'), danger: true, onClick: () => onDelete(c) },
  ]
  return (
    <li className="cn-conn" data-testid="connector-row" data-id={c.id} data-state={state} data-on={c.enabled ? 'true' : 'false'}
      data-attention={state === 'attention' ? 'true' : undefined} data-selected={selected ? 'true' : undefined}>
      <button type="button" className="cn-conn__open" aria-pressed={selected} data-testid="connector-open"
        aria-label={s('conn.row.open', { name: c.name, state: stateText })} onClick={() => onOpen(c.id)}>
        <Tile c={c} />
        <span className="cn-conn__text">
          <span className="cn-conn__head">
            <span className="cn-conn__name">{c.name}</span>
            <StateChip state={state} label={stateText} title={attention?.reason || undefined} />
          </span>
          <span className="cn-conn__facts" data-testid="connector-facts" title={facts}>{facts}</span>
          {line && <span className="cn-conn__line" data-testid={line.testId} title={line.text}>{line.text}</span>}
        </span>
      </button>
      <span className="cn-conn__side">
        {fix ? (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid={`connector-fix-${fix.kind}`} onClick={() => onFix(c, fix.kind)}>{fix.label}</button>
        ) : (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-busy={testing ? 'true' : undefined} disabled={testing}
            aria-label={s('conn.row.testNamed', { name: c.name })} data-testid="connector-test" onClick={() => onTest(c)}>{t('test')}</button>
        )}
        {messaging && !c.credentials_only && (
          <Switch checked={Boolean(c.enabled)} disabled={busy} label={t('onFor', { name: c.name })} testId="connector-switch" onChange={(on) => onToggle(c, on)} />
        )}
        <ActionMenu items={items} ariaLabel={s('conn.row.menu', { name: c.name })} compact />
      </span>
    </li>
  )
}

/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import ActionMenu from '../ActionMenu'
import { Button, Switch } from '../ui'
import { mcpTarget, sentence, testedWhen as when } from '../../utils/chatMCP'
import { ConnectorIcon, StateChip } from './ConnectorBits'

// One connector as a row of the Add-ons list: identity tile, name and state,
// one line that says what it does or what went wrong, and a meta line (where
// it is, its tools, what is saved, when it was tested). The row body opens the
// details beside the list. On the right: the one action its state asks for,
// the switch that applies at once, and the overflow menu.
export default function ConnectorRow({ c, state, check, selected, testing, busy, onOpen, onToggle, onTest, onEdit, onReauth, onRemove }) {
  const { t, i18n } = useTranslation('chats')
  const target = mcpTarget(c)
  const stdio = c.transport === 'stdio'
  const tested = when(check?.at, i18n.language)
  const stateText = t(`mcpPage.state.${state}`)

  let line
  if (state === 'off') line = t('mcpPage.row.offLine')
  else if (state === 'unchecked') line = t('mcpPage.row.notChecked')
  else if (state === 'ok') line = check.tools.length ? check.tools.map(x => x.name).join(', ') : t('mcpPage.row.noTools')
  else line = sentence(check.error)

  const saved = [
    c.has_token && t('mcpPage.row.tokenSaved'),
    c.has_headers && t('mcpPage.row.headersSaved'),
    c.has_env && t('mcpPage.row.envSaved'),
  ].filter(Boolean)

  // The one action that fits the state: a credential to replace, or a test.
  let act = null
  if (state === 'auth' && !stdio) {
    act = <Button size="sm" icon="key" onClick={() => onReauth(c)} data-testid="mcp-row-reauth">{t('mcpPage.actions.reauth')}</Button>
  } else if (state === 'unchecked' || state === 'error' || state === 'auth') {
    act = (
      <Button size="sm" icon="refresh" busy={testing} onClick={() => onTest(c)} aria-label={t('mcpPage.actions.testNamed', { name: c.name })} data-testid="mcp-row-test">
        {testing ? t('mcpPage.actions.testing') : t('mcpPage.actions.test')}
      </Button>
    )
  }

  const items = [
    { key: 'details', icon: 'info', label: t('mcpPage.actions.details'), onClick: () => onOpen(c.id) },
    { key: 'test', icon: 'refresh', label: t('mcpPage.actions.test'), disabled: testing, onClick: () => onTest(c) },
    { key: 'edit', icon: 'pencil', label: t('mcpPage.actions.edit'), onClick: () => onEdit(c) },
    { key: 'reauth', icon: 'key', label: t('mcpPage.actions.reauth'), hidden: stdio, onClick: () => onReauth(c) },
    { divider: true, key: 'divider' },
    { key: 'remove', icon: 'trash', label: t('mcpPage.actions.remove'), danger: true, onClick: () => onRemove(c) },
  ]

  return (
    <li
      className="ad-tile mcp-row"
      data-testid="mcp-row"
      data-id={c.id}
      data-on={c.enabled ? 'true' : 'false'}
      data-state={state === 'off' ? 'stopped' : state}
      data-attention={state === 'auth' || state === 'error' ? 'true' : undefined}
      data-selected={selected ? 'true' : undefined}
    >
      <button type="button" className="ad-tile__open" aria-pressed={selected} aria-label={t('mcpPage.row.open', { name: c.name, state: stateText })} onClick={() => onOpen(c.id)}>
        <ConnectorIcon c={c} />
        <span className="ad-tile__text">
          <span className="ad-tile__head">
            <span className="ad-tile__title">{c.name}</span>
            <StateChip state={state} />
          </span>
          <span className={`ad-tile__desc mcp-row__line mcp-row__line--${state}`} data-testid="mcp-row-line" title={line}>{line}</span>
          <span className="ad-meta">
            <span className="ad-meta__item mcp-row__host" title={target}>
              <Icon name={stdio ? 'terminal' : 'globe'} aria-hidden="true" />
              <span className="mcp-row__hosttext">{target}</span>
            </span>
            {check?.ok && (
              <span className="ad-meta__item"><Icon name="wrench" aria-hidden="true" /> {t('mcpPage.row.tools', { count: check.tools.length })}</span>
            )}
            {saved.length > 0 && (
              <span className="ad-meta__item"><Icon name="lock" aria-hidden="true" /> {saved.join(', ')}</span>
            )}
            {tested.label && (
              <span className="ad-meta__item" title={tested.exact}><Icon name="clock" aria-hidden="true" /> {t('mcpPage.row.checked', { when: tested.label })}</span>
            )}
          </span>
        </span>
      </button>
      <div className="ad-tile__side mcp-row__side">
        {act && <span className="ad-tile__act mcp-row__act">{act}</span>}
        <span className="mcp-row__switch">
          <Switch checked={c.enabled} disabled={busy} onChange={(on) => onToggle(c, on)} label={t('mcpPage.row.toggle', { name: c.name })} testId="mcp-switch" />
        </span>
        <ActionMenu items={items} ariaLabel={t('mcpPage.row.menu', { name: c.name })} compact />
      </div>
    </li>
  )
}

/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, StatusChip } from '../ui'
import { AppIcon } from '../addons/AddonBits'
import './mcp.css'

// The small parts of the MCP connectors page: the state chip, the identity
// tile, a secret field, the rows of name and secret, and the tool list. The
// tile and the row frame are the Add-ons ones, so the two pages look the same.

// One state per connector (utils/chatMCP connectorState) on the shared chip.
const CHIP = {
  ok: { status: 'running' },
  auth: { tone: 'warn', icon: 'key' },
  error: { status: 'failed' },
  unchecked: { tone: 'idle' },
  off: { status: 'stopped' },
}

export function StateChip({ state, title }) {
  const { t } = useTranslation('chats')
  const c = CHIP[state] || CHIP.unchecked
  const text = t(`mcpPage.state.${state}`)
  return (
    <span className="ad-state mcp-state" data-state={state} role="img" aria-label={t('mcpPage.state.label', { state: text })} title={title} data-testid="mcp-state">
      <StatusChip status={c.status} tone={c.tone} label={text} dot />
    </span>
  )
}

// The identity tile: the connector's first letter on a hue of its own.
export function ConnectorIcon({ c, size = 36 }) {
  return <AppIcon id={c.id} name={c.name} size={size} />
}

// A field for a secret: masked, with a button that shows it while the person
// checks what they typed. The value only goes to the server.
export function SecretInput({ id, value, onChange, placeholder, disabled, describedBy, label, inputRef, invalid }) {
  const { t } = useTranslation('chats')
  const [shown, setShown] = useState(false)
  return (
    <span className="mcp-secret">
      <input
        id={id}
        ref={inputRef}
        className="dk-input dk-input--mono mcp-secret__input"
        type={shown ? 'text' : 'password'}
        value={value}
        onChange={onChange}
        placeholder={placeholder}
        disabled={disabled}
        autoComplete="off"
        spellCheck={false}
        aria-label={label}
        aria-describedby={describedBy}
        aria-invalid={invalid || undefined}
      />
      <Button
        variant="ghost"
        size="sm"
        icon={shown ? 'eye-off' : 'eye'}
        className="mcp-secret__toggle"
        aria-label={shown ? t('mcpPage.sheet.hideValue') : t('mcpPage.sheet.showValue')}
        aria-pressed={shown}
        disabled={disabled}
        onClick={() => setShown(v => !v)}
      />
    </span>
  )
}

// Rows of a name and a secret value (headers, environment variables). A saved
// value is never shown: rows hold only what is new.
export function PairRows({ label, help, rows, onChange, nameLabel, valueLabel, addLabel, testId }) {
  const { t } = useTranslation('chats')
  const helpId = useId()
  return (
    <div className="dk-field" data-testid={testId} role="group" aria-label={label} aria-describedby={help ? helpId : undefined}>
      <span className="dk-label">{label}</span>
      {rows.map((r, i) => (
        <div className="mcp-pair" key={i}>
          <input className="dk-input dk-input--mono" aria-label={`${nameLabel} ${i + 1}`} placeholder={nameLabel} value={r.name} autoComplete="off" spellCheck={false}
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
          <SecretInput label={`${valueLabel} ${i + 1}`} value={r.value} placeholder={valueLabel}
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} />
          <Button variant="ghost" size="sm" icon="trash" className="mcp-pair__remove" aria-label={t('mcpPage.sheet.removeRow', { n: i + 1 })}
            onClick={() => onChange(rows.filter((_, j) => j !== i))} />
        </div>
      ))}
      <div>
        <Button size="sm" variant="ghost" icon="plus" onClick={() => onChange([...rows, { name: '', value: '' }])}>{addLabel}</Button>
      </div>
      {help && <p className="dk-hint" id={helpId}>{help}</p>}
    </div>
  )
}

// The tools a test found: name in mono, the description under it.
export function ToolList({ tools, testId = 'mcp-tools' }) {
  const { t } = useTranslation('chats')
  return (
    <ul className="mcp-tools" data-testid={testId}>
      {tools.map(x => (
        <li key={x.name} className="mcp-tool">
          <span className="mcp-tool__name"><Icon name="wrench" aria-hidden="true" />{x.name}</span>
          <span className={`mcp-tool__desc${x.description ? '' : ' mcp-tool__desc--none'}`}>{x.description || t('mcpPage.details.noDescription')}</span>
        </li>
      ))}
    </ul>
  )
}

import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { checkHost, hostEntries, hostsValue, MAX_ALLOWED_HOSTS } from '../../utils/egressHosts'

// The editor of a list of exact host names kept in one comma separated
// setting (Always allowed hosts): the entries as tags, an input that adds one
// on Enter, a comma or the Add button (a pasted list adds them all), the
// reason when an entry is refused, and the count. The list is a draft of the
// page, applied with the other settings.
export default function HostListField({ id, value, onChange, disabled, placeholder, allowPrivate }) {
  const { t } = useTranslation('admin')
  const errId = useId()
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const entries = hostEntries(value)

  const add = (raw) => {
    const parts = String(raw).split(/[,\s]+/).filter(Boolean)
    if (parts.length === 0) return
    const next = [...entries]
    let problem = ''
    for (const part of parts) {
      const r = checkHost(part, { existing: next, allowPrivate })
      if (r.entry && !r.error) next.push(r.entry)
      else if (!problem) problem = t(`settings.hosts.errors.${r.error}`, { value: part, max: MAX_ALLOWED_HOSTS })
    }
    if (next.length !== entries.length) onChange(hostsValue(next))
    setError(problem)
    setText(problem ? parts.join(', ') : '')
  }

  const remove = (entry) => {
    setError('')
    onChange(hostsValue(entries.filter(e => e !== entry)))
  }

  return (
    <div className="st-hosts" data-testid="settings-hosts">
      <div className="st-hosts__add">
        <input
          id={id} className="dk-input dk-input--mono" value={text} placeholder={placeholder} disabled={disabled}
          spellCheck={false} autoComplete="off" autoCapitalize="off"
          aria-invalid={!!error || undefined} aria-describedby={error ? errId : undefined}
          onChange={(e) => { setText(e.target.value); if (error) setError('') }}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add(text) }
          }}
          data-testid="settings-hosts-input"
        />
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={disabled || !text.trim()} onClick={() => add(text)} data-testid="settings-hosts-add">
          <Icon name="plus" /> {t('settings.hosts.add')}
        </button>
      </div>
      {error && <p id={errId} className="st-hosts__error" role="alert" data-testid="settings-hosts-error">{error}</p>}
      {entries.length > 0 && (
        <ul className="st-hosts__list" aria-label={t('settings.hosts.listLabel')} data-testid="settings-hosts-list">
          {entries.map(entry => (
            <li key={entry} className="st-hosts__item" data-testid="settings-hosts-item">
              <span className="st-hosts__name">{entry}</span>
              <button
                type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" disabled={disabled}
                aria-label={t('settings.hosts.remove', { host: entry })} title={t('settings.hosts.remove', { host: entry })}
                onClick={() => remove(entry)}
              >
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <p className="st-hosts__foot">
        <span>{entries.length === 0 ? t('settings.hosts.empty') : t('settings.hosts.hint')}</span>
        <span data-testid="settings-hosts-count">{t('settings.hosts.count', { n: entries.length, max: MAX_ALLOWED_HOSTS })}</span>
      </p>
    </div>
  )
}

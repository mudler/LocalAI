/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { SearchField, StatusChip } from '../ui'
import { STRING_KEYS, templateProblem } from '../../utils/look'
import { greetingKey } from '../../utils/greeting'

// defaultText is what the app shows for a key when the user has not changed
// it, in the language of the page, with its placeholders kept as {name}.
export function useDefaultText() {
  const { t } = useTranslation(['workspace', 'home', 'chat', 'nav'])
  return useMemo(() => ({
    'home.headline': t(`home:greeting.${greetingKey()}`),
    'composer.placeholder': t('chat:input.placeholderModel', { model: '{model}', interpolation: { escapeValue: false } }),
    'chat.empty.title': t('chat:empty.title'),
    'nav.newChat': t('nav:newChat'),
    'nav.home': t('nav:items.home'),
    'nav.recentChats': t('nav:recentChats'),
    'nav.allChats': t('nav:items.allChats'),
  }), [t])
}

// StringsField edits the text overrides of ui.json: one row per key of the
// allow-list, with the default text beside the user's own. An empty row
// removes the key. A broken placeholder shows on its row and blocks the save;
// the server refuses it too. Keys that are not on the list do not show and
// cannot be added here.
export default function StringsField({ id, name, value, onChange, disabled, describedBy }) {
  const { t } = useTranslation('workspace')
  const v = useMemo(() => (value && typeof value === 'object' && !Array.isArray(value) ? value : {}), [value])
  const defaults = useDefaultText()
  const [q, setQ] = useState('')
  const changed = Object.keys(STRING_KEYS).filter(k => typeof v[k] === 'string' && v[k] !== '').length
  const needle = q.trim().toLowerCase()
  const rows = Object.keys(STRING_KEYS).filter(k => !needle || k.toLowerCase().includes(needle) || (defaults[k] || '').toLowerCase().includes(needle) || String(v[k] || '').toLowerCase().includes(needle))
  const set = (k, text) => {
    const next = { ...v }
    if (text === '') delete next[k]
    else next[k] = text
    onChange(next)
  }

  return (
    <div className="wk-strings" id={id} aria-describedby={describedBy} data-testid={`wk-field-${name}-strings`}>
      <div className="wk-strings__bar">
        {changed > 0 && <StatusChip tone="accent" icon="pencil" testId="wk-strings-changed">{t('wellKnown.strings.changed', { count: changed })}</StatusChip>}
        <SearchField value={q} onChange={setQ} label={t('wellKnown.strings.find')} testId="wk-strings-find" />
      </div>
      <table className="wk-strings__table">
        <thead>
          <tr><th scope="col">{t('wellKnown.strings.key')}</th><th scope="col">{t('wellKnown.strings.default')}</th><th scope="col">{t('wellKnown.strings.yours')}</th></tr>
        </thead>
        <tbody>
          {rows.map(k => {
            const text = typeof v[k] === 'string' ? v[k] : ''
            const problem = templateProblem(text, STRING_KEYS[k])
            const rid = `${id}-${k.replace(/\./g, '-')}`
            return (
              <tr key={k} data-testid={`wk-strings-row-${k}`}>
                <th scope="row"><code>{k}</code></th>
                <td className="wk-strings__default">{defaults[k]}</td>
                <td>
                  <input
                    type="text" className="dk-input" id={rid} value={text} maxLength={120} disabled={disabled}
                    placeholder={t('wellKnown.strings.same')} aria-label={t('wellKnown.strings.yoursFor', { key: k })}
                    aria-invalid={problem ? 'true' : undefined} aria-describedby={problem ? `${rid}-err` : undefined}
                    data-testid={`wk-strings-input-${k}`} onChange={e => set(k, e.target.value)}
                  />
                  {problem && (
                    <p className="dk-field-error" id={`${rid}-err`} data-testid={`wk-strings-error-${k}`}>
                      {problem.unknown.length
                        ? t(STRING_KEYS[k].length ? 'wellKnown.placeholders.unknownUse' : 'wellKnown.placeholders.none', { names: problem.unknown.join(', '), use: STRING_KEYS[k].join(', '), count: problem.unknown.length })
                        : t('wellKnown.placeholders.brace')}
                    </p>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <p className="dk-hint">{t('wellKnown.strings.hint')}</p>
    </div>
  )
}

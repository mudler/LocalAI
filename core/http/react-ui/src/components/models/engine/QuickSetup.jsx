import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Callout, Segmented } from '../../ui'
import { CUSTOM_PROFILE, readProfile, withProfile } from '../../../utils/engineSettings'
import { estimateBody, memoryChange, memoryNow } from '../../../utils/runtimeMemory'
import { useRuntimeEstimates } from '../../../hooks/useRuntimeEstimates'

const NOW = '__now'

// Quick setup: the exclusive profiles of the engine (balanced, long prompts,
// more users...) as one choice, plus Custom for the fields below. The choice
// shown is the profile the values hold, so a file saved with a profile opens
// on it. Each profile shows how much more or less memory it needs than the
// current values, from the VRAM estimate of the server; nothing is applied
// until the person picks one.
//
//   schema     the normalized schema (profiles, estimate)
//   model      the model name, for the estimate
//   values     the editor's flat values
//   onPick     (profile) => void; applies the profile like a preset
//   readOnly   draw the choice without any way to change it
export default function QuickSetup({ schema, model, values, onPick, readOnly = false }) {
  const { t } = useTranslation('modelEditor')
  const [customPicked, setCustomPicked] = useState(false)
  const held = readProfile(schema, values)
  const current = customPicked ? CUSTOM_PROFILE : held
  // An edit or an undo that lands on a profile shows that profile again.
  useEffect(() => { setCustomPicked(false) }, [held])

  const bodies = useMemo(() => {
    const out = { [NOW]: estimateBody(schema, model, values) }
    for (const p of schema.profiles) out[p.id] = estimateBody(schema, model, withProfile(schema, values, p))
    return out
  }, [schema, model, values])
  const estimates = useRuntimeEstimates(bodies)
  const now = estimates.answers[NOW]
  const nowWords = memoryNow(now)
  const hasEstimate = Boolean(bodies[NOW])

  const name = (p) => t(`engineSettings.profiles.${p.id}.name`, { defaultValue: p.name })
  const text = (p) => t(`engineSettings.profiles.${p.id}.text`, { defaultValue: p.description })
  const rows = [
    ...schema.profiles.map(p => ({ id: p.id, name: name(p), text: text(p), profile: p })),
    { id: CUSTOM_PROFILE, name: t('engineSettings.quick.custom'), text: t('engineSettings.quick.customText') },
  ]

  const pick = (id) => {
    if (readOnly) return
    if (id === CUSTOM_PROFILE) { setCustomPicked(true); return }
    setCustomPicked(false)
    const p = schema.profiles.find(x => x.id === id)
    if (p && id !== held) onPick(p)
  }

  const memoryOf = (row) => {
    if (!hasEstimate || row.id === CUSTOM_PROFILE) return null
    if (row.id === held) return { kind: 'current', words: t('engineSettings.quick.current') }
    if (estimates.status === 'loading' || estimates.status === 'idle') return { kind: 'loading', words: t('engineSettings.quick.estimating') }
    const change = memoryChange(now, estimates.answers[row.id])
    if (!change) return { kind: 'none', words: t('engineSettings.quick.noEstimate') }
    if (change.kind === 'same') return { kind: 'same', words: t('engineSettings.quick.same') }
    return { kind: change.kind, words: t(`engineSettings.quick.${change.kind}`, { size: change.text }) }
  }

  const note = current !== CUSTOM_PROFILE
    ? t(`engineSettings.profiles.${current}.note`, { defaultValue: '' })
    : ''

  return (
    <div className="rt-quick" data-testid="rt-quick">
      <div className="rt-quick__head">
        <span className="dk-eyebrow" id="rt-quick-label">{t('engineSettings.quick.title')}</span>
        <Segmented
          mode="radio"
          labelledBy="rt-quick-label"
          value={current}
          options={rows.map(r => ({ id: r.id, label: r.name, disabled: readOnly }))}
          onChange={pick}
          itemTestId="rt-quick-"
          testId="rt-quick-choice"
        />
      </div>

      <ul className="rt-quick__list">
        {rows.map(r => {
          const mem = memoryOf(r)
          return (
            <li key={r.id} className={r.id === current ? 'rt-quick__row rt-quick__row--current' : 'rt-quick__row'} data-testid={`rt-quick-row-${r.id}`}>
              <span className="rt-quick__name">{r.name}</span>
              <span className="rt-quick__text">{r.text}</span>
              {mem && <span className={`rt-quick__mem rt-quick__mem--${mem.kind}`} data-testid={`rt-quick-mem-${r.id}`}>{mem.words}</span>}
            </li>
          )
        })}
      </ul>

      {hasEstimate && nowWords && (
        <p className="text-meta rt-quick__now" data-testid="rt-quick-now">
          {t('engineSettings.quick.now', nowWords)}{' '}
          {nowWords.approximate ? t('engineSettings.quick.approximate') : t('engineSettings.quick.estimate')}
        </p>
      )}
      {hasEstimate && estimates.status === 'error' && (
        <p className="text-meta rt-quick__now" data-testid="rt-quick-error">{t('engineSettings.quick.failed')}</p>
      )}

      {note && <Callout tone="info" testId="rt-quick-note">{note}</Callout>}
    </div>
  )
}

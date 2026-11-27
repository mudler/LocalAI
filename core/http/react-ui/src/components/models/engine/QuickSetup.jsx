import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, Segmented } from '../../ui'
import {
  CONCURRENCY_SET, CUSTOM_PROFILE, MEMORY_KINDS, readCombined, readProfile, resolveProfile, withProfile,
} from '../../../utils/engineSettings'
import {
  estimateBody, memoryChange, memoryKind, memoryNow, profileFit, tokensPerStream,
} from '../../../utils/runtimeMemory'
import { formatBytes } from '../../../utils/format'
import { useRuntimeEstimates } from '../../../hooks/useRuntimeEstimates'
import { useHostMemory } from '../../../hooks/useHostMemory'
import { useStoredChoice } from '../../../hooks/useStoredChoice'

const NOW = '__now'
const KIND_KEY = 'localai.runtime.memoryKind'

// Quick setup: the exclusive profiles of the engine as two choices, the batch
// (Balanced, Long prompts) and the concurrency (Default, 8, 16, 32+ streams),
// plus Custom for the fields below. Each choice shows the profile the values
// hold, so a file saved with one opens on it. Each profile shows how much more
// or less memory it needs than the current values, from the VRAM estimate of
// the server; nothing is applied until the person picks one.
//
// The values of a profile depend on the memory kind of the host (unified,
// discrete or no GPU): the server detects it, the person can override it, and
// the decision table in the schema gives the values of each kind.
//
//   schema     the normalized schema (profiles, concurrency, memory, estimate)
//   model      the model name, for the estimate
//   values     the editor's flat values
//   onPick     (preset) => void; applies a profile or an advice like a preset
//   readOnly   draw the choices without any way to change them
export default function QuickSetup({ schema, model, values, onPick, readOnly = false }) {
  const { t } = useTranslation('modelEditor')
  const host = useHostMemory()
  const [override, setOverride] = useStoredChoice(KIND_KEY, '', ['', ...MEMORY_KINDS])
  const { kind, source } = memoryKind(schema, host, override)
  // The host's memory size describes the detected kind only.
  const memory = host && host.kind === kind ? Number(host.total) || 0 : 0
  const fraction = schema.memory?.fitFraction || 1

  const bodies = useMemo(() => {
    const out = { [NOW]: estimateBody(schema, model, values) }
    for (const p of schema.profiles) out[`b:${p.id}`] = estimateBody(schema, model, withProfile(schema, values, resolveProfile(p, kind)))
    for (const p of schema.concurrency) {
      out[`c:${p.id}`] = estimateBody(schema, model, withProfile(schema, values, resolveProfile(p, kind)))
      const tight = resolveProfile(p, kind, true)
      if (tight.tight) out[`t:${p.id}`] = estimateBody(schema, model, withProfile(schema, values, tight))
    }
    return out
  }, [schema, model, values, kind])
  const estimates = useRuntimeEstimates(bodies)
  const now = estimates.answers[NOW]
  const nowWords = memoryNow(now)
  const hasEstimate = Boolean(bodies[NOW])

  const advice = (schema.memory?.advice || []).filter(a => a.kind === kind).map(a => {
    const c = schema.combined.find(x => x.id === a.control)
    if (!c || !a.avoid.includes(readCombined(schema, values, c))) return null
    const choice = c.choices.find(ch => ch.value === a.value)
    return choice ? { advice: a, combined: c, choice } : null
  }).filter(Boolean)

  return (
    <div className="rt-quick" data-testid="rt-quick">
      <span className="dk-eyebrow">{t('engineSettings.quick.title')}</span>

      {schema.memory && (
        <MemoryLine t={t} schema={schema} host={host} kind={kind} source={source}
          override={override} onOverride={setOverride} />
      )}

      <ProfileChoice
        t={t} set="" testId="rt-quick" schema={schema} values={values} kind={kind} readOnly={readOnly}
        title={t('engineSettings.quick.batch')} profiles={schema.profiles} onPick={onPick}
        estimates={estimates} hasEstimate={hasEstimate} customInChoice
      />

      {schema.concurrency.length > 0 && (
        <ProfileChoice
          t={t} set={CONCURRENCY_SET} testId="rt-conc" schema={schema} values={values} kind={kind} readOnly={readOnly}
          title={t('engineSettings.quick.concurrency')} profiles={schema.concurrency} onPick={onPick}
          estimates={estimates} hasEstimate={hasEstimate} memory={memory} fraction={fraction}
          context={now?.runtime?.context_length}
        />
      )}

      {hasEstimate && nowWords && (
        <p className="text-meta rt-quick__now" data-testid="rt-quick-now">
          {t('engineSettings.quick.now', nowWords)}{' '}
          {nowWords.approximate ? t('engineSettings.quick.approximate') : t('engineSettings.quick.estimate')}
        </p>
      )}
      {hasEstimate && estimates.status === 'error' && (
        <p className="text-meta rt-quick__now" data-testid="rt-quick-error">{t('engineSettings.quick.failed')}</p>
      )}

      {advice.map(({ advice: a, combined: c, choice }) => {
        const label = t(`engineSettings.choices.${c.id}.${choice.value}`, { defaultValue: choice.label })
        return (
          <Callout
            key={a.id} tone="warn" testId={`rt-advice-${a.id}`}
            actions={readOnly ? null : (
              <Button size="sm" onClick={() => onPick({ id: a.id, name: label, settings: choice.set, profile: false })} data-testid={`rt-advice-${a.id}-fix`}>
                {t('engineSettings.memory.useValue', { value: label })}
              </Button>
            )}
          >
            {t(`engineSettings.memory.advice.${a.id}`, { defaultValue: a.doc })}
          </Callout>
        )
      })}
    </div>
  )
}

// One line that says which memory kind the setups use, with a choice to
// override what the server detected.
function MemoryLine({ t, schema, host, kind, source, override, onOverride }) {
  const kindLabel = (k) => {
    const info = schema.memory.kinds.find(x => x.kind === k)
    return t(`engineSettings.memory.kinds.${k}.label`, { defaultValue: info?.label || k })
  }
  const info = schema.memory.kinds.find(x => x.kind === kind)
  let line
  if (source === 'detected') {
    line = host.device
      ? t('engineSettings.memory.detectedDevice', { kind: kindLabel(kind), device: host.device, size: host.total ? formatBytes(host.total) : '?' })
      : t('engineSettings.memory.detected', { kind: kindLabel(kind), size: host.total ? formatBytes(host.total) : '?' })
  } else if (source === 'override') {
    line = t('engineSettings.memory.chosen', { kind: kindLabel(kind) })
  } else {
    line = t('engineSettings.memory.unknown', { kind: kindLabel(kind) })
  }
  return (
    <div className="rt-quick__memory" data-testid="rt-memory">
      <div className="rt-quick__memory-text">
        <span className="rt-quick__memory-line" data-testid="rt-memory-line">{line}</span>
        <span className="text-meta">{t(`engineSettings.memory.kinds.${kind}.doc`, { defaultValue: info?.doc || '' })}</span>
      </div>
      <span className="dk-select-wrap">
        <select
          className="dk-select"
          aria-label={t('engineSettings.memory.label')}
          value={override}
          onChange={e => onOverride(e.target.value)}
          data-testid="rt-memory-kind"
        >
          <option value="">{host?.kind ? t('engineSettings.memory.auto', { kind: kindLabel(host.kind) }) : t('engineSettings.memory.autoUnknown')}</option>
          {schema.memory.kinds.map(k => <option key={k.kind} value={k.kind}>{kindLabel(k.kind)}</option>)}
        </select>
      </span>
    </div>
  )
}

// One exclusive choice over a set of profiles, with a row per profile.
function ProfileChoice({
  t, set, testId, title, schema, values, kind, profiles, onPick, readOnly, estimates, hasEstimate,
  customInChoice = false, memory = 0, fraction = 1, context,
}) {
  const [customPicked, setCustomPicked] = useState(false)
  const held = readProfile(schema, values, { set, kind })
  const current = customPicked ? CUSTOM_PROFILE : held
  // An edit or an undo that lands on a profile shows that profile again.
  useEffect(() => { setCustomPicked(false) }, [held])
  const pre = set ? 'c' : 'b'
  const now = estimates.answers[NOW]

  const fits = {}
  for (const p of profiles) {
    fits[p.id] = set
      ? profileFit(p, kind, memory, fraction, { plain: estimates.answers[`c:${p.id}`], tight: estimates.answers[`t:${p.id}`], now })
      : { state: 'ok', tight: false }
  }

  const name = (p) => t(`engineSettings.profiles.${p.id}.name`, { defaultValue: p.name })
  // The choice uses short labels (8, 16, 32+) so four fit a phone.
  const short = (p) => t(`engineSettings.profiles.${p.id}.short`, { defaultValue: name(p) })
  const text = (p) => t(`engineSettings.profiles.${p.id}.text`, { defaultValue: p.description })
  const rows = profiles.map(p => ({ id: p.id, name: name(p), short: short(p), text: text(p), profile: p }))
  const showCustom = customInChoice || current === CUSTOM_PROFILE
  if (showCustom) {
    const custom = t('engineSettings.quick.custom')
    rows.push({ id: CUSTOM_PROFILE, name: custom, short: custom, text: t('engineSettings.quick.customText') })
  }

  const pick = (id) => {
    if (readOnly) return
    if (id === CUSTOM_PROFILE) { setCustomPicked(true); return }
    setCustomPicked(false)
    const p = profiles.find(x => x.id === id)
    if (!p || id === held || fits[id].state !== 'ok') return
    onPick(resolveProfile(p, kind, fits[id].tight))
  }

  const memoryOf = (row) => {
    if (row.id === CUSTOM_PROFILE) return null
    const fit = fits[row.id]
    if (fit.state === 'unsupported') return { kind: 'off', words: t('engineSettings.quick.unsupported') }
    if (fit.state === 'small') return { kind: 'off', words: t('engineSettings.quick.needs', { size: formatBytes(fit.need) }) }
    if (fit.state === 'nofit') return { kind: 'off', words: t('engineSettings.quick.noFit', { size: formatBytes(fit.need), limit: formatBytes(fit.limit) }) }
    if (!hasEstimate) return null
    if (row.id === held) return { kind: 'current', words: t('engineSettings.quick.current') }
    if (estimates.status === 'loading' || estimates.status === 'idle') return { kind: 'loading', words: t('engineSettings.quick.estimating') }
    const answer = estimates.answers[`${fit.tight ? 't' : pre}:${row.id}`]
    const change = memoryChange(now, answer)
    if (!change) return { kind: 'none', words: t('engineSettings.quick.noEstimate') }
    if (change.kind === 'same') return { kind: 'same', words: t('engineSettings.quick.same') }
    return { kind: change.kind, words: t(`engineSettings.quick.${change.kind}`, { size: change.text }) }
  }

  const choice = rows.filter(r => r.id !== CUSTOM_PROFILE || customInChoice)
  const note = current !== CUSTOM_PROFILE
    ? t(`engineSettings.profiles.${current}.note`, { defaultValue: '' })
    : ''
  const labelId = `${testId}-label`

  return (
    <div className="rt-quick__set" data-testid={`${testId}-set`}>
      <div className="rt-quick__head">
        <span className="rt-quick__title" id={labelId}>{title}</span>
        <Segmented
          mode="radio"
          labelledBy={labelId}
          value={current}
          options={choice.map(r => ({ id: r.id, label: r.short, disabled: readOnly || (r.profile && fits[r.id].state !== 'ok') }))}
          onChange={pick}
          itemTestId={`${testId}-`}
          testId={`${testId}-choice`}
        />
      </div>

      <ul className="rt-quick__list">
        {rows.map(r => {
          const mem = memoryOf(r)
          const p = r.profile
          const share = p?.streams ? tokensPerStream(context, p.streams) : null
          const kindNote = p?.memory?.[kind]?.note
            ? t(`engineSettings.profiles.${p.id}.memory.${kind}`, { defaultValue: p.memory[kind].note })
            : ''
          const tight = p && fits[p.id].tight && fits[p.id].state === 'ok'
          return (
            <li key={r.id} className={r.id === current ? 'rt-quick__row rt-quick__row--current' : 'rt-quick__row'} data-testid={`${testId}-row-${r.id}`}>
              <span className="rt-quick__name">{r.name}</span>
              <span className="rt-quick__text">
                {r.text}
                {p?.bestFor && (
                  <span className="rt-quick__meta" data-testid={`${testId}-best-${r.id}`}>
                    {t('engineSettings.quick.bestFor', { use: t(`engineSettings.profiles.${p.id}.bestFor`, { defaultValue: p.bestFor }) })}
                  </span>
                )}
                {share !== null && (
                  <span className="rt-quick__meta" data-testid={`${testId}-share-${r.id}`}>
                    {t('engineSettings.quick.share', { tokens: share.toLocaleString() })}
                  </span>
                )}
                {(kindNote || tight) && (
                  <span className="rt-quick__meta" data-testid={`${testId}-kind-${r.id}`}>
                    {tight ? t('engineSettings.quick.tight') : kindNote}
                  </span>
                )}
              </span>
              {mem && <span className={`rt-quick__mem rt-quick__mem--${mem.kind}`} data-testid={`${testId}-mem-${r.id}`}>{mem.words}</span>}
            </li>
          )
        })}
      </ul>

      {set && <p className="text-meta rt-quick__now" data-testid={`${testId}-shared`}>{t('engineSettings.quick.shared')}</p>}
      {note && <Callout tone="info" testId={`${testId}-note`}>{note}</Callout>}
    </div>
  )
}

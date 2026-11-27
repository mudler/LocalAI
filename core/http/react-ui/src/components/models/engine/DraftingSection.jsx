import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, ListPicker, Segmented, StatusChip } from '../../ui'
import Icon from '../../Icon'
import { useRuntimeEstimates } from '../../../hooks/useRuntimeEstimates'
import { modelFileOf, useDraftCandidates } from '../../../hooks/useDraftCandidates'
import {
  CUSTOM_MODE, draftEstimateBody, draftFit, draftMemory, modelFacts, offeredModes, readDraftMode, writeDraftMode,
} from '../../../utils/drafting'
// eslint-disable-next-line no-unused-vars
import EngineField from './EngineField'

// The picker row of a draft file that is not an installed model.
const FILE_ROW = '__file'
const keyOf = (id) => id.replace(/\./g, '_')

// Drafting (speculative decoding): one choice of how tokens are drafted
// (off, n-grams, a draft model, the model's MTP head), the few fields that
// choice needs, and the engine's other drafting settings folded under it.
// The modes come from the engine's drafting capability; they write the same
// spec_type, draft_model and options entries as the YAML tab.
//
//   schema          the normalized schema (schema.drafting)
//   engine          the engine summary ({ id, name })
//   model           the model name: its estimate says whether it has an MTP
//                   head, and the draft picker leaves it out
//   values          the editor's flat values
//   more            the group's settings no mode draws
//   commit          (edit) => void; edits a copy of the values
//   onWrite         (item, value) => void
//   onValidity      (id, ok) => void
//   readOnly        draw without any way to change
export default function DraftingSection({ schema, engine, model, values, more, commit, onWrite, onValidity, readOnly = false }) {
  const { t } = useTranslation('modelEditor')
  const d = schema.drafting
  const [pending, setPending] = useState(null)
  const [picked, setPicked] = useState(null)
  const [pickError, setPickError] = useState(null)

  // The model's own estimate first: it names the architecture, the
  // vocabulary and an MTP head. read depends on it (an automatic mode).
  const [draftName, setDraftName] = useState(null)
  const bodies = useMemo(() => ({
    main: model ? { model } : null,
    draft: draftEstimateBody(schema, values, draftName),
  }), [schema, values, model, draftName])
  const estimates = useRuntimeEstimates(bodies)
  const facts = modelFacts(estimates.answers.main)

  const read = readDraftMode(schema, values, facts)
  const modelMode = d.modes.find(m => m.model)
  const shown = pending ?? read.mode
  // A pending choice ends when the file holds it.
  useEffect(() => { if (pending && read.mode === pending) setPending(null) }, [pending, read.mode])

  const pickerOn = Boolean(modelMode) && shown === modelMode.id
  const candidates = useDraftCandidates(model, engine.id, read.file, pickerOn)
  const current = read.file ? (candidates.matched || (picked && candidates.status !== 'ready' ? picked : null)) : null
  useEffect(() => { setDraftName(pickerOn ? current : null) }, [pickerOn, current])

  const mode = d.modes.find(m => m.id === shown)
  const offered = offeredModes(schema, facts, read.mode === CUSTOM_MODE ? null : read.mode)
  const modeWords = (m, part, fallback) => t(`engineSettings.drafting.modes.${m.id}.${part}`, { defaultValue: fallback })

  const choose = (id) => {
    if (readOnly || id === shown) return
    setPickError(null)
    const m = d.modes.find(x => x.id === id)
    if (m?.model && !read.file) { setPending(id); return }
    setPending(null)
    commit(draft => writeDraftMode(schema, draft, id, { facts, file: read.file }))
  }

  const pickModel = async (name) => {
    if (readOnly || !name || name === FILE_ROW) return
    setPickError(null)
    setPicked(name)
    try {
      const file = await modelFileOf(name)
      if (!file) throw new Error(t('engineSettings.drafting.picker.noFile', { name }))
      commit(draft => writeDraftMode(schema, draft, modelMode.id, { facts, file }))
    } catch (err) {
      setPicked(null)
      setPickError(err?.message || String(err))
    }
  }

  // A mode field in the mode's words. For an automatic mode left unset, the
  // default shown is the value the backend applies.
  const fieldOf = (id) => {
    const item = schema.items.find(i => i.id === id)
    if (!item) return null
    const base = `engineSettings.drafting.fields.${mode.id}.${keyOf(id)}`
    const label = t(`${base}.label`, { defaultValue: '' })
    const doc = t(`${base}.hint`, { defaultValue: '' })
    const auto = read.auto && mode.set[id] !== undefined
    return { ...item, label: label || undefined, doc: doc || item.doc, default: auto ? String(mode.set[id]) : item.default }
  }
  const streamsItem = d.streams && {
    ...d.streams.item,
    label: t('engineSettings.drafting.streams.label'),
    doc: t('engineSettings.drafting.streams.hint', { defaultValue: d.streams.doc }),
  }
  const fields = mode && shown !== d.modes[0].id ? [...mode.fields.map(fieldOf).filter(Boolean), ...(streamsItem ? [streamsItem] : [])] : []

  return (
    <fieldset className="rt-group rt-draft" data-testid="rt-drafting">
      <legend className="rt-group__title">{t('engineSettings.drafting.title')}</legend>
      <p className="dk-hint rt-group__lead">{t('engineSettings.drafting.lead')}</p>

      <div className="rt-quick__set rt-draft__choice">
        <span className="rt-quick__title" id="rt-draft-mode-label">{t('engineSettings.drafting.mode')}</span>
        <div className="rt-quick__control">
          <Segmented
            mode="radio"
            labelledBy="rt-draft-mode-label"
            value={shown === CUSTOM_MODE ? '' : shown}
            options={offered.map(m => ({ id: m.id, label: modeWords(m, 'label', m.label), disabled: readOnly }))}
            onChange={choose}
            itemTestId="rt-draft-mode-"
            testId="rt-draft-mode"
          />
          {mode && (
            <div className="rt-draft__about" data-testid={`rt-draft-panel-${mode.id}`}>
              <p className="rt-quick__line">{modeWords(mode, 'doc', mode.doc)}</p>
              {mode.note && (
                <p className="rt-draft__note" data-testid="rt-draft-note">
                  <Icon name="scale" className="rt-draft__note-icon" />
                  <span>{modeWords(mode, 'note', mode.note)}</span>
                </p>
              )}
            </div>
          )}
        </div>
      </div>

      {read.mode === CUSTOM_MODE && !pending && (
        <Callout tone="warn" title={t('engineSettings.drafting.custom.title')} testId="rt-draft-custom">
          {t('engineSettings.drafting.custom.body', { kinds: read.kinds.join(', ') })}
        </Callout>
      )}

      {mode && read.auto && shown === read.mode && (
        <Callout tone="info" testId="rt-draft-auto">
          {t('engineSettings.drafting.auto', { mode: modeWords(mode, 'label', mode.label) })}
        </Callout>
      )}

      {mode && (mode.model || fields.length > 0) && (
        <div className="rt-draft__panel">
          {mode.model && (
            <DraftPicker
              t={t}
              engine={engine}
              read={read}
              current={current}
              candidates={candidates}
              onPick={pickModel}
              error={pickError}
              readOnly={readOnly}
              facts={facts}
              estimates={estimates}
              model={model}
            />
          )}

          {fields.length > 0 && (
            <div className="rt-group__grid">
              {fields.map(item => (
                <EngineField key={item.id} item={item} values={values} onWrite={onWrite} onValidity={onValidity} readOnly={readOnly} />
              ))}
            </div>
          )}
        </div>
      )}

      {more.length > 0 && (
        <details className="rt-draft__more" data-testid="rt-draft-more">
          <summary className="rt-group__summary rt-draft__more-summary" data-testid="rt-draft-more-toggle">
            <Icon name="chevron-right" className="rt-group__chevron" />
            {t('engineSettings.drafting.more')}
            <span className="text-meta">{t('engineSettings.groupCount', { count: more.length })}</span>
          </summary>
          <div className="rt-group__grid rt-draft__more-grid">
            {more.map(item => (
              <EngineField key={item.id} item={item} values={values} onWrite={onWrite} onValidity={onValidity} readOnly={readOnly} />
            ))}
          </div>
        </details>
      )}
    </fieldset>
  )
}

// The draft model choice: the installed models an engine with drafting
// serves, the same family first, then whether the pick fits and what it
// costs in memory.
// eslint-disable-next-line no-unused-vars
function DraftPicker({ t, engine, read, current, candidates, onPick, error, readOnly, facts, estimates, model }) {
  const items = candidates.candidates.map(c => ({
    id: c.id, label: c.id, mono: true, group: c.sameFamily ? 'same' : 'other',
  }))
  const unmatched = read.file && !current && candidates.status === 'ready'
  if (unmatched) {
    items.unshift({ id: FILE_ROW, label: read.file, mono: true, group: 'file', description: t('engineSettings.drafting.picker.fileRow') })
  }
  const value = current || (unmatched ? FILE_ROW : '')
  const draftAnswer = estimates.answers.draft
  const fit = current && draftAnswer ? draftFit(facts, modelFacts(draftAnswer), { main: model, draft: current }) : null
  const mem = current ? draftMemory(draftAnswer) : null
  const estimating = current && (estimates.status === 'loading' || estimates.status === 'idle')

  return (
    <div className="rt-draft__model">
      <div className="dk-field rt-field">
        <label className="dk-label" htmlFor="rt-draft-model">{t('engineSettings.drafting.picker.label')}</label>
        <ListPicker
          id="rt-draft-model"
          variant="field"
          icon="cpu"
          items={items}
          value={value}
          onChange={onPick}
          placeholder={t('engineSettings.drafting.picker.placeholder')}
          groups={{
            same: t('engineSettings.drafting.picker.same'),
            other: t('engineSettings.drafting.picker.other'),
            file: t('engineSettings.drafting.picker.file'),
          }}
          loading={candidates.status === 'loading'}
          loadingText={t('engineSettings.drafting.picker.loading')}
          emptyText={t('engineSettings.drafting.picker.empty', { engine: engine.name })}
          filterPlaceholder={t('engineSettings.drafting.picker.filter')}
          disabled={readOnly}
          describedBy="rt-draft-model-hint"
          testId="rt-draft-model"
        />
        <p className="dk-hint rt-field__hint" id="rt-draft-model-hint">
          <code className="rt-flag rt-flag--hint">draft_model</code>
          <span>{t('engineSettings.drafting.picker.hint')}</span>
        </p>
      </div>

      {candidates.status === 'error' && (
        <Callout
          tone="error"
          role="alert"
          testId="rt-draft-models-error"
          actions={<Button size="sm" icon="refresh" onClick={candidates.retry}>{t('engineSettings.schemaError.retry')}</Button>}
        >
          {t('engineSettings.drafting.picker.failed')}
        </Callout>
      )}
      {error && <Callout tone="error" role="alert" testId="rt-draft-pick-error">{error}</Callout>}

      {(current || unmatched) && (
        <dl className="rt-draft__facts" data-testid="rt-draft-facts">
          <div className="rt-draft__fact">
            <dt className="dk-eyebrow">{t('engineSettings.drafting.fit.title')}</dt>
            <dd data-testid="rt-draft-fit">
              {unmatched && <span className="text-note">{t('engineSettings.drafting.fit.unmatched')}</span>}
              {estimating && !fit && <span className="text-note">{t('engineSettings.quick.estimating')}</span>}
              {fit && <FitWords t={t} fit={fit} />}
            </dd>
          </div>
          <div className="rt-draft__fact">
            <dt className="dk-eyebrow">{t('engineSettings.drafting.memory.title')}</dt>
            <dd data-testid="rt-draft-memory">
              {unmatched && <span className="text-note">{t('engineSettings.drafting.memory.unmatched')}</span>}
              {estimating && !mem && <span className="text-note">{t('engineSettings.quick.estimating')}</span>}
              {mem && (
                <span>
                  <strong className="rt-draft__size">{t('engineSettings.drafting.memory.total', { size: mem.total })}</strong>
                  {' '}
                  {mem.weights && mem.kv && mem.context
                    ? t('engineSettings.drafting.memory.split', { weights: mem.weights, kv: mem.kv, context: mem.context.toLocaleString() })
                    : null}
                  {' '}
                  <span className="text-note">{mem.approximate ? t('engineSettings.quick.approximate') : t('engineSettings.quick.estimate')}</span>
                </span>
              )}
              {current && !estimating && !mem && <span className="text-note">{t('engineSettings.drafting.memory.none')}</span>}
            </dd>
          </div>
        </dl>
      )}
    </div>
  )
}

// eslint-disable-next-line no-unused-vars
function FitWords({ t, fit }) {
  if (fit.kind === 'ok') {
    return (
      <span className="rt-draft__fit">
        <StatusChip tone="ok" testId="rt-draft-fit-chip">{t('engineSettings.drafting.fit.ok')}</StatusChip>
        <span>{t('engineSettings.drafting.fit.okText', { tokens: fit.vocab.toLocaleString() })}</span>
      </span>
    )
  }
  if (fit.kind === 'mismatch') {
    return (
      <span className="rt-draft__fit">
        <StatusChip tone="error" testId="rt-draft-fit-chip">{t('engineSettings.drafting.fit.mismatch')}</StatusChip>
        <span>{t('engineSettings.drafting.fit.mismatchText', { main: fit.main.toLocaleString(), draft: fit.draft.toLocaleString() })}</span>
      </span>
    )
  }
  if (fit.kind === 'family') {
    return (
      <span className="rt-draft__fit">
        <StatusChip tone="idle" testId="rt-draft-fit-chip">{t('engineSettings.drafting.fit.family')}</StatusChip>
        <span>{t('engineSettings.drafting.fit.familyText', { family: fit.architecture })}</span>
      </span>
    )
  }
  return (
    <span className="rt-draft__fit">
      <StatusChip tone="idle" testId="rt-draft-fit-chip">{t('engineSettings.drafting.fit.unknown')}</StatusChip>
      <span>{t('engineSettings.drafting.fit.unknownText')}</span>
    </span>
  )
}

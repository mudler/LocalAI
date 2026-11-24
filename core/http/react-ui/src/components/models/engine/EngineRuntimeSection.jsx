import { useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, Tag } from '../../ui'
import Icon from '../../Icon'
import { useEngineSchema } from '../../../hooks/useEngineSettings'
import { engineSettingsApi } from '../../../utils/api'
import {
  CAPABILITY_COMMAND_LINE, MODEL_FILE_KEY, applyParsed, applyPreset, describeChanges, diffPaths,
  groupItems, hasCapability, ownedOptionNames, rawOptionFor, readCombined, splitEntry, undoChanges,
  unflatten, writeCombined, writeEntry, writeItem,
} from '../../../utils/engineSettings'
// eslint-disable-next-line no-unused-vars
import EngineField from './EngineField'
import { choiceWords, fieldLabel } from './labels'
// eslint-disable-next-line no-unused-vars
import TensorPlacement from './TensorPlacement'
// eslint-disable-next-line no-unused-vars
import RawOptions from './RawOptions'
// eslint-disable-next-line no-unused-vars
import { CommandPreview, PasteCommand } from './CommandTools'
import './engine-settings.css'

const humanize = (id) => id.replace(/_/g, ' ').replace(/^./, c => c.toUpperCase())
const testIdOf = (id) => `rt-${id.replace(/_/g, '-')}`

function combinedLabel(t, c) {
  return t(`engineSettings.fields.${c.id}`, { defaultValue: humanize(c.id) })
}

function choiceLabel(t, c, value) {
  const ch = c.choices.find(x => x.value === value)
  return t(`engineSettings.choices.${c.id}.${value}`, { defaultValue: ch?.label ?? value })
}

// The words of one value in the list of what a preset or a command changed.
function valueWords(t, v, change) {
  if (v === undefined || v === null) return t('engineSettings.default')
  if (change?.combined) return choiceLabel(t, change.combined, v)
  return choiceWords(t, v)
}

function changeLabel(t, change) {
  if (change.item) return fieldLabel(t, change.item)
  if (change.combined) return combinedLabel(t, change.combined)
  if (change.placement) return t('engineSettings.placement.title')
  return change.id
}

// Runtime tuning: how the engine of the model's backend loads and runs it, as
// the settings of the engine's schema. It edits the same values as the rest of
// the editor (YAML keys and `options:` entries), so the YAML tab and a save see
// exactly what the form shows. Everything engine-specific comes from the
// server: the fields, the groups, the combined controls, the presets, and the
// parts of a capability (the command preview and the paste box for
// command-line, the placement block for tensor-placement).
//
//   engine          the engine summary of GET /api/engines ({ id, name, binary })
//   values          the editor's flat values (dotted paths, `options` list)
//   onChange        (path, value) => void; undefined unsets the key
//   readOnly        draw the settings without any way to change them
//   onOpenYaml      switches the editor to the YAML tab (the fallback when the
//                   schema cannot be read)
//   onValidity      (ok) => void; false while a field holds text that does not validate
export default function EngineRuntimeSection({ engine, values, onChange, readOnly = false, onOpenYaml, onValidity }) {
  const { t } = useTranslation('modelEditor')
  const state = useEngineSchema(engine.id)
  const schema = state.schema
  const blocks = useMemo(() => (schema ? groupItems(schema) : []), [schema])
  const owned = useMemo(() => (schema ? ownedOptionNames(schema) : new Set()), [schema])
  const words = { engine: engine.name, binary: engine.binary || engine.name }

  // The last preset or pasted command, so its changes can be listed and undone.
  const [applied, setApplied] = useState(null)

  // Fields holding text that does not validate. The editor keeps Save off
  // while any does, so a half-typed value is never mistaken for a saved one.
  const invalid = useRef(new Set())
  const onFieldValidity = useCallback((id, ok) => {
    const had = invalid.current.size > 0
    if (ok) invalid.current.delete(id)
    else invalid.current.add(id)
    const has = invalid.current.size > 0
    if (had !== has) onValidity?.(!has)
  }, [onValidity])

  // Every edit is made on a copy of the values, then each path that changed is
  // handed to the editor: one rule for a field, a preset and a pasted command.
  const commit = useCallback((edit) => {
    const draft = { ...values }
    const out = edit(draft)
    for (const [path, value] of diffPaths(values, draft)) onChange(path, value)
    return { draft, out }
  }, [values, onChange])

  const write = useCallback((item, value) => commit(d => writeItem(d, item, value)), [commit])

  // A preset with a tensor placement asks the server for its entry first, so
  // the pattern is the one the server builds everywhere else.
  const [presetBusy, setPresetBusy] = useState(null)
  const [presetError, setPresetError] = useState(null)
  const runPreset = async (preset) => {
    setPresetError(null)
    let entry
    if (preset.placement) {
      setPresetBusy(preset.id)
      try {
        entry = (await engineSettingsApi.placement(engine.id, { placement: preset.placement }))?.option
      } catch (err) {
        setPresetError(t('engineSettings.placement.failed', { message: err?.message || String(err) }))
        return
      } finally {
        setPresetBusy(null)
      }
    }
    const { draft } = commit(d => applyPreset(schema, d, preset, entry))
    const changes = describeChanges(schema, values, draft)
    setApplied({ kind: 'preset', name: t(`engineSettings.presets.${preset.id}.name`, { defaultValue: preset.name }), changes })
  }

  const writePlacement = useCallback((entry) => commit(d => {
    const next = writeEntry(d.options, schema.placement.names, entry)
    if (next) d.options = next
    else delete d.options
  }), [commit, schema])

  const applyCommand = (parsed) => {
    const { draft, out } = commit(d => applyParsed(schema, d, parsed))
    const changes = describeChanges(schema, values, draft)
    setApplied(changes.length ? { kind: 'paste', name: t('engineSettings.paste.source'), changes } : null)
    return { count: changes.length, modelFile: out.modelFile }
  }

  const undo = () => {
    if (!applied) return
    commit(d => undoChanges(d, applied.changes))
    setApplied(null)
  }

  const keepRaw = (unmapped) => {
    const entry = rawOptionFor(unmapped)
    commit(d => {
      const list = Array.isArray(d.options) ? d.options : []
      if (!list.includes(entry)) d.options = [...list, entry]
    })
  }
  // Removes a raw entry the server kept for an unmapped flag.
  const dropRaw = (flag) => {
    commit(d => {
      const rest = (Array.isArray(d.options) ? d.options : []).filter(e => splitEntry(e).name !== flag)
      if (rest.length) d.options = rest
      else delete d.options
    })
  }

  const config = useMemo(() => unflatten(values), [values])

  if (state.status === 'loading' || state.status === 'idle') {
    return (
      <section className="rt" data-testid="runtime" aria-busy="true">
        <Head t={t} engine={engine} />
        <div className="rt__skeleton" role="status" aria-label={t('engineSettings.loading', words)} data-testid="runtime-loading">
          <span className="dk-skeleton dk-skeleton--line" />
          <span className="dk-skeleton dk-skeleton--block" />
          <span className="dk-skeleton dk-skeleton--block" />
        </div>
      </section>
    )
  }

  if (state.status === 'error') {
    return (
      <section className="rt" data-testid="runtime">
        <Head t={t} engine={engine} />
        <Callout
          tone="warn"
          role="alert"
          title={t('engineSettings.schemaError.title')}
          testId="runtime-schema-error"
          actions={(
            <>
              <Button size="sm" icon="refresh" onClick={state.retry} data-testid="runtime-retry">{t('engineSettings.schemaError.retry')}</Button>
              {onOpenYaml && <Button size="sm" icon="code" onClick={onOpenYaml} data-testid="runtime-open-yaml">{t('engineSettings.schemaError.openYaml')}</Button>}
            </>
          )}
        >
          {t('engineSettings.schemaError.body', { ...words, message: state.error })}
        </Callout>
      </section>
    )
  }

  const commandLine = hasCapability(schema, CAPABILITY_COMMAND_LINE)
  const groupTitle = (id) => t(`engineSettings.groups.${id}`, { defaultValue: humanize(id) })

  return (
    <section className="rt" data-testid="runtime">
      <Head t={t} engine={engine} />

      {readOnly && (
        <Callout tone="info" icon="lock" title={t('engineSettings.readOnly.title')} testId="runtime-read-only">
          {t('engineSettings.readOnly.body')}
        </Callout>
      )}

      {!readOnly && schema.presets.length > 0 && (
        <div className="rt__presets" role="group" aria-labelledby="rt-presets-label" data-testid="rt-presets">
          <span className="dk-eyebrow" id="rt-presets-label">{t('engineSettings.presets.title')}</span>
          <div className="rt__preset-list">
            {schema.presets.map(p => (
              <div className="rt__preset" key={p.id}>
                <Button size="sm" icon="sliders" busy={presetBusy === p.id} disabled={Boolean(presetBusy)} onClick={() => runPreset(p)} data-testid={`rt-preset-${p.id}`}>
                  {t(`engineSettings.presets.${p.id}.name`, { defaultValue: p.name })}
                </Button>
                <span className="text-meta">{t(`engineSettings.presets.${p.id}.text`, { defaultValue: p.description })}</span>
              </div>
            ))}
          </div>
          {presetError && <Callout tone="error" role="alert" testId="rt-preset-error">{presetError}</Callout>}
        </div>
      )}

      {applied && (
        <Callout
          tone="info"
          title={t('engineSettings.applied.title', { name: applied.name, count: applied.changes.length })}
          testId="rt-applied"
          actions={(
            <>
              <Button size="sm" icon="undo" onClick={undo} data-testid="rt-undo">{t('engineSettings.applied.undo')}</Button>
              <Button size="sm" variant="ghost" onClick={() => setApplied(null)} data-testid="rt-applied-dismiss">{t('engineSettings.applied.dismiss')}</Button>
            </>
          )}
        >
          {applied.changes.length === 0
            ? t('engineSettings.applied.none')
            : (
              <ul className="rt-changes" data-testid="rt-changes">
                {applied.changes.map(c => (
                  <li key={c.id} data-testid={`rt-change-${c.id.replace(/\./g, '-')}`}>
                    <span className="rt-changes__name">{changeLabel(t, c)}</span>
                    <span className={c.from === undefined ? 'rt-changes__unset' : 'rt-changes__from'}>{valueWords(t, c.from, c)}</span>
                    <Icon name="arrow-right" className="rt-changes__arrow" />
                    <span className={c.to === undefined ? 'rt-changes__unset' : 'rt-changes__to'}>{valueWords(t, c.to, c)}</span>
                  </li>
                ))}
              </ul>
            )}
          {applied.changes.length > 0 && <p className="rt-changes__note">{t('engineSettings.applied.note')}</p>}
        </Callout>
      )}

      <div className={commandLine ? 'rt__layout' : 'rt__layout rt__layout--single'}>
        <div className="rt__main">
          {blocks.map(g => (g.placement
            ? (
              <fieldset className="rt-group" key={g.id} data-testid={`rt-group-${g.id}`}>
                <legend className="rt-group__title">{t('engineSettings.placement.title')}</legend>
                <TensorPlacement engine={engine} placement={schema.placement} values={values} onEntry={writePlacement} onValidity={onFieldValidity} readOnly={readOnly} />
              </fieldset>
            )
            : (
              <fieldset className="rt-group" key={g.id} data-testid={`rt-group-${g.id}`}>
                <legend className="rt-group__title">{groupTitle(g.id)}</legend>
                <div className="rt-group__grid">
                  {g.items.map(item => (
                    <EngineField key={item.id} item={item} values={values} onWrite={write} onValidity={onFieldValidity} readOnly={readOnly} />
                  ))}
                  {g.combined.map(c => {
                    const id = testIdOf(c.id)
                    return (
                      <div className="dk-field rt-field" key={c.id} data-testid={`${id}-field`}>
                        <label className="dk-label" htmlFor={id}>{combinedLabel(t, c)}</label>
                        <span className="dk-select-wrap">
                          <select
                            id={id}
                            className="dk-select"
                            value={readCombined(schema, values, c)}
                            disabled={readOnly}
                            aria-describedby={`${id}-hint`}
                            onChange={e => commit(d => writeCombined(schema, d, c, e.target.value))}
                            data-testid={id}
                          >
                            {c.choices.map(ch => <option key={ch.value} value={ch.value}>{choiceLabel(t, c, ch.value)}</option>)}
                          </select>
                        </span>
                        <p className="dk-hint rt-field__hint" id={`${id}-hint`}>
                          {c.flag && <code className="rt-flag">{c.flag}</code>}
                          <span>{t(`engineSettings.hints.${c.id}`, { defaultValue: c.doc })}</span>
                        </p>
                      </div>
                    )
                  })}
                </div>
              </fieldset>
            )))}

          <fieldset className="rt-group" data-testid="rt-group-advanced">
            <legend className="rt-group__title">{t('engineSettings.raw.title')}</legend>
            <p className="dk-hint rt-group__lead">{t('engineSettings.raw.hint', words)}</p>
            <RawOptions
              values={values}
              owned={owned}
              onOptions={next => onChange('options', next)}
              onValidity={onFieldValidity}
              readOnly={readOnly}
            />
          </fieldset>
        </div>

        {commandLine && (
          <aside className="rt__aside" aria-label={t('engineSettings.preview.aside')}>
            <CommandPreview engine={engine} config={config} />
            {!readOnly && (
              <PasteCommand
                engine={engine}
                example={schema.example}
                onApply={applyCommand}
                onKeep={keepRaw}
                onDrop={dropRaw}
                onUseModelFile={file => onChange(MODEL_FILE_KEY, file)}
                currentModelFile={values[MODEL_FILE_KEY]}
              />
            )}
          </aside>
        )}
      </div>
    </section>
  )
}

// eslint-disable-next-line no-unused-vars
function Head({ t, engine }) {
  return (
    <div className="rt__head">
      <h2 className="rt__title">{t('engineSettings.title')}</h2>
      <Tag mono testId="runtime-engine">{engine.name}</Tag>
      <p className="rt__lead text-note">{engine.binary ? t('engineSettings.lead', { engine: engine.name, binary: engine.binary }) : t('engineSettings.leadNoCommand', { engine: engine.name })}</p>
    </div>
  )
}

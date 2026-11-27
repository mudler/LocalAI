import { useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, Switch, Tag } from '../../ui'
import Icon from '../../Icon'
import { useEngineSchema } from '../../../hooks/useEngineSettings'
import { engineSettingsApi } from '../../../utils/api'
import { useStoredChoice } from '../../../hooks/useStoredChoice'
import {
  CAPABILITY_COMMAND_LINE, MODEL_FILE_KEY, applyParsed, applyPreset, applyProfile, describeChanges, diffPaths,
  groupItems, hasCapability, ownedOptionNames, rawOptionFor, readCombined, splitEntry, undoChanges,
  unflatten, writeCombined, writeEntry, writeItem,
} from '../../../utils/engineSettings'
// eslint-disable-next-line no-unused-vars
import EngineField from './EngineField'
import { choiceWords, fieldLabel } from './labels'
// eslint-disable-next-line no-unused-vars
import MoreText from './MoreText'
// eslint-disable-next-line no-unused-vars
import TensorPlacement from './TensorPlacement'
// eslint-disable-next-line no-unused-vars
import RawOptions from './RawOptions'
// eslint-disable-next-line no-unused-vars
import { CommandPreview, PasteCommand } from './CommandTools'
// eslint-disable-next-line no-unused-vars
import QuickSetup from './QuickSetup'
// eslint-disable-next-line no-unused-vars
import DraftingSection from './DraftingSection'
import './engine-settings.css'

// The command-line flags under each field are for people who know the
// engine; they stay hidden until asked for, and the choice is remembered.
const FLAGS_KEY = 'localai.runtime.showFlags'

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
//   model           the model name, for the memory figures of the quick setup
//   values          the editor's flat values (dotted paths, `options` list)
//   onChange        (path, value) => void; undefined unsets the key
//   readOnly        draw the settings without any way to change them
//   onOpenYaml      switches the editor to the YAML tab (the fallback when the
//                   schema cannot be read)
//   onValidity      (ok) => void; false while a field holds text that does not validate
export default function EngineRuntimeSection({ engine, model, values, onChange, readOnly = false, onOpenYaml, onValidity }) {
  const { t } = useTranslation('modelEditor')
  const state = useEngineSchema(engine.id)
  const schema = state.schema
  const blocks = useMemo(() => (schema ? groupItems(schema) : []), [schema])
  const owned = useMemo(() => (schema ? ownedOptionNames(schema) : new Set()), [schema])
  const words = { engine: engine.name, binary: engine.binary || engine.name }
  const [flags, setFlags] = useStoredChoice(FLAGS_KEY, 'off', ['on', 'off'])

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
    const { draft } = commit(d => (preset.profile ? applyProfile(schema, d, preset) : applyPreset(schema, d, preset, entry)))
    const changes = describeChanges(schema, values, draft)
    const ns = preset.profile ? 'profiles' : 'presets'
    setApplied({ kind: 'preset', name: t(`engineSettings.${ns}.${preset.id}.name`, { defaultValue: preset.name }), changes })
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

  // The fields and combined controls of one group.
  const groupGrid = (g) => (
    <div className="rt-group__grid">
      {g.items.map(item => (
        <EngineField key={item.id} item={item} values={values} onWrite={write} onValidity={onFieldValidity} readOnly={readOnly} />
      ))}
      {g.combined.map(c => {
        const id = testIdOf(c.id)
        const chosen = readCombined(schema, values, c)
        const chosenDoc = c.choices.find(ch => ch.value === chosen)?.doc
        return (
          <div className="dk-field rt-field" key={c.id} data-testid={`${id}-field`}>
            <label className="dk-label" htmlFor={id}>{combinedLabel(t, c)}</label>
            <span className="dk-select-wrap">
              <select
                id={id}
                className="dk-select"
                value={chosen}
                disabled={readOnly}
                aria-describedby={`${id}-hint`}
                onChange={e => commit(d => writeCombined(schema, d, c, e.target.value))}
                data-testid={id}
              >
                {c.choices.map(ch => <option key={ch.value} value={ch.value}>{choiceLabel(t, c, ch.value)}</option>)}
              </select>
            </span>
            <div className="dk-hint rt-field__hint" id={`${id}-hint`}>
              {c.flag && <code className="rt-flag rt-flag--hint">{c.flag}</code>}
              <MoreText text={t(`engineSettings.hints.${c.id}`, { defaultValue: c.doc })} />
              {chosenDoc && (
                <MoreText className="rt-field__choice-hint" testId={`${id}-choice-hint`}
                  text={t(`engineSettings.choiceHints.${c.id}.${chosen}`, { defaultValue: chosenDoc })} />
              )}
            </div>
          </div>
        )
      })}
    </div>
  )

  const hasFlags = schema.items.some(i => i.flag) || schema.combined.some(c => c.flag)

  return (
    <section className="rt" data-flags={flags} data-testid="runtime">
      <Head t={t} engine={engine} flags={hasFlags ? flags : null} onFlags={on => setFlags(on ? 'on' : 'off')} />

      {readOnly && (
        <Callout tone="info" icon="lock" title={t('engineSettings.readOnly.title')} testId="runtime-read-only">
          {t('engineSettings.readOnly.body')}
        </Callout>
      )}

      {schema.profiles.length > 0 && (
        <QuickSetup schema={schema} model={model} values={values} onPick={runPreset} readOnly={readOnly} />
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
          {blocks.map(g => (g.drafting
            ? (
              <DraftingSection
                key={g.id}
                schema={schema}
                engine={engine}
                model={model}
                values={values}
                more={g.items}
                commit={commit}
                onWrite={write}
                onValidity={onFieldValidity}
                readOnly={readOnly}
              />
            )
            : g.placement
            ? (
              <fieldset className="rt-group" key={g.id} data-testid={`rt-group-${g.id}`}>
                <legend className="rt-group__title">{t('engineSettings.placement.title')}</legend>
                <TensorPlacement engine={engine} placement={schema.placement} values={values} onEntry={writePlacement} onValidity={onFieldValidity} readOnly={readOnly} />
              </fieldset>
            )
            : g.collapsed
              ? (
                <details className="rt-group rt-group--fold" key={g.id} data-testid={`rt-group-${g.id}`}>
                  <summary className="rt-group__title rt-group__summary" data-testid={`rt-group-${g.id}-toggle`}>
                    <Icon name="chevron-right" className="rt-group__chevron" />
                    {groupTitle(g.id)}
                    <span className="text-meta">{t('engineSettings.groupCount', { count: g.items.length + g.combined.length })}</span>
                  </summary>
                  {groupGrid(g)}
                </details>
              )
              : (
                <fieldset className="rt-group" key={g.id} data-testid={`rt-group-${g.id}`}>
                  <legend className="rt-group__title">{groupTitle(g.id)}</legend>
                  {groupGrid(g)}
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
function Head({ t, engine, flags = null, onFlags }) {
  return (
    <div className="rt__head">
      <h2 className="rt__title">{t('engineSettings.title')}</h2>
      <Tag mono testId="runtime-engine">{engine.name}</Tag>
      {flags !== null && (
        <span className="rt__flags">
          <span className="rt__flags-label" id="rt-flags-label">{t('engineSettings.flags.show', { engine: engine.name })}</span>
          <Switch checked={flags === 'on'} onChange={onFlags} labelledBy="rt-flags-label" testId="rt-flags-toggle" />
        </span>
      )}
      <p className="rt__lead text-note">{t('engineSettings.leadNoCommand', { engine: engine.name })}</p>
    </div>
  )
}

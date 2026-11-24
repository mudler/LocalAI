import { useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, Tag } from '../../ui'
import Icon from '../../Icon'
import { useLlamaCppSchema } from '../../../hooks/useLlamaCppRuntime'
import { llamaCppApi } from '../../../utils/api'
import {
  LOAD_MODES, SETUP_PRESETS, TENSOR_ITEM, TENSOR_OPTION, applyParsed, applyPreset, describeChanges,
  diffPaths, groupItems, ownedOptionNames, placementRequest, rawOptionFor, readLoadMode, splitEntry,
  undoChanges, unflatten, writeEntry, writeItem, writeLoadMode,
} from '../../../utils/llamaCppRuntime'
// eslint-disable-next-line no-unused-vars
import RuntimeField from './RuntimeField'
import { choiceWords, fieldLabel } from './labels'
// eslint-disable-next-line no-unused-vars
import TensorPlacement from './TensorPlacement'
// eslint-disable-next-line no-unused-vars
import RawOptions from './RawOptions'
// eslint-disable-next-line no-unused-vars
import { CommandPreview, PasteCommand } from './CommandTools'
import './runtime.css'

// The words of one value in the list of what a preset or a command changed.
function valueWords(t, v, change) {
  if (v === undefined || v === null) return t('runtime.default')
  if (change?.id === 'loadMode') return t(`runtime.loadModes.${v}`)
  return choiceWords(t, v)
}

function changeLabel(t, change) {
  if (change.item) return fieldLabel(t, change.item)
  if (change.id === 'loadMode') return t('runtime.fields.loadMode')
  if (change.id === TENSOR_ITEM.id) return t('runtime.ot.title')
  return change.id
}

// Runtime tuning: how the llama.cpp backend loads and runs this model, as the
// llama-server flags it maps to. It edits the same values as the rest of the
// editor (YAML keys and `options:` entries), so the YAML tab and a save see
// exactly what the form shows. The settings come from the server's schema.
//
//   values          the editor's flat values (dotted paths, `options` list)
//   onChange        (path, value) => void; undefined unsets the key
//   readOnly        draw the settings without any way to change them
//   onOpenYaml      switches the editor to the YAML tab (the fallback when the
//                   schema cannot be read)
//   onValidity      (ok) => void; false while a field holds text that does not validate
export default function RuntimeTuningSection({ values, onChange, readOnly = false, onOpenYaml, onValidity }) {
  const { t } = useTranslation('modelEditor')
  const schema = useLlamaCppSchema(true)
  const items = schema.items
  const groups = useMemo(() => groupItems(items), [items])
  const owned = useMemo(() => ownedOptionNames(items), [items])

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

  // A preset with a tensor placement asks the server for its -ot entry first,
  // so the pattern is the one the server builds everywhere else.
  const [presetBusy, setPresetBusy] = useState(null)
  const [presetError, setPresetError] = useState(null)
  const runPreset = async (preset) => {
    setPresetError(null)
    let entry
    if (preset.tensor) {
      setPresetBusy(preset.id)
      try {
        entry = (await llamaCppApi.tensorPlacement({ placement: placementRequest(preset.tensor).placement }))?.option
      } catch (err) {
        setPresetError(t('runtime.ot.failed', { message: err?.message || String(err) }))
        return
      } finally {
        setPresetBusy(null)
      }
    }
    const { draft } = commit(d => applyPreset(d, items, preset, entry))
    const changes = describeChanges(items, values, draft)
    setApplied({ kind: 'preset', name: t(`runtime.presets.${preset.id}.name`), changes })
  }

  const writeTensor = useCallback((entry) => commit(d => {
    const next = writeEntry(d.options, TENSOR_OPTION, entry)
    if (next) d.options = next
    else delete d.options
  }), [commit])

  const applyCommand = (parsed) => {
    const { draft, out } = commit(d => applyParsed(d, items, parsed))
    const changes = describeChanges(items, values, draft)
    setApplied(changes.length ? { kind: 'paste', name: t('runtime.paste.source'), changes } : null)
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
  const loadMode = readLoadMode(values)

  if (schema.status === 'loading' || schema.status === 'idle') {
    return (
      <section className="rt" data-testid="runtime" aria-busy="true">
        <Head t={t} />
        <div className="rt__skeleton" role="status" aria-label={t('runtime.loading')} data-testid="runtime-loading">
          <span className="dk-skeleton dk-skeleton--line" />
          <span className="dk-skeleton dk-skeleton--block" />
          <span className="dk-skeleton dk-skeleton--block" />
        </div>
      </section>
    )
  }

  if (schema.status === 'error') {
    return (
      <section className="rt" data-testid="runtime">
        <Head t={t} />
        <Callout
          tone="warn"
          role="alert"
          title={t('runtime.schemaError.title')}
          testId="runtime-schema-error"
          actions={(
            <>
              <Button size="sm" icon="refresh" onClick={schema.retry} data-testid="runtime-retry">{t('runtime.schemaError.retry')}</Button>
              {onOpenYaml && <Button size="sm" icon="code" onClick={onOpenYaml} data-testid="runtime-open-yaml">{t('runtime.schemaError.openYaml')}</Button>}
            </>
          )}
        >
          {t('runtime.schemaError.body', { message: schema.error })}
        </Callout>
      </section>
    )
  }

  const groupTitle = (id) => t(`runtime.groups.${id}`, { defaultValue: id.replace(/_/g, ' ') })

  return (
    <section className="rt" data-testid="runtime">
      <Head t={t} />

      {readOnly && (
        <Callout tone="info" icon="lock" title={t('runtime.readOnly.title')} testId="runtime-read-only">
          {t('runtime.readOnly.body')}
        </Callout>
      )}

      {!readOnly && (
        <div className="rt__presets" role="group" aria-labelledby="rt-presets-label" data-testid="rt-presets">
          <span className="dk-eyebrow" id="rt-presets-label">{t('runtime.presets.title')}</span>
          <div className="rt__preset-list">
            {SETUP_PRESETS.map(p => (
              <div className="rt__preset" key={p.id}>
                <Button size="sm" icon="sliders" busy={presetBusy === p.id} disabled={Boolean(presetBusy)} onClick={() => runPreset(p)} data-testid={`rt-preset-${p.id}`}>
                  {t(`runtime.presets.${p.id}.name`)}
                </Button>
                <span className="text-meta">{t(`runtime.presets.${p.id}.text`)}</span>
              </div>
            ))}
          </div>
          {presetError && <Callout tone="error" role="alert" testId="rt-preset-error">{presetError}</Callout>}
        </div>
      )}

      {applied && (
        <Callout
          tone="info"
          title={t('runtime.applied.title', { name: applied.name, count: applied.changes.length })}
          testId="rt-applied"
          actions={(
            <>
              <Button size="sm" icon="undo" onClick={undo} data-testid="rt-undo">{t('runtime.applied.undo')}</Button>
              <Button size="sm" variant="ghost" onClick={() => setApplied(null)} data-testid="rt-applied-dismiss">{t('runtime.applied.dismiss')}</Button>
            </>
          )}
        >
          {applied.changes.length === 0
            ? t('runtime.applied.none')
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
          {applied.changes.length > 0 && <p className="rt-changes__note">{t('runtime.applied.note')}</p>}
        </Callout>
      )}

      <div className="rt__layout">
        <div className="rt__main">
          {groups.map(g => (
            <fieldset className="rt-group" key={g.id} data-testid={`rt-group-${g.id}`}>
              <legend className="rt-group__title">{groupTitle(g.id)}</legend>
              <div className="rt-group__grid">
                {g.items.map(item => (
                  <RuntimeField key={item.id} item={item} values={values} onWrite={write} onValidity={onFieldValidity} readOnly={readOnly} />
                ))}
                {g.id === 'memory' && (
                  <div className="dk-field rt-field" data-testid="rt-load-mode-field">
                    <label className="dk-label" htmlFor="rt-load-mode">{t('runtime.fields.loadMode')}</label>
                    <span className="dk-select-wrap">
                      <select
                        id="rt-load-mode"
                        className="dk-select"
                        value={loadMode}
                        disabled={readOnly}
                        aria-describedby="rt-load-mode-hint"
                        onChange={e => commit(d => writeLoadMode(d, e.target.value))}
                        data-testid="rt-load-mode"
                      >
                        {LOAD_MODES.map(m => <option key={m} value={m}>{t(`runtime.loadModes.${m}`)}</option>)}
                      </select>
                    </span>
                    <p className="dk-hint rt-field__hint" id="rt-load-mode-hint">
                      <code className="rt-flag">--load-mode</code>
                      <span>{t('runtime.loadModeHint')}</span>
                    </p>
                  </div>
                )}
              </div>
            </fieldset>
          )).flatMap((node, i) => (groups[i].id === 'speed' || (groups[i].id === 'memory' && !groups.some(g => g.id === 'speed'))
            ? [node, (
              <fieldset className="rt-group" key="placement" data-testid="rt-group-placement">
                <legend className="rt-group__title">{t('runtime.ot.title')}</legend>
                <TensorPlacement values={values} onEntry={writeTensor} onValidity={onFieldValidity} readOnly={readOnly} />
              </fieldset>
            )]
            : [node]))}

          <fieldset className="rt-group" data-testid="rt-group-advanced">
            <legend className="rt-group__title">{t('runtime.raw.title')}</legend>
            <p className="dk-hint rt-group__lead">{t('runtime.raw.hint')}</p>
            <RawOptions
              values={values}
              owned={owned}
              onOptions={next => onChange('options', next)}
              onValidity={onFieldValidity}
              readOnly={readOnly}
            />
          </fieldset>
        </div>

        <aside className="rt__aside" aria-label={t('runtime.preview.aside')}>
          <CommandPreview config={config} />
          {!readOnly && (
            <PasteCommand
              onApply={applyCommand}
              onKeep={keepRaw}
              onDrop={dropRaw}
              onUseModelFile={file => onChange('parameters.model', file)}
              currentModelFile={values['parameters.model']}
            />
          )}
        </aside>
      </div>
    </section>
  )
}

// eslint-disable-next-line no-unused-vars
function Head({ t }) {
  return (
    <div className="rt__head">
      <h2 className="rt__title">{t('runtime.title')}</h2>
      <Tag mono>llama.cpp</Tag>
      <p className="rt__lead text-note">{t('runtime.lead')}</p>
    </div>
  )
}

import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button } from '../../ui'
import Icon from '../../Icon'
import { llamaCppApi } from '../../../utils/api'
import {
  TENSOR_OPTION, choiceFromPlacement, patternOf, placementRequest, readEntry, splitEntry,
  validateOverride,
} from '../../../utils/llamaCppRuntime'

const MODES = ['off', 'experts', 'ranges', 'rest', 'raw']

// Fills the parts of a choice a mode needs, so switching modes keeps a usable shape.
function shape(choice) {
  return {
    mode: choice.mode,
    gpuLayers: choice.gpuLayers ?? 20,
    totalLayers: choice.totalLayers ?? '',
    ranges: choice.ranges?.length ? choice.ranges : [{ from: 0, to: 0 }],
    raw: choice.raw ?? '',
  }
}

// The entries this block reads back through the server. An older
// override_tensor entry has the same value but is not an -ot flag, so it is
// shown as raw text and becomes an -ot entry on the first edit.
const isOt = (entry) => ['-ot', '--override-tensor'].includes(splitEntry(entry).name)

// Where tensors go: llama-server's --override-tensor (-ot), kept in the model's
// `options` list as "-ot:pattern=buffer". Off writes nothing. The presets and
// the layer ranges are built by the server (POST /api/llama-cpp/tensor-placement),
// and an entry in the file is read back the same way, so the picker shows a
// pattern the server generated and anything else as raw text.
//
//   values    the editor's flat values
//   onEntry   (entry | undefined) => void; writes or removes the -ot entry
export default function TensorPlacement({ values, onEntry, onValidity, readOnly }) {
  const { t } = useTranslation('modelEditor')
  const current = readEntry(values.options, TENSOR_OPTION)
  const [choice, setChoice] = useState(() => shape(current ? { mode: 'raw', raw: patternOf(current) } : { mode: 'off' }))
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)
  // The entry this block wrote last, so its own write is not read back.
  const written = useRef(undefined)
  const seq = useRef(0)

  // An entry from outside (the file, a preset, a pasted command, Undo) is read
  // back by the server into the picker, or shown as raw text.
  useEffect(() => {
    if (current === written.current) return undefined
    written.current = current
    const ticket = ++seq.current
    setError(null)
    if (current === undefined) { setChoice(c => shape({ ...c, mode: 'off' })); return undefined }
    if (!isOt(current)) { setChoice(shape({ mode: 'raw', raw: patternOf(current) })); return undefined }
    setBusy(true)
    llamaCppApi.tensorPlacement({ option: current })
      .then(res => {
        if (ticket !== seq.current) return
        setChoice(shape(choiceFromPlacement(res?.raw ? null : res?.placement, patternOf(current))))
      })
      .catch(() => { if (ticket === seq.current) setChoice(shape({ mode: 'raw', raw: patternOf(current) })) })
      .finally(() => { if (ticket === seq.current) setBusy(false) })
    return undefined
  }, [current])

  useEffect(() => { onValidity?.('options.-ot', !error) }, [error, onValidity])

  const write = (entry) => {
    written.current = entry
    onEntry(entry)
  }

  const update = (patch) => {
    const next = { ...choice, ...patch }
    setChoice(next)
    const ticket = ++seq.current
    setBusy(false)
    if (next.mode === 'off') { setError(null); write(undefined); return }
    if (next.mode === 'raw') {
      const raw = String(next.raw).trim()
      const bad = validateOverride(raw)
      if (bad) { setError(t(`runtime.errors.${bad.error}`, bad.params || {})); return }
      setError(null)
      write(raw ? `${TENSOR_OPTION}:${raw}` : undefined)
      return
    }
    const req = placementRequest(next)
    if (req?.error) { setError(t(`runtime.errors.${req.error}`)); return }
    setError(null)
    setBusy(true)
    llamaCppApi.tensorPlacement({ placement: req.placement })
      .then(res => {
        if (ticket !== seq.current) return
        if (res?.option) write(res.option)
      })
      .catch(err => { if (ticket === seq.current) setError(t('runtime.ot.failed', { message: err?.message || String(err) })) })
      .finally(() => { if (ticket === seq.current) setBusy(false) })
  }

  const setRange = (i, patch) => update({ ranges: choice.ranges.map((r, j) => (j === i ? { ...r, ...patch } : r)) })
  const shown = error ? null : patternOf(current)

  return (
    <div className="rt-ot" data-testid="rt-ot" aria-busy={busy || undefined}>
      <div className="dk-field rt-ot__mode">
        <label className="dk-label" htmlFor="rt-ot-mode">{t('runtime.ot.mode')}</label>
        <span className="dk-select-wrap">
          <select
            id="rt-ot-mode"
            className="dk-select"
            value={choice.mode}
            disabled={readOnly}
            aria-describedby="rt-ot-hint"
            onChange={e => update({ mode: e.target.value })}
            data-testid="rt-ot-mode"
          >
            {MODES.map(m => <option key={m} value={m}>{t(`runtime.ot.modes.${m}`)}</option>)}
          </select>
        </span>
        <p className="dk-hint rt-field__hint" id="rt-ot-hint">
          <code className="rt-flag">-ot</code>
          <span>{t(`runtime.ot.help.${choice.mode}`)}</span>
        </p>
      </div>

      {choice.mode === 'rest' && (
        <div className="rt-ot__row">
          <div className="dk-field rt-ot__narrow">
            <label className="dk-label" htmlFor="rt-ot-gpu-layers">{t('runtime.ot.gpuLayers')}</label>
            <input
              id="rt-ot-gpu-layers"
              className="dk-input"
              inputMode="numeric"
              value={String(choice.gpuLayers)}
              readOnly={readOnly}
              onChange={e => update({ gpuLayers: e.target.value })}
              data-testid="rt-ot-gpu-layers"
            />
          </div>
          <div className="dk-field rt-ot__narrow">
            <label className="dk-label" htmlFor="rt-ot-total-layers">{t('runtime.ot.totalLayers')}</label>
            <input
              id="rt-ot-total-layers"
              className="dk-input"
              inputMode="numeric"
              value={String(choice.totalLayers)}
              placeholder={t('runtime.ot.totalUnknown')}
              readOnly={readOnly}
              onChange={e => update({ totalLayers: e.target.value })}
              data-testid="rt-ot-total-layers"
            />
          </div>
        </div>
      )}

      {choice.mode === 'ranges' && (
        <div className="rt-ot__ranges" role="group" aria-labelledby="rt-ot-ranges-label" data-testid="rt-ot-ranges">
          <span className="dk-label" id="rt-ot-ranges-label">{t('runtime.ot.layers')}</span>
          {choice.ranges.map((r, i) => (
            <div className="rt-ot__range" key={i} role="group" aria-label={t('runtime.ot.rangeN', { n: i + 1 })}>
              <input
                className="dk-input rt-ot__num"
                inputMode="numeric"
                aria-label={t('runtime.ot.from')}
                value={String(r.from)}
                readOnly={readOnly}
                onChange={e => setRange(i, { from: e.target.value })}
                data-testid={`rt-ot-from-${i}`}
              />
              <span className="text-meta" aria-hidden="true">–</span>
              <input
                className="dk-input rt-ot__num"
                inputMode="numeric"
                aria-label={t('runtime.ot.to')}
                value={String(r.to)}
                readOnly={readOnly}
                onChange={e => setRange(i, { to: e.target.value })}
                data-testid={`rt-ot-to-${i}`}
              />
              {!readOnly && choice.ranges.length > 1 && (
                <Button
                  variant="ghost"
                  icon="trash"
                  aria-label={t('runtime.ot.removeRange', { n: i + 1 })}
                  onClick={() => update({ ranges: choice.ranges.filter((_, j) => j !== i) })}
                  data-testid={`rt-ot-remove-${i}`}
                />
              )}
            </div>
          ))}
          {!readOnly && (
            <div>
              <Button
                size="sm"
                icon="plus"
                onClick={() => {
                  const last = choice.ranges[choice.ranges.length - 1]
                  const start = (Number(last?.to) || 0) + 2
                  update({ ranges: [...choice.ranges, { from: start, to: start }] })
                }}
                data-testid="rt-ot-add"
              >
                {t('runtime.ot.addRange')}
              </Button>
            </div>
          )}
        </div>
      )}

      {choice.mode === 'raw' && (
        <div className="dk-field">
          <label className="dk-label" htmlFor="rt-ot-raw">{t('runtime.ot.raw')}</label>
          <input
            id="rt-ot-raw"
            className="dk-input dk-input--mono"
            value={choice.raw}
            placeholder="blk\.[0-9]+\.ffn_.*_exps.*=CPU"
            readOnly={readOnly}
            spellCheck={false}
            aria-invalid={error ? 'true' : undefined}
            aria-describedby="rt-ot-raw-hint"
            onChange={e => update({ raw: e.target.value })}
            data-testid="rt-ot-raw"
          />
          <p className="dk-hint" id="rt-ot-raw-hint">{t('runtime.ot.rawHint')}</p>
        </div>
      )}

      {error && <p className="dk-field-error" role="alert" data-testid="rt-ot-error"><Icon name="alert-circle" />{error}</p>}

      <div className="rt-ot__generated" data-testid="rt-ot-generated">
        <span className="dk-eyebrow">{t('runtime.ot.generated')}</span>
        {shown
          ? <code className="rt-code" data-testid="rt-ot-regex">{shown}</code>
          : <span className="text-note" data-testid="rt-ot-none">{busy ? t('runtime.ot.building') : t('runtime.ot.nothing')}</span>}
      </div>
    </div>
  )
}

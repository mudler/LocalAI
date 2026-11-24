import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button } from '../../ui'
import Icon from '../../Icon'
import { rawOptions, replaceRawOptions, validateRawOption } from '../../../utils/engineSettings'

const same = (a, b) => a.length === b.length && a.every((x, i) => x === b[i])

// The `options:` entries no field above sets, one per row, written as typed:
// "name:value", "name", or a command-line flag such as "--rope-scale:2". An
// entry a field already sets is refused here, so no setting is written twice.
export default function RawOptions({ values, owned, onOptions, onValidity, readOnly }) {
  const { t } = useTranslation('modelEditor')
  const stored = useMemo(() => rawOptions(values.options, owned), [values.options, owned])
  const [rows, setRows] = useState(stored)
  const written = useRef(stored)

  // Entries changed elsewhere (a pasted command, "Keep as raw option", the
  // YAML tab) replace the rows, unless the rows already say the same.
  useEffect(() => {
    if (!same(stored, written.current)) {
      written.current = stored
      setRows(stored)
    }
  }, [stored])

  const errors = rows.map(r => (String(r).trim() === '' ? null : validateRawOption(r, owned)))
  const invalid = errors.some(Boolean)
  useEffect(() => { onValidity?.('rawOptions', !invalid) }, [invalid, onValidity])

  const commit = (next) => {
    setRows(next)
    const bad = next.some(r => String(r).trim() !== '' && validateRawOption(r, owned))
    if (bad) return
    const clean = next.map(r => String(r).trim()).filter(Boolean)
    written.current = clean
    onOptions(replaceRawOptions(values.options, owned, clean))
  }

  return (
    <div className="rt-raw" data-testid="rt-raw">
      {rows.length === 0 && <p className="text-note" data-testid="rt-raw-empty">{t('engineSettings.raw.empty')}</p>}
      {rows.map((row, i) => {
        const err = errors[i]
        return (
          <div className="rt-raw__row" key={i}>
            <div className="dk-field rt-raw__field">
              <input
                className="dk-input dk-input--mono"
                value={row}
                aria-label={t('engineSettings.raw.entry', { n: i + 1 })}
                placeholder="name:value"
                spellCheck={false}
                readOnly={readOnly}
                aria-invalid={err ? 'true' : undefined}
                onChange={e => commit(rows.map((r, j) => (j === i ? e.target.value : r)))}
                data-testid={`rt-raw-${i}`}
              />
              {err && (
                <p className="dk-field-error" role="alert" data-testid={`rt-raw-error-${i}`}>
                  <Icon name="alert-circle" />{t(`engineSettings.errors.${err.error === 'owned' ? 'rawOwned' : 'empty'}`, err.params || {})}
                </p>
              )}
            </div>
            {!readOnly && (
              <Button
                variant="ghost"
                icon="trash"
                aria-label={t('engineSettings.raw.remove', { entry: row || i + 1 })}
                onClick={() => commit(rows.filter((_, j) => j !== i))}
                data-testid={`rt-raw-remove-${i}`}
              />
            )}
          </div>
        )
      })}
      {!readOnly && (
        <div>
          <Button size="sm" icon="plus" onClick={() => setRows([...rows, ''])} data-testid="rt-raw-add">
            {t('engineSettings.raw.add')}
          </Button>
        </div>
      )}
    </div>
  )
}

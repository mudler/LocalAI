/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { Segmented, Slider, Switch } from '../ui'
import SearchableSelect from '../SearchableSelect'
import { filterControl, filterText, filterValue, sliderRange } from '../../utils/modelSearch'

// The filters that sources declare, drawn as the controls the page already
// uses. The host draws them from data: a source ships no markup, and its
// labels are plain text. A filter of a type the host does not know is skipped.
//
//   sources   the sources with filters: { id, title, filters, i18n }
//   values    { [sourceId]: { [filterId]: value } }: what the person changed
//   onChange  (sourceId, filterId, value) => void
//   lang      the interface language, for the labels a source translates
//   showSource  put the source name before each label (when several sources
//               are shown together)
export default function SourceFilters({ sources, values, onChange, lang, showSource = false }) {
  const rows = []
  for (const src of sources) {
    for (const f of src.filters || []) {
      const control = filterControl(f)
      if (!control) continue
      const text = filterText(src, f, lang)
      const label = showSource ? `${src.title} · ${text.label}` : text.label
      const testId = `source-filter-${src.id}-${f.id}`
      const value = filterValue(f, values?.[src.id])
      const set = (v) => onChange(src.id, f.id, v)
      rows.push(
        <div key={`${src.id}:${f.id}`} className="source-filter" data-testid={testId} title={text.help || undefined}>
          {control === 'switch' ? (
            <label className="filter-bar-group__toggle">
              <Switch checked={!!value} onChange={set} label={label} />
              <span>{label}</span>
            </label>
          ) : (
            <>
              <span className="source-filter__label">{label}</span>
              {control === 'segmented' && (
                <Segmented
                  label={label}
                  value={value}
                  onChange={set}
                  options={f.values.map(v => ({ id: v, label: text.value(v) }))}
                  itemTestId={`${testId}-`}
                />
              )}
              {control === 'select' && (
                <SearchableSelect
                  value={value}
                  onChange={set}
                  options={f.values.map(v => ({ value: v, label: text.value(v) }))}
                  placeholder={label}
                />
              )}
              {control === 'range' && (
                <RangeFilter filter={f} label={label} value={value} onChange={set} testId={testId} />
              )}
              {control === 'text' && <TextFilter filter={f} label={label} value={value} onChange={set} testId={testId} />}
            </>
          )}
        </div>,
      )
    }
  }
  if (rows.length === 0) return null
  return <div className="source-filters" data-testid="source-filters">{rows}</div>
}

// A range filter is the kit slider. The slider holds the value as text, and
// the filter sends a number.
function RangeFilter({ filter, label, value, onChange, testId }) {
  const range = sliderRange(filter)
  return (
    <span className="source-filter__range">
      <Slider
        range={range}
        value={String(value)}
        label={label}
        placeholder={String(range.min)}
        resetLabel={label}
        onChange={(text) => {
          const n = Number(text)
          if (text !== '' && Number.isFinite(n)) onChange(Math.min(range.max, Math.max(range.min, n)))
        }}
        testId={`${testId}-slider`}
      />
      {filter.unit && <span className="source-filter__unit">{filter.unit}</span>}
    </span>
  )
}

// A text filter is sent when the person leaves the box or presses Enter, so a
// source is not asked at each keystroke.
function TextFilter({ filter, label, value, onChange, testId }) {
  const [draft, setDraft] = useState(String(value ?? ''))
  useEffect(() => { setDraft(String(value ?? '')) }, [value])
  const commit = () => { if (draft !== String(value ?? '')) onChange(draft) }
  return (
    <input
      type="text"
      className="dk-input source-filter__text"
      aria-label={label}
      maxLength={filter.maxLength > 0 ? filter.maxLength : 200}
      value={draft}
      data-testid={`${testId}-input`}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => { if (e.key === 'Enter') commit() }}
    />
  )
}

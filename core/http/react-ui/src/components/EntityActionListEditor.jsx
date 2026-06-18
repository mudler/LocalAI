import { useMemo } from 'react'
import SearchableSelect from './SearchableSelect'

// Editor for a detector model's pii_detection.entity_actions map:
// entity-group name -> action. The value is an object {GROUP: action};
// this component renders one row per entry and emits a fresh object on
// every change. Entity-group names are model-defined (the privacy-filter
// family emits uppercase names with no separators), so the group field is
// free text with a datalist of common high-value categories for
// convenience — any string the model emits is valid.

const ACTION_OPTIONS = [
  { value: 'mask', label: 'mask — replace with [REDACTED:ner:GROUP]' },
  { value: 'block', label: 'block — reject the request (HTTP 400)' },
  { value: 'allow', label: 'allow — detect & log, leave text unchanged' },
]

// Common categories surfaced as datalist hints. Not exhaustive and not
// authoritative — the model's own label set is the source of truth.
const COMMON_GROUPS = [
  'PASSWORD', 'PIN', 'CVV', 'CREDITCARD', 'IBAN', 'BIC', 'BANKACCOUNT', 'SSN',
  'BITCOINADDRESS', 'ETHEREUMADDRESS', 'LITECOINADDRESS',
  'EMAIL', 'PHONE', 'URL', 'IPADDRESS', 'MACADDRESS',
  'FIRSTNAME', 'LASTNAME', 'MIDDLENAME', 'USERNAME', 'DATEOFBIRTH',
  'STREET', 'CITY', 'STATE', 'ZIPCODE', 'GPSCOORDINATES',
]

export default function EntityActionListEditor({ value, onChange }) {
  // value is an object map; preserve insertion order via Object.entries.
  const entries = useMemo(
    () => (value && typeof value === 'object' && !Array.isArray(value) ? Object.entries(value) : []),
    [value]
  )

  const datalistId = 'pii-entity-groups'

  const update = (index, key, action) => {
    const next = entries.map((e, i) => (i === index ? [key, action] : e))
    onChange(Object.fromEntries(next.filter(([k]) => k !== '')))
  }

  const remove = (index) => {
    onChange(Object.fromEntries(entries.filter((_, i) => i !== index)))
  }

  const add = () => {
    // New rows default to mask; an empty key is tolerated transiently and
    // filtered out on the next edit / when serialised.
    onChange(Object.fromEntries([...entries, ['', 'mask']]))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, width: '100%' }}>
      <datalist id={datalistId}>
        {COMMON_GROUPS.map(g => <option key={g} value={g} />)}
      </datalist>

      {entries.length === 0 && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
          No per-entity actions — every detected group uses the default action. Add a row to
          block or allow-log a specific entity group (e.g. <code>PASSWORD</code> → block).
        </div>
      )}

      {entries.map(([group, action], i) => (
        <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
          <input
            className="input"
            list={datalistId}
            value={group}
            placeholder="Entity group (e.g. PASSWORD)"
            onChange={e => update(i, e.target.value, action)}
            style={{ flex: '1 1 220px', minWidth: 180, fontSize: '0.8125rem' }}
            aria-label="Entity group"
          />
          <SearchableSelect
            value={action || 'mask'}
            onChange={v => update(i, group, v)}
            options={ACTION_OPTIONS}
            placeholder="Action..."
            style={{ flex: '1 1 240px', minWidth: 220 }}
          />
          <button type="button" className="btn btn-secondary btn-sm"
            onClick={() => remove(i)}
            style={{ padding: '2px 8px', fontSize: '0.75rem' }}
            aria-label="Remove entity action">
            <i className="fas fa-times" />
          </button>
        </div>
      ))}

      <button type="button" className="btn btn-secondary btn-sm" onClick={add}
        style={{ alignSelf: 'flex-start', fontSize: '0.75rem' }}>
        <i className="fas fa-plus" /> Add entity action
      </button>
    </div>
  )
}

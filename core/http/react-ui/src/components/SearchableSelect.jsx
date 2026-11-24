import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ListPicker from './ui/ListPicker'

// A choice from a list, drawn by ListPicker. Kept for its callers; new code
// uses ListPicker (or a native dk-select, see components/ui/README.md).
//
//   options       strings, or { value, label, badge, badgeTooltip, isHeader }:
//                 a header row starts a group, a badge is a tag on the row
//   allOption     a first row that empties the value ("All backends"); a string
//                 or { label }
//   variant       "field" (default, a form control) or "chip" (a toolbar filter)
//   The filter box is always there: callers and their specs expect a search.
//   placeholder, searchPlaceholder, disabled, id, className, style, aria-label
export default function SearchableSelect({
  value, onChange, options, placeholder,
  allOption, searchPlaceholder,
  disabled = false, style, className = '', triggerClassName = '', id,
  variant = 'field', 'aria-label': ariaLabel, testId,
}) {
  const { t } = useTranslation('common')

  const items = useMemo(() => {
    const out = []
    let group
    for (const raw of options || []) {
      const o = typeof raw === 'string' ? { value: raw, label: raw } : raw
      if (o.isHeader) { group = o.label; continue }
      out.push({
        id: o.value,
        label: o.label ?? String(o.value),
        group,
        tags: o.badge ? [{ label: o.badge, title: o.badgeTooltip || undefined }] : undefined,
      })
    }
    return out
  }, [options])

  const allLabel = allOption && typeof allOption === 'object' ? allOption.label : allOption

  return (
    <ListPicker
      id={id}
      variant={variant}
      className={className}
      style={style}
      triggerClassName={triggerClassName}
      items={items}
      value={value}
      onChange={onChange}
      placeholder={placeholder || allLabel || t('forms.selectPlaceholder')}
      noneLabel={allLabel || undefined}
      filter
      filterLabel={searchPlaceholder || t('forms.search')}
      filterPlaceholder={searchPlaceholder || t('forms.search')}
      noMatchText={t('forms.noMatch')}
      disabled={disabled}
      ariaLabel={ariaLabel}
      testId={testId}
    />
  )
}

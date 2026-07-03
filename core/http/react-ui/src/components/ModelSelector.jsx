import { useEffect, useMemo, useCallback } from 'react'
import { useModels } from '../hooks/useModels'
import SearchableSelect from './SearchableSelect'
import { useTranslation } from 'react-i18next'

// Remember the last model the user picked, keyed by capability, so returning to
// a page (Home chat box, Image, TTS, Talk...) defaults to that model instead of
// whatever happens to sort first. Only persisted when a capability key exists —
// `externalOptions` callers pass no capability and get the old first-item
// behaviour. localStorage access is wrapped because private-browsing modes throw.
const LAST_MODEL_PREFIX = 'localai_last_model:'

function readLastModel(capability) {
  if (!capability) return null
  try { return localStorage.getItem(LAST_MODEL_PREFIX + capability) } catch { return null }
}

function writeLastModel(capability, model) {
  if (!capability || !model) return
  try { localStorage.setItem(LAST_MODEL_PREFIX + capability, model) } catch { /* ignore */ }
}

export default function ModelSelector({
  value, onChange, capability, className = '',
  options: externalOptions, loading: externalLoading,
  disabled: externalDisabled, searchPlaceholder, style,
}) {
  const { t } = useTranslation('models')
  // Skip capability fetch when external options are provided (capability will be undefined)
  const { models: hookModels, loading: hookLoading } = useModels(externalOptions ? undefined : capability)

  const modelNames = useMemo(
    () => externalOptions || hookModels.map(m => m.id),
    [externalOptions, hookModels]
  )
  const isLoading = externalOptions ? (externalLoading || false) : hookLoading
  const isDisabled = isLoading || (externalDisabled || false)

  // Persist genuine selections so the next visit can restore them.
  const handleChange = useCallback((next) => {
    writeLastModel(capability, next)
    onChange(next)
  }, [capability, onChange])

  useEffect(() => {
    if (modelNames.length > 0 && (!value || !modelNames.includes(value))) {
      // Prefer the remembered model when it's still available; otherwise fall
      // back to the first option. Don't re-persist here — auto-select is not a
      // user choice, and writing back the stored value would be a harmless but
      // pointless round-trip.
      const remembered = readLastModel(capability)
      onChange(remembered && modelNames.includes(remembered) ? remembered : modelNames[0])
    }
  }, [modelNames, value, onChange, capability])

  return (
    <SearchableSelect
      value={value || ''}
      onChange={handleChange}
      options={modelNames}
      placeholder={isLoading ? t('selector.loading') : (modelNames.length === 0 ? t('selector.noModels') : t('selector.selectModel'))}
      searchPlaceholder={searchPlaceholder || t('selector.searchPlaceholder')}
      disabled={isDisabled}
      className={className}
      style={style}
    />
  )
}

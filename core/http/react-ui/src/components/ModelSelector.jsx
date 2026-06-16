import { useEffect, useMemo } from 'react'
import { useModels } from '../hooks/useModels'
import SearchableSelect from './SearchableSelect'
import { useTranslation } from 'react-i18next'

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

  useEffect(() => {
    if (modelNames.length > 0 && (!value || !modelNames.includes(value))) {
      onChange(modelNames[0])
    }
  }, [modelNames, value, onChange])

  return (
    <SearchableSelect
      value={value || ''}
      onChange={onChange}
      options={modelNames}
      placeholder={isLoading ? t('selector.loading') : (modelNames.length === 0 ? t('selector.noModels') : t('selector.selectModel'))}
      searchPlaceholder={searchPlaceholder || t('selector.searchPlaceholder')}
      disabled={isDisabled}
      className={className}
      style={style}
    />
  )
}

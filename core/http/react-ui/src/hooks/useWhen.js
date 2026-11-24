import { useTranslation } from 'react-i18next'
import { ageParts } from '../utils/updates'

// "12 minutes ago" in the language of the page.
export function useWhen() {
  const { t, i18n } = useTranslation('updates')
  return (iso, now) => {
    const age = ageParts(iso, now)
    if (!age) return ''
    if (age.justNow) return t('when.justNow')
    try {
      return new Intl.RelativeTimeFormat(i18n.language, { numeric: 'auto' }).format(-age.value, age.unit)
    } catch {
      return `${age.value} ${age.unit}`
    }
  }
}


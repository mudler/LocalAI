// Stand-in for `react-i18next`: useTranslation over the SDK's language and the
// locale files the add-on registered (see ../i18n.js).
import { getSdk } from '../runtime.js'
import { translator } from '../i18n.js'

const { useEffect, useMemo, useState } = getSdk().React

export function useTranslation(namespaces) {
  const sdk = getSdk()
  const [language, setLanguage] = useState(() => sdk.locale.language())
  useEffect(() => {
    setLanguage(sdk.locale.language())
    return sdk.locale.subscribe((lng) => setLanguage(String(lng || 'en')))
  }, [sdk])
  const key = Array.isArray(namespaces) ? namespaces.join('|') : String(namespaces || '')
  const t = useMemo(() => translator(language, Array.isArray(namespaces) ? namespaces : [namespaces]), [language, key]) // eslint-disable-line react-hooks/exhaustive-deps
  return { t, i18n: { language, changeLanguage: async () => {} }, ready: true }
}

import i18n from 'i18next'
import HttpBackend from 'i18next-http-backend'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'
import { apiUrl } from '../utils/basePath'

export const SUPPORTED_LANGUAGES = [
  { code: 'en', name: 'English', flag: 'EN' },
  { code: 'it', name: 'Italiano', flag: 'IT' },
  { code: 'es', name: 'Español', flag: 'ES' },
  { code: 'de', name: 'Deutsch', flag: 'DE' },
  { code: 'zh-CN', name: '简体中文', flag: 'ZH' },
]

export const NAMESPACES = [
  'common',
  'nav',
  'errors',
  'auth',
  'home',
  'chat',
  'studio',
  'models',
  'agents',
  'skills',
  'collections',
  'biometrics',
  'media',
  'tools',
  'admin',
  'usage',
  'explorer',
]

i18n
  .use(HttpBackend)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    fallbackLng: 'en',
    supportedLngs: SUPPORTED_LANGUAGES.map((l) => l.code),
    ns: ['common', 'nav', 'errors'],
    defaultNS: 'common',
    debug: import.meta.env.DEV,
    interpolation: { escapeValue: false },
    backend: {
      loadPath: apiUrl('/locales/{{lng}}/{{ns}}.json'),
    },
    detection: {
      order: ['localStorage', 'navigator', 'htmlTag'],
      lookupLocalStorage: 'localai-language',
      caches: ['localStorage'],
    },
    react: {
      useSuspense: true,
    },
  })

i18n.on('languageChanged', (lng) => {
  document.documentElement.setAttribute('lang', lng)
})

export default i18n

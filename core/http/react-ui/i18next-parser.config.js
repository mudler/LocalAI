export default {
  locales: ['en', 'it', 'es', 'de', 'zh-CN'],
  defaultNamespace: 'common',
  output: 'public/locales/$LOCALE/$NAMESPACE.json',
  input: ['src/**/*.{js,jsx}'],
  keySeparator: '.',
  namespaceSeparator: ':',
  defaultValue: (locale, _ns, key) => (locale === 'en' ? key : ''),
  sort: true,
  createOldCatalogs: false,
  keepRemoved: false,
}

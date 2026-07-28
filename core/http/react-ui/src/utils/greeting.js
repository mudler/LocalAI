// Pure time-of-day bucket for the editorial home greeting.
// Returns an i18n key suffix; caller resolves t(`greeting.${key}`).
export function greetingKey(date = new Date()) {
  const h = date.getHours()
  if (h < 5) return 'night'
  if (h < 12) return 'morning'
  if (h < 18) return 'afternoon'
  return 'evening'
}

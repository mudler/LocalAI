// What a row of the model list can do, for the agent editor. The server lists
// capabilities as "vision" or as the flag name ("FLAG_VISION"). Chat is what
// the list is filtered by, and tokenize is not something a person chooses a
// model for, so neither is shown.
const HIDDEN = new Set(['chat', 'tokenize'])

export function modelCaps(model) {
  const seen = []
  for (const c of model?.capabilities || []) {
    const key = String(c).replace(/^FLAG_/, '').toLowerCase()
    if (key && !HIDDEN.has(key) && !seen.includes(key)) seen.push(key)
  }
  return seen
}

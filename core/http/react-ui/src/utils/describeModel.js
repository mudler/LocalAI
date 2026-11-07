import { modelCaps } from './modelCaps.js'
import { seesImages } from './chatVision.js'

// The row the model picker draws for a chat model of the agent form: whether it
// sees images, and its other capabilities as words (capLabel turns an id into
// words). The editor and the new-agent wizard draw the same row.
export function describeChatModel(models, name, capLabel) {
  const m = (models || []).find((x) => x.id === name)
  return { vision: seesImages(m), caps: modelCaps(m).filter((c) => c !== 'vision').map(capLabel) }
}

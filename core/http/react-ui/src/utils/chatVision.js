// Whether the model of a chat can look at the images attached to a message,
// and what the composer tells the person when it cannot.
//
// The server sends an attached image to the chat's model inside the message
// when the model can look at images. Otherwise the model asks describe_image,
// which uses the "image model" of GET /api/chats (image_model), and with no
// such model it cannot look at the image at all.

const IMAGE_NAME = /\.(png|jpe?g|gif|webp|bmp)$/i

// seesImages reads a row of /v1/models/capabilities. The server says
// "vision"; older lists used the flag name.
export function seesImages(model) {
  const caps = model?.capabilities || []
  return caps.includes('vision') || caps.includes('FLAG_VISION')
}

export function isImageFile(f) {
  if (f?.type) return f.type.startsWith('image/')
  return IMAGE_NAME.test(f?.name || '')
}

// visionNotice is null when nothing needs saying: no image is attached, the
// model sees images, or the model is not in the list (yet). Otherwise key is
// "describedBy" (model names the model that will describe the image) or
// "none". The keys are under chats:vision.
export function visionNotice({ files = [], model, models = [], imageModel = '' }) {
  if (!model || !files.some(isImageFile)) return null
  const row = models.find(m => m.id === model)
  if (!row || seesImages(row)) return null
  return imageModel ? { key: 'describedBy', model: imageModel } : { key: 'none', model: '' }
}

// The cloud-proxy editor's "can look at images" switch is a view of
// known_usecases: LocalAI cannot ask the upstream, so the config declares it.
const VISION = ['vision', 'FLAG_VISION']

export function declaresVision(usecases) {
  return Array.isArray(usecases) && usecases.some(u => VISION.includes(u))
}

export function withVision(usecases, on) {
  const list = Array.isArray(usecases) && usecases.length > 0 ? usecases : ['chat']
  const rest = list.filter(u => !VISION.includes(u))
  return on ? [...rest, 'vision'] : rest
}

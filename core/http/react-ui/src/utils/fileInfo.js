// What the file views show about a file: its size, its extension and an icon
// for its type. Shared by the Files page and the files pane of a chat.

export const TEXT_EXT = /\.(txt|md|markdown|json|ya?ml|csv|tsv|log|xml|html?|css|js|jsx|ts|tsx|py|go|rs|c|h|cpp|sh|sql|toml|ini)$/i
export const IMAGE_EXT = /\.(png|jpe?g|gif|webp)$/i
const CODE_EXT = /\.(json|ya?ml|xml|html?|css|js|jsx|ts|tsx|py|go|rs|c|h|cpp|sh|sql|toml|ini)$/i

// The extension an add-on preview is matched on: lower case, without the dot.
// A name with no extension, or one the slot would refuse, has none.
export function extensionOf(name) {
  const m = /\.([A-Za-z0-9]{1,8})$/.exec(name || '')
  return m ? m[1].toLowerCase() : ''
}

export function fileSize(bytes) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

// The icon of a file, from its type when the server gave one, else its name.
export function fileIcon(name, mime = '') {
  if (mime.startsWith('image/') || IMAGE_EXT.test(name)) return 'image'
  if (mime.startsWith('audio/')) return 'music'
  if (mime.startsWith('video/')) return 'video'
  if (CODE_EXT.test(name)) return 'file-code'
  if (mime.startsWith('text/') || TEXT_EXT.test(name)) return 'file-text'
  return 'file'
}

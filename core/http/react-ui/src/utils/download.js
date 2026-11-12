// Saves a value as a JSON file through the browser.
export function downloadJSON(filename, value) {
  saveBlob(filename, new Blob([JSON.stringify(value, null, 2)], { type: 'application/json' }))
}

// Saves a Blob as a file through the browser.
export function saveBlob(filename, blob) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

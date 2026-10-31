// The files of a drop or a paste, with the path of each inside what was
// dropped, so a dropped folder arrives as a folder. Browsers expose folders
// only through webkitGetAsEntry, which reads a directory in batches.

// readEntries reads every child of a directory entry: one call returns at
// most one batch (100 in Chrome), so it is called until it returns none.
function readAll(dir) {
  const reader = dir.createReader()
  return new Promise((resolve, reject) => {
    const out = []
    const next = () => reader.readEntries((batch) => {
      if (batch.length === 0) resolve(out)
      else { out.push(...batch); next() }
    }, reject)
    next()
  })
}

const fileOf = (entry) => new Promise((resolve, reject) => entry.file(resolve, reject))

async function walk(entry, prefix, out) {
  if (entry.isFile) {
    out.push({ file: await fileOf(entry), rel: `${prefix}${entry.name}` })
  } else if (entry.isDirectory) {
    const kids = await readAll(entry)
    for (const kid of kids) await walk(kid, `${prefix}${entry.name}/`, out)
  }
}

// filesOfDrop returns [{ file, rel }] for a DataTransfer: rel is the path of
// the file inside the drop ("photos/a.jpg" for a dropped folder "photos").
// top lists the names that were dropped, for the clash check.
export async function filesOfDrop(dt) {
  const out = []
  const entries = Array.from(dt?.items || [])
    .filter(i => i.kind === 'file')
    .map(i => (typeof i.webkitGetAsEntry === 'function' ? i.webkitGetAsEntry() : null))
  if (entries.length > 0 && entries.every(Boolean)) {
    for (const e of entries) await walk(e, '', out)
    return out
  }
  for (const file of Array.from(dt?.files || [])) out.push({ file, rel: file.name })
  return out
}

// hasFiles says whether a drag carries files from outside the page, not a row
// of the list dragged onto a folder.
export function hasFiles(dt) {
  return Array.from(dt?.types || []).includes('Files')
}

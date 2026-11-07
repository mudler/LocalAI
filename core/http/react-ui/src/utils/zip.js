// A small ZIP writer for the browser. Entries are stored without compression,
// which keeps the writer short and is fine for a bundle of files that the
// person downloads once. No ZIP64: the bundle and each entry stay under 4 GiB.

const MAX = 0xffffffff

let table
function crcTable() {
  if (table) return table
  table = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    table[n] = c >>> 0
  }
  return table
}

export function crc32(bytes) {
  const t = crcTable()
  let c = 0xffffffff
  for (let i = 0; i < bytes.length; i++) c = t[(c ^ bytes[i]) & 0xff] ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}

function dosTime(date) {
  const year = Math.min(Math.max(date.getFullYear(), 1980), 2107)
  return {
    time: (date.getHours() << 11) | (date.getMinutes() << 5) | (date.getSeconds() >> 1),
    date: ((year - 1980) << 9) | ((date.getMonth() + 1) << 5) | date.getDate(),
  }
}

// entries: [{ name, data: Uint8Array, date?: Date }]. Names use '/' and are
// relative; a leading '/' is dropped. Returns a Blob of type application/zip.
export function makeZip(entries, now = new Date()) {
  const enc = new TextEncoder()
  const parts = []
  const central = []
  let offset = 0
  for (const e of entries) {
    const name = enc.encode(String(e.name).replace(/^\/+/, ''))
    const size = e.data.length
    if (size > MAX || offset > MAX || entries.length > 0xffff) throw new Error('zip too large')
    const crc = crc32(e.data)
    const { time, date } = dosTime(e.date || now)
    const head = new DataView(new ArrayBuffer(30))
    head.setUint32(0, 0x04034b50, true)
    head.setUint16(4, 20, true)
    head.setUint16(6, 0x0800, true) // names are UTF-8
    head.setUint16(8, 0, true) // stored
    head.setUint16(10, time, true)
    head.setUint16(12, date, true)
    head.setUint32(14, crc, true)
    head.setUint32(18, size, true)
    head.setUint32(22, size, true)
    head.setUint16(26, name.length, true)
    parts.push(head.buffer, name, e.data)

    const dir = new DataView(new ArrayBuffer(46))
    dir.setUint32(0, 0x02014b50, true)
    dir.setUint16(4, 20, true)
    dir.setUint16(6, 20, true)
    dir.setUint16(8, 0x0800, true)
    dir.setUint16(12, time, true)
    dir.setUint16(14, date, true)
    dir.setUint32(16, crc, true)
    dir.setUint32(20, size, true)
    dir.setUint32(24, size, true)
    dir.setUint16(28, name.length, true)
    dir.setUint32(42, offset, true)
    central.push(dir.buffer, name)
    offset += 30 + name.length + size
  }
  const dirSize = central.reduce((n, p) => n + (p.byteLength ?? p.length), 0)
  if (offset + dirSize > MAX) throw new Error('zip too large')
  const end = new DataView(new ArrayBuffer(22))
  end.setUint32(0, 0x06054b50, true)
  end.setUint16(8, entries.length, true)
  end.setUint16(10, entries.length, true)
  end.setUint32(12, dirSize, true)
  end.setUint32(16, offset, true)
  return new Blob([...parts, ...central, end.buffer], { type: 'application/zip' })
}

// The name of each file inside the zip: its path below the folder that all
// the files share, so a bundle of files from one folder is not wrapped in
// that folder's path while files from different folders keep their layout.
export function zipNames(paths) {
  const dirs = paths.map(p => String(p).replace(/^\/+/, '').split('/').slice(0, -1))
  let common = dirs[0] || []
  for (const d of dirs) {
    let i = 0
    while (i < common.length && i < d.length && common[i] === d[i]) i++
    common = common.slice(0, i)
  }
  return paths.map(p => String(p).replace(/^\/+/, '').split('/').slice(common.length).join('/'))
}

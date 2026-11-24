'use strict'
// Change the ELF interpreter (PT_INTERP) of a file in place, without moving
// anything: the new path must be shorter than the old one, and the rest of
// the field is filled with NUL bytes, so the file keeps its size and layout.
// ELF64 little-endian only (the x86_64 Electron binaries).
const fs = require('node:fs')

const PT_INTERP = 3

/**
 * @param {string} file
 * @param {string} from  the interpreter the file must have now
 * @param {string} to    the new interpreter (not longer than `from`)
 * @returns {boolean} true when the file was changed, false when it is not an
 *   ELF64 file with that interpreter
 */
function patchInterpreter(file, from, to) {
  if (Buffer.byteLength(to) > Buffer.byteLength(from)) {
    throw new Error(`new interpreter ${JSON.stringify(to)} is longer than ${JSON.stringify(from)}`)
  }
  const fd = fs.openSync(file, 'r+')
  try {
    const eh = Buffer.alloc(64)
    if (fs.readSync(fd, eh, 0, 64, 0) < 64) return false
    // magic, ELFCLASS64, little endian
    if (eh.readUInt32BE(0) !== 0x7f454c46 || eh[4] !== 2 || eh[5] !== 1) return false
    const phoff = Number(eh.readBigUInt64LE(0x20))
    const phentsize = eh.readUInt16LE(0x36)
    const phnum = eh.readUInt16LE(0x38)
    for (let i = 0; i < phnum; i++) {
      const ph = Buffer.alloc(phentsize)
      fs.readSync(fd, ph, 0, phentsize, phoff + i * phentsize)
      if (ph.readUInt32LE(0) !== PT_INTERP) continue
      const off = Number(ph.readBigUInt64LE(8))
      const size = Number(ph.readBigUInt64LE(32))
      const cur = Buffer.alloc(size)
      fs.readSync(fd, cur, 0, size, off)
      const want = Buffer.alloc(size)
      Buffer.from(from + '\0').copy(want)
      if (!cur.equals(want)) return false
      const next = Buffer.alloc(size)
      Buffer.from(to + '\0').copy(next)
      fs.writeSync(fd, next, 0, size, off)
      return true
    }
    return false
  } finally {
    fs.closeSync(fd)
  }
}

/** Interpreter of an ELF64 file, or null. */
function readInterpreter(file) {
  const fd = fs.openSync(file, 'r')
  try {
    const eh = Buffer.alloc(64)
    if (fs.readSync(fd, eh, 0, 64, 0) < 64) return null
    if (eh.readUInt32BE(0) !== 0x7f454c46 || eh[4] !== 2 || eh[5] !== 1) return null
    const phoff = Number(eh.readBigUInt64LE(0x20))
    const phentsize = eh.readUInt16LE(0x36)
    const phnum = eh.readUInt16LE(0x38)
    for (let i = 0; i < phnum; i++) {
      const ph = Buffer.alloc(phentsize)
      fs.readSync(fd, ph, 0, phentsize, phoff + i * phentsize)
      if (ph.readUInt32LE(0) !== PT_INTERP) continue
      const buf = Buffer.alloc(Number(ph.readBigUInt64LE(32)))
      fs.readSync(fd, buf, 0, buf.length, Number(ph.readBigUInt64LE(8)))
      const nul = buf.indexOf(0)
      return buf.subarray(0, nul < 0 ? buf.length : nul).toString('latin1')
    }
    return null
  } finally {
    fs.closeSync(fd)
  }
}

module.exports = { patchInterpreter, readInterpreter }

// Where the experts of a mixture-of-experts model sit, as the llama.cpp backend
// reads it from the model's `options` list. Three entries matter:
//
//   cpu_moe:true       keep every expert on the CPU.
//   n_cpu_moe:N        keep the experts of the first N layers on the CPU.
//   moe_cache_mib:N    MiB of GPU cache for experts held in host memory
//                      (experimental; 0 or absent is off).
//
// The list also holds many unrelated entries, so every function here leaves
// those exactly as they are, in the same order. Pure functions, so the rules can
// be tested without rendering anything.

export const MOE_KEYS = ['cpu_moe', 'n_cpu_moe', 'moe_cache_mib']

// The generic passthrough spellings of the same switches (an entry whose name
// starts with "-" goes to the llama.cpp argument parser unchanged). They are
// read, never written: the section shows them as set elsewhere.
const PASSTHROUGH = new Set(['--cpu-moe', '-cmoe', '--n-cpu-moe', '-ncmoe', '--moe-cache-mib'])

// The words the backend reads as "on" and "off" for a flag.
const TRUE_WORDS = new Set(['true', '1', 'yes', 'on', 'enabled'])
const FALSE_WORDS = new Set(['false', '0', 'no', 'off', 'disabled'])

// "name:value" to { name, value }. A bare "name" has no value (value null). The
// backend reads only the text up to the next colon as the value.
function splitEntry(entry) {
  const text = String(entry).trim()
  const at = text.indexOf(':')
  if (at < 0) return { name: text, value: null }
  return { name: text.slice(0, at).trim(), value: text.slice(at + 1).trim() }
}

// What a list says about experts.
//
//   experts   'gpu' (nothing keeps them on the CPU), 'some' (first `layers`
//             layers), 'all', or 'other' when the entries cannot be edited
//             safely (see `elsewhere`).
//   layers    the N of n_cpu_moe, or null.
//   cacheMib  the moe_cache_mib value, 0 when off.
//   elsewhere the entries that make this 'other': a spelling this section does
//             not write, a value it cannot read, an entry given twice, or
//             cpu_moe together with n_cpu_moe. Empty when it can edit.
export function parseMoeOptions(options) {
  const list = Array.isArray(options) ? options : []
  const seen = {}
  const elsewhere = []
  let all = false
  let layers = null
  let cacheMib = 0

  for (const raw of list) {
    const { name, value } = splitEntry(raw)
    if (PASSTHROUGH.has(name)) { elsewhere.push(String(raw)); continue }
    if (!MOE_KEYS.includes(name)) continue
    if (seen[name]) { elsewhere.push(String(raw)); continue }
    seen[name] = true
    if (name === 'cpu_moe') {
      const word = value === null ? 'true' : value.toLowerCase()
      if (TRUE_WORDS.has(word)) all = true
      else if (!FALSE_WORDS.has(word)) elsewhere.push(String(raw))
    } else if (/^\d+$/.test(value ?? '')) {
      if (name === 'n_cpu_moe') layers = Number(value)
      else cacheMib = Number(value)
    } else {
      elsewhere.push(String(raw))
    }
  }
  if (all && layers !== null) {
    elsewhere.push(...list.filter(raw => ['cpu_moe', 'n_cpu_moe'].includes(splitEntry(raw).name)).map(String))
  }
  if (elsewhere.length > 0) {
    return { experts: 'other', layers, cacheMib, elsewhere: [...new Set(elsewhere)] }
  }
  return { experts: all ? 'all' : layers !== null ? 'some' : 'gpu', layers, cacheMib, elsewhere: [] }
}

// Whether the list sets anything this section owns.
export function hasMoeOptions(options) {
  const parsed = parseMoeOptions(options)
  return parsed.experts !== 'gpu' || parsed.cacheMib > 0
}

// The list after a choice.
//
//   experts   'gpu' | 'some' | 'all'
//   layers    the N for 'some'
//   cacheMib  GPU cache in MiB; 0 removes the entry. Ignored (removed) when the
//             experts are on the GPU, because a cache only helps experts held
//             in host memory.
//
// An entry that already says what is wanted stays as written (cpu_moe:1 is not
// rewritten to cpu_moe:true). Unrelated entries keep their order and text; a new
// entry goes at the end. Returns undefined for an empty list, so the key is
// unset rather than saved as [].
export function setMoeOptions(options, { experts, layers = 1, cacheMib = 0 }) {
  const list = Array.isArray(options) ? options : []
  const wanted = {
    cpu_moe: experts === 'all' ? 'true' : null,
    n_cpu_moe: experts === 'some' ? String(Math.max(0, Math.floor(Number(layers) || 0))) : null,
    moe_cache_mib: experts !== 'gpu' && cacheMib > 0 ? String(Math.floor(cacheMib)) : null,
  }
  const kept = {}
  const out = []
  for (const raw of list) {
    const { name } = splitEntry(raw)
    if (!MOE_KEYS.includes(name)) { out.push(raw); continue }
    if (kept[name]) continue
    const want = wanted[name]
    if (want === null) continue
    const parsed = parseMoeOptions([raw])
    const same = name === 'cpu_moe'
      ? parsed.experts === 'all'
      : name === 'n_cpu_moe' ? parsed.layers === Number(want) : parsed.cacheMib === Number(want)
    out.push(same ? raw : `${name}:${want}`)
    kept[name] = true
  }
  for (const name of MOE_KEYS) {
    if (wanted[name] !== null && !kept[name]) out.push(`${name}:${wanted[name]}`)
  }
  return out.length > 0 ? out : undefined
}

// The GPU cache sizes offered as chips, in MiB.
export const MOE_CACHE_PRESETS = [2048, 4096, 8192]

export function cacheLabel(mib) {
  return mib > 0 && mib % 1024 === 0 ? `${mib / 1024} GB` : `${mib} MiB`
}

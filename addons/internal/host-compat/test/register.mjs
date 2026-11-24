// Import this first, then import the source under test with `await import()`:
// static imports are linked before any hook is installed.
import { register } from 'node:module'

register('./loader.mjs', import.meta.url)

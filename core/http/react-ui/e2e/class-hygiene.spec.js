import { test, expect } from '@playwright/test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

// Font Awesome's `fas` / `far` / `fab` classes do not draw an icon on their
// own — they set `font-family: "Font Awesome 6 Free"` and a weight on whatever
// element carries them, and the matching `fa-*` class supplies the glyph
// through ::before. Put them on a <button> and the button's own label text is
// rendered in the icon font; put two `fa-*` classes on one element and they
// fight over the same ::before.
//
// This is not hypothetical. A class-merging edit at some point collapsed
// several "wrapper + button + icon" trios into a single className string, which
// left icon classes on buttons and layout wrappers, and left the real buttons
// with no class at all — rendering them in the browser's own chrome. Both
// symptoms read as "unstyled button" and neither fails a build.
//
// These tests read the source rather than the DOM on purpose: several of the
// affected pages need agent or voice data before they render their header, so a
// route walk would silently skip exactly the pages that had the bug.

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..', 'src')

function jsxFiles(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) out.push(...jsxFiles(full))
    else if (entry.endsWith('.jsx')) out.push(full)
  }
  return out
}

// Every className="..." literal in the tree, with enough context to report a
// useful location. Template literals are skipped: they are built at runtime and
// this check is about hand-written constant strings.
function classLiterals() {
  const found = []
  for (const file of jsxFiles(SRC)) {
    const text = readFileSync(file, 'utf8')
    const lines = text.split('\n')
    lines.forEach((line, i) => {
      for (const m of line.matchAll(/className="([^"]*)"/g)) {
        found.push({
          file: relative(join(HERE, '..'), file),
          line: i + 1,
          classes: m[1].split(/\s+/).filter(Boolean),
          // The tag this className sits on, when it is on the same line.
          tag: (line.slice(0, m.index).match(/<([A-Za-z][\w.]*)(?![\s\S]*<)/) || [])[1] || '',
        })
      }
    })
  }
  return found
}

const isIconFont = (c) => c === 'fas' || c === 'far' || c === 'fab'

// Font Awesome's `fa-*` namespace holds two different kinds of class: the glyph
// (`fa-plus`), of which an element may have exactly one, and modifiers that
// size, spin or align it, of which it may have any number. `fa-spinner fa-spin`
// is the documented spinner idiom and must not be mistaken for a conflict.
const FA_MODIFIERS = new Set([
  'fa-2xs', 'fa-xs', 'fa-sm', 'fa-lg', 'fa-xl', 'fa-2xl',
  'fa-fw', 'fa-ul', 'fa-li', 'fa-border', 'fa-inverse',
  'fa-pull-left', 'fa-pull-right',
  'fa-beat', 'fa-fade', 'fa-beat-fade', 'fa-bounce', 'fa-flip', 'fa-shake',
  'fa-spin', 'fa-spin-pulse', 'fa-spin-reverse', 'fa-pulse',
  'fa-rotate-90', 'fa-rotate-180', 'fa-rotate-270', 'fa-rotate-by',
  'fa-flip-horizontal', 'fa-flip-vertical', 'fa-flip-both',
  'fa-stack', 'fa-stack-1x', 'fa-stack-2x',
  'fa-sr-only', 'fa-sr-only-focusable',
])
const isGlyph = (c) =>
  c.startsWith('fa-') && !FA_MODIFIERS.has(c) && !/^fa-\d+x$/.test(c)

test.describe('class hygiene', () => {
  test('the icon font is only ever set on an icon element', () => {
    // <i> and <span> are the icon carriers. Anything else wearing `fas` is
    // rendering its own text in the icon font.
    const offenders = classLiterals()
      .filter(c => c.classes.some(isIconFont))
      .filter(c => c.tag && c.tag !== 'i' && c.tag !== 'span')
      .map(c => `${c.file}:${c.line} <${c.tag} class="${c.classes.join(' ')}">`)

    expect(offenders, 'icon-font classes belong on an <i>, not on the element whose text they would restyle').toEqual([])
  })

  test('no element carries two competing glyphs or two button variants', () => {
    const offenders = []
    for (const c of classLiterals()) {
      const glyphs = c.classes.filter(isGlyph)
      if (glyphs.length > 1) {
        offenders.push(`${c.file}:${c.line} two glyphs (${glyphs.join(', ')}) on one ::before`)
      }
      const variants = c.classes.filter(x => /^btn-(primary|secondary|danger|ghost)$/.test(x))
      if (variants.length > 1) {
        offenders.push(`${c.file}:${c.line} two button variants (${variants.join(', ')}) on one element`)
      }
      // A layout wrapper is not a button. Both together means two elements'
      // classes were merged into one.
      if (c.classes.includes('hstack') && c.classes.includes('btn')) {
        offenders.push(`${c.file}:${c.line} a layout wrapper is also styled as a button`)
      }
    }

    expect(offenders, 'merged class strings leave one element over-styled and its siblings bare').toEqual([])
  })
})

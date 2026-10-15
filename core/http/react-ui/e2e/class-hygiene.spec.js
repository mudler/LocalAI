import { test, expect } from '@playwright/test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

// The app draws icons with the Icon component (an svg from the kit sprite).
// Two kinds of slip are cheap to make in a bulk edit and do not fail a build:
// an icon-font class left on an element, which now draws nothing, and a class
// string merged from several elements into one, which leaves a wrapper styled
// as a button and the real buttons bare.
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

const isIconFont = (c) => c === 'fas' || c === 'far' || c === 'fab' || c === 'fa-solid' || /^fa-[a-z0-9-]+$/.test(c)

test.describe('class hygiene', () => {
  test('no element still carries a Font Awesome class', () => {
    const offenders = classLiterals()
      .filter(c => c.classes.some(isIconFont))
      .map(c => `${c.file}:${c.line} class="${c.classes.join(' ')}"`)

    expect(offenders, 'draw icons with <Icon name="..." />, not with an icon-font class').toEqual([])
  })

  test('no element carries two button variants or a wrapper styled as a button', () => {
    const offenders = []
    for (const c of classLiterals()) {
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

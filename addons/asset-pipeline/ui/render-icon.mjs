// Rebuild the committed PNG from its editable SVG using the dev-shell browser.
import { createRequire } from 'node:module'
import { readFileSync, writeFileSync } from 'node:fs'
const require = createRequire(new URL('../../../core/http/react-ui/package.json', import.meta.url))
const { chromium } = require('@playwright/test')
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH })
try {
  const page = await browser.newPage()
  const svg = readFileSync(new URL('./icon.svg', import.meta.url), 'utf8')
  const png = await page.evaluate(async source => {
    const image = new Image()
    image.src = `data:image/svg+xml;base64,${btoa(source)}`
    await image.decode()
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 128
    canvas.getContext('2d').drawImage(image, 0, 0)
    return canvas.toDataURL('image/png').split(',')[1]
  }, svg)
  writeFileSync(new URL('./icon.png', import.meta.url), Buffer.from(png, 'base64'))
} finally {
  await browser.close()
}

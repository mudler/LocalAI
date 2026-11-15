// Checks of electron-builder.yml: the builder's own schema, and the packaging
// rules of the design (artifact names, targets, entitlements, feed files).
import fs from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import yaml from 'js-yaml'
import { describe, expect, it } from 'vitest'

// The builder's validation is internal API; load it the way the CLI does.
const req = createRequire(__filename)
const { validateConfiguration } = req('app-builder-lib/out/util/config/config')
const { DebugLogger } = req('builder-util')

const root = path.resolve(__dirname, '..')
const load = (): any => yaml.load(fs.readFileSync(path.join(root, 'electron-builder.yml'), 'utf8'))
const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'))

describe('electron-builder.yml', () => {
  it('passes the schema validation of the pinned electron-builder', async () => {
    await expect(validateConfiguration(load(), new DebugLogger(false))).resolves.toBeUndefined()
  })

  it('is rejected by the same validation when a key is unknown', async () => {
    const cfg = load()
    cfg.linux.notAnOption = true
    await expect(validateConfiguration(cfg, new DebugLogger(false))).rejects.toThrow(/configuration/)
  })

  it('pins Electron and electron-builder to exact versions', () => {
    for (const name of ['electron', 'electron-builder']) {
      expect(pkg.devDependencies[name]).toMatch(/^\d+\.\d+\.\d+$/)
    }
  })

  it('never names the launcher like the server binary', () => {
    const cfg = load()
    expect(cfg.executableName).not.toBe('local-ai')
    expect(cfg.executableName).toMatch(/^[a-z0-9-]+$/)
  })

  it('builds AppImages for x64 and arm64 with names without spaces', () => {
    const cfg = load()
    const appimage = cfg.linux.target.find((t: { target: string }) => t.target === 'AppImage')
    expect(appimage.arch).toEqual(['x64', 'arm64'])
    expect(cfg.linux.artifactName).toBe('LocalAI-Launcher-${arch}.AppImage')
    expect(cfg.linux.category).toBe('Development;Utility')
    expect(cfg.linux.icon).toBe('build/icon.png')
    // No forced --no-sandbox in the desktop entry (see the config comment).
    expect(cfg.linux.executableArgs).toEqual([])
    expect(pkg.desktopName).toBe(`${cfg.executableName}.desktop`)
  })

  it('builds the DMG and the updater zip for Apple Silicon only', () => {
    const cfg = load()
    expect(cfg.mac.target).toEqual([
      { target: 'dmg', arch: ['arm64'] },
      { target: 'zip', arch: ['arm64'] },
    ])
    expect(cfg.dmg.artifactName).toBe('LocalAI.dmg')
    // The workflow signs and notarizes the DMG file with scripts/sign-dmg.sh.
    // The builder's own DMG signing has no secure timestamp.
    expect(cfg.dmg.sign).not.toBe(true)
    expect(cfg.mac.artifactName).not.toMatch(/\s/)
    expect(cfg.mac.artifactName).toContain('${version}')
  })

  it('uses the hardened runtime with only the entitlements the design allows', () => {
    const cfg = load()
    expect(cfg.mac.hardenedRuntime).toBe(true)
    const plist = fs.readFileSync(path.join(root, cfg.mac.entitlements), 'utf8')
    const keys = [...plist.matchAll(/<key>([^<]+)<\/key>/g)].map((m) => m[1]).sort()
    expect(keys).toEqual([
      'com.apple.security.cs.allow-jit',
      'com.apple.security.cs.allow-unsigned-executable-memory',
      'com.apple.security.device.audio-input',
      'com.apple.security.device.camera',
    ])
    expect(cfg.mac.entitlementsInherit).toBe(cfg.mac.entitlements)
    expect(cfg.mac.extendInfo.NSMicrophoneUsageDescription).toMatch(/WebUI/)
    expect(cfg.mac.extendInfo.NSCameraUsageDescription).toMatch(/WebUI/)
  })

  it('writes the GitHub feed files on the latest channel, also for a release candidate', () => {
    const cfg = load()
    expect(cfg.publish).toEqual({ provider: 'github', owner: 'mudler', repo: 'LocalAI' })
    expect(cfg.detectUpdateChannel).toBe(false)
  })

  it('never publishes from a dist script', () => {
    for (const [name, cmd] of Object.entries(pkg.scripts as Record<string, string>)) {
      if (cmd.includes('electron-builder')) expect(cmd, name).toContain('--publish never')
    }
  })
})

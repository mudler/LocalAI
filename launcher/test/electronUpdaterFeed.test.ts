// Runs electron-updater's own GitHub provider against a fake HTTP executor, to
// record which URLs it requests. No network. The deep import avoids loading Electron.
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { HttpError } from 'builder-util-runtime'
import semver from 'semver'
// eslint-disable-next-line @typescript-eslint/no-require-imports
const { GitHubProvider } = require('electron-updater/out/providers/GitHubProvider') as typeof import('electron-updater/out/providers/GitHubProvider')

const atom = (tag: string) => `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>tag:github.com,2008:Repository/1/${tag}</id>
    <link rel="alternate" type="text/html" href="https://github.com/mudler/LocalAI/releases/tag/${tag}"/>
    <title>${tag}</title>
    <content type="html">notes</content>
  </entry>
</feed>`

const latestYml = `version: 4.2.1
files:
  - url: LocalAI-Launcher-x86_64.AppImage
    sha512: abc
    size: 1
path: LocalAI-Launcher-x86_64.AppImage
sha512: abc
releaseDate: '2026-11-14T00:00:00.000Z'
`

function run(opts: { ymlPresent: boolean }) {
  const requests: { host?: string; path?: string; accept?: string; auth?: string }[] = []
  const executor = {
    request: async (o: { hostname?: string; path?: string; headers?: Record<string, string> }) => {
      requests.push({ host: o.hostname, path: o.path, accept: o.headers?.accept ?? o.headers?.Accept, auth: o.headers?.authorization })
      if (o.path === '/mudler/LocalAI/releases.atom') return atom('v4.2.1')
      if (o.path === '/mudler/LocalAI/releases/latest') return JSON.stringify({ tag_name: 'v4.2.1' })
      if (o.path === '/mudler/LocalAI/releases/download/v4.2.1/latest-linux.yml' && opts.ymlPresent) return latestYml
      throw new HttpError(404, 'Not Found')
    },
  }
  const updater = { allowPrerelease: false, channel: null, currentVersion: new semver.SemVer('0.1.0'), fullChangelog: false }
  const provider = new GitHubProvider({ provider: 'github', owner: 'mudler', repo: 'LocalAI' }, updater as never, {
    executor: executor as never,
    isUseMultipleRangeRequest: false,
    platform: 'linux',
  } as never)
  return { provider, requests }
}

describe('electron-updater GitHub provider (public repository)', () => {
  // electron-updater names the channel file after process.arch (latest-linux-arm64.yml
  // on arm64). The fixture serves the x64 name, so pin the architecture: the test
  // then passes on an arm64 machine as well.
  const realArch = Object.getOwnPropertyDescriptor(process, 'arch')
  beforeAll(() => {
    Object.defineProperty(process, 'arch', { value: 'x64', configurable: true })
  })
  afterAll(() => {
    if (realArch) Object.defineProperty(process, 'arch', realArch)
  })

  it('finds the release through github.com web endpoints, not the REST API, and needs no token', async () => {
    const { provider, requests } = run({ ymlPresent: true })
    const info = await provider.getLatestVersion()
    expect(info.version).toBe('4.2.1')
    expect(requests.map((r) => `${r.host}${r.path}`)).toEqual([
      'github.com/mudler/LocalAI/releases.atom',
      'github.com/mudler/LocalAI/releases/latest',
      'github.com/mudler/LocalAI/releases/download/v4.2.1/latest-linux.yml',
    ])
    expect(requests[1].accept).toBe('application/json')
    expect(requests.every((r) => r.auth === undefined)).toBe(true)
    expect(provider.resolveFiles(info)[0].url.href).toBe(
      'https://github.com/mudler/LocalAI/releases/download/v4.2.1/LocalAI-Launcher-x86_64.AppImage',
    )
  })

  it('fails when the latest release has no latest-linux.yml (a server-only release)', async () => {
    const { provider, requests } = run({ ymlPresent: false })
    await expect(provider.getLatestVersion()).rejects.toMatchObject({ code: 'ERR_UPDATER_CHANNEL_FILE_NOT_FOUND' })
    expect(requests).toHaveLength(3)
  })
})

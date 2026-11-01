package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The shared pages fixture (--shared-pages-fixture): a shared folder
// /shared/team that the account "luca" writes and the user of the UI (auth is
// off, so the anonymous principal) may read, and /shared/board that the user
// may write. Its pages:
//
//	standup   a page that reads, writes and lists its folder through the bridge
//	releases  a page the user allowed in an earlier version (a consent to an
//	          older hash), so it opens with the "has changed" sheet
//	board     a page in the writable folder, so its writes go through
//	digest    a page split into files (style sheet, script, image, data) that
//	          the viewer puts into the frame with its consent
const sharedDigest = `<!doctype html><link rel="stylesheet" href="style.css"><h1 id="h">Digest</h1>
<img id="dot" src="dot.svg" width="8" height="8"><p id="data"></p><p id="out"></p><script src="app.js"></script>`

const sharedDigestScript = `fetch('data.json').then((r) => r.json()).then((d) => { document.getElementById('data').textContent = d.n })
fetch('../board/data.json').then(() => { document.getElementById('out').textContent = 'LEAK' }, (e) => { document.getElementById('out').textContent = 'refused: ' + e.message })
`

const (
	sharedAuthor = "luca"
	sharedTeam   = "/shared/team"
	sharedBoard  = "/shared/board"
)

const sharedProbe = `<!doctype html><html><body>
<h1 id="title">Team standup</h1><p id="read">reading</p><p id="write"></p><p id="list"></p><p id="later"></p>
<button id="w">write</button><button id="again">read again</button>
<script>
const out = (id, v) => { document.getElementById(id).textContent = v }
localai.files.read('data.json').then((f) => out('read', f.content), (e) => out('read', 'ERR ' + e.message))
localai.files.list().then((l) => out('list', l.map((f) => f.path).join(',')), (e) => out('list', 'ERR ' + e.message))
document.getElementById('w').onclick = () => localai.files.write('data.json', '{"n":2}').then((f) => out('write', 'v' + f.version), (e) => out('write', 'ERR ' + e.message))
document.getElementById('again').onclick = () => localai.files.read('data.json').then((f) => out('later', f.content), (e) => out('later', 'ERR ' + e.message))
</script></body></html>`

func seedSharedPages(ctx context.Context, app *application.Application) error {
	f := app.StudioFiles()
	svc := app.StudioService()
	if f == nil || !f.Available() || svc == nil || svc.PageConsents() == nil {
		return errors.New("the shared pages fixture needs the chats and their files")
	}
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	luca := workspace.Actor{Principal: sharedAuthor}
	viewer := studio.AnonymousPrincipal
	for _, p := range []string{sharedTeam + "/pages", sharedBoard + "/pages"} {
		if _, err := f.Store().Mkdir(ctx, admin, p); err != nil && !errors.Is(err, workspace.ErrExists) {
			return err
		}
	}
	grants := []struct {
		who, path string
		mode      workspace.Mode
	}{
		{sharedAuthor, sharedTeam, workspace.ModeWrite}, {sharedAuthor, sharedBoard, workspace.ModeWrite},
		{viewer, sharedTeam, workspace.ModeRead}, {viewer, sharedBoard, workspace.ModeWrite},
	}
	for _, g := range grants {
		if err := f.Store().Grant(ctx, admin, g.who, g.path, g.mode); err != nil {
			return err
		}
	}
	put := func(p, body string) error {
		_, err := f.Store().Put(ctx, luca, p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		return err
	}
	for p, body := range map[string]string{
		sharedTeam + "/pages/standup/index.html":  sharedProbe,
		sharedTeam + "/pages/standup/page.json":   `{"title":"Team standup","icon":"users","description":"What each of us does today."}`,
		sharedTeam + "/pages/standup/data.json":   `{"n":1}`,
		sharedTeam + "/pages/releases/index.html": "<!doctype html><h1>Release calendar</h1>\n<p>v3.9 code freeze, Monday</p>\n<p>v3.9 release, Thursday</p>\n",
		sharedTeam + "/pages/releases/page.json":  `{"title":"Release calendar","icon":"calendar"}`,
		sharedBoard + "/pages/board/index.html":   sharedProbe,
		sharedBoard + "/pages/board/page.json":    `{"title":"Team board"}`,
		sharedBoard + "/pages/board/data.json":    `{"n":1}`,
		sharedBoard + "/pages/digest/index.html":  sharedDigest,
		sharedBoard + "/pages/digest/page.json":   `{"title":"Team digest"}`,
		sharedBoard + "/pages/digest/style.css":   "h1 { color: rgb(1, 2, 3); }",
		sharedBoard + "/pages/digest/dot.svg":     `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><circle cx="4" cy="4" r="4"/></svg>`,
		sharedBoard + "/pages/digest/app.js":      sharedDigestScript,
		sharedBoard + "/pages/digest/data.json":   `{"n":7}`,
	} {
		if err := put(p, body); err != nil {
			return err
		}
	}
	// The viewer allowed an older version of the releases page.
	rel, err := f.Store().Stat(ctx, admin, sharedTeam+"/pages/releases")
	if err != nil {
		return err
	}
	at := time.Now().Add(-18 * 24 * time.Hour).UTC()
	c := pages.Consent{Principal: viewer, PageID: rel.ID, Folder: sharedTeam, Name: "releases", Hash: "an-earlier-version",
		Kind: pages.ConsentAlways, Size: 61, Lines: 3, At: at}
	return svc.PageConsents().Put(ctx, c, pages.ConsentEvent{Principal: viewer, PageID: rel.ID, Folder: sharedTeam, Name: "releases",
		Hash: c.Hash, Action: "allow", At: at})
}

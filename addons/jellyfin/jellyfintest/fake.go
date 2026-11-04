// SPDX-License-Identifier: MIT

// Package jellyfintest is a fake of the part of the Jellyfin HTTP API that the
// Jellyfin add-on uses, like githubtest for GitHub. It checks the API key of
// each call, keeps the calls so a test can see what was asked and with which
// key, applies the playback and user-data commands it receives, and can answer
// with a redirect, an error status or a rate limit.
package jellyfintest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Ticks is the number of Jellyfin time ticks in one second.
const Ticks = 10_000_000

// Server is a fake Jellyfin server.
type Server struct {
	*httptest.Server

	mu        sync.Mutex
	keys      map[string]bool
	info      Info
	Users     []*User
	Items     []*Item
	Sessions  []*Session
	calls     []Call
	failing   map[string]int // path prefix -> status
	redirect  string         // when set, every authenticated call answers 302 to it
	dropWrite bool
}

// Info is what the server says about itself.
type Info struct {
	Name, Version, OS string
	PendingRestart    bool
}

// Call is one request the fake served.
type Call struct {
	Method string
	Path   string
	Query  string
	// Auth is the whole Authorization header.
	Auth string
	// Key is the API key found in the Authorization header, "" when none.
	Key  string
	Body string
}

// User is a user of the fake.
type User struct {
	ID, Name string
	Admin    bool
}

// Item is a library item of the fake. Folder items (libraries, playlists,
// series) hold their children through ParentID.
type Item struct {
	ID, Name, Type, ParentID string
	CollectionType           string
	Year                     int
	Overview                 string
	RuntimeMin               int
	Rating                   float64
	OfficialRating           string
	Genres                   []string
	People                   []Person
	SeriesName, SeasonName   string
	Season, Episode          int
	// Path is a file on the server; the add-on must never return it.
	Path string
	// Resume is the playback position in seconds for each user ID.
	Resume map[string]int
	// Played and Favorite are the user data by user ID.
	Played, Favorite map[string]bool
	// Members are the item IDs of a playlist, in order.
	Members []string
	// NextUpFor lists the user IDs whose next up list holds this episode.
	NextUpFor []string
	// Added orders "latest": a larger number is newer.
	Added int
}

// Person is a cast or crew entry.
type Person struct{ Name, Role, Type string }

// Session is a client of the fake.
type Session struct {
	ID, UserID, UserName, Client, DeviceName string
	Controllable                             bool
	NowPlaying                               string // item ID
	PositionSec                              int
	Paused                                   bool
	Volume                                   int
	// Commands holds what the fake received, as "Pause", "Seek:90", "Play:<id>@<s>", "SetVolume:40".
	Commands []string
}

// New returns a running fake that accepts no key; add one with AddKey.
func New() *Server {
	s := &Server{keys: map[string]bool{}, failing: map[string]int{},
		info: Info{Name: "Test Jellyfin", Version: "10.10.3", OS: "Linux"}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// NewTLS returns a fake served over HTTPS with a self-signed certificate.
func NewTLS() *Server {
	s := &Server{keys: map[string]bool{}, failing: map[string]int{},
		info: Info{Name: "Test Jellyfin", Version: "10.10.3", OS: "Linux"}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	return s
}

// AddKey makes an API key valid.
func (s *Server) AddKey(k string) { s.mu.Lock(); s.keys[k] = true; s.mu.Unlock() }

// SetUsers replaces the users.
func (s *Server) SetUsers(u []*User) { s.mu.Lock(); s.Users = u; s.mu.Unlock() }

// SetInfo sets what /System/Info answers.
func (s *Server) SetInfo(i Info) { s.mu.Lock(); s.info = i; s.mu.Unlock() }

// Fail makes every path with the prefix answer with the status.
func (s *Server) Fail(prefix string, status int) {
	s.mu.Lock()
	s.failing[prefix] = status
	s.mu.Unlock()
}

// RedirectTo makes every authenticated call answer with a redirect to url.
func (s *Server) RedirectTo(url string) { s.mu.Lock(); s.redirect = url; s.mu.Unlock() }

// DropWrites makes the fake apply a change and then cut the connection
// before it answers.
func (s *Server) DropWrites(v bool) { s.mu.Lock(); s.dropWrite = v; s.mu.Unlock() }

// Calls returns the requests served so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Writes returns the requests that changed something.
func (s *Server) Writes() []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.Method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

// Session returns the session with the ID, or nil.
func (s *Server) Session(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.Sessions {
		if x.ID == id {
			c := *x
			c.Commands = append([]string(nil), x.Commands...)
			return &c
		}
	}
	return nil
}

// Item returns a copy of the item with the ID, or nil.
func (s *Server) Item(id string) *Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it := s.item(id); it != nil {
		c := *it
		c.Members = append([]string(nil), it.Members...)
		return &c
	}
	return nil
}

// SetNowPlaying changes what a session plays; a test uses it to make a
// preview stale.
func (s *Server) SetNowPlaying(session, item string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.Sessions {
		if x.ID == session {
			x.NowPlaying = item
		}
	}
}

func (s *Server) item(id string) *Item {
	for _, it := range s.Items {
		if strings.EqualFold(it.ID, id) {
			return it
		}
	}
	return nil
}

func keyOf(auth string) string {
	const p = `Token="`
	i := strings.Index(auth, p)
	if !strings.HasPrefix(auth, "MediaBrowser ") || i < 0 {
		return ""
	}
	rest := auth[i+len(p):]
	if j := strings.Index(rest, `"`); j >= 0 {
		return rest[:j]
	}
	return ""
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	auth := r.Header.Get("Authorization")
	c := Call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: auth, Key: keyOf(auth), Body: string(body)}
	s.mu.Lock()
	s.calls = append(s.calls, c)
	defer s.mu.Unlock()

	if r.URL.Path != "/System/Info/Public" && r.URL.Path != "/System/Ping" {
		if r.URL.Query().Get("api_key") != "" || r.URL.Query().Get("ApiKey") != "" || !s.keys[c.Key] {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if s.redirect != "" {
			http.Redirect(w, r, s.redirect, http.StatusFound)
			return
		}
	}
	for p, st := range s.failing {
		if strings.HasPrefix(r.URL.Path, p) {
			if st == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "17")
			}
			http.Error(w, "failing: SECRET-BODY-TEXT", st)
			return
		}
	}
	q := r.URL.Query()
	lq := lowerQuery(q)
	path := r.URL.Path
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	write := func(do func()) {
		do()
		if s.dropWrite {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
	switch {
	case path == "/System/Info/Public":
		reply(map[string]any{"ServerName": s.info.Name, "Version": s.info.Version, "ProductName": "Jellyfin Server", "Id": "srv1", "LocalAddress": "http://10.0.0.5:8096"})
	case path == "/System/Info":
		reply(map[string]any{"ServerName": s.info.Name, "Version": s.info.Version, "OperatingSystem": s.info.OS, "HasPendingRestart": s.info.PendingRestart,
			"LocalAddress": "http://10.0.0.5:8096", "WebPath": "/usr/share/jellyfin-web"})
	case path == "/Users" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for _, u := range s.Users {
			out = append(out, map[string]any{"Id": u.ID, "Name": u.Name, "Policy": map[string]any{"IsAdministrator": u.Admin}})
		}
		reply(out)
	case path == "/Library/MediaFolders":
		out := []map[string]any{}
		for _, it := range s.Items {
			if it.CollectionType != "" {
				out = append(out, s.dto(it, ""))
			}
		}
		reply(map[string]any{"Items": out, "TotalRecordCount": len(out)})
	case path == "/Items" && r.Method == http.MethodGet:
		reply(s.query(lq, lq["userid"]))
	case path == "/Items/Latest":
		uid := lq["userid"]
		var list []*Item
		for _, it := range s.Items {
			if it.CollectionType == "" && it.Type != "Playlist" && (lq["parentid"] == "" || it.ParentID == lq["parentid"]) {
				list = append(list, it)
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].Added > list[j].Added })
		out := []map[string]any{}
		for _, it := range list[:min(len(list), atoi(lq["limit"], 20))] {
			out = append(out, s.dto(it, uid))
		}
		reply(out)
	case path == "/Shows/NextUp":
		uid := lq["userid"]
		out := []map[string]any{}
		for _, it := range s.Items {
			for _, u := range it.NextUpFor {
				if u == uid {
					out = append(out, s.dto(it, uid))
				}
			}
		}
		reply(map[string]any{"Items": out, "TotalRecordCount": len(out)})
	case path == "/Sessions" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for _, x := range s.Sessions {
			out = append(out, s.sessionDTO(x))
		}
		reply(out)
	case strings.HasPrefix(path, "/Sessions/") && r.Method == http.MethodPost:
		s.sessionCommand(w, r, path, lq, body, write)
	case strings.HasPrefix(path, "/UserPlayedItems/") || strings.HasPrefix(path, "/UserFavoriteItems/"):
		id := strings.TrimPrefix(strings.TrimPrefix(path, "/UserPlayedItems/"), "/UserFavoriteItems/")
		it := s.item(id)
		if it == nil || lq["userid"] == "" {
			http.NotFound(w, r)
			return
		}
		set := r.Method == http.MethodPost
		write(func() {
			if strings.HasPrefix(path, "/UserPlayedItems/") {
				if it.Played == nil {
					it.Played = map[string]bool{}
				}
				it.Played[lq["userid"]] = set
			} else {
				if it.Favorite == nil {
					it.Favorite = map[string]bool{}
				}
				it.Favorite[lq["userid"]] = set
			}
		})
	case strings.HasPrefix(path, "/Playlists/") && strings.HasSuffix(path, "/Items") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/Playlists/"), "/Items")
		pl := s.item(id)
		if pl == nil || pl.Type != "Playlist" {
			http.NotFound(w, r)
			return
		}
		write(func() { pl.Members = append(pl.Members, splitList(lq["ids"])...) })
	case strings.HasPrefix(path, "/Playlists/") && strings.HasSuffix(path, "/Items") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/Playlists/"), "/Items")
		pl := s.item(id)
		if pl == nil {
			http.NotFound(w, r)
			return
		}
		out := []map[string]any{}
		for _, m := range pl.Members {
			if it := s.item(m); it != nil {
				out = append(out, s.dto(it, lq["userid"]))
			}
		}
		reply(map[string]any{"Items": out, "TotalRecordCount": len(out)})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) sessionCommand(w http.ResponseWriter, r *http.Request, path string, lq map[string]string, body []byte, write func(func())) {
	rest := strings.TrimPrefix(path, "/Sessions/")
	id, cmd, _ := strings.Cut(rest, "/")
	var sess *Session
	for _, x := range s.Sessions {
		if x.ID == id {
			sess = x
		}
	}
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case cmd == "Playing":
		write(func() {
			sess.Commands = append(sess.Commands, "Play:"+lq["itemids"]+"@"+strconv.Itoa(atoi(lq["startpositionticks"], 0)/Ticks))
			sess.NowPlaying, sess.PositionSec, sess.Paused = splitList(lq["itemids"])[0], atoi(lq["startpositionticks"], 0)/Ticks, false
		})
	case cmd == "Playing/Pause":
		write(func() { sess.Commands = append(sess.Commands, "Pause"); sess.Paused = true })
	case cmd == "Playing/Unpause":
		write(func() { sess.Commands = append(sess.Commands, "Unpause"); sess.Paused = false })
	case cmd == "Playing/Stop":
		write(func() { sess.Commands = append(sess.Commands, "Stop"); sess.NowPlaying = "" })
	case cmd == "Playing/Seek":
		write(func() {
			sess.PositionSec = atoi(lq["seekpositionticks"], 0) / Ticks
			sess.Commands = append(sess.Commands, "Seek:"+strconv.Itoa(sess.PositionSec))
		})
	case cmd == "Command":
		var g struct {
			Name      string
			Arguments map[string]string
		}
		if json.Unmarshal(body, &g) != nil || g.Name == "" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		write(func() {
			sess.Commands = append(sess.Commands, g.Name+":"+g.Arguments["Volume"])
			if g.Name == "SetVolume" {
				sess.Volume = atoi(g.Arguments["Volume"], 0)
			}
		})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) sessionDTO(x *Session) map[string]any {
	m := map[string]any{"Id": x.ID, "UserId": x.UserID, "UserName": x.UserName, "Client": x.Client, "DeviceName": x.DeviceName,
		"DeviceId": "device-secret-id", "RemoteEndPoint": "192.168.1.77", "SupportsRemoteControl": x.Controllable,
		"LastActivityDate": "2026-11-04T10:00:00Z",
		"PlayState":        map[string]any{"PositionTicks": x.PositionSec * Ticks, "IsPaused": x.Paused, "VolumeLevel": x.Volume, "IsMuted": false}}
	if it := s.item(x.NowPlaying); it != nil {
		m["NowPlayingItem"] = s.dto(it, x.UserID)
	}
	return m
}

func (s *Server) dto(it *Item, uid string) map[string]any {
	m := map[string]any{"Id": it.ID, "Name": it.Name, "Type": it.Type, "Path": it.Path,
		"ImageTags": map[string]string{"Primary": "tagsecret"}, "MediaSources": []map[string]any{{"Path": it.Path, "Id": "ms1"}}}
	set := func(k string, v any, ok bool) {
		if ok {
			m[k] = v
		}
	}
	set("ProductionYear", it.Year, it.Year > 0)
	set("Overview", it.Overview, it.Overview != "")
	set("RunTimeTicks", int64(it.RuntimeMin)*60*Ticks, it.RuntimeMin > 0)
	set("CommunityRating", it.Rating, it.Rating > 0)
	set("OfficialRating", it.OfficialRating, it.OfficialRating != "")
	set("Genres", it.Genres, len(it.Genres) > 0)
	set("SeriesName", it.SeriesName, it.SeriesName != "")
	set("SeasonName", it.SeasonName, it.SeasonName != "")
	set("IndexNumber", it.Episode, it.Episode > 0)
	set("ParentIndexNumber", it.Season, it.Season > 0)
	set("CollectionType", it.CollectionType, it.CollectionType != "")
	set("ParentId", it.ParentID, it.ParentID != "")
	if len(it.People) > 0 {
		ps := []map[string]any{}
		for _, p := range it.People {
			ps = append(ps, map[string]any{"Name": p.Name, "Role": p.Role, "Type": p.Type, "PrimaryImageTag": "x"})
		}
		m["People"] = ps
	}
	if it.Type == "Playlist" {
		m["ChildCount"] = len(it.Members)
	}
	if uid != "" {
		m["UserData"] = map[string]any{"Played": it.Played[uid], "IsFavorite": it.Favorite[uid], "PlaybackPositionTicks": it.Resume[uid] * Ticks, "PlayCount": 0}
	}
	return m
}

func (s *Server) query(lq map[string]string, uid string) map[string]any {
	var hits []*Item
	types := map[string]bool{}
	for _, t := range splitList(lq["includeitemtypes"]) {
		types[strings.ToLower(t)] = true
	}
	ids := map[string]bool{}
	for _, i := range splitList(lq["ids"]) {
		ids[strings.ToLower(i)] = true
	}
	for _, it := range s.Items {
		switch {
		case len(ids) > 0 && !ids[strings.ToLower(it.ID)]:
			continue
		case len(types) > 0 && !types[strings.ToLower(it.Type)]:
			continue
		case lq["parentid"] != "" && it.ParentID != lq["parentid"]:
			continue
		case lq["searchterm"] != "" && !strings.Contains(strings.ToLower(it.Name), strings.ToLower(lq["searchterm"])):
			continue
		case lq["genres"] != "" && !hasAny(it.Genres, splitList(lq["genres"], "|")):
			continue
		case lq["years"] != "" && !strconvIn(it.Year, splitList(lq["years"])):
			continue
		case strings.Contains(strings.ToLower(lq["filters"]), "isresumable") && it.Resume[uid] == 0:
			continue
		case it.CollectionType != "" && len(ids) == 0 && len(types) == 0 && lq["parentid"] == "":
			continue
		}
		hits = append(hits, it)
	}
	if strings.EqualFold(lq["sortorder"], "descending") {
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Name > hits[j].Name })
	} else {
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	}
	start := atoi(lq["startindex"], 0)
	if start > len(hits) {
		start = len(hits)
	}
	end := len(hits)
	if l := atoi(lq["limit"], 0); l > 0 && start+l < end {
		end = start + l
	}
	out := []map[string]any{}
	for _, it := range hits[start:end] {
		out = append(out, s.dto(it, uid))
	}
	return map[string]any{"Items": out, "TotalRecordCount": len(hits), "StartIndex": start}
}

func lowerQuery(q map[string][]string) map[string]string {
	out := map[string]string{}
	for k, v := range q {
		if len(v) > 0 {
			out[strings.ToLower(k)] = v[0]
		}
	}
	return out
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func splitList(s string, sep ...string) []string {
	d := ","
	if len(sep) > 0 {
		d = sep[0]
	}
	var out []string
	for _, p := range strings.Split(s, d) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hasAny(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if strings.EqualFold(h, w) {
				return true
			}
		}
	}
	return false
}

func strconvIn(n int, list []string) bool {
	for _, l := range list {
		if l == strconv.Itoa(n) {
			return true
		}
	}
	return false
}

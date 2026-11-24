// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/addons/jellyfin/internal/jfapi"
)

const ticksPerSecond = 10_000_000

// rawItem is the part of a Jellyfin item that the add-on reads. Files, image
// tags, media sources and stream data are not read at all.
type rawItem struct {
	ID                string   `json:"Id"`
	Name              string   `json:"Name"`
	Type              string   `json:"Type"`
	ProductionYear    int      `json:"ProductionYear"`
	Overview          string   `json:"Overview"`
	Taglines          []string `json:"Taglines"`
	RunTimeTicks      int64    `json:"RunTimeTicks"`
	CommunityRating   float64  `json:"CommunityRating"`
	OfficialRating    string   `json:"OfficialRating"`
	Genres            []string `json:"Genres"`
	SeriesName        string   `json:"SeriesName"`
	SeasonName        string   `json:"SeasonName"`
	IndexNumber       int      `json:"IndexNumber"`
	ParentIndexNumber int      `json:"ParentIndexNumber"`
	CollectionType    string   `json:"CollectionType"`
	ChildCount        int      `json:"ChildCount"`
	Studios           []struct {
		Name string `json:"Name"`
	} `json:"Studios"`
	People []struct {
		Name string `json:"Name"`
		Role string `json:"Role"`
		Type string `json:"Type"`
	} `json:"People"`
	UserData *struct {
		Played                bool  `json:"Played"`
		IsFavorite            bool  `json:"IsFavorite"`
		PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
		PlayCount             int   `json:"PlayCount"`
	} `json:"UserData"`
}

type itemsPage struct {
	Items            []rawItem `json:"Items"`
	TotalRecordCount int       `json:"TotalRecordCount"`
	StartIndex       int       `json:"StartIndex"`
}

type personOut struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
	Type string `json:"type,omitempty"`
}

// itemOut is one item as the model sees it.
type itemOut struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Type           string      `json:"type,omitempty"`
	Year           int         `json:"year,omitempty"`
	Series         string      `json:"series,omitempty"`
	Season         string      `json:"season,omitempty"`
	SeasonNumber   int         `json:"seasonNumber,omitempty"`
	EpisodeNumber  int         `json:"episodeNumber,omitempty"`
	RuntimeMinutes int         `json:"runtimeMinutes,omitempty"`
	Rating         float64     `json:"rating,omitempty"`
	AgeRating      string      `json:"ageRating,omitempty"`
	Genres         []string    `json:"genres,omitempty"`
	Overview       string      `json:"overview,omitempty"`
	OverviewCut    bool        `json:"overviewCut,omitempty"`
	Tagline        string      `json:"tagline,omitempty"`
	Studios        []string    `json:"studios,omitempty"`
	People         []personOut `json:"people,omitempty"`
	PeopleMore     int         `json:"peopleNotShown,omitempty"`
	Played         *bool       `json:"played,omitempty"`
	Favorite       *bool       `json:"favorite,omitempty"`
	ResumeMinutes  int         `json:"resumeAtMinute,omitempty"`
	Entries        int         `json:"entries,omitempty"`
	Library        string      `json:"libraryKind,omitempty"`
}

func (it rawItem) out(overviewMax int, detail bool) itemOut {
	o := itemOut{ID: it.ID, Name: oneLine(it.Name, 200), Type: it.Type, Year: it.ProductionYear,
		Series: oneLine(it.SeriesName, 200), Season: oneLine(it.SeasonName, 100), SeasonNumber: it.ParentIndexNumber,
		EpisodeNumber: it.IndexNumber, Rating: it.CommunityRating, AgeRating: oneLine(it.OfficialRating, 20),
		Entries: it.ChildCount, Library: oneLine(it.CollectionType, 40)}
	if it.RunTimeTicks > 0 {
		o.RuntimeMinutes = int(it.RunTimeTicks / ticksPerSecond / 60)
	}
	for i, g := range it.Genres {
		if i >= 8 {
			break
		}
		o.Genres = append(o.Genres, oneLine(g, 40))
	}
	o.Overview, o.OverviewCut = runes(it.Overview, overviewMax)
	if it.UserData != nil {
		p, f := it.UserData.Played, it.UserData.IsFavorite
		o.Played, o.Favorite = &p, &f
		o.ResumeMinutes = int(it.UserData.PlaybackPositionTicks / ticksPerSecond / 60)
	}
	if detail {
		if len(it.Taglines) > 0 {
			o.Tagline = oneLine(it.Taglines[0], 200)
		}
		for i, s := range it.Studios {
			if i >= 5 {
				break
			}
			o.Studios = append(o.Studios, oneLine(s.Name, 100))
		}
		for i, p := range it.People {
			if i >= maxPeople {
				o.PeopleMore = len(it.People) - maxPeople
				break
			}
			o.People = append(o.People, personOut{Name: oneLine(p.Name, 100), Role: oneLine(p.Role, 100), Type: oneLine(p.Type, 30)})
		}
	}
	return o
}

type listOut struct {
	Items      []itemOut `json:"items"`
	Total      int       `json:"total"`
	StartIndex int       `json:"startIndex"`
	// Next is the startIndex to ask for the next page, or 0 when this is the last.
	Next int `json:"next,omitempty"`
}

func (p itemsPage) list(start, limit int) listOut {
	out := listOut{Items: []itemOut{}, Total: max(p.TotalRecordCount, len(p.Items)), StartIndex: start}
	for _, it := range p.Items {
		out.Items = append(out.Items, it.out(ListOverview, false))
	}
	if start+len(p.Items) < p.TotalRecordCount && len(p.Items) > 0 {
		out.Next = start + len(p.Items)
	}
	_ = limit
	return out
}

const listFields = "Overview,Genres,CommunityRating,OfficialRating,ProductionYear,RunTimeTicks"

type user struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

func (c call) users() ([]user, error) {
	var us []user
	if err := c.r.API.GetJSON(c.ctx, c.server, c.key, []string{"Users"}, nil, &us); err != nil {
		return nil, err
	}
	return us, nil
}

// resolveUser turns a user name into the ID the server wants. The model works
// with names only. An empty name is accepted when the server has one user.
func (c call) resolveUser(name string, required bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" && !required {
		return "", nil
	}
	if err := checkText("user", name, 100, false); err != nil {
		return "", err
	}
	us, err := c.users()
	if err != nil {
		return "", err
	}
	if name == "" {
		if len(us) == 1 {
			return us[0].ID, nil
		}
		return "", bad("user is required: give a name from list-users")
	}
	for _, u := range us {
		if strings.EqualFold(u.Name, name) {
			return u.ID, nil
		}
	}
	return "", bad("no user with that name: use a name from list-users")
}

func (c call) items(query url.Values) (itemsPage, error) {
	var p itemsPage
	return p, c.r.API.GetJSON(c.ctx, c.server, c.key, []string{"Items"}, query, &p)
}

type searchArgs struct {
	Query      string   `json:"query"`
	Types      []string `json:"types"`
	User       string   `json:"jellyfinUser"`
	Limit      int      `json:"limit"`
	StartIndex int      `json:"startIndex"`
}

func (c call) searchItems(raw map[string]json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkText("query", a.Query, 200, true); err != nil {
		return nil, err
	}
	if err := checkTypes(a.Types); err != nil {
		return nil, err
	}
	uid, err := c.resolveUser(a.User, false)
	if err != nil {
		return nil, err
	}
	limit := pageOr(a.Limit, 20)
	q := url.Values{"searchTerm": {a.Query}, "recursive": {"true"}, "limit": {strconv.Itoa(limit)},
		"startIndex": {strconv.Itoa(max(a.StartIndex, 0))}, "fields": {listFields}, "enableImages": {"false"}}
	if len(a.Types) > 0 {
		q.Set("includeItemTypes", strings.Join(a.Types, ","))
	}
	if uid != "" {
		q.Set("userId", uid)
	}
	p, err := c.items(q)
	if err != nil {
		return nil, err
	}
	return p.list(max(a.StartIndex, 0), limit), nil
}

func (c call) listLibraries(raw map[string]json.RawMessage) (any, error) {
	var a struct{}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	var p itemsPage
	if err := c.r.API.GetJSON(c.ctx, c.server, c.key, []string{"Library", "MediaFolders"}, nil, &p); err != nil {
		return nil, err
	}
	out := listOut{Items: []itemOut{}, Total: len(p.Items)}
	for i, it := range p.Items {
		if i >= MaxEntries {
			break
		}
		out.Items = append(out.Items, it.out(0, false))
	}
	return out, nil
}

type browseArgs struct {
	ParentID   string   `json:"parentId"`
	Types      []string `json:"types"`
	SortBy     string   `json:"sortBy"`
	SortOrder  string   `json:"sortOrder"`
	Genre      string   `json:"genre"`
	Year       int      `json:"year"`
	User       string   `json:"jellyfinUser"`
	Limit      int      `json:"limit"`
	StartIndex int      `json:"startIndex"`
}

var sortFields = map[string]string{"name": "SortName", "added": "DateCreated", "premiere": "PremiereDate", "rating": "CommunityRating", "year": "ProductionYear"}

func (c call) browseLibrary(raw map[string]json.RawMessage) (any, error) {
	var a browseArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkItemID("parentId", a.ParentID); err != nil {
		return nil, err
	}
	if err := checkTypes(a.Types); err != nil {
		return nil, err
	}
	if err := checkText("genre", a.Genre, 60, false); err != nil || strings.ContainsAny(a.Genre, ",|") {
		return nil, bad("genre is one genre name")
	}
	if a.Year != 0 && (a.Year < 1850 || a.Year > 2200) {
		return nil, bad("year is a four digit year")
	}
	sortBy := "SortName"
	if a.SortBy != "" {
		s, ok := sortFields[a.SortBy]
		if !ok {
			return nil, bad("sortBy is name, added, premiere, rating or year")
		}
		sortBy = s
	}
	order := "Ascending"
	switch strings.ToLower(a.SortOrder) {
	case "", "ascending":
	case "descending":
		order = "Descending"
	default:
		return nil, bad("sortOrder is ascending or descending")
	}
	uid, err := c.resolveUser(a.User, false)
	if err != nil {
		return nil, err
	}
	limit := pageOr(a.Limit, 20)
	q := url.Values{"parentId": {a.ParentID}, "limit": {strconv.Itoa(limit)}, "startIndex": {strconv.Itoa(max(a.StartIndex, 0))},
		"sortBy": {sortBy}, "sortOrder": {order}, "fields": {listFields}, "enableImages": {"false"}}
	if len(a.Types) > 0 {
		q.Set("includeItemTypes", strings.Join(a.Types, ","))
		q.Set("recursive", "true")
	}
	if a.Genre != "" {
		q.Set("genres", a.Genre)
	}
	if a.Year != 0 {
		q.Set("years", strconv.Itoa(a.Year))
	}
	if uid != "" {
		q.Set("userId", uid)
	}
	p, err := c.items(q)
	if err != nil {
		return nil, err
	}
	return p.list(max(a.StartIndex, 0), limit), nil
}

func (c call) getItem(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		ItemID string `json:"itemId"`
		User   string `json:"jellyfinUser"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkItemID("itemId", a.ItemID); err != nil {
		return nil, err
	}
	uid, err := c.resolveUser(a.User, false)
	if err != nil {
		return nil, err
	}
	q := url.Values{"ids": {a.ItemID}, "fields": {listFields + ",People,Studios,Taglines"}, "enableImages": {"false"}}
	if uid != "" {
		q.Set("userId", uid)
	}
	p, err := c.items(q)
	if err != nil {
		return nil, err
	}
	if len(p.Items) == 0 {
		return nil, bad("no item with that ID")
	}
	return p.Items[0].out(DetailOverview, true), nil
}

func (c call) listLatest(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		ParentID string `json:"parentId"`
		User     string `json:"jellyfinUser"`
		Limit    int    `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.ParentID != "" {
		if err := checkItemID("parentId", a.ParentID); err != nil {
			return nil, err
		}
	}
	uid, err := c.resolveUser(a.User, true)
	if err != nil {
		return nil, err
	}
	limit := pageOr(a.Limit, 20)
	q := url.Values{"userId": {uid}, "limit": {strconv.Itoa(limit)}, "fields": {listFields}, "enableImages": {"false"}}
	if a.ParentID != "" {
		q.Set("parentId", a.ParentID)
	}
	var items []rawItem
	if err := c.r.API.GetJSON(c.ctx, c.server, c.key, []string{"Items", "Latest"}, q, &items); err != nil {
		return nil, err
	}
	return itemsPage{Items: items, TotalRecordCount: len(items)}.list(0, limit), nil
}

type userLimitArgs struct {
	User  string `json:"jellyfinUser"`
	Limit int    `json:"limit"`
}

func (c call) listContinue(raw map[string]json.RawMessage) (any, error) {
	var a userLimitArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	uid, err := c.resolveUser(a.User, true)
	if err != nil {
		return nil, err
	}
	limit := pageOr(a.Limit, 20)
	// IsResumable on /Items works on every server version, unlike the
	// dedicated resume route that moved between releases.
	q := url.Values{"userId": {uid}, "filters": {"IsResumable"}, "recursive": {"true"}, "sortBy": {"DatePlayed"}, "sortOrder": {"Descending"},
		"includeItemTypes": {"Movie,Episode,Audio,Video"}, "limit": {strconv.Itoa(limit)}, "fields": {listFields}, "enableImages": {"false"}}
	p, err := c.items(q)
	if err != nil {
		return nil, err
	}
	return p.list(0, limit), nil
}

func (c call) listNextUp(raw map[string]json.RawMessage) (any, error) {
	var a userLimitArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	uid, err := c.resolveUser(a.User, true)
	if err != nil {
		return nil, err
	}
	limit := pageOr(a.Limit, 20)
	q := url.Values{"userId": {uid}, "limit": {strconv.Itoa(limit)}, "fields": {listFields}, "enableImages": {"false"}}
	var p itemsPage
	if err := c.r.API.GetJSON(c.ctx, c.server, c.key, []string{"Shows", "NextUp"}, q, &p); err != nil {
		return nil, err
	}
	return p.list(0, limit), nil
}

func (c call) listPlaylists(raw map[string]json.RawMessage) (any, error) {
	var a userLimitArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	uid, err := c.resolveUser(a.User, false)
	if err != nil {
		return nil, err
	}
	limit := pageOr(a.Limit, 20)
	q := url.Values{"includeItemTypes": {"Playlist"}, "recursive": {"true"}, "limit": {strconv.Itoa(limit)}, "enableImages": {"false"}, "sortBy": {"SortName"}}
	if uid != "" {
		q.Set("userId", uid)
	}
	p, err := c.items(q)
	if err != nil {
		return nil, err
	}
	return p.list(0, limit), nil
}

type rawSession struct {
	ID                    string   `json:"Id"`
	UserName              string   `json:"UserName"`
	Client                string   `json:"Client"`
	DeviceName            string   `json:"DeviceName"`
	SupportsRemoteControl bool     `json:"SupportsRemoteControl"`
	NowPlayingItem        *rawItem `json:"NowPlayingItem"`
	PlayState             struct {
		PositionTicks int64 `json:"PositionTicks"`
		IsPaused      bool  `json:"IsPaused"`
		VolumeLevel   int   `json:"VolumeLevel"`
		IsMuted       bool  `json:"IsMuted"`
	} `json:"PlayState"`
}

type nowPlaying struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type,omitempty"`
	Series         string `json:"series,omitempty"`
	Year           int    `json:"year,omitempty"`
	RuntimeMinutes int    `json:"runtimeMinutes,omitempty"`
}

type sessionOut struct {
	ID              string      `json:"id"`
	User            string      `json:"user,omitempty"`
	Client          string      `json:"client,omitempty"`
	Device          string      `json:"device,omitempty"`
	CanBeControlled bool        `json:"canBeControlled"`
	NowPlaying      *nowPlaying `json:"nowPlaying,omitempty"`
	PositionSeconds int         `json:"positionSeconds,omitempty"`
	Paused          bool        `json:"paused,omitempty"`
	Volume          *int        `json:"volume,omitempty"`
}

func (s rawSession) out() sessionOut {
	o := sessionOut{ID: s.ID, User: oneLine(s.UserName, 100), Client: oneLine(s.Client, 60), Device: oneLine(s.DeviceName, 100), CanBeControlled: s.SupportsRemoteControl}
	if it := s.NowPlayingItem; it != nil {
		o.NowPlaying = &nowPlaying{ID: it.ID, Name: oneLine(it.Name, 200), Type: it.Type, Series: oneLine(it.SeriesName, 200), Year: it.ProductionYear, RuntimeMinutes: int(it.RunTimeTicks / ticksPerSecond / 60)}
		o.PositionSeconds = int(s.PlayState.PositionTicks / ticksPerSecond)
		o.Paused = s.PlayState.IsPaused
		if s.SupportsRemoteControl {
			v := s.PlayState.VolumeLevel
			o.Volume = &v
		}
	}
	return o
}

func (c call) sessions() ([]rawSession, error) {
	var ss []rawSession
	q := url.Values{"activeWithinSeconds": {"600"}}
	if err := c.r.API.GetJSON(c.ctx, c.server, c.key, []string{"Sessions"}, q, &ss); err != nil {
		return nil, err
	}
	return ss, nil
}

func (c call) listSessions(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		PlayingOnly bool `json:"playingOnly"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	ss, err := c.sessions()
	if err != nil {
		return nil, err
	}
	out := struct {
		Sessions []sessionOut `json:"sessions"`
		Total    int          `json:"total"`
	}{Sessions: []sessionOut{}}
	for _, s := range ss {
		if a.PlayingOnly && s.NowPlayingItem == nil {
			continue
		}
		out.Total++
		if len(out.Sessions) < MaxEntries {
			out.Sessions = append(out.Sessions, s.out())
		}
	}
	return out, nil
}

func (c call) listUsers(raw map[string]json.RawMessage) (any, error) {
	var a struct{}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	us, err := c.users()
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, u := range us {
		names = append(names, oneLine(u.Name, 100))
	}
	sort.Strings(names)
	if len(names) > MaxEntries {
		names = names[:MaxEntries]
	}
	return map[string]any{"users": names, "total": len(us)}, nil
}

// serverInfo answers get-server-info, and check for the settings page. The
// public call needs no key; the call with the key proves the key works.
func (c call) serverInfo(raw map[string]json.RawMessage, check bool) (any, error) {
	var a struct{}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	var pub struct {
		ServerName  string `json:"ServerName"`
		Version     string `json:"Version"`
		ProductName string `json:"ProductName"`
	}
	r, err := c.r.API.Do(c.ctx, c.server, "", jfapi.Request{Path: []string{"System", "Info", "Public"}, NoAuth: true})
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(r.Body, &pub) != nil {
		return nil, jfapi.ErrUnavailable
	}
	out := map[string]any{"name": oneLine(pub.ServerName, 100), "version": oneLine(pub.Version, 30), "product": oneLine(pub.ProductName, 60), "reachable": true}
	var info struct {
		OS             string `json:"OperatingSystem"`
		PendingRestart bool   `json:"HasPendingRestart"`
	}
	if err := c.r.API.GetJSON(c.ctx, c.server, c.key, []string{"System", "Info"}, nil, &info); err != nil {
		if check && (errors.Is(err, jfapi.ErrUnauthorized) || errors.Is(err, jfapi.ErrForbidden)) {
			out["keyWorks"] = false
			out["keyProblem"] = jfapi.Describe(err)
			return out, nil
		}
		return nil, err
	}
	out["keyWorks"] = true
	out["operatingSystem"] = oneLine(info.OS, 60)
	out["restartPending"] = info.PendingRestart
	return out, nil
}

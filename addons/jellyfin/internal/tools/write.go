// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/jellyfin/internal/jfapi"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The write tools of the manifest. All of them are write tools: the host
// decides, from the write mode of the person's connection, whether each one is
// refused, stored as a proposal for the person, or applied at once.
const (
	ToolControlPlayback = "control-playback"
	ToolMarkPlayed      = "mark-played"
	ToolSetFavorite     = "set-favorite"
	ToolAddToPlaylist   = "add-to-playlist"
	maxPlaylistAdd      = 20
	maxSeekSeconds      = 60 * 60 * 48
)

// WriteTools lists the write tools. The add-on has no tool that deletes,
// edits the library, manages users or scans the library.
func WriteTools() []string {
	return []string{ToolControlPlayback, ToolMarkPlayed, ToolSetFavorite, ToolAddToPlaylist}
}

func isWrite(tool string) bool {
	for _, t := range WriteTools() {
		if t == tool {
			return true
		}
	}
	return false
}

// The commands of control-playback.
var playbackCommands = []string{"play", "pause", "resume", "stop", "seek", "set-volume"}

type writeArgs struct {
	Server     string   `json:"server"`
	Session    string   `json:"playerId"`
	Command    string   `json:"command"`
	ItemID     string   `json:"itemId"`
	ItemIDs    []string `json:"itemIds"`
	PlaylistID string   `json:"playlistId"`
	Position   *int     `json:"positionSeconds"`
	Volume     *int     `json:"volume"`
	Played     *bool    `json:"played"`
	Favorite   *bool    `json:"favorite"`
	User       string   `json:"jellyfinUser"`
}

type write struct {
	tool string
	writeArgs
}

// parseWrite checks the arguments of a write tool.
func parseWrite(tool string, raw map[string]json.RawMessage) (write, error) {
	var a writeArgs
	if err := decodeArgs(raw, &a); err != nil {
		return write{}, err
	}
	w := write{tool: tool, writeArgs: a}
	if err := checkText("user", a.User, 100, false); err != nil {
		return w, err
	}
	switch tool {
	case ToolControlPlayback:
		if !sessionIDShape.MatchString(a.Session) {
			return w, bad("session is a session ID from list-sessions")
		}
		if a.Played != nil || a.Favorite != nil || a.PlaylistID != "" || len(a.ItemIDs) > 0 || a.User != "" {
			return w, bad("an argument does not belong to this tool")
		}
		ok := false
		for _, c := range playbackCommands {
			ok = ok || c == a.Command
		}
		if !ok {
			return w, bad("command is play, pause, resume, stop, seek or set-volume")
		}
		if a.Command == "play" {
			if err := checkItemID("itemId", a.ItemID); err != nil {
				return w, err
			}
		} else if a.ItemID != "" {
			return w, bad("itemId goes with the command play only")
		}
		if a.Command == "seek" && a.Position == nil {
			return w, bad("seek needs positionSeconds")
		}
		if a.Position != nil {
			if a.Command != "seek" && a.Command != "play" {
				return w, bad("positionSeconds goes with seek or play")
			}
			if *a.Position < 0 || *a.Position > maxSeekSeconds {
				return w, bad("positionSeconds is 0 or more")
			}
		}
		if a.Command == "set-volume" {
			if a.Volume == nil || *a.Volume < 0 || *a.Volume > 100 {
				return w, bad("set-volume needs volume from 0 to 100")
			}
		} else if a.Volume != nil {
			return w, bad("volume goes with set-volume")
		}
	case ToolMarkPlayed, ToolSetFavorite:
		if a.Session != "" || a.Command != "" || a.PlaylistID != "" || len(a.ItemIDs) > 0 || a.Position != nil || a.Volume != nil {
			return w, bad("an argument does not belong to this tool")
		}
		if err := checkItemID("itemId", a.ItemID); err != nil {
			return w, err
		}
		if tool == ToolMarkPlayed && (a.Played == nil || a.Favorite != nil) {
			return w, bad("played is true or false")
		}
		if tool == ToolSetFavorite && (a.Favorite == nil || a.Played != nil) {
			return w, bad("favorite is true or false")
		}
	case ToolAddToPlaylist:
		if a.Session != "" || a.Command != "" || a.ItemID != "" || a.Position != nil || a.Volume != nil || a.Played != nil || a.Favorite != nil {
			return w, bad("an argument does not belong to this tool")
		}
		if err := checkItemID("playlistId", a.PlaylistID); err != nil {
			return w, err
		}
		if len(a.ItemIDs) < 1 || len(a.ItemIDs) > maxPlaylistAdd {
			return w, bad("itemIds has 1 to %d item IDs", maxPlaylistAdd)
		}
		seen := map[string]bool{}
		for _, id := range a.ItemIDs {
			if err := checkItemID("itemIds", id); err != nil {
				return w, err
			}
			if seen[strings.ToLower(id)] || strings.EqualFold(id, a.PlaylistID) {
				return w, bad("itemIds has a repeated ID or the playlist itself")
			}
			seen[strings.ToLower(id)] = true
		}
	default:
		return w, bad("unknown tool")
	}
	return w, nil
}

func sum64(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(p)))
		h.Write(l[:])
		h.Write([]byte(p))
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8]) | 1
}

// target is what a write works on, read from the server just before the
// preview and again before the change.
type target struct {
	Session  *rawSession
	Item     *rawItem
	Playlist *rawItem
	Items    []rawItem
	UserID   string
}

func (c call) item(id, uid string) (*rawItem, error) {
	q := url.Values{"ids": {id}, "fields": {"Overview"}, "enableImages": {"false"}}
	if uid != "" {
		q.Set("userId", uid)
	}
	p, err := c.items(q)
	if err != nil {
		return nil, err
	}
	if len(p.Items) == 0 {
		return nil, bad("no item with ID %s", id)
	}
	return &p.Items[0], nil
}

// inspect reads the target of a write, checks the tool fits it, and returns a
// small hash of what makes the write wrong if it changed. It is how a stale
// approval is noticed.
func (c call) inspect(w write) (target, uint64, error) {
	var t target
	switch w.tool {
	case ToolControlPlayback:
		ss, err := c.sessions()
		if err != nil {
			return t, 0, err
		}
		for i := range ss {
			if ss[i].ID == w.Session {
				t.Session = &ss[i]
			}
		}
		if t.Session == nil {
			return t, 0, bad("no such session: it may have ended; run list-sessions again")
		}
		if !t.Session.SupportsRemoteControl {
			return t, 0, bad("that client cannot be controlled remotely")
		}
		playing := ""
		if t.Session.NowPlayingItem != nil {
			playing = t.Session.NowPlayingItem.ID
		}
		switch w.Command {
		case "pause", "resume", "stop", "seek":
			if playing == "" {
				return t, 0, bad("nothing is playing on that session")
			}
		case "play":
			it, err := c.item(w.ItemID, "")
			if err != nil {
				return t, 0, err
			}
			t.Item = it
		}
		if w.Command == "seek" {
			if rt := t.Session.NowPlayingItem.RunTimeTicks / ticksPerSecond; rt > 0 && int64(*w.Position) >= rt {
				return t, 0, bad("positionSeconds is past the end of what plays")
			}
		}
		return t, sum64("session", w.Session, playing, w.Command), nil
	case ToolMarkPlayed, ToolSetFavorite:
		uid, err := c.resolveUser(w.User, true)
		if err != nil {
			return t, 0, err
		}
		t.UserID = uid
		it, err := c.item(w.ItemID, uid)
		if err != nil {
			return t, 0, err
		}
		t.Item = it
		var have, want bool
		if it.UserData != nil {
			have = it.UserData.Played
			if w.tool == ToolSetFavorite {
				have = it.UserData.IsFavorite
			}
		}
		if w.tool == ToolMarkPlayed {
			want = *w.Played
		} else {
			want = *w.Favorite
		}
		if have == want {
			return t, 0, bad("it is already so")
		}
		return t, sum64(w.tool, w.ItemID, uid, strconv.FormatBool(have)), nil
	case ToolAddToPlaylist:
		uid, err := c.resolveUser(w.User, true)
		if err != nil {
			return t, 0, err
		}
		t.UserID = uid
		pl, err := c.item(w.PlaylistID, uid)
		if err != nil {
			return t, 0, err
		}
		if pl.Type != "Playlist" {
			return t, 0, bad("playlistId is not a playlist")
		}
		t.Playlist = pl
		for _, id := range w.ItemIDs {
			it, err := c.item(id, uid)
			if err != nil {
				return t, 0, err
			}
			if it.Type == "Playlist" || it.CollectionType != "" {
				return t, 0, bad("a playlist holds media, not playlists or libraries")
			}
			t.Items = append(t.Items, *it)
		}
		// The count of entries moves when the same add was already made, so a
		// repeated approval is rejected instead of adding the items twice.
		return t, sum64("playlist", w.PlaylistID, strconv.Itoa(pl.ChildCount)), nil
	}
	return t, 0, bad("unknown tool")
}

func plainJSON(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// sample is what the person sees before a change: facts built from the
// arguments and the server's own records, never from model prose. It stays
// small, since a chat keeps only a short tool result.
func sample(w write, t target) map[string]any {
	s := map[string]any{"tool": w.tool, "notice": "Names below come from the Jellyfin server."}
	switch w.tool {
	case ToolControlPlayback:
		s["command"] = w.Command
		ss := t.Session
		s["session"] = ss.ID
		s["user"], s["client"], s["device"] = oneLine(ss.UserName, 60), oneLine(ss.Client, 40), oneLine(ss.DeviceName, 60)
		if ss.NowPlayingItem != nil {
			s["nowPlaying"] = oneLine(ss.NowPlayingItem.Name, 120)
			s["positionSeconds"] = int(ss.PlayState.PositionTicks / ticksPerSecond)
		}
		if t.Item != nil {
			s["item"] = oneLine(t.Item.Name, 120)
			s["itemId"] = t.Item.ID
		}
		if w.Position != nil {
			s["toSeconds"] = *w.Position
		}
		if w.Volume != nil {
			s["volume"] = *w.Volume
		}
	case ToolMarkPlayed:
		s["item"], s["itemId"], s["played"] = oneLine(t.Item.Name, 120), t.Item.ID, *w.Played
		if t.Item.SeriesName != "" {
			s["series"] = oneLine(t.Item.SeriesName, 120)
		}
	case ToolSetFavorite:
		s["item"], s["itemId"], s["favorite"] = oneLine(t.Item.Name, 120), t.Item.ID, *w.Favorite
	case ToolAddToPlaylist:
		s["playlist"], s["playlistId"] = oneLine(t.Playlist.Name, 120), t.Playlist.ID
		names := []string{}
		for i, it := range t.Items {
			if i >= 5 {
				break
			}
			names = append(names, oneLine(it.Name, 80))
		}
		s["items"], s["itemCount"] = names, len(t.Items)
	}
	return s
}

func (r *Runtime) prepare(ctx context.Context, env contract.Envelope) (write, call, error) {
	var args map[string]json.RawMessage
	if json.Unmarshal(env.Request.Arguments, &args) != nil {
		return write{}, call{}, bad("the arguments are not an object")
	}
	var tool string
	if json.Unmarshal(args["tool"], &tool) != nil || !isWrite(tool) {
		return write{}, call{}, bad("unknown tool")
	}
	delete(args, "tool")
	w, err := parseWrite(tool, args)
	if err != nil {
		return w, call{}, err
	}
	c, err := r.newCall(ctx, env, w.Server)
	if err != nil {
		return w, call{}, err
	}
	return w, c, nil
}

// Preview checks a write and describes it. It changes nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Preview{}, err
	}
	t, rev, err := c.inspect(w)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	summary := plainJSON(map[string]any{"count": 1, "sample": sample(w, t)})
	if len(summary) > 3<<10 {
		return contract.Preview{}, bad("the preview is too large")
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: rev},
		ExpiresAt: r.now().Add(5 * time.Minute), Summary: summary}, nil
}

// Receipt answers from the journal. An operation without a receipt is
// unknown: the add-on cannot tell whether the server got it.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.journal().receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// Apply makes the change once. The target is read again first; when it moved
// since the preview the change is rejected and nothing is sent.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	op := env.Request.OperationID
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	j := r.journal()
	if rc, ok := j.receipt(op); ok {
		return rc, nil
	}
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Receipt{}, err
	}
	t, now, err := c.inspect(w)
	if err != nil {
		return contract.Receipt{}, translateWrite(err)
	}
	if now != rev.Data {
		rc := contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: smallResult(map[string]any{"stale": true, "note": "The target changed after the preview. Nothing was sent."})}}
		return rc, j.commit(rc)
	}
	out, err := c.send(w, t)
	switch {
	case err == nil:
	case errors.Is(err, jfapi.ErrUnreadable):
		out = map[string]any{"note": "sent; the answer of the server was not readable"}
	case definite(err):
		return contract.Receipt{}, translateWrite(err)
	default:
		// The request may or may not have arrived. The host will ask for the
		// receipt, get "unknown", and leave the decision to the person.
		return contract.Receipt{}, err
	}
	rc := contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: smallResult(out)}}
	return rc, j.commit(rc)
}

// smallResult wraps what the receipt echoes: ids and plain words, no text of
// the server.
func smallResult(data map[string]any) json.RawMessage {
	return plainJSON(map[string]any{"source": "jellyfin", "untrusted": true, "data": data})
}

// definite reports an error that proves the server did not make the change.
func definite(err error) bool {
	var limited *contract.ErrRateLimited
	return errors.As(err, &limited) || errors.Is(err, jfapi.ErrUnauthorized) || errors.Is(err, jfapi.ErrForbidden) ||
		errors.Is(err, jfapi.ErrNotFound) || errors.Is(err, jfapi.ErrInvalid) || errors.Is(err, jfapi.ErrRedirect) ||
		errors.Is(err, jfapi.ErrCertificate) || errors.Is(err, jfapi.ErrPin)
}

func translateWrite(err error) error {
	var limited *contract.ErrRateLimited
	if errors.As(err, &limited) {
		return bad("the server is limiting calls; nothing was sent. Propose it again in %d seconds", limited.Seconds())
	}
	return translate(err)
}

func (c call) post(method string, path []string, q url.Values, body any) error {
	_, err := c.r.API.Do(c.ctx, c.server, c.key, jfapi.Request{Method: method, Path: path, Query: q, Body: body})
	return err
}

// send makes the request for a checked write.
func (c call) send(w write, t target) (map[string]any, error) {
	out := map[string]any{"tool": w.tool}
	var err error
	switch w.tool {
	case ToolControlPlayback:
		out["command"], out["session"] = w.Command, w.Session
		sess := []string{"Sessions", w.Session}
		switch w.Command {
		case "play":
			q := url.Values{"playCommand": {"PlayNow"}, "itemIds": {t.Item.ID}}
			if w.Position != nil {
				q.Set("startPositionTicks", strconv.FormatInt(int64(*w.Position)*ticksPerSecond, 10))
			}
			out["itemId"] = t.Item.ID
			err = c.post("POST", append(sess, "Playing"), q, nil)
		case "pause":
			err = c.post("POST", append(sess, "Playing", "Pause"), nil, nil)
		case "resume":
			err = c.post("POST", append(sess, "Playing", "Unpause"), nil, nil)
		case "stop":
			err = c.post("POST", append(sess, "Playing", "Stop"), nil, nil)
		case "seek":
			err = c.post("POST", append(sess, "Playing", "Seek"), url.Values{"seekPositionTicks": {strconv.FormatInt(int64(*w.Position)*ticksPerSecond, 10)}}, nil)
		case "set-volume":
			out["volume"] = *w.Volume
			err = c.post("POST", append(sess, "Command"), nil, map[string]any{"Name": "SetVolume", "Arguments": map[string]string{"Volume": strconv.Itoa(*w.Volume)}})
		}
	case ToolMarkPlayed, ToolSetFavorite:
		out["itemId"] = t.Item.ID
		seg, method, field := "UserPlayedItems", "POST", "played"
		want := false
		if w.tool == ToolMarkPlayed {
			want = *w.Played
		} else {
			seg, field, want = "UserFavoriteItems", "favorite", *w.Favorite
		}
		if !want {
			method = "DELETE"
		}
		out[field] = want
		err = c.post(method, []string{seg, t.Item.ID}, url.Values{"userId": {t.UserID}}, nil)
	case ToolAddToPlaylist:
		ids := make([]string, 0, len(t.Items))
		for _, it := range t.Items {
			ids = append(ids, it.ID)
		}
		out["playlistId"], out["itemCount"] = t.Playlist.ID, len(ids)
		err = c.post("POST", []string{"Playlists", t.Playlist.ID, "Items"}, url.Values{"ids": {strings.Join(ids, ",")}, "userId": {t.UserID}}, nil)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

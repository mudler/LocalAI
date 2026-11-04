// SPDX-License-Identifier: MIT
package transport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
)

// FollowOption is the ID of the option that says which threads the bot follows.
const FollowOption = "followThreads"

// The values of the option FollowOption.
const (
	// FollowMentioned follows a thread once the bot is mentioned in it: later
	// messages of the thread need no new mention. It is the default.
	FollowMentioned = "mentioned"
	// FollowNever follows nothing: every message needs a mention.
	FollowNever = "never"
)

// followMode is the value of the option, or FollowMentioned for an unknown one.
func followMode(v string) string {
	if v == FollowNever {
		return v
	}
	return FollowMentioned
}

// touchEvery is the shortest time between two writes that only renew a
// thread that is followed already.
const touchEvery = 10 * time.Minute

// stateFile is the file of the followed threads of one link: the name holds a
// hash of the token, never the token.
func stateFile(dir, key string) string {
	return filepath.Join(dir, "threads-"+key[:16]+".json")
}

type storedThread struct {
	Channel string `json:"channel"`
	Thread  string `json:"thread"`
	At      int64  `json:"at"`
}

type storedThreads struct {
	Threads []storedThread `json:"threads"`
}

// loadThreads reads the followed threads of a link and drops the expired
// ones. A missing or damaged file is no threads: the bot then waits for a
// mention, which is the safe side.
func loadThreads(dir, key string, now time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	if dir == "" {
		return out
	}
	raw, err := os.ReadFile(stateFile(dir, key))
	if err != nil {
		return out
	}
	var st storedThreads
	if json.Unmarshal(raw, &st) != nil {
		return out
	}
	for _, t := range st.Threads {
		at := time.Unix(t.At, 0)
		if t.Channel != "" && t.Thread != "" && now.Sub(at) < threadLife && len(out) < maxThreads {
			out[threadKey(t.Channel, t.Thread)] = at
		}
	}
	return out
}

func threadKey(channel, thread string) string { return channel + "\x00" + thread }

// saveThreads writes the followed threads atomically: a file next to the
// target is written and renamed over it. An empty set removes the file.
func saveThreads(dir, key string, threads map[string]time.Time) error {
	if dir == "" {
		return nil
	}
	path := stateFile(dir, key)
	if len(threads) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var st storedThreads
	for k, at := range threads {
		channel, thread, _ := strings.Cut(k, "\x00")
		st.Threads = append(st.Threads, storedThread{Channel: channel, Thread: thread, At: at.Unix()})
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, raw, 0o600)
}

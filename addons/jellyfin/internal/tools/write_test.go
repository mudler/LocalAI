// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/jellyfin/internal/jfapi"
	"github.com/mudler/LocalAI/addons/jellyfin/internal/tools"
	"github.com/mudler/LocalAI/addons/jellyfin/jellyfintest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the write tools", func() {
	var f *fixture
	BeforeEach(func() {
		srv := jellyfintest.New()
		DeferCleanup(srv.Close)
		f = newFixture(srv)
	})

	wenv := func(tool, op string, args map[string]any) contract.Envelope {
		args["tool"], args["server"] = tool, f.srv.URL
		e := f.env(f.srv.URL, args)
		e.Request.OperationID = op
		return e
	}
	// run previews and applies, as the host does after the person confirms.
	run := func(tool, op string, args map[string]any) (contract.Preview, contract.Receipt, error) {
		pv, err := f.rt.Preview(context.Background(), wenv(tool, op, args))
		if err != nil {
			return pv, contract.Receipt{}, err
		}
		rc, err := f.rt.Apply(context.Background(), wenv(tool, op, args), pv.Revision)
		return pv, rc, err
	}
	sampleOf := func(pv contract.Preview) map[string]any {
		var s struct {
			Count  int            `json:"count"`
			Sample map[string]any `json:"sample"`
		}
		Expect(json.Unmarshal(pv.Summary, &s)).To(Succeed())
		Expect(s.Count).To(Equal(1))
		return s.Sample
	}

	Describe("the preview", func() {
		It("changes nothing and names the user, device and what plays", func() {
			pv, err := f.rt.Preview(context.Background(), wenv("control-playback", "p1", map[string]any{"playerId": sessionTV, "command": "stop"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(f.srv.Writes()).To(BeEmpty())
			s := sampleOf(pv)
			Expect(s["user"]).To(Equal("Alice"))
			Expect(s["device"]).To(Equal("Living room"))
			Expect(s["nowPlaying"]).To(Equal("Dune"))
			Expect(s["command"]).To(Equal("stop"))
			Expect(string(pv.Summary)).NotTo(ContainSubstring(apiKey))
			Expect(len(pv.Summary)).To(BeNumerically("<", 3<<10))
		})
	})

	Describe("control-playback", func() {
		DescribeTable("sends the right command to the player",
			func(args map[string]any, want string, path string) {
				args["playerId"] = sessionTV
				_, rc, err := run("control-playback", "op-"+want, args)
				Expect(err).NotTo(HaveOccurred())
				Expect(rc.State).To(Equal(contract.ReceiptCommitted))
				w := f.srv.Writes()
				Expect(w).To(HaveLen(1))
				Expect(w[0].Path).To(Equal(path))
				Expect(w[0].Key).To(Equal(apiKey))
				Expect(f.srv.Session(sessionTV).Commands).To(Equal([]string{want}))
			},
			Entry("pause", map[string]any{"command": "pause"}, "Pause", "/Sessions/"+sessionTV+"/Playing/Pause"),
			Entry("stop", map[string]any{"command": "stop"}, "Stop", "/Sessions/"+sessionTV+"/Playing/Stop"),
			Entry("seek", map[string]any{"command": "seek", "positionSeconds": 90}, "Seek:90", "/Sessions/"+sessionTV+"/Playing/Seek"),
			Entry("volume", map[string]any{"command": "set-volume", "volume": 25}, "SetVolume:25", "/Sessions/"+sessionTV+"/Command"),
			Entry("play", map[string]any{"command": "play", "itemId": arrival, "positionSeconds": 30}, "Play:"+arrival+"@30", "/Sessions/"+sessionTV+"/Playing"),
		)
		It("resumes a paused player", func() {
			_, _, err := run("control-playback", "a", map[string]any{"playerId": sessionTV, "command": "pause"})
			Expect(err).NotTo(HaveOccurred())
			_, _, err = run("control-playback", "b", map[string]any{"playerId": sessionTV, "command": "resume"})
			Expect(err).NotTo(HaveOccurred())
			Expect(f.srv.Session(sessionTV).Paused).To(BeFalse())
		})
		It("sends the key in the header and never in the address", func() {
			_, _, err := run("control-playback", "k", map[string]any{"playerId": sessionTV, "command": "pause"})
			Expect(err).NotTo(HaveOccurred())
			for _, c := range f.srv.Writes() {
				Expect(c.Query + c.Path + c.Body).NotTo(ContainSubstring(apiKey))
			}
		})
		It("refuses a command that does not fit the arguments", func() {
			for _, a := range []map[string]any{
				{"playerId": sessionTV, "command": "seek"},
				{"playerId": sessionTV, "command": "set-volume"},
				{"playerId": sessionTV, "command": "set-volume", "volume": 101},
				{"playerId": sessionTV, "command": "play"},
				{"playerId": sessionTV, "command": "pause", "itemId": dune},
				{"playerId": sessionTV, "command": "delete"},
				{"playerId": "../Users", "command": "pause"},
				{"playerId": sessionTV + "/Playing/Stop", "command": "pause"},
				{"playerId": sessionTV, "command": "seek", "positionSeconds": -1},
				{"playerId": sessionTV, "command": "pause", "played": true},
			} {
				_, err := f.rt.Preview(context.Background(), wenv("control-playback", "x", a))
				Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue(), "%v", a)
			}
			Expect(f.srv.Writes()).To(BeEmpty())
		})
		It("refuses a client that cannot be controlled, an idle player and an unknown session", func() {
			for _, c := range []struct{ id, cmd string }{{sessionX, "pause"}, {sessionPhone, "pause"}, {"ghost", "pause"}} {
				_, err := f.rt.Preview(context.Background(), wenv("control-playback", "y", map[string]any{"playerId": c.id, "command": c.cmd}))
				Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue(), c.id)
			}
		})
		It("refuses to seek past the end", func() {
			_, err := f.rt.Preview(context.Background(), wenv("control-playback", "z", map[string]any{"playerId": sessionTV, "command": "seek", "positionSeconds": 155 * 60}))
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
		})
		It("rejects a stale approval when something else plays now", func() {
			args := map[string]any{"playerId": sessionTV, "command": "stop"}
			pv, err := f.rt.Preview(context.Background(), wenv("control-playback", "s1", args))
			Expect(err).NotTo(HaveOccurred())
			f.srv.SetNowPlaying(sessionTV, arrival)
			rc, err := f.rt.Apply(context.Background(), wenv("control-playback", "s1", args), pv.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptRejected))
			Expect(f.srv.Writes()).To(BeEmpty())
		})
	})

	Describe("mark-played and set-favorite", func() {
		It("marks an item played and unplayed", func() {
			_, rc, err := run("mark-played", "m1", map[string]any{"itemId": dune, "played": true, "jellyfinUser": "Alice"})
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(f.srv.Item(dune).Played[alice]).To(BeTrue())
			Expect(f.srv.Writes()[0].Method).To(Equal("POST"))
			_, _, err = run("mark-played", "m2", map[string]any{"itemId": dune, "played": false, "jellyfinUser": "Alice"})
			Expect(err).NotTo(HaveOccurred())
			Expect(f.srv.Item(dune).Played[alice]).To(BeFalse())
			Expect(f.srv.Writes()[1].Method).To(Equal("DELETE"))
		})
		It("sets and clears a favourite", func() {
			_, _, err := run("set-favorite", "f1", map[string]any{"itemId": arrival, "favorite": true, "jellyfinUser": "Bob"})
			Expect(err).NotTo(HaveOccurred())
			Expect(f.srv.Item(arrival).Favorite[bob]).To(BeTrue())
			_, _, err = run("set-favorite", "f2", map[string]any{"itemId": arrival, "favorite": false, "jellyfinUser": "Bob"})
			Expect(err).NotTo(HaveOccurred())
			Expect(f.srv.Item(arrival).Favorite[bob]).To(BeFalse())
		})
		It("refuses a change that is already so, a missing user and a missing flag", func() {
			for _, c := range []struct {
				tool string
				args map[string]any
			}{
				{"mark-played", map[string]any{"itemId": arrival, "played": true, "jellyfinUser": "Alice"}},
				{"mark-played", map[string]any{"itemId": dune, "played": true}},
				{"mark-played", map[string]any{"itemId": dune, "jellyfinUser": "Alice"}},
				{"set-favorite", map[string]any{"itemId": dune, "favorite": false, "jellyfinUser": "Alice"}},
				{"set-favorite", map[string]any{"itemId": "x", "favorite": true, "jellyfinUser": "Alice"}},
			} {
				_, err := f.rt.Preview(context.Background(), wenv(c.tool, "r", c.args))
				Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue(), "%v", c.args)
			}
			Expect(f.srv.Writes()).To(BeEmpty())
		})
	})

	Describe("add-to-playlist", func() {
		It("adds items to the end of a playlist", func() {
			pv, rc, err := run("add-to-playlist", "pl1", map[string]any{"playlistId": playlist, "itemIds": []string{dune, ep1}, "jellyfinUser": "Alice"})
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(f.srv.Item(playlist).Members).To(Equal([]string{arrival, dune, ep1}))
			s := sampleOf(pv)
			Expect(s["playlist"]).To(Equal("Friday night"))
			Expect(s["itemCount"]).To(BeNumerically("==", 2))
		})
		It("rejects the same approval a second time instead of adding twice", func() {
			args := map[string]any{"playlistId": playlist, "itemIds": []string{dune}, "jellyfinUser": "Alice"}
			pv, err := f.rt.Preview(context.Background(), wenv("add-to-playlist", "pl2", args))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.rt.Apply(context.Background(), wenv("add-to-playlist", "pl2", args), pv.Revision)
			Expect(err).NotTo(HaveOccurred())
			rc, err := f.rt.Apply(context.Background(), wenv("add-to-playlist", "pl2", args), pv.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted), "the journal answers a repeated operation")
			pv2, _ := f.rt.Preview(context.Background(), wenv("add-to-playlist", "pl3", args))
			// A new operation made from the old preview sees the count moved.
			rc, err = f.rt.Apply(context.Background(), wenv("add-to-playlist", "pl3", args), pv.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptRejected))
			Expect(pv2.Revision).NotTo(Equal(pv.Revision))
			Expect(f.srv.Item(playlist).Members).To(HaveLen(2))
		})
		It("refuses a playlist that is not one, a library as an item, repeats and too many items", func() {
			many := []string{}
			for i := 0; i < 21; i++ {
				many = append(many, strings.Repeat("a", 31)+string(rune('a'+i%6)))
			}
			for _, a := range []map[string]any{
				{"playlistId": dune, "itemIds": []string{arrival}},
				{"playlistId": playlist, "itemIds": []string{libMovies}},
				{"playlistId": playlist, "itemIds": []string{dune, dune}},
				{"playlistId": playlist, "itemIds": []string{}},
				{"playlistId": playlist, "itemIds": many},
				{"playlistId": playlist, "itemIds": []string{playlist}},
				{"playlistId": playlist, "itemIds": []string{"../x"}},
			} {
				a["jellyfinUser"] = "Alice"
				_, err := f.rt.Preview(context.Background(), wenv("add-to-playlist", "q", a))
				Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue(), "%v", a)
			}
		})
	})

	Describe("failures and receipts", func() {
		It("answers a repeated operation from the journal and sends nothing twice", func() {
			args := map[string]any{"playerId": sessionTV, "command": "pause"}
			pv, err := f.rt.Preview(context.Background(), wenv("control-playback", "dup", args))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.rt.Apply(context.Background(), wenv("control-playback", "dup", args), pv.Revision)
			Expect(err).NotTo(HaveOccurred())
			rc, err := f.rt.Apply(context.Background(), wenv("control-playback", "dup", args), pv.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(f.srv.Writes()).To(HaveLen(1))
			got, err := f.rt.Receipt(context.Background(), "dup")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(contract.ReceiptCommitted))
			Expect(string(got.Result.Payload)).NotTo(ContainSubstring(apiKey))
			unk, _ := f.rt.Receipt(context.Background(), "never")
			Expect(unk.State).To(Equal(contract.ReceiptUnknown))
		})
		It("keeps receipts across a restart", func() {
			dir := GinkgoT().TempDir()
			rt := tools.New(jfapi.New()).WithDataDir(dir)
			args := map[string]any{"playerId": sessionTV, "command": "pause"}
			pv, err := rt.Preview(context.Background(), wenv("control-playback", "persist", args))
			Expect(err).NotTo(HaveOccurred())
			_, err = rt.Apply(context.Background(), wenv("control-playback", "persist", args), pv.Revision)
			Expect(err).NotTo(HaveOccurred())
			b, err := os.ReadFile(filepath.Join(dir, "journal.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring(apiKey))
			again := tools.New(jfapi.New()).WithDataDir(dir)
			rc, err := again.Receipt(context.Background(), "persist")
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		})
		It("leaves the outcome unknown when the connection drops after the change", func() {
			args := map[string]any{"playerId": sessionTV, "command": "pause"}
			pv, err := f.rt.Preview(context.Background(), wenv("control-playback", "drop", args))
			Expect(err).NotTo(HaveOccurred())
			f.srv.DropWrites(true)
			_, err = f.rt.Apply(context.Background(), wenv("control-playback", "drop", args), pv.Revision)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeFalse())
			rc, _ := f.rt.Receipt(context.Background(), "drop")
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		})
		It("turns a refused key into a plain refusal", func() {
			args := map[string]any{"playerId": sessionTV, "command": "pause"}
			pv, err := f.rt.Preview(context.Background(), wenv("control-playback", "k2", args))
			Expect(err).NotTo(HaveOccurred())
			f.srv.Fail("/Sessions/"+sessionTV+"/Playing", 403)
			_, err = f.rt.Apply(context.Background(), wenv("control-playback", "k2", args), pv.Revision)
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
			Expect(err.Error()).NotTo(ContainSubstring("SECRET-BODY-TEXT"))
		})
		It("refuses a server other than the one the host allowed", func() {
			e := wenv("control-playback", "o", map[string]any{"playerId": sessionTV, "command": "pause"})
			e.Request.ResourceKey = "http://other.example"
			_, err := f.rt.Preview(context.Background(), e)
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
			Expect(f.srv.Calls()).To(BeEmpty())
		})
	})
})

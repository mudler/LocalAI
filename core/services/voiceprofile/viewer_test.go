package voiceprofile_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/mudler/LocalAI/core/services/voiceprofile"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Viewer", func() {
	var (
		store          *Store
		dir            string
		alice, bob     = Viewer{UserID: "alice"}, Viewer{UserID: "bob"}
		admin          = Viewer{UserID: "root", Admin: true}
		ctx            = context.Background()
		create         func(v Viewer) Profile
		inputForAnyone = CreateInput{Name: "Voice", Transcript: "words", ConsentConfirmed: true}
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		store = NewStore(dir)
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
		create = func(v Viewer) Profile {
			p, err := store.CreateFor(ctx, v, inputForAnyone, []ReferenceInput{{Transcript: "words", Audio: bytes.NewReader(pcmWAV(2e9))}})
			Expect(err).NotTo(HaveOccurred())
			return p
		}
	})

	It("stamps the owner of a user and leaves an administrator's profile instance-wide", func() {
		Expect(create(alice).OwnerID).To(Equal("alice"))
		Expect(create(admin).OwnerID).To(BeEmpty())
		Expect(create(AdminViewer()).OwnerID).To(BeEmpty())
	})

	It("shows a user their own and the instance-wide profiles, and an administrator all", func() {
		mine, theirs, shared := create(alice), create(bob), create(admin)
		list := func(v Viewer) []string {
			ps, err := store.ListFor(ctx, v)
			Expect(err).NotTo(HaveOccurred())
			var ids []string
			for _, p := range ps {
				ids = append(ids, p.ID)
			}
			return ids
		}
		Expect(list(alice)).To(ConsistOf(mine.ID, shared.ID))
		Expect(list(bob)).To(ConsistOf(theirs.ID, shared.ID))
		Expect(list(admin)).To(ConsistOf(mine.ID, theirs.ID, shared.ID))
	})

	It("tells each caller whether it may delete a profile, and never stores that", func() {
		mine, shared := create(alice), create(admin)
		ps, err := store.ListFor(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		can := map[string]bool{}
		for _, p := range ps {
			can[p.ID] = p.CanDelete
		}
		Expect(can).To(Equal(map[string]bool{mine.ID: true, shared.ID: false}))
		raw, err := os.ReadFile(filepath.Join(dir, DirectoryName, mine.ID, "profile.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("can_delete"))
	})

	It("answers not found, for every way to resolve it, for another user's profile", func() {
		theirs := create(bob)
		_, err := store.GetFor(ctx, alice, theirs.ID)
		Expect(err).To(MatchError(ErrNotFound))
		_, _, err = store.OpenAudioFor(ctx, alice, theirs.ID)
		Expect(err).To(MatchError(ErrNotFound))
		_, _, _, err = store.LeaseAudioFor(ctx, alice, theirs.ID)
		Expect(err).To(MatchError(ErrNotFound))
		_, _, _, err = store.LeaseAudiosFor(ctx, alice, theirs.ID)
		Expect(err).To(MatchError(ErrNotFound))
		Expect(store.DeleteFor(ctx, alice, theirs.ID)).To(MatchError(ErrNotFound))

		_, paths, release, err := store.LeaseAudiosFor(ctx, bob, theirs.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(paths).To(HaveLen(1))
		release()
		_, _, release, err = store.LeaseAudiosFor(ctx, admin, theirs.ID)
		Expect(err).NotTo(HaveOccurred())
		release()
	})

	It("lets the owner and an administrator delete, and an instance-wide profile only the administrator", func() {
		mine, shared := create(alice), create(admin)
		Expect(store.DeleteFor(ctx, alice, shared.ID)).To(MatchError(ErrForbidden))
		Expect(store.DeleteFor(ctx, alice, mine.ID)).To(Succeed())
		other := create(bob)
		Expect(store.DeleteFor(ctx, admin, other.ID)).To(Succeed())
		Expect(store.DeleteFor(ctx, admin, shared.ID)).To(Succeed())
	})

	It("reads a profile stored without an owner as instance-wide", func() {
		p := create(admin)
		meta := filepath.Join(dir, DirectoryName, p.ID, "profile.json")
		raw, err := os.ReadFile(meta)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("owner_id"))
		got, err := store.GetFor(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.OwnerID).To(BeEmpty())
		Expect(alice.CanDelete(got)).To(BeFalse())
	})

	It("limits the profiles of a user, not of an administrator", func() {
		for i := 0; i < MaxProfilesPerUser; i++ {
			create(alice)
		}
		_, err := store.CreateFor(ctx, alice, inputForAnyone, []ReferenceInput{{Transcript: "words", Audio: bytes.NewReader(pcmWAV(2e9))}})
		Expect(err).To(MatchError(ErrQuotaExceeded))
		create(bob)
		for i := 0; i < MaxProfilesPerUser+1; i++ {
			create(admin)
		}
		// Deleting one makes room again.
		ps, _ := store.ListFor(ctx, alice)
		var mine string
		for _, p := range ps {
			if p.OwnerID == "alice" {
				mine = p.ID
				break
			}
		}
		Expect(store.DeleteFor(ctx, alice, mine)).To(Succeed())
		create(alice)
	})

	It("limits the total audio of a user, not of an administrator", func() {
		big := func(v Viewer) (Profile, error) {
			refs := make([]ReferenceInput, MaxReferences)
			for i := range refs {
				refs[i] = ReferenceInput{Transcript: "words", Audio: bytes.NewReader(pcmWAV(110 * time.Second))}
			}
			return store.CreateFor(ctx, v, inputForAnyone, refs)
		}
		for i := 0; i < 2; i++ {
			_, err := big(alice)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := big(alice)
		Expect(err).To(MatchError(ErrQuotaExceeded))
		Expect(err.Error()).To(ContainSubstring("audio per user"))
		ps, _ := store.ListFor(ctx, alice)
		Expect(ps).To(HaveLen(2), "a refused profile leaves nothing behind")
		_, err = big(bob)
		Expect(err).NotTo(HaveOccurred())
		for i := 0; i < 3; i++ {
			_, err = big(admin)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("tells a path from a voice name", func() {
		for _, v := range []string{"/etc/passwd", "../x.wav", "a/b.wav", `a\b.wav`, "..", "x..y"} {
			Expect(UnsafeVoice(v)).To(BeTrue(), v)
		}
		for _, v := range []string{"alloy", "en_US-lessac-medium", "af_heart", ""} {
			Expect(UnsafeVoice(v)).To(BeFalse(), v)
		}
	})
})

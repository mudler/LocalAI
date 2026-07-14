package voiceprofile_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/voiceprofile"
)

func pcmWAV(duration time.Duration) []byte {
	const (
		sampleRate    = 16000
		channels      = 1
		bitsPerSample = 16
	)
	samples := int(duration.Seconds() * sampleRate)
	dataSize := samples * channels * bitsPerSample / 8
	buf := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*channels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

var _ = Describe("Store", func() {
	var store *voiceprofile.Store

	BeforeEach(func() {
		store = voiceprofile.NewStore(GinkgoT().TempDir())
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
	})

	It("atomically creates, lists, opens, leases, and deletes a profile", func(ctx SpecContext) {
		created, err := store.Create(ctx, voiceprofile.CreateInput{
			Name:             "  Studio narrator  ",
			Description:      "Warm and measured",
			Language:         "en-US",
			Transcript:       "This reference sentence is spoken clearly.",
			ConsentConfirmed: true,
		}, bytes.NewReader(pcmWAV(2*time.Second)))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.ID).To(MatchRegexp(`^[0-9a-f-]{36}$`))
		Expect(created.Name).To(Equal("Studio narrator"))
		Expect(created.Voice).To(Equal(voiceprofile.Reference(created.ID)))
		Expect(created.Audio.DurationMilliseconds).To(BeNumerically("~", 2000, 2))
		Expect(created.Audio.SampleRate).To(Equal(uint32(16000)))

		profiles, err := store.List(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(profiles).To(ConsistOf(created))

		file, profile, err := store.OpenAudio(ctx, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(profile).To(Equal(created))
		opened, err := os.ReadFile(file.Name())
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Close()).To(Succeed())
		Expect(opened).To(Equal(pcmWAV(2 * time.Second)))

		_, leasePath, release, err := store.LeaseAudio(ctx, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(leasePath).To(BeAnExistingFile())
		Expect(store.Delete(ctx, created.ID)).To(Succeed())
		Expect(leasePath).To(BeAnExistingFile(), "a concurrent synthesis lease must survive deletion")
		release()
		Expect(leasePath).NotTo(BeAnExistingFile())

		_, err = store.Get(ctx, created.ID)
		Expect(errors.Is(err, voiceprofile.ErrNotFound)).To(BeTrue())
	})

	DescribeTable("rejects invalid creation input",
		func(input voiceprofile.CreateInput, audio []byte, expected error) {
			_, err := store.Create(GinkgoT().Context(), input, bytes.NewReader(audio))
			Expect(errors.Is(err, expected)).To(BeTrue(), "error was %v", err)
		},
		Entry("missing name", voiceprofile.CreateInput{Transcript: "words", ConsentConfirmed: true}, pcmWAV(2*time.Second), voiceprofile.ErrInvalidInput),
		Entry("missing transcript", voiceprofile.CreateInput{Name: "Voice", ConsentConfirmed: true}, pcmWAV(2*time.Second), voiceprofile.ErrInvalidInput),
		Entry("missing consent", voiceprofile.CreateInput{Name: "Voice", Transcript: "words"}, pcmWAV(2*time.Second), voiceprofile.ErrConsentRequired),
		Entry("non-WAV audio", voiceprofile.CreateInput{Name: "Voice", Transcript: "words", ConsentConfirmed: true}, []byte("not audio"), voiceprofile.ErrUnsupportedWAV),
		Entry("too-short audio", voiceprofile.CreateInput{Name: "Voice", Transcript: "words", ConsentConfirmed: true}, pcmWAV(500*time.Millisecond), voiceprofile.ErrInvalidInput),
	)

	It("rejects traversal and non-canonical profile IDs", func(ctx SpecContext) {
		_, err := store.Get(ctx, "../../etc/passwd")
		Expect(errors.Is(err, voiceprofile.ErrInvalidInput)).To(BeTrue())
		Expect(store.Delete(ctx, "NOT-A-UUID")).To(MatchError(ContainSubstring("invalid id")))
	})

	It("stores private files below the data path", func(ctx SpecContext) {
		dataPath := GinkgoT().TempDir()
		privateStore := voiceprofile.NewStore(dataPath)
		DeferCleanup(func() { Expect(privateStore.Close()).To(Succeed()) })
		created, err := privateStore.Create(ctx, voiceprofile.CreateInput{
			Name:             "Private",
			Transcript:       "A private reference.",
			ConsentConfirmed: true,
		}, bytes.NewReader(pcmWAV(time.Second)))
		Expect(err).NotTo(HaveOccurred())

		audioInfo, err := os.Stat(filepath.Join(dataPath, voiceprofile.DirectoryName, created.ID, "reference.wav"))
		Expect(err).NotTo(HaveOccurred())
		Expect(audioInfo.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})
})

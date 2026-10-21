package localai

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeFTJobs struct {
	job      *schema.FineTuneJob
	getErr   error
	cps      []*pb.CheckpointInfo
	exported string
	exports  int
}

func (f *fakeFTJobs) GetJob(string, string) (*schema.FineTuneJob, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.job, nil
}

func (f *fakeFTJobs) ListCheckpoints(context.Context, string, string) ([]*pb.CheckpointInfo, error) {
	return f.cps, nil
}

func (f *fakeFTJobs) ExportModel(context.Context, string, string, schema.ExportRequest) (string, error) {
	f.exports++
	return f.exported, nil
}

type fakeQJobs struct {
	job      *schema.QuantizationJob
	getErr   error
	imported string
	imports  int
}

func (f *fakeQJobs) GetJob(string, string) (*schema.QuantizationJob, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.job, nil
}

func (f *fakeQJobs) ImportModel(context.Context, string, string, schema.QuantizationImportRequest) (string, error) {
	f.imports++
	return f.imported, nil
}

var errNoJob = errors.New("job not found: j9")

var _ = Describe("FineTuneModels", func() {
	It("maps a missing job to not found", func() {
		f := FineTuneModels{Svc: &fakeFTJobs{getErr: errNoJob}}
		_, err := f.ExportModel(context.Background(), "u1", "j9", schema.ExportRequest{})
		Expect(err).To(MatchError(hostops.ErrNotFound), "export")
		Expect(err).To(MatchError(errNoJob), "export")
		_, err = f.Checkpoints(context.Background(), "u1", "j9")
		Expect(err).To(MatchError(hostops.ErrNotFound), "checkpoints")
	})

	It("refuses an export that is running as a conflict", func() {
		svc := &fakeFTJobs{job: &schema.FineTuneJob{ID: "j1", ExportStatus: "exporting"}, exported: "/models/m"}
		f := FineTuneModels{Svc: svc}
		_, err := f.ExportModel(context.Background(), "u1", "j1", schema.ExportRequest{})
		Expect(err).To(MatchError(hostops.ErrConflict))
		Expect(svc.exports).To(BeZero(), "service export ran for a running export")
	})

	It("passes a finished job to the service", func() {
		svc := &fakeFTJobs{job: &schema.FineTuneJob{ID: "j1", ExportStatus: ""}, exported: "/models/m"}
		f := FineTuneModels{Svc: svc}
		got, err := f.ExportModel(context.Background(), "u1", "j1", schema.ExportRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("/models/m"))
		Expect(svc.exports).To(Equal(1))
	})

	It("lists checkpoints with their paths", func() {
		svc := &fakeFTJobs{job: &schema.FineTuneJob{ID: "j1"}, cps: []*pb.CheckpointInfo{{Step: 7, Path: "/out/ckpt-7"}}}
		got, err := FineTuneModels{Svc: svc}.Checkpoints(context.Background(), "u1", "j1")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Step).To(BeEquivalentTo(7))
		Expect(got[0].Path).To(Equal("/out/ckpt-7"))
	})
})

var _ = Describe("QuantizationModels", func() {
	It("maps a missing job to not found", func() {
		q := QuantizationModels{Svc: &fakeQJobs{getErr: errNoJob}}
		_, err := q.ImportModel(context.Background(), "u1", "j9", schema.QuantizationImportRequest{})
		Expect(err).To(MatchError(hostops.ErrNotFound))
	})

	It("refuses a job that is not completed as a conflict", func() {
		svc := &fakeQJobs{job: &schema.QuantizationJob{ID: "q1", Status: "running"}, imported: "/models/q"}
		_, err := (QuantizationModels{Svc: svc}).ImportModel(context.Background(), "u1", "q1", schema.QuantizationImportRequest{})
		Expect(err).To(MatchError(hostops.ErrConflict))
		Expect(svc.imports).To(BeZero(), "service import ran for an unfinished job")
	})

	It("refuses an import that is running as a conflict", func() {
		svc := &fakeQJobs{job: &schema.QuantizationJob{ID: "q1", Status: "completed", ImportStatus: "importing"}, imported: "/models/q"}
		_, err := (QuantizationModels{Svc: svc}).ImportModel(context.Background(), "u1", "q1", schema.QuantizationImportRequest{})
		Expect(err).To(MatchError(hostops.ErrConflict))
	})

	It("passes a completed job to the service", func() {
		svc := &fakeQJobs{job: &schema.QuantizationJob{ID: "q1", Status: "completed"}, imported: "/models/q"}
		got, err := (QuantizationModels{Svc: svc}).ImportModel(context.Background(), "u1", "q1", schema.QuantizationImportRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("/models/q"))
		Expect(svc.imports).To(Equal(1))
	})
})

package localai

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
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

func TestFineTuneModelsMapsAMissingJobToNotFound(t *testing.T) {
	f := FineTuneModels{Svc: &fakeFTJobs{getErr: errNoJob}}
	if _, err := f.ExportModel(context.Background(), "u1", "j9", schema.ExportRequest{}); !errors.Is(err, hostops.ErrNotFound) || !errors.Is(err, errNoJob) {
		t.Fatalf("export: got %v, want not found", err)
	}
	if _, err := f.Checkpoints(context.Background(), "u1", "j9"); !errors.Is(err, hostops.ErrNotFound) {
		t.Fatalf("checkpoints: got %v, want not found", err)
	}
}

func TestFineTuneModelsRefusesAnExportThatIsRunningAsAConflict(t *testing.T) {
	svc := &fakeFTJobs{job: &schema.FineTuneJob{ID: "j1", ExportStatus: "exporting"}, exported: "/models/m"}
	f := FineTuneModels{Svc: svc}
	if _, err := f.ExportModel(context.Background(), "u1", "j1", schema.ExportRequest{}); !errors.Is(err, hostops.ErrConflict) {
		t.Fatalf("got %v, want conflict", err)
	}
	if svc.exports != 0 {
		t.Fatalf("service export ran %d times for a running export", svc.exports)
	}
}

func TestFineTuneModelsPassesAFinishedJobToTheService(t *testing.T) {
	svc := &fakeFTJobs{job: &schema.FineTuneJob{ID: "j1", ExportStatus: ""}, exported: "/models/m"}
	f := FineTuneModels{Svc: svc}
	got, err := f.ExportModel(context.Background(), "u1", "j1", schema.ExportRequest{})
	if err != nil || got != "/models/m" || svc.exports != 1 {
		t.Fatalf("got %q %v exports=%d", got, err, svc.exports)
	}
}

func TestFineTuneModelsListsCheckpointsWithTheirPaths(t *testing.T) {
	svc := &fakeFTJobs{job: &schema.FineTuneJob{ID: "j1"}, cps: []*pb.CheckpointInfo{{Step: 7, Path: "/out/ckpt-7"}}}
	got, err := FineTuneModels{Svc: svc}.Checkpoints(context.Background(), "u1", "j1")
	if err != nil || len(got) != 1 || got[0].Step != 7 || got[0].Path != "/out/ckpt-7" {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestQuantizationModelsMapsAMissingJobToNotFound(t *testing.T) {
	q := QuantizationModels{Svc: &fakeQJobs{getErr: errNoJob}}
	if _, err := q.ImportModel(context.Background(), "u1", "j9", schema.QuantizationImportRequest{}); !errors.Is(err, hostops.ErrNotFound) {
		t.Fatalf("got %v, want not found", err)
	}
}

func TestQuantizationModelsRefusesAJobThatIsNotCompletedAsAConflict(t *testing.T) {
	svc := &fakeQJobs{job: &schema.QuantizationJob{ID: "q1", Status: "running"}, imported: "/models/q"}
	if _, err := (QuantizationModels{Svc: svc}).ImportModel(context.Background(), "u1", "q1", schema.QuantizationImportRequest{}); !errors.Is(err, hostops.ErrConflict) {
		t.Fatalf("got %v, want conflict", err)
	}
	if svc.imports != 0 {
		t.Fatalf("service import ran %d times for an unfinished job", svc.imports)
	}
}

func TestQuantizationModelsRefusesAnImportThatIsRunningAsAConflict(t *testing.T) {
	svc := &fakeQJobs{job: &schema.QuantizationJob{ID: "q1", Status: "completed", ImportStatus: "importing"}, imported: "/models/q"}
	if _, err := (QuantizationModels{Svc: svc}).ImportModel(context.Background(), "u1", "q1", schema.QuantizationImportRequest{}); !errors.Is(err, hostops.ErrConflict) {
		t.Fatalf("got %v, want conflict", err)
	}
}

func TestQuantizationModelsPassesACompletedJobToTheService(t *testing.T) {
	svc := &fakeQJobs{job: &schema.QuantizationJob{ID: "q1", Status: "completed"}, imported: "/models/q"}
	got, err := (QuantizationModels{Svc: svc}).ImportModel(context.Background(), "u1", "q1", schema.QuantizationImportRequest{})
	if err != nil || got != "/models/q" || svc.imports != 1 {
		t.Fatalf("got %q %v imports=%d", got, err, svc.imports)
	}
}

package modelartifacts

import "context"

type Phase string

const (
	PhaseResolving   Phase = "resolving"
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseCommitting  Phase = "committing"
	PhasePersisting  Phase = "persisting"
)

type ProgressEvent struct {
	Phase          Phase
	Artifact       string
	File           string
	CurrentBytes   int64
	TotalBytes     int64
	CompletedFiles int
	TotalFiles     int
}

type ProgressSink func(ProgressEvent)

type progressSinkKey struct{}

func WithProgressSink(ctx context.Context, sink ProgressSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, progressSinkKey{}, sink)
}

func ReportProgress(ctx context.Context, event ProgressEvent) {
	if sink, ok := ctx.Value(progressSinkKey{}).(ProgressSink); ok && sink != nil {
		sink(event)
	}
}

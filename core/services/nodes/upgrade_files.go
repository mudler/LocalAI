package nodes

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// ModelFilePrefetcher sends model.prefetch to one worker. A worker that does
// not serve the verb (an older worker) answers nothing: ErrNoRoute over NATS.
type ModelFilePrefetcher interface {
	PrefetchModelFiles(ctx context.Context, nodeID string, req workerctl.ModelPrefetchRequest, timeout time.Duration) (*workerctl.ModelPrefetchReply, error)
}

// NATSModelFilePrefetcher is the NATS carrier of model.prefetch.
type NATSModelFilePrefetcher struct{ Bus messaging.MessagingClient }

// PrefetchModelFiles sends the request and waits for the worker's answer, or
// until ctx ends: a stage can take hours, and a cancelled upgrade must not
// wait for it. A refusal of the worker is the reply's Error with a nil error.
func (p NATSModelFilePrefetcher) PrefetchModelFiles(ctx context.Context, nodeID string, req workerctl.ModelPrefetchRequest, timeout time.Duration) (*workerctl.ModelPrefetchReply, error) {
	return controlRequestJSONContext[workerctl.ModelPrefetchRequest, workerctl.ModelPrefetchReply](ctx, p.Bus, messaging.SubjectNodeModelPrefetch(nodeID), req, timeout)
}

// PrefetchModelFiles sends model.prefetch over the link of the carrier. It
// returns when ctx ends, like NATSModelFilePrefetcher. A worker that answers
// and does not serve the verb is an older worker: ErrNoRoute, as over NATS.
func (a *nodeControl) PrefetchModelFiles(ctx context.Context, nodeID string, req workerctl.ModelPrefetchRequest, timeout time.Duration) (*workerctl.ModelPrefetchReply, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	type result struct {
		reply *workerctl.ModelPrefetchReply
		err   error
	}
	done := make(chan result, 1)
	go func() {
		reply, err := callVerb[workerctl.ModelPrefetchRequest, workerctl.ModelPrefetchReply](ctx, a.link, nodeID, workerctl.VerbModelPrefetch, req, timeout)
		if err != nil && errors.Is(err, errVerbNotServed) {
			err = fmt.Errorf("%w: %v", ErrNoRoute, err)
		}
		done <- result{reply, err}
	}()
	select {
	case res := <-done:
		return res.reply, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// controlRequestJSONContext is controlRequestJSON that returns when ctx ends.
// The bus request has no context, so it runs on until its own timeout (capped
// by the deadline of ctx) and its answer is dropped.
func controlRequestJSONContext[Req, Reply any](ctx context.Context, bus messaging.MessagingClient, subject string, req Req, timeout time.Duration) (*Reply, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	type result struct {
		reply *Reply
		err   error
	}
	done := make(chan result, 1)
	go func() {
		reply, err := controlRequestJSON[Req, Reply](bus, subject, req, timeout)
		done <- result{reply, err}
	}()
	select {
	case res := <-done:
		return res.reply, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// UpgradeFileNodes is the part of the registry the upgrade file steps read.
type UpgradeFileNodes interface {
	FindNodesWithModel(ctx context.Context, modelName string) ([]BackendNode, error)
	List(ctx context.Context) ([]BackendNode, error)
}

// ObjectDeleter removes an object storage key (storage.FileManager).
type ObjectDeleter interface {
	Delete(ctx context.Context, key string) error
}

// UpgradeFiles has each worker of a cluster download, verify and place its
// own copy of the files of a model upgrade (the Workers seam of
// modelupdates). It keeps no state between the steps: a commit goes to every
// healthy backend node, and each one places what it staged or drops its copy.
//
// With object storage, a load sends a file only when its key is missing, so
// Commit and an evicting Discard also delete the keys of the changed files:
// the next load uploads what the frontend has now.
type UpgradeFiles struct {
	Nodes  UpgradeFileNodes
	Sender ModelFilePrefetcher
	// Objects is nil without object storage.
	Objects ObjectDeleter
	// StageTimeout bounds one worker's download. Zero uses two hours.
	StageTimeout time.Duration
}

const upgradeFilesStepTimeout = time.Minute

// Stage asks the nodes that run the model to download the files beside their
// place. It returns the error of each node that did not stage, for the log
// and the tests; a node in the map loads the frontend's files instead.
func (u *UpgradeFiles) Stage(ctx context.Context, model string, files []workerctl.ModelPrefetchFile) map[string]error {
	nodes, err := u.Nodes.FindNodesWithModel(ctx, model)
	if err != nil {
		xlog.Warn("Could not list the nodes that run a model; they get the upgrade's files from the frontend", "model", model, "error", err)
		return nil
	}
	timeout := u.StageTimeout
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	return u.send(ctx, unique(nodes), workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchStage, ModelName: model, Files: files}, timeout)
}

// Commit runs after the frontend switched its files. An error (an object
// storage key that cannot be deleted, or the node list) rolls the upgrade
// back; a node that does not answer is logged.
func (u *UpgradeFiles) Commit(ctx context.Context, model string, files []workerctl.ModelPrefetchFile) error {
	if err := u.deleteObjects(ctx, model, files); err != nil {
		return err
	}
	nodes, err := u.backendNodes(ctx)
	if err != nil {
		return err
	}
	u.send(ctx, nodes, workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchCommit, ModelName: model, Files: files}, upgradeFilesStepTimeout)
	return nil
}

// Discard drops the staged files on every healthy backend node, and with
// evict their copies in place and the object storage keys.
func (u *UpgradeFiles) Discard(ctx context.Context, model string, files []workerctl.ModelPrefetchFile, evict bool) {
	if evict {
		if err := u.deleteObjects(ctx, model, files); err != nil {
			xlog.Warn("Could not delete the object storage copies of an upgrade", "model", model, "error", err)
		}
	}
	nodes, err := u.backendNodes(ctx)
	if err != nil {
		xlog.Warn("Could not list the nodes to drop the files of an upgrade", "model", model, "error", err)
		return
	}
	u.send(ctx, nodes, workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchDiscard, ModelName: model, Files: files, Evict: evict}, upgradeFilesStepTimeout)
}

func (u *UpgradeFiles) deleteObjects(ctx context.Context, model string, files []workerctl.ModelPrefetchFile) error {
	if u.Objects == nil {
		return nil
	}
	var errs []error
	for _, f := range files {
		key := storage.ModelKey(path.Join(model, f.Filename))
		if err := u.Objects.Delete(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("delete %s from object storage: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

func (u *UpgradeFiles) backendNodes(ctx context.Context) ([]BackendNode, error) {
	all, err := u.Nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []BackendNode
	for _, n := range all {
		if n.NodeType == NodeTypeBackend && n.Status == StatusHealthy {
			out = append(out, n)
		}
	}
	return out, nil
}

// send asks every node at once and returns the error of each node that did
// not do what was asked. An older worker without the verb is told apart in
// the log: it simply gets the files from the frontend.
func (u *UpgradeFiles) send(ctx context.Context, nodes []BackendNode, req workerctl.ModelPrefetchRequest, timeout time.Duration) map[string]error {
	var (
		mu     sync.Mutex
		failed = map[string]error{}
		wg     sync.WaitGroup
	)
	for _, n := range nodes {
		wg.Go(func() {
			reply, err := u.Sender.PrefetchModelFiles(ctx, n.ID, req, timeout)
			switch {
			case err != nil && errors.Is(err, ErrNoRoute):
				xlog.Info("A node does not take model.prefetch (an older worker?); it gets the files from the frontend at its next load",
					"node", n.Name, "action", req.Action, "model", req.ModelName)
			case err != nil:
				xlog.Warn("model.prefetch failed on a node; it gets the files from the frontend at its next load",
					"node", n.Name, "action", req.Action, "model", req.ModelName, "error", err)
			case !reply.Success:
				err = errors.New(reply.Error)
				xlog.Warn("A node refused model.prefetch; it gets the files from the frontend at its next load",
					"node", n.Name, "action", req.Action, "model", req.ModelName, "error", reply.Error)
			}
			if err != nil {
				mu.Lock()
				failed[n.ID] = err
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return failed
}

func unique(nodes []BackendNode) []BackendNode {
	seen := map[string]bool{}
	var out []BackendNode
	for _, n := range nodes {
		if !seen[n.ID] {
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	return out
}

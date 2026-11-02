// SPDX-License-Identifier: MIT
package music

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type runnerCompletion struct {
	event     Event
	candidate Candidate
	prepared  PreparedOutput
	err       error
	interim   bool
	inputAck  bool
}

// Run drives the real reducer. It does not return while a dependency still owns
// a lane or while an issued storage operation can still create an artifact.
func (r *Runner) Run(parent context.Context, in ValidatedRequest) (Result, error) {
	in.GenerationInput = in.GenerationInput.snapshot()
	now := time.Now()
	deadline := now.Add(900 * time.Second)
	if !in.Deadline.IsZero() && in.Deadline.Before(deadline) {
		deadline = in.Deadline
	}
	if d, ok := parent.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	abc := ""
	if in.GenerationInput.ABC != nil {
		abc = *in.GenerationInput.ABC
		if abc == "" {
			return Result{}, ErrPreflight
		}
	}
	if r.Storage == nil {
		return Result{}, ErrPreflight
	}
	issuer := r.Storage.Issuer()
	if issuer == nil {
		return Result{}, ErrPreflight
	}

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if r.Backend == nil || r.Storage == nil || (in.Operation != Transcription && r.Publisher == nil) {
		return Result{}, ErrPreflight
	}
	state, why := Preflight(request{ID: in.ID, Principal: in.Principal, Operation: operation(in.Operation), Transcription: in.Transcription.facts(), Generation: in.Generation.facts(), Feature: in.Feature, Quota: in.Quota, InputsValid: in.InputsValid, StorageReady: in.StorageReady, ABC: abc, Now: now.Unix(), Deadline: deadline.Unix()})
	if why != "" {
		return Result{}, ErrPreflight
	}
	if !issuer.claim(in.ID, in.Source, in.Operation == Transcription || in.Operation == Cover) {
		return Result{}, ErrPreflight
	}
	defer issuer.unclaim(in.ID)
	// Cleanup is deliberately independent of the request cancellation. Expiration
	// cannot stand in for ownership acknowledgement, so no synthetic join timeout.
	cleanup := context.WithoutCancel(ctx)
	completions := make(chan runnerCompletion, 16)
	pending := 0
	launch := func(f func() runnerCompletion) { pending++; go func() { completions <- f() }() }
	var lease StageLease
	var execution StageExecution
	var candidate Candidate
	var prepared PreparedOutput
	artifacts := map[Candidate]struct{}{}
	var ownershipErr error
	var deferredClean []CleanInput
	inputCleaned := false
	responded := false
	cancelled := false
	var apply func(Event)
	var effects func([]Effect)
	inputCleaning := false
	clean := func(e CleanInput) {
		if inputCleaned {
			apply(InputCleaned{correlation{e.Request, e.Stage, e.ID}})
			return
		}
		if inputCleaning {
			return
		}
		inputCleaning = true
		launch(func() runnerCompletion {
			var err error
			// The task returns only after definitive resource release.
			err = r.Storage.CleanInput(cleanup, in.ID, in.Source).Wait()
			issuer.retire(in.Source.token)
			c := correlation{e.Request, e.Stage, e.ID}
			if err != nil {
				return runnerCompletion{event: EffectFailed{c}, err: err, inputAck: true}
			}
			return runnerCompletion{event: InputCleaned{c}, inputAck: true}
		})
	}

	stageRequest := func(st stage, id, exactABC string) StageRequest {
		sel := in.Generation
		if st == transcribe {
			sel = in.Transcription
		}
		g := in.GenerationInput.snapshot()
		if exactABC != "" {
			a := exactABC
			g.ABC = &a
		}
		return StageRequest{in.ID, id, Stage(st), sel, in.Source, g}
	}
	effects = func(es []Effect) {
		for _, effect := range es {
			switch e := effect.(type) {
			case Admit:
				execution = nil
				if ctx.Err() != nil {
					applyCancellation(ctx, &cancelled, apply)
					apply(AdmissionDenied{correlation{e.Request, e.Stage, e.ID}})
					continue
				}
				var err error
				lease, err = r.Backend.Admit(ctx, stageRequest(e.Stage, e.ID, ""))
				// A returned lease always retains ownership, even on error.
				if err != nil {
					ownershipErr = errors.Join(ownershipErr, err)
				}
				c := correlation{e.Request, e.Stage, e.ID}
				if lease == nil {
					apply(AdmissionDenied{c})

				} else if err != nil {
					owned := lease
					launch(func() runnerCompletion {
						releaseErr := owned.Release(cleanup).Wait()
						return runnerCompletion{event: AdmissionDenied{c}, err: releaseErr}
					})
				} else if ctx.Err() != nil {
					applyCancellation(ctx, &cancelled, apply)
					apply(Admitted{c})
					owned := lease
					launch(func() runnerCompletion {
						releaseErr := owned.Release(cleanup).Wait()
						return runnerCompletion{event: StageSettled{correlation{e.Request, e.Stage, "stop"}}, err: releaseErr}
					})
				} else {
					apply(Admitted{c})
				}
			case Run:
				var err error
				execution, err = lease.Start(ctx, stageRequest(e.Stage, e.ID, e.ABC))
				c := correlation{e.Request, e.Stage, e.ID}
				if execution == nil {
					apply(TaskFailed{c})
					owned := lease
					launch(func() runnerCompletion {
						err := owned.Release(cleanup).Wait()
						return runnerCompletion{event: StageSettled{correlation{e.Request, e.Stage, "stop"}}, err: err}
					})
					break
				}
				startErr := err // A nonnil execution retains ownership even when Start reports error.
				actual := execution
				launch(func() runnerCompletion {
					// Consume both channels concurrently: a collaborator may send
					// release before outcome on unbuffered channels. Forward the
					// outcome first to Reduce, but never manufacture release.
					outcomes, acks := actual.Outcome(), actual.Settled()
					captured, settled := false, false
					for !captured || !settled {
						select {
						case out, ok := <-outcomes:
							outcomes = nil
							captured = true
							if !ok {
								out = StageOutcome{Err: errors.New("missing music outcome")}
							}
							event := Event(TaskFailed{c})
							valid := out.Valid && out.Err == nil && startErr == nil
							artifact := uint8(0)
							owned := issuer.owns(out.Candidate.token, in.ID, Stage(e.Stage), 2)
							if owned {
								artifact = 1
							}
							if out.Candidate.token != nil && !owned {
								valid = false
							}
							if e.Stage == generate && !owned {
								valid = false
							}
							exact := ""
							if out.ABC != nil {
								exact = *out.ABC
							}
							if out.Err == nil {
								event = TaskCompleted{c, exact, valid, artifact}
							}
							h := Candidate{}
							if owned {
								h = out.Candidate
							}
							completions <- runnerCompletion{event: event, candidate: h, interim: true}
						case ack, ok := <-acks:
							if !ok {
								acks = nil
								continue
							}
							if ack == nil {
								settled = true
								acks = nil
							}
						}
					}
					return runnerCompletion{event: StageSettled{correlation{e.Request, e.Stage, "stop"}}}

				})
			case Stop:
				if execution != nil {
					execution.Stop()
				}
			case CleanInput:
				s, _ := stateSession(state)
				if s.Ownership == unowned {
					for h := range artifacts {
						v, _ := issuer.record(h.token)
						if v.stage == TranscriptionStage {
							ownershipErr = errors.Join(ownershipErr, r.Storage.CleanArtifact(cleanup, in.ID, h, PreparedOutput{}).Wait())
							issuer.retire(h.token)
							delete(artifacts, h)
						}
					}
				}
				if s.Ownership != unowned {
					deferredClean = append(deferredClean, e)
				} else {
					clean(e)
				}
			case CheckGeneration:
				launch(func() runnerCompletion {
					sel, err := r.Backend.CheckGeneration(ctx, in)
					c := correlation{e.Request, e.Stage, e.ID}
					if err != nil {
						return runnerCompletion{event: EffectFailed{c}, err: err}
					}
					return runnerCompletion{event: GenerationChecked{c, sel.facts()}}
				})
			case FetchValidate:
				owned := candidate
				launch(func() runnerCompletion {
					h, err := r.Storage.FetchValidate(ctx, in.ID, owned)
					valid := err == nil && issuer.owns(h.token, in.ID, GenerationStage, 2)
					return runnerCompletion{event: OutputFetched{correlation{e.Request, e.Stage, e.ID}, valid}, candidate: h, err: err}
				})
			case PreparePublication:
				owned := candidate
				launch(func() runnerCompletion {
					h, err := r.Storage.Prepare(ctx, in.ID, owned)
					return runnerCompletion{event: OutputPrepared{correlation{e.Request, e.Stage, e.ID}, err == nil && issuer.owns(h.token, in.ID, GenerationStage, 3)}, prepared: h, err: err}
				})
			case CommitPublication:
				// Observe cancellation before entering the serialized commit. No worker
				// callback can publish, and cancellation after commit cannot revoke success.
				if ctx.Err() != nil {
					applyCancellation(ctx, &cancelled, apply)
					continue
				}
				output, err := r.Publisher.Commit(cleanup, in.ID, prepared)
				c := correlation{e.Request, e.Stage, e.ID}
				if err != nil {
					apply(EffectFailed{c})
				} else {
					apply(OutputPublished{c, output})
				}
			case Revoke:
				launch(func() runnerCompletion { return runnerCompletion{err: r.Storage.Revoke(cleanup, in.ID).Wait()} })
			case CleanArtifact:
				// Late artifacts stay quarantined in the request ledger until
				// explicit settlement AND all producers have joined. Reducer's
				// stage-local cleanup ID never substitutes a different handle.
				// The ledger is populated only by owned completion handles.

			case Respond:
				responded = true
			}
		}
	}
	apply = func(event Event) {
		t := Reduce(state, event)
		state = t.State
		if r.Trace != nil {
			r.Trace(t)
		}
		effects(t.Effects)
	}
	apply(Start{})
	for {
		s, terminal := stateSession(state)
		if s.Ownership == unowned && len(deferredClean) > 0 {
			waiting := deferredClean
			deferredClean = nil
			for _, e := range waiting {
				clean(e)
			}
		}
		if terminal && responded && pending == 0 && s.Ownership == unowned {
			if !inputCleaned && in.Source.token != nil {
				ownershipErr = errors.Join(ownershipErr, r.Storage.CleanInput(cleanup, in.ID, in.Source).Wait())
				issuer.retire(in.Source.token)
				inputCleaned = true
			}
			_, success := state.(Succeeded)
			// Successful publication transfers only the final candidate and
			// prepared object. Validation replacements do not transfer old files.
			for h := range artifacts {
				if success && h == candidate {
					issuer.retire(h.token)
					delete(artifacts, h)
					continue
				}
				attached := PreparedOutput{}
				if !success {
					attached = prepared
				}
				ownershipErr = errors.Join(ownershipErr, r.Storage.CleanArtifact(cleanup, in.ID, h, attached).Wait())
				issuer.retire(h.token)
				if !success {
					issuer.retire(prepared.token)
					prepared = PreparedOutput{}
				}
				delete(artifacts, h)
			}
			if !success && prepared.token != nil {
				ownershipErr = errors.Join(ownershipErr, r.Storage.CleanArtifact(cleanup, in.ID, Candidate{}, prepared).Wait())
				issuer.retire(prepared.token)
				prepared = PreparedOutput{}
			}
			if success {
				issuer.retire(prepared.token)
			}
			break
		}
		done := ctx.Done()
		if cancelled {
			done = nil
		}
		select {
		case <-done:
			applyCancellation(ctx, &cancelled, apply)
		case c := <-completions:
			if !c.interim {
				pending--
			}
			if c.err != nil {
				ownershipErr = errors.Join(ownershipErr, c.err)
			}
			if c.inputAck {
				inputCleaned = true
				inputCleaning = false
			}
			if v, ok := issuer.record(c.candidate.token); ok && v.request == in.ID && v.kind == 2 {
				artifacts[c.candidate] = struct{}{}
				if v.stage == GenerationStage {
					candidate = c.candidate
				}
			}
			if issuer.owns(c.prepared.token, in.ID, GenerationStage, 3) {
				prepared = c.prepared
			}

			if c.event != nil {
				apply(c.event)
			}
		}
	}
	s, _ := stateSession(state)
	result := Result{RequestID: in.ID, ResolvedModels: map[Stage]string{}, ABCSource: s.ABCSource}
	if in.Operation != Generation {
		result.ResolvedModels[TranscriptionStage] = in.Transcription.CanonicalModel
	}
	if in.Operation != Transcription {
		result.ResolvedModels[GenerationStage] = in.Generation.CanonicalModel
	}
	if s.ABC != "" {
		a := s.ABC
		result.ABC = &a
	}
	switch v := state.(type) {
	case Succeeded:
		result.OutputID = v.Output
	case Failed:
		result.StageError = v.Code
	case Cancelled:
		result.StageError = v.Reason
	}
	if result.StageError != "" {
		return result, errors.Join(fmt.Errorf("music: %s", result.StageError), ownershipErr)
	}
	return result, ownershipErr
}
func applyCancellation(ctx context.Context, observed *bool, apply func(Event)) {
	if *observed {
		return
	}
	*observed = true
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		apply(DeadlineExpired{time.Now().Unix()})
	} else {
		apply(Cancel{})
	}
}

// SPDX-License-Identifier: MIT
// Package musicapi provides request-scoped music POST orchestration. It does not
// install routes or supply a production model resolver or native executor.
package musicapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai/musicoutput"
	"github.com/mudler/LocalAI/core/http/endpoints/localai/musicrequest"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/music"
	"github.com/mudler/LocalAI/core/services/music/outputstore"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type Runner interface {
	Run(context.Context, music.ValidatedRequest) (music.Result, error)
}

// Bind must register canonical metadata and import only the exact owned upload.
// Finish joins request cleanup while preserving an already committed object.
type Bridge interface {
	Bind(context.Context, music.ValidatedRequest, *musicrequest.OwnedUpload) (music.OwnedInput, error)
	Finish(context.Context, string) music.CleanupTask
	Lookup(context.Context, string) (outputstore.Manifest, error)
}

// PublicError is implemented by trusted collaborators, never decoded from input.
// The fixed public vocabulary prevents arbitrary native diagnostics escaping.
type PublicError interface {
	error
	HTTPStatus() int
	MusicStage() string
	MusicReason() string
}
type Config struct {
	Parser      *musicrequest.Parser
	Resolver    backend.MusicResolver
	Runner      Runner
	Bridge      Bridge
	AuthEnabled bool
	Clock       func() time.Time
}
type Handlers struct{ Transcriptions, Generations, Covers echo.HandlerFunc }
type entryKey struct{}
type entry struct {
	ID       string
	Deadline time.Time
}

func randomID() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	return r.Kind() == reflect.Pointer && r.IsNil()
}
func NewHandlers(cfg Config) Handlers {
	return Handlers{handler(cfg, music.Transcription), handler(cfg, music.Generation), handler(cfg, music.Cover)}
}
func handler(cfg Config, op music.Operation) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Entry time precedes authentication and every byte of upload parsing.
		clock := cfg.Clock
		if clock == nil {
			clock = time.Now
		}
		deadline := clock().Add(900 * time.Second)
		if d, ok := c.Request().Context().Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		ctx, cancel := context.WithDeadline(c.Request().Context(), deadline)
		defer cancel()
		id, err := randomID()
		if err != nil {
			return c.NoContent(500)
		}
		ctx = context.WithValue(ctx, entryKey{}, entry{id, deadline})
		c.SetRequest(c.Request().WithContext(ctx))
		c.Response().Header().Set(echo.HeaderXRequestID, id)
		fail := func(status int, stage, reason string, abc *string) error {
			return writeError(c, id, status, stage, reason, abc)
		}
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return fail(504, "validation", "deadline_exceeded", nil)
		}
		principal := musicoutput.OpenPrincipal
		if cfg.AuthEnabled {
			u := auth.GetUser(c)
			if u == nil || u.ID == "" {
				return fail(401, "validation", "authentication_required", nil)
			}
			principal = u.ID
		}
		if cfg.Parser == nil {
			return fail(503, "validation", "service_unavailable", nil)
		}
		in := music.ValidatedRequest{ID: id, Principal: principal, Operation: op, Deadline: deadline}
		var upload *musicrequest.OwnedUpload
		var g musicrequest.Generation
		switch op {
		case music.Transcription:
			var t musicrequest.Transcription
			t, err = cfg.Parser.ParseTranscription(c.Request())
			in.Transcription.CanonicalModel = t.Model
			upload = t.Audio
		case music.Generation:
			g, err = cfg.Parser.ParseGeneration(c.Request())
		case music.Cover:
			var v musicrequest.Cover
			v, err = cfg.Parser.ParseCover(c.Request())
			in.Transcription.CanonicalModel = v.TranscriptionModel
			g = v.Generation
			upload = v.Audio
		}
		// The parser joins failed parse allocations itself. Successful transfers are
		// explicitly joined here BEFORE writing any terminal response, not by defer.
		finishUpload := func() error {
			if upload == nil {
				return nil
			}
			closeErr := upload.Writer.Close()
			cleanErr := upload.Cleanup().Wait()
			upload = nil
			return errors.Join(closeErr, cleanErr)
		}
		if err != nil {
			return emitError(c, id, ctx, music.Result{}, errors.Join(err, finishUpload()), "validation")
		}
		in.Generation.CanonicalModel = g.Model
		if op != music.Transcription {
			in.GenerationInput = music.GenerationInput{Style: g.Style, Lyrics: g.Lyrics, ABC: g.ABC, Params: &pb.MusicGenerationParams{Cot: map[string]pb.MusicCOT{"off": pb.MusicCOT_MUSIC_COT_OFF, "melody": pb.MusicCOT_MUSIC_COT_MELODY, "full": pb.MusicCOT_MUSIC_COT_FULL}[g.Params.COT], Seed: uint32(g.Params.Seed), GuidanceScale: g.Params.Guidance, NumInferenceSteps: uint32(g.Params.Steps)}}
		}
		if nilValue(cfg.Resolver) || nilValue(cfg.Runner) || nilValue(cfg.Bridge) {
			if cleanupErr := finishUpload(); cleanupErr != nil {
				return emitError(c, id, ctx, music.Result{}, cleanupErr, "validation")
			}
			return fail(503, "validation", "service_unavailable", nil)
		}
		resolved, err := cfg.Resolver.ResolveMusic(ctx, in)
		// A resolver establishes facts but cannot change request ownership or input.
		resolved.ID = id
		resolved.Principal = principal
		resolved.Operation = op
		resolved.Deadline = deadline
		resolved.Source = music.OwnedInput{}
		resolved.GenerationInput = in.GenerationInput
		if err != nil {
			return emitError(c, id, ctx, music.Result{}, errors.Join(err, finishUpload()), "validation")
		}
		source, err := cfg.Bridge.Bind(ctx, resolved, upload)
		resolved.Source = source
		cleanErr := finishUpload()
		if err != nil || cleanErr != nil {
			endErr := cfg.Bridge.Finish(context.WithoutCancel(ctx), id).Wait()
			return emitError(c, id, ctx, music.Result{}, errors.Join(err, cleanErr, endErr), "validation")
		}
		result, runErr := cfg.Runner.Run(ctx, resolved)
		endErr := cfg.Bridge.Finish(context.WithoutCancel(ctx), id).Wait()
		if runErr != nil || endErr != nil || result.StageError != "" {
			return emitError(c, id, ctx, result, errors.Join(runErr, endErr), stageFor(op, result))
		}
		// Success publication has already won Runner's serialized cancellation race.
		if op == music.Transcription {
			if result.ABC == nil || music.ValidateABC(*result.ABC) != nil {
				return fail(502, "transcription", "backend_failed", nil)
			}
			return c.JSON(200, map[string]any{"request_id": id, "model": result.ResolvedModels[music.TranscriptionStage], "abc": *result.ABC})
		}
		if !validID(result.OutputID) {
			return fail(500, "delivery", "publication_failed", result.ABC)
		}
		m, err := cfg.Bridge.Lookup(context.WithoutCancel(ctx), result.OutputID)
		if err != nil || m.ID != result.OutputID || m.Principal != principal || m.GenerationModel != resolved.Generation.CanonicalModel || m.TranscriptionModel != resolved.Transcription.CanonicalModel || m.ContentType != "audio/wav" || m.Size <= 0 || m.Size > outputstore.MaxWAVBytes || !m.ExpiresAt.After(clock()) {
			return fail(500, "delivery", "publication_failed", result.ABC)
		}
		var abcSource any
		if result.ABC != nil {
			if music.ValidateABC(*result.ABC) != nil {
				return fail(502, "generation", "backend_failed", nil)
			}
			abcSource = result.ABCSource
		}
		response := map[string]any{"request_id": id, "model": result.ResolvedModels[music.GenerationStage], "audio": map[string]any{"url": "/v1/music/outputs/" + m.ID + "/audio", "content_type": "audio/wav", "expires_at": m.ExpiresAt.UTC().Format(time.RFC3339)}, "abc": result.ABC, "abc_source": abcSource}
		if op == music.Cover {
			response["transcription_model"] = result.ResolvedModels[music.TranscriptionStage]
		}
		return c.JSON(http.StatusOK, response)
	}
}
func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 32
}
func stageFor(op music.Operation, r music.Result) string {
	if op == music.Transcription || op == music.Cover && r.ABC == nil {
		return "transcription"
	}
	return "generation"
}

type stageError struct {
	*schema.APIError
	Stage  string  `json:"stage"`
	Reason string  `json:"reason"`
	ABC    *string `json:"abc,omitempty"`
}

func writeError(c echo.Context, id string, status int, stage, reason string, abc *string) error {
	return c.JSON(status, struct {
		RequestID string     `json:"request_id"`
		Error     stageError `json:"error"`
	}{id, stageError{&schema.APIError{Code: status, Type: "music_stage_error", Message: "Music request failed."}, stage, reason, abc}})
}

// These are exact reducer domain codes, not parsing of error.Error(). The
// reducer loses some collaborator distinctions; production adapters must expose
// PublicError where those distinctions are available. Unknown errors stay 500.
func emitError(c echo.Context, id string, ctx context.Context, r music.Result, err error, stage string) error {
	status, reason := 500, "internal_error"
	switch r.StageError {
	case "capacity_429":
		status, reason = 429, "capacity_unavailable"
	case "backend":
		status, reason = 502, "backend_failed"
	case "fetch_validation":
		status, stage, reason = 502, "delivery", "output_invalid"
	case "publication_prepare", "publication_commit":
		status, stage, reason = 500, "delivery", "publication_failed"
	case "generation_preflight":
		status, stage, reason = 403, "generation", "access_denied"
	case "effect":
		status, reason = 500, "effect_failed"
	case "deadline":
		status, reason = 504, "deadline_exceeded"
	case "client_disconnect":
		return context.Canceled
	}
	var pe *musicrequest.Error
	if errors.As(err, &pe) {
		status, stage, reason = pe.Code, pe.Stage, pe.Reason
	}
	var public PublicError
	if errors.As(err, &public) && allowedPublic(public.HTTPStatus(), public.MusicStage(), public.MusicReason()) {
		status, stage, reason = public.HTTPStatus(), public.MusicStage(), public.MusicReason()
	}
	if errors.Is(err, music.ErrSettlementUnsupported) {
		status, reason = 503, "service_unavailable"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		status, reason = 504, "deadline_exceeded"
	}
	return writeError(c, id, status, stage, reason, r.ABC)
}
func allowedPublic(status int, stage, reason string) bool {
	switch stage {
	case "validation", "transcription", "generation", "delivery":
	default:
		return false
	}
	switch status {
	case 400, 401, 403, 404, 413, 415, 422, 429, 500, 502, 503, 504:
	default:
		return false
	}
	switch reason {
	case "authentication_required", "access_denied", "model_not_found", "size_limit", "unsupported_media_type", "invalid_input", "invalid_abc", "context_limit", "unsupported_capability", "capacity_unavailable", "backend_failed", "service_unavailable", "deadline_exceeded", "publication_failed", "output_invalid":
		return true
	}
	return false
}

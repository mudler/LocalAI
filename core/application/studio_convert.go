package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/convert"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// studioConversionLink is where a notification of a conversion leads.
const studioConversionLink = "/app/chats/conversion"

// StudioConversion returns the report of the last conversion, and whether there
// is one. It holds the items of every user: the caller filters.
func (a *Application) StudioConversion(ctx context.Context) (convert.Report, bool, error) {
	if a.studioMarks == nil {
		return convert.Report{}, false, nil
	}
	return a.studioMarks.Report(ctx)
}

// prepareStudioConversion opens the marks, so the report can be read from the
// start on. It does not convert: see ConvertStudioData.
func (a *Application) prepareStudioConversion(svc *studio.Service) {
	marks, err := a.studioConversionMarks()
	if err != nil {
		xlog.Warn("Chats: the old data is not converted: no place for the marks", "error", err)
		return
	}
	a.studioMarks = marks
	svc.SetConversion(marks)
	// Without the collections service nothing starts later that the conversion
	// waits for, so it runs now.
	if !a.applicationConfig.AgentPool.Enabled {
		a.ConvertStudioData()
	}
}

// ConvertStudioData turns what the old chat, agents, tasks and skills left into
// definitions of the chats. What was converted is marked, so a later run does
// nothing for it. In a cluster one frontend converts at a time, and the others
// find the marks when their turn comes.
//
// It runs after the collections service has started (the server calls it right
// after StartCollections, off the start-up path, as the import of a long history
// takes time): an agent with a knowledge base gets the grant on the collection
// named like it only when the service can list the collections. When the
// service is off the conversion runs at once, and such an agent is reported as
// failed, not converted without its grant.
func (a *Application) ConvertStudioData() {
	svc := a.studioService
	if svc == nil || a.studioMarks == nil {
		return
	}
	ctx := a.applicationConfig.Context
	if ctx == nil {
		ctx = context.Background()
	}
	zone, note := convert.LocalZone()
	src := convert.Sources{StateDir: a.applicationConfig.AgentPool.StateDir, DataDir: a.applicationConfig.DataPath,
		DB: a.authDB, AuthEnabled: a.authDB != nil, Zone: zone, ZoneNote: note}
	run := func() error {
		rep, err := convert.Run(ctx, src, studioTargets{a: a, svc: svc}, a.studioMarks)
		if err != nil {
			return err
		}
		a.tellConversion(ctx, rep)
		return nil
	}
	var err error
	if a.Distributed() != nil && a.authDB != nil {
		err = advisorylock.WithLockCtx(ctx, a.authDB, advisorylock.KeyChatsConvert, run)
	} else {
		err = run()
	}
	if err != nil {
		xlog.Warn("Chats: the old data could not be converted", "error", err)
	}
}

// studioConversionMarks keeps the marks beside the chats: in the auth database,
// in a folder under the data path, or nowhere (then nothing is converted: the
// old data is a folder, and without a data path there is none).
func (a *Application) studioConversionMarks() (convert.Marks, error) {
	switch {
	case a.authDB != nil:
		return convert.NewGORMMarks(a.authDB)
	case a.applicationConfig.DataPath != "":
		return convert.NewJSONMarks(filepath.Join(a.applicationConfig.DataPath, "chats", "conversion"))
	}
	return nil, errors.New("no database and no data path")
}

// tellConversion gives each user with news one notification. The news of a run
// are what it converted or dropped for the first time and the failures that are
// new; an item that cannot convert, and fails or is dropped again at each start,
// says nothing after the first time.
func (a *Application) tellConversion(ctx context.Context, rep convert.Report) {
	if a.studioInbox == nil {
		return
	}
	news := convert.Report{At: rep.At, Items: rep.Notable}
	for _, user := range news.Users() {
		c := news.ForUser(user).Counts()
		look := c[convert.StatusFailed] + c[convert.StatusDropped]
		_, err := a.studioInbox.Add(ctx, notify.Notification{
			Principal: studioPrincipal(user), Kind: notify.KindNeedsAttention, Status: notify.StatusOK,
			Title: "Your old agents, skills and chats were converted",
			Body:  fmt.Sprintf("%d items converted, %d need a look. Converted triggers and connectors are switched off.", c[convert.StatusConverted], look),
			Link:  studioConversionLink, Created: time.Now().UTC(),
		})
		if err != nil {
			xlog.Warn("Chats: could not tell a user about the conversion", "error", err)
		}
	}
}

func studioPrincipal(user string) string {
	if user == "" {
		return studio.AnonymousPrincipal
	}
	return user
}

// studioTargets puts converted things into the services of the chats.
type studioTargets struct {
	a   *Application
	svc *studio.Service
}

var _ convert.Targets = studioTargets{}

func (t studioTargets) UserExists(ctx context.Context, user string) (bool, error) {
	if t.a.authDB == nil {
		return user == "", nil
	}
	var n int64
	err := t.a.authDB.WithContext(ctx).Model(&auth.User{}).Where("id = ?", user).Count(&n).Error
	return n > 0, err
}

func (t studioTargets) Collections(ctx context.Context, user string) ([]string, error) {
	if t.a.CollectionsService() == nil {
		return nil, convert.ErrCollectionsUnavailable
	}
	names, err := studioCollections{t.a}.List(ctx, user)
	if errors.Is(err, studio.ErrNoCollection) {
		return nil, nil // the user has no collections feature: there is no grant to make
	}
	return names, err
}

func (t studioTargets) SaveAgent(ctx context.Context, user string, in convert.AgentIn) (string, error) {
	ag, err := t.svc.SaveAgent(ctx, user, agents.Agent{Name: in.Name, Description: in.Description, Instructions: in.Instructions,
		Model: in.Model, Collections: in.Collections, Skills: in.Skills})
	if errors.Is(err, agents.ErrNameTaken) {
		return "", convert.ErrNameTaken
	}
	return ag.ID, invalid(err)
}

func (t studioTargets) CreateTrigger(ctx context.Context, user string, in convert.TriggerIn) error {
	h := t.svc.Triggers()
	if h == nil {
		return studio.ErrTriggersOff
	}
	_, err := h.CreateTrigger(ctx, user, studio.TriggerInput{Name: in.Name, Prompt: in.Prompt, Kind: triggers.KindSchedule,
		Schedule: in.Schedule, TZ: in.TZ, Model: in.Model, AgentID: in.AgentID, Enabled: false})
	return invalid(err)
}

func (t studioTargets) skillActor(user string, shared bool) string {
	if shared {
		return files.AdminPrincipal
	}
	return studioPrincipal(user)
}

func (t studioTargets) layer(shared bool) skills.Layer {
	if shared {
		return skills.LayerShared
	}
	return skills.LayerPersonal
}

func (t studioTargets) SkillExists(ctx context.Context, user string, shared bool, name string) (bool, error) {
	_, _, err := t.svc.Files().ReadSkill(ctx, t.skillActor(user, shared), t.layer(shared), name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, files.ErrUnavailable):
		return false, err
	}
	return false, nil // a skill that cannot be read is one we may write
}

func (t studioTargets) WriteSkill(ctx context.Context, user string, shared bool, name, description, body string) error {
	return invalid(t.svc.Files().WriteSkill(ctx, t.skillActor(user, shared), t.layer(shared), name, description, body))
}

func (t studioTargets) ImportChat(ctx context.Context, user string, in convert.ChatIn) error {
	entries := make([]studio.ImportedEntry, 0, len(in.Entries))
	for _, e := range in.Entries {
		entries = append(entries, studio.ImportedEntry{Role: e.Role, Text: e.Text, At: e.At})
	}
	_, err := t.svc.ImportChat(ctx, user, in.Title, in.Model, in.Created, in.Updated, entries)
	return err
}

// DiscardChat deletes what an import that a crash cut short left: the chats of
// the user with the title and the creation time the import was given.
func (t studioTargets) DiscardChat(ctx context.Context, user, title string, created time.Time) error {
	chats, err := t.svc.ListChats(ctx, user, 0)
	if err != nil {
		return err
	}
	for _, c := range chats {
		if c.Title == title && c.Created.UnixMilli() == created.UnixMilli() {
			if err := t.svc.Delete(ctx, user, c.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t studioTargets) CreateBinding(ctx context.Context, user string, in convert.BindingIn) error {
	c := t.svc.Connectors()
	if c == nil {
		return errors.New("connectors are not available")
	}
	_, err := c.CreateConnector(ctx, user, studio.ConnectorInput{Name: in.Name, Token: in.Token, AgentID: in.AgentID, Allow: in.Allow, Enabled: false})
	return invalid(err)
}

// invalid marks an error that says the item is not valid for the service, so the
// conversion drops the item with the reason instead of trying it at each start.
func invalid(err error) error {
	switch {
	case errors.Is(err, agents.ErrInvalid), errors.Is(err, files.ErrBadSkill), errors.Is(err, studio.ErrBadTrigger),
		errors.Is(err, studio.ErrTooManyTriggers), errors.Is(err, studio.ErrBadSkillName), errors.Is(err, connectors.ErrInvalid):
		return fmt.Errorf("%w: %s", convert.ErrInvalid, err)
	}
	return err
}

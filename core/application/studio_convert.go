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

// convertStudioData turns what the old chat, agents, tasks and skills left into
// definitions of the chats. It runs once at each start, after every service of
// the chats exists; what was converted is marked, so a later start does nothing
// for it. In a cluster one frontend converts at a time, and the others find the
// marks when their turn comes.
func (a *Application) convertStudioData(svc *studio.Service) {
	ctx := a.applicationConfig.Context
	if ctx == nil {
		ctx = context.Background()
	}
	marks, err := a.studioConversionMarks()
	if err != nil {
		xlog.Warn("Chats: the old data is not converted: no place for the marks", "error", err)
		return
	}
	a.studioMarks = marks
	svc.SetConversion(marks)
	src := convert.Sources{StateDir: a.applicationConfig.AgentPool.StateDir, DataDir: a.applicationConfig.DataPath,
		DB: a.authDB, AuthEnabled: a.authDB != nil}
	run := func() error {
		rep, err := convert.Run(ctx, src, studioTargets{a: a, svc: svc}, marks)
		if err != nil {
			return err
		}
		a.tellConversion(ctx, rep)
		return nil
	}
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

// tellConversion gives each user with something new one notification. A run
// that converted and failed nothing, as a start after the first one does, says
// nothing.
func (a *Application) tellConversion(ctx context.Context, rep convert.Report) {
	if a.studioInbox == nil {
		return
	}
	for _, user := range rep.Users() {
		own := rep.ForUser(user)
		c := own.Counts()
		if c[convert.StatusConverted]+c[convert.StatusFailed]+c[convert.StatusDropped] == 0 {
			continue
		}
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
	names, err := studioCollections{t.a}.List(ctx, user)
	if errors.Is(err, studio.ErrNoCollection) {
		return nil, nil
	}
	return names, err
}

func (t studioTargets) SaveAgent(ctx context.Context, user string, in convert.AgentIn) (string, error) {
	ag, err := t.svc.SaveAgent(ctx, user, agents.Agent{Name: in.Name, Description: in.Description, Instructions: in.Instructions,
		Model: in.Model, Collections: in.Collections, Skills: in.Skills})
	if errors.Is(err, agents.ErrNameTaken) {
		return "", convert.ErrNameTaken
	}
	return ag.ID, err
}

func (t studioTargets) CreateTrigger(ctx context.Context, user string, in convert.TriggerIn) error {
	h := t.svc.Triggers()
	if h == nil {
		return studio.ErrTriggersOff
	}
	_, err := h.CreateTrigger(ctx, user, studio.TriggerInput{Name: in.Name, Prompt: in.Prompt, Kind: triggers.KindSchedule,
		Schedule: in.Schedule, Model: in.Model, AgentID: in.AgentID, Enabled: false})
	return err
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
	return t.svc.Files().WriteSkill(ctx, t.skillActor(user, shared), t.layer(shared), name, description, body)
}

func (t studioTargets) ImportChat(ctx context.Context, user string, in convert.ChatIn) error {
	entries := make([]studio.ImportedEntry, 0, len(in.Entries))
	for _, e := range in.Entries {
		entries = append(entries, studio.ImportedEntry{Role: e.Role, Text: e.Text, At: e.At})
	}
	_, err := t.svc.ImportChat(ctx, user, in.Title, in.Model, in.Created, in.Updated, entries)
	return err
}

func (t studioTargets) CreateBinding(ctx context.Context, user string, in convert.BindingIn) error {
	c := t.svc.Connectors()
	if c == nil {
		return errors.New("connectors are not available")
	}
	_, err := c.CreateConnector(ctx, user, studio.ConnectorInput{Name: in.Name, Token: in.Token, AgentID: in.AgentID, Allow: in.Allow, Enabled: false})
	return err
}

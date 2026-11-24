package application

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/projects"
)

// initStudioProjects builds the projects service on the project index and the
// files of the chats. It returns nil when either is missing. The service keeps
// nothing in memory, so each frontend of a cluster builds its own.
func (a *Application) initStudioProjects() *projects.Service {
	f := a.StudioFiles()
	if a.studioProjectStore == nil || !f.Available() {
		return nil
	}
	return projects.NewService(projects.Options{
		Store:     a.studioProjectStore,
		Files:     f,
		Permitted: a.studioChatsPermitted,
		OwnerName: a.studioOwnerName,
	})
}

// studioOwnerName is the display name of a user for the projects that the
// user shares. It is the name only, never the email or the ID: an empty
// answer makes the projects service use a neutral text. A name that looks
// like an email address also gets the empty answer: some sign-in providers
// store the address as the name. The account's status
// and features do not matter here: a disabled owner's shared project still
// has its owner's name.
func (a *Application) studioOwnerName(ctx context.Context, user string) string {
	if a.authDB == nil || user == "" {
		return ""
	}
	var name string
	if err := a.authDB.WithContext(ctx).Model(&auth.User{}).Select("name").Where("id = ?", user).Limit(1).Scan(&name).Error; err != nil {
		return ""
	}
	if strings.Contains(name, "@") {
		return ""
	}
	return name
}

// studioChatsPermitted says whether a user may use the chats now: the account
// is active and has the chats feature. It is asked on every use.
func (a *Application) studioChatsPermitted(ctx context.Context, user string) bool {
	if a.authDB == nil {
		return true
	}
	_, err := a.loadStudioUser(ctx, user)
	return err == nil
}

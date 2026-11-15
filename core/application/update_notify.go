package application

import (
	"context"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/updatenotify"
)

// updateEmitter returns the writer of the inbox items of the update center. It
// looks the inbox up at each call: the inbox starts after the update checker,
// and it does not exist when the chats are off.
func (a *Application) updateEmitter() *updatenotify.Emitter {
	return &updatenotify.Emitter{
		Inbox:  func() *notify.Service { return a.studioInbox },
		Admins: a.updateAdmins,
	}
}

// updateAdmins lists who is told about updates: the active administrators, or
// the one anonymous principal when authentication is off (the person at the
// keyboard is then the administrator). The synthetic user of the legacy API
// key has no browser and no row in the user table, so it is not listed.
func (a *Application) updateAdmins(ctx context.Context) ([]string, error) {
	if a.authDB == nil {
		return []string{studio.AnonymousPrincipal}, nil
	}
	var ids []string
	err := a.authDB.WithContext(ctx).Model(&auth.User{}).
		Where("role = ? AND status = ?", auth.RoleAdmin, auth.StatusActive).Order("id").Pluck("id", &ids).Error
	return ids, err
}

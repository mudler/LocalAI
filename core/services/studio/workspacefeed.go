package studio

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/wsfeed"
)

// SubscribeWorkspace returns the changes of the user's home folder, whichever
// process made them. With resume set they start after revision after;
// otherwise from now on. Call cancel when done.
func (s *Service) SubscribeWorkspace(ctx context.Context, user string, after int64, resume bool) (<-chan wsfeed.Event, func(), error) {
	if s.feed == nil {
		return nil, nil, fmt.Errorf("%w: %s", files.ErrUnavailable, s.d.Files.Unavailable())
	}
	return s.feed.Subscribe(ctx, principal(user), after, resume)
}

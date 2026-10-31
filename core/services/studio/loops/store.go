package loops

import "context"

// Store keeps loops, the owners' daily budgets of runs and which chats have
// confirmed a loop once. A Store is safe for concurrent use, also from several
// processes for the GORM store. loopstest.RunStoreSuite checks every method.
type Store interface {
	// Add stores a new loop. It refuses, and stores nothing, when the chat
	// or the owner already has as many loops that are not ended as lim
	// allows (ErrTooManyInChat, ErrTooManyForOwner). A limit of zero or less
	// is no limit. The count and the insert are one step, also across
	// frontends.
	Add(ctx context.Context, l Loop, lim Limits) error
	// Get returns ErrNotFound for an unknown ID.
	Get(ctx context.Context, id string) (Loop, error)
	// Update changes a loop in one atomic step: change gets the current loop
	// and edits it. An error from change leaves the loop as it was and is
	// returned. The ID, owner and chat do not change. ErrNotFound for an
	// unknown ID.
	Update(ctx context.Context, id string, change func(*Loop) error) (Loop, error)
	// Delete removes a loop. It is not an error when it does not exist.
	Delete(ctx context.Context, id string) error
	// ListChat returns the loops of one chat of one owner, oldest first.
	ListChat(ctx context.Context, owner, chatID string) ([]Loop, error)
	// ListOwner returns the loops of one owner, oldest first.
	ListOwner(ctx context.Context, owner string) ([]Loop, error)
	// Working returns every loop that is active or has a run going on, of
	// every owner, oldest first. The runner reads it at each pass.
	Working(ctx context.Context) ([]Loop, error)
	// DeleteChat removes every loop of a chat and the chat's consent, and
	// returns how many loops it removed.
	DeleteChat(ctx context.Context, owner, chatID string) (int, error)

	// TakeRun takes one run from the owner's budget of day (a Day string):
	// it returns false, and takes nothing, when budget runs were taken that
	// day already. A budget of zero or less is no limit. Two concurrent calls
	// never both take the last run.
	TakeRun(ctx context.Context, owner, day string, budget int) (bool, error)

	// Consented reports whether the owner confirmed a loop in the chat once.
	Consented(ctx context.Context, owner, chatID string) (bool, error)
	// SetConsented records that the owner confirmed a loop in the chat.
	SetConsented(ctx context.Context, owner, chatID string) error
}

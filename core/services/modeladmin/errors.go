package modeladmin

import "errors"

// Sentinel errors callers can switch on. HTTP handlers map them to specific
// status codes; the inproc MCP client surfaces them verbatim to the LLM.
var (
	// ErrNameRequired is returned when an operation needs a model name and got nothing.
	ErrNameRequired = errors.New("model name is required")
	// ErrNotFound is returned when the model name doesn't exist in the loader.
	ErrNotFound = errors.New("model configuration not found")
	// ErrConfigFileMissing is returned when the loader knows the model but its
	// config file is unset (in-memory-only model — shouldn't happen on disk).
	ErrConfigFileMissing = errors.New("model configuration file not found")
	// ErrPathNotTrusted is returned when utils.VerifyPath rejects a config path.
	ErrPathNotTrusted = errors.New("model configuration path not trusted")
	// ErrConflict is returned when a rename would clobber an existing model.
	ErrConflict = errors.New("a model with that name already exists")
	// ErrBadAction is returned when toggle/state actions are not in the allowed set.
	ErrBadAction = errors.New("invalid action")
	// ErrInvalidConfig is returned when the new YAML/JSON fails validation.
	ErrInvalidConfig = errors.New("invalid model configuration")
	// ErrEmptyBody is returned when the request body is empty.
	ErrEmptyBody = errors.New("request body is empty")
	// ErrPathSeparator is returned when a renamed model name contains path separators.
	ErrPathSeparator = errors.New("model name must not contain path separators")
)

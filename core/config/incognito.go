package config

import "github.com/mudler/LocalAI/core/privacy"

// IncognitoPolicy is the setting incognito: whether web UI sessions may start
// incognito conversations (privacy.PolicyAllow, the default) or not
// (privacy.PolicyForbid). A value that is not a policy reads as allow, the
// way an empty value does; the settings endpoint refuses such a value before
// it is stored.
func (o *ApplicationConfig) IncognitoPolicy() privacy.Policy {
	p, err := privacy.ParsePolicy(o.Incognito)
	if err != nil {
		return privacy.PolicyAllow
	}
	return p
}

// IncognitoMaxLevel is the setting incognito_max_level: the highest level
// the server honours. Empty or unknown is zero-retention.
func (o *ApplicationConfig) IncognitoMaxLevel() privacy.Level {
	l, err := privacy.ParseMaxLevel(o.IncognitoMaxLevelName)
	if err != nil {
		return privacy.ZeroRetention
	}
	return l
}

// ValidIncognito says whether v is a value of the setting incognito.
func ValidIncognito(v string) bool {
	_, err := privacy.ParsePolicy(v)
	return err == nil
}

// ValidIncognitoMaxLevel says whether v is a value of incognito_max_level.
func ValidIncognitoMaxLevel(v string) bool {
	_, err := privacy.ParseMaxLevel(v)
	return err == nil
}

// WithIncognito sets the incognito policy: allow or forbid.
func WithIncognito(policy string) AppOption {
	return func(o *ApplicationConfig) { o.Incognito = string(mustPolicy(policy)) }
}

// WithIncognitoMaxLevel sets the highest incognito level the server honours.
func WithIncognitoMaxLevel(level string) AppOption {
	return func(o *ApplicationConfig) {
		if l, err := privacy.ParseMaxLevel(level); err == nil {
			o.IncognitoMaxLevelName = l.String()
		}
	}
}

func mustPolicy(v string) privacy.Policy {
	p, err := privacy.ParsePolicy(v)
	if err != nil {
		return privacy.PolicyAllow
	}
	return p
}

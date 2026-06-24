package messaging

// Option configures NATS client connection behavior.
type Option func(*connectConfig)

// CredentialProvider returns the NATS user JWT and signing seed to use for the
// next (re)connect. It is consulted on every connection attempt, so a refresh
// loop can rotate credentials before they expire and the connection picks them
// up automatically when the server expires the old JWT and triggers a reconnect.
type CredentialProvider func() (jwt, seed string)

type connectConfig struct {
	userJWT     string
	userSeed    string
	jwtProvider CredentialProvider
	tls         TLSFiles
}

// WithUserJWT connects using a static NATS user JWT and signing seed (UserJWTAndSeed).
func WithUserJWT(jwt, seed string) Option {
	return func(c *connectConfig) {
		c.userJWT = jwt
		c.userSeed = seed
	}
}

// WithUserJWTProvider connects using credentials fetched from provider on each
// (re)connect, enabling JWT rotation without dropping the client. Takes
// precedence over WithUserJWT when both are set.
func WithUserJWTProvider(provider CredentialProvider) Option {
	return func(c *connectConfig) {
		c.jwtProvider = provider
	}
}

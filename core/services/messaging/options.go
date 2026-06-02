package messaging

// Option configures NATS client connection behavior.
type Option func(*connectConfig)

type connectConfig struct {
	userJWT  string
	userSeed string
	tls      TLSFiles
}

// WithUserJWT connects using a NATS user JWT and signing seed (UserJWTAndSeed).
func WithUserJWT(jwt, seed string) Option {
	return func(c *connectConfig) {
		c.userJWT = jwt
		c.userSeed = seed
	}
}
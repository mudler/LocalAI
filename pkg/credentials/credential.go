package credentials

// Kind is how a credential authenticates.
type Kind int

const (
	KindBasic Kind = iota + 1
	KindBearer
	KindHeader
)

func (k Kind) String() string {
	switch k {
	case KindBasic:
		return "basic"
	case KindBearer:
		return "bearer"
	case KindHeader:
		return "header"
	}
	return "unknown"
}

// LookupEnvFunc has the signature of os.LookupEnv. It is injected because
// packages under pkg/ must not read the process environment directly; the CLI
// layer passes os.LookupEnv.
type LookupEnvFunc func(string) (string, bool)

type secretRef struct {
	literal string
	env     string
	file    string
}

func (r secretRef) set() bool {
	return r.literal != "" || r.env != "" || r.file != ""
}

// Credential is one rule from the credentials file. Secret sources are
// unexported and resolved only when a request needs them.
type Credential struct {
	// Match is the rule's match string as written. It never holds secret
	// material, so it is what errors and logs use to name a rule.
	Match string
	Kind  Kind

	username    string
	password    secretRef
	bearer      secretRef
	headerName  string
	headerValue secretRef

	allowInsecure bool
	scheme        string
	host          string
	path          string
	lookupEnv     LookupEnvFunc
}

func (c Credential) warnIfUnresolved() {}

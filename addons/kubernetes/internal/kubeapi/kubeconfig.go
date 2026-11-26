// SPDX-License-Identifier: MIT
package kubeapi

import (
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Errors of a pasted kubeconfig. None of them holds a value of the kubeconfig.
var (
	ErrTooLarge         = fmt.Errorf("the kubeconfig is larger than %d bytes", contract.MaxCredentialBytes)
	ErrUnsafeKubeconfig = errors.New("the kubeconfig cannot be used")
	ErrNoContext        = errors.New("the kubeconfig has no context of the connection")
	ErrServerMismatch   = errors.New("the context of the kubeconfig points at another server than the connection")
)

// UserAgent is the user agent of every request.
const UserAgent = "localai-kubernetes"

func unsafeField(field, why string) error {
	return fmt.Errorf("%w: %s is not supported (%s)", ErrUnsafeKubeconfig, field, why)
}

// pinned is the one context of a kubeconfig, its cluster and its user.
type pinned struct {
	ctx     *clientcmdapi.Context
	cluster *clientcmdapi.Cluster
	user    *clientcmdapi.AuthInfo
}

func load(cred string) (*clientcmdapi.Config, error) {
	if len(cred) > contract.MaxCredentialBytes {
		return nil, ErrTooLarge
	}
	// Load only decodes: it resolves no path and runs no plugin.
	cfg, err := clientcmd.Load([]byte(cred))
	if err != nil {
		return nil, fmt.Errorf("%w: it is not a readable kubeconfig", ErrUnsafeKubeconfig)
	}
	return cfg, nil
}

// check reads one context and refuses anything that would make the add-on
// read a file on its host, run a program, talk through a proxy, skip TLS
// verification or send a password. It runs before any client-go call that
// could act on those fields.
func check(cfg *clientcmdapi.Config, name string) (pinned, string, error) {
	ctx, ok := cfg.Contexts[name]
	if !ok || ctx == nil {
		return pinned{}, "", ErrNoContext
	}
	cl, ok := cfg.Clusters[ctx.Cluster]
	if !ok || cl == nil {
		return pinned{}, "", fmt.Errorf("%w: the context names no cluster of the kubeconfig", ErrUnsafeKubeconfig)
	}
	user, ok := cfg.AuthInfos[ctx.AuthInfo]
	if !ok || user == nil {
		user = &clientcmdapi.AuthInfo{}
	}
	switch {
	case cl.CertificateAuthority != "":
		return pinned{}, "", unsafeField("certificate-authority", "embed it as certificate-authority-data")
	case cl.ProxyURL != "":
		return pinned{}, "", unsafeField("proxy-url", "the add-on connects directly")
	case cl.InsecureSkipTLSVerify:
		return pinned{}, "", unsafeField("insecure-skip-tls-verify", "the server certificate must be checked")
	case user.ClientCertificate != "":
		return pinned{}, "", unsafeField("client-certificate", "embed it as client-certificate-data")
	case user.ClientKey != "":
		return pinned{}, "", unsafeField("client-key", "embed it as client-key-data")
	case user.TokenFile != "":
		return pinned{}, "", unsafeField("tokenFile", "embed the token")
	case user.Exec != nil:
		return pinned{}, "", unsafeField("exec", "the add-on runs no credential plugin; use a token")
	case user.AuthProvider != nil:
		return pinned{}, "", unsafeField("auth-provider", "the add-on runs no credential plugin; use a token")
	case user.Username != "" || user.Password != "":
		return pinned{}, "", unsafeField("username", "use a token or a client certificate")
	}
	server, err := NormalizeServer(cl.Server)
	if err != nil {
		return pinned{}, "", fmt.Errorf("%w: %s", ErrUnsafeKubeconfig, err.Error())
	}
	return pinned{ctx: ctx, cluster: cl, user: user}, server, nil
}

// ParseKubeconfig builds the client config of the pinned context. The rest of
// the kubeconfig is not used, so another context cannot change the call.
func ParseKubeconfig(cred string, conn Conn) (*rest.Config, error) {
	cfg, err := load(cred)
	if err != nil {
		return nil, err
	}
	p, server, err := check(cfg, conn.Context)
	if err != nil {
		return nil, err
	}
	want, err := NormalizeServer(conn.Server)
	if err != nil || want != server {
		return nil, ErrServerMismatch
	}
	only := clientcmdapi.NewConfig()
	only.Clusters["c"] = p.cluster.DeepCopy()
	only.AuthInfos["u"] = p.user.DeepCopy()
	only.Contexts["x"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u", Namespace: p.ctx.Namespace}
	only.CurrentContext = "x"
	rc, err := clientcmd.NewNonInteractiveClientConfig(*only, "x", &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("%w: the context is incomplete", ErrUnsafeKubeconfig)
	}
	rc.UserAgent = UserAgent
	rc.Timeout = 25 * time.Second
	rc.QPS, rc.Burst = 20, 40
	return rc, nil
}

// Contexts lists the contexts of a kubeconfig that ParseKubeconfig accepts,
// each with its server.
func Contexts(cred string) []Conn {
	cfg, err := load(cred)
	if err != nil {
		return nil
	}
	var out []Conn
	for name := range cfg.Contexts {
		if !validContext(name) {
			continue
		}
		if _, server, err := check(cfg, name); err == nil {
			out = append(out, Conn{Server: server, Context: name})
		}
	}
	return out
}

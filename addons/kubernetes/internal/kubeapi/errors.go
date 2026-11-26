// SPDX-License-Identifier: MIT
package kubeapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const maxMessage = 2 << 10

// The fixed hints of a refusal of the connection's own credential. They hold
// nothing the cluster sent.
const (
	hintUnauthorized = "The cluster refused the credential of this connection (401). Tell the person to replace the kubeconfig in the connection settings. Do not retry, and do not ask for the credential."
	hintForbidden    = "The credential of this connection has no RBAC permission for this (403). Tell the person; an administrator of the cluster can grant it. Do not try another tool to get around it."
)

// Describe maps an error of the API server to what the model reads: the HTTP
// status, the reason and the message of the server, so it can act on them.
// Any other error passes through.
func Describe(err error) error {
	var st apierrors.APIStatus
	if err == nil || !errors.As(err, &st) {
		return err
	}
	s := st.Status()
	msg, _ := Cap(s.Message, maxMessage)
	out := fmt.Errorf("%w: %d %s: %s", protocol.ErrBadRequest, s.Code, s.Reason, msg)
	switch s.Code {
	case http.StatusUnauthorized:
		return protocol.Upstream(http.StatusUnauthorized, hintUnauthorized, out)
	case http.StatusForbidden:
		return protocol.Upstream(http.StatusForbidden, hintForbidden, out)
	}
	return out
}

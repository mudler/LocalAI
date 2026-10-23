// SPDX-License-Identifier: MIT

// Package hostclient is the add-on side of the host operation channel. An
// add-on process uses it to ask the host for an operation on behalf of a
// request it is serving.
package hostclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrNoHostOps says the host gave this add-on no host operation channel.
var ErrNoHostOps = errors.New("the host offers no host operations to this add-on")

// ErrBadAnswer says the host answered in a form the contract does not allow.
var ErrBadAnswer = errors.New("the host answered a host operation with an invalid response")

// Client calls host operations. A nil Client is valid and offers none.
type Client struct {
	endpoint, credential string
	max                  int64
	http                 *http.Client
}

// New returns nil when the startup holds no endpoint or an endpoint that is
// not http://127.0.0.1:<port>.
func New(startup protocol.Startup) *Client {
	u, err := url.Parse(startup.HostEndpoint)
	if err != nil || startup.HostEndpoint == "" || startup.HostCredential == "" || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() != "127.0.0.1" {
		return nil
	}
	if p, err := strconv.Atoi(u.Port()); err != nil || p < 1 || p > 65535 {
		return nil
	}
	max := startup.Limits.PayloadBytes
	if max <= 0 {
		max = contract.DefaultLimits().PayloadBytes
	}
	t := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, MaxResponseHeaderBytes: 16 << 10}
	return &Client{endpoint: startup.HostEndpoint, credential: startup.HostCredential, max: max, http: &http.Client{Transport: t}}
}

// Do calls op for the request operationID. args is marshalled to a JSON
// object (nil is an empty one); the result is decoded into out when out is
// not nil. A refusal of the host is returned as *contract.HostError.
func (c *Client) Do(ctx context.Context, operationID, op string, args, out any) error {
	if c == nil {
		return ErrNoHostOps
	}
	raw := json.RawMessage(`{}`)
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return err
		}
		raw = b
	}
	body, err := json.Marshal(contract.HostRequest{OperationID: operationID, Op: op, Arguments: raw})
	if err != nil {
		return err
	}
	if int64(len(body)) > c.max {
		return &contract.HostError{Code: contract.HostTooLarge, Message: "request too large"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/host", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(contract.HostCredentialHeader, c.credential)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.max+1))
	if err != nil || int64(len(b)) > c.max {
		return ErrBadAnswer
	}
	var r contract.HostResponse
	if json.Unmarshal(b, &r) != nil {
		return ErrBadAnswer
	}
	if r.Error != nil {
		return r.Error
	}
	if resp.StatusCode != http.StatusOK {
		return ErrBadAnswer
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

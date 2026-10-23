package utils

import (
	"net"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// ValidateExternalURL checks that the given URL does not point to a private,
// loopback, link-local, or otherwise internal network address. It is the
// policy implemented in pkg/httpclient, kept here for existing callers.
func ValidateExternalURL(rawURL string) error { return httpclient.ValidateExternalURL(rawURL) }

// IsPublicIP reports whether ip refers to a host on the public internet. See
// httpclient.IsPublicIP for the ranges that are refused.
func IsPublicIP(ip net.IP) bool { return httpclient.IsPublicIP(ip) }

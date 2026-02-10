package utils

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateExternalURL checks that the given URL does not point to a private,
// loopback, link-local, or otherwise internal network address. This prevents
// Server-Side Request Forgery (SSRF) attacks where a user-supplied URL could
// be used to probe internal services or cloud metadata endpoints.
func ValidateExternalURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme: %s", scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL has no hostname")
	}

	// Block well-known internal hostnames
	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("requests to internal hosts are not allowed")
	}

	// Block cloud metadata service hostnames
	if lower == "metadata.google.internal" || lower == "instance-data" {
		return fmt.Errorf("requests to cloud metadata services are not allowed")
	}

	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return fmt.Errorf("unable to parse resolved IP: %s", ipStr)
		}

		if !isPublicIP(ip) {
			return fmt.Errorf("requests to internal network addresses are not allowed")
		}
	}

	return nil
}

func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() {
		return false
	}

	// Block IPv4-mapped IPv6 addresses that wrap private IPv4
	if ip4 := ip.To4(); ip4 != nil {
		return !ip4.IsLoopback() &&
			!ip4.IsLinkLocalUnicast() &&
			!ip4.IsPrivate() &&
			!ip4.IsUnspecified()
	}

	return true
}

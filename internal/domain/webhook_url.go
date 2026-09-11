package domain

import (
	"net"
	"net/url"
	"strings"
)

// maxWebhookURLLen bounds the stored webhook address.
const maxWebhookURLLen = 1000

// validateWebhookURL rejects addresses that are unusable or point at infrastructure
// the platform must never be tricked into calling (SSRF).
//
// MVP simplification: only literal IP addresses are checked. Hostnames are trusted, because
// the demo partner is reachable as the docker-compose name "partner-restaurant". A production
// setup resolves the host and re-checks the address at dial time via a custom net.Dialer.
func validateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if len(raw) > maxWebhookURLLen {
		return ErrInvalidInput
	}

	u, err := url.Parse(raw)
	if err != nil {
		return ErrInvalidInput
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrInvalidInput
	}
	if u.Host == "" {
		return ErrInvalidInput
	}

	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return ErrInvalidInput
	}

	return nil
}

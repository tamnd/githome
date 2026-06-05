package webhook

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// errBlockedAddress is returned by the guarded dialer when a hook URL resolves
// to an address the policy forbids. It surfaces as the delivery's error and is
// recorded on the attempt, so an operator sees why a hook never fired.
var errBlockedAddress = errors.New("webhook: destination address is not allowed")

// ClientOptions configures the delivery HTTP client. Timeout bounds a single
// POST. Allow lists CIDR ranges that are permitted even though they would
// otherwise be blocked as private or loopback, the escape hatch an operator uses
// to deliver to an internal receiver on a trusted network (and the test harness
// uses to reach its loopback listener). InsecureSkipVerify mirrors a hook's
// insecure_ssl flag at the transport level.
type ClientOptions struct {
	Timeout time.Duration
	Allow   []netip.Prefix
}

// NewClient builds an HTTP client whose dialer refuses to connect to private,
// loopback, link-local, or unspecified addresses unless an Allow prefix covers
// them. The guard runs after DNS resolution on the address the dialer is about
// to connect to, so a public hostname that resolves to a private IP is still
// blocked, the rebinding case a URL-only check misses.
func NewClient(opts ClientOptions) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	allow := append([]netip.Prefix(nil), opts.Allow...)
	base := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if err := checkAddress(address, allow); err != nil {
				return nil, err
			}
			return base.DialContext(ctx, network, address)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// A webhook delivery does not chase redirects: the recorded result is the
		// first response the configured URL returns, the way GitHub reports it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// insecureTransport clones the guarded transport with certificate verification
// disabled, the behavior a hook with insecure_ssl set asks for. Cloning keeps
// the dialer guard while relaxing only TLS verification.
func insecureClient(c *http.Client) *http.Client {
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		return c
	}
	clone := tr.Clone()
	clone.TLSClientConfig.InsecureSkipVerify = true
	return &http.Client{Timeout: c.Timeout, Transport: clone, CheckRedirect: c.CheckRedirect}
}

// checkAddress parses the dialer's host:port target and rejects it when its IP
// is private, loopback, link-local, multicast, or unspecified and no Allow
// prefix covers it.
func checkAddress(address string, allow []netip.Prefix) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		// The dialer always hands a literal IP here; a non-literal means resolution
		// produced something unexpected, which the guard treats as blocked.
		return errBlockedAddress
	}
	addr = addr.Unmap()
	for _, p := range allow {
		if p.Contains(addr) {
			return nil
		}
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return errBlockedAddress
	}
	return nil
}

// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// httpTimeout bounds one request from dial to the last body byte.
	httpTimeout = 60 * time.Second
	// responseHeaderTimeout bounds the wait for the status line once the
	// request has been written, so a stalled origin fails before httpTimeout
	// and before the whole budget is spent.
	responseHeaderTimeout = 30 * time.Second
)

// httpClientSettings are the knobs a test turns on the production client. The
// zero value is production: the system trust roots and the timeouts above.
type httpClientSettings struct {
	// rootCAs replaces the system pool, so a test can trust an httptest TLS
	// server's certificate.
	rootCAs *x509.CertPool
	// timeout and responseHeaderTimeout replace the defaults when non-zero.
	timeout               time.Duration
	responseHeaderTimeout time.Duration
}

// newHTTPClient builds the one client every request leaves through: the
// admin client's, the SDK's, and the SDK's federation token exchange.
//
// Redirects are not followed. net/http would follow up to ten, https to http
// included. On a cross-host hop it drops only Authorization, Cookie and
// Proxy-*, and keeps even those for a subdomain of the origin; x-api-key and
// anthropic-beta go to whatever host the 3xx names, and a 307/308 replays the
// body, which for the token exchange is the identity token. With
// http.ErrUseLastResponse the 3xx is returned as the response instead, and
// the callers report it as an error naming the destination (refuseRedirect
// for the SDK, admin.Client for its own requests).
func newHTTPClient(s httpClientSettings) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = cmp.Or(s.responseHeaderTimeout, responseHeaderTimeout)
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: s.rootCAs}

	return &http.Client{
		Transport: transport,
		Timeout:   cmp.Or(s.timeout, httpTimeout),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// refuseRedirect is SDK middleware for the 3xx newHTTPClient hands back: it
// becomes an error naming the destination. Left alone, the SDK treats any
// status under 400 as success and reports a JSON decode failure instead. The
// response travels with the error so the SDK does not take it for a
// connection error.
func refuseRedirect(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if err != nil || res.StatusCode < 300 || res.StatusCode > 399 {
		return res, err
	}
	_ = res.Body.Close()
	return res, fmt.Errorf("%s %q: refused to follow the %d redirect to %q",
		req.Method, req.URL, res.StatusCode, res.Header.Get("Location"))
}

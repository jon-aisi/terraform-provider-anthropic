// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

func TestNewHTTPClientProductionSettings(t *testing.T) {
	hc := newHTTPClient(httpClientSettings{})

	if hc.Timeout != httpTimeout {
		t.Errorf("Timeout = %s, want %s", hc.Timeout, httpTimeout)
	}
	if err := hc.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect returned %v, want http.ErrUseLastResponse", err)
	}
	transport, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T, want *http.Transport", hc.Transport)
	}
	if transport.ResponseHeaderTimeout != responseHeaderTimeout {
		t.Errorf("ResponseHeaderTimeout = %s, want %s", transport.ResponseHeaderTimeout, responseHeaderTimeout)
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("TLSClientConfig = %+v, want MinVersion TLS 1.2", transport.TLSClientConfig)
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.RootCAs != nil {
		t.Error("production must use the system trust roots")
	}
	if transport.Proxy == nil {
		t.Error("the default transport's proxy-from-environment support was lost")
	}
}

// seenRequest is one request that reached the redirect target.
type seenRequest struct {
	method, path string
	header       http.Header
}

// victim is the host a redirect points at. Nothing may arrive there; whatever
// does is recorded so the failure names the headers that travelled.
type victim struct {
	*httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

func newVictim(t *testing.T) *victim {
	t.Helper()
	v := &victim{}
	v.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v.mu.Lock()
		v.seen = append(v.seen, seenRequest{r.Method, r.URL.Path, r.Header.Clone()})
		v.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stolen","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(v.Close)
	return v
}

func (v *victim) snapshot() []seenRequest {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]seenRequest(nil), v.seen...)
}

// redirectingServer answers every request with a 307 to the same path on
// target: the status that makes net/http replay the request body.
func redirectingServer(t *testing.T, target string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStockClientFollowsTheRedirect is the control for
// TestHTTPClientRefusesRedirects: with net/http defaults the same 307 is
// followed and the admin key arrives at the second host.
func TestStockClientFollowsTheRedirect(t *testing.T) {
	v := newVictim(t)
	origin := redirectingServer(t, v.URL)

	c := &admin.Client{ApiKey: "sk-ant-admin03-x", BaseURL: origin.URL, HTTPClient: origin.Client()}
	if _, err := c.DoRequest(context.Background(), http.MethodPost, "/v1/organizations/workspaces", map[string]string{"name": "x"}); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	seen := v.snapshot()
	if len(seen) != 1 || seen[0].header.Get("X-Api-Key") != "sk-ant-admin03-x" {
		t.Fatalf("second host saw %+v, want one request carrying the admin key", seen)
	}
}

// TestHTTPClientRefusesRedirects: a 307 from the origin is followed by none
// of the three request paths. Followed, it would carry x-api-key or the
// bearer to the new host and replay the body, which for the token exchange
// is the identity token.
func TestHTTPClientRefusesRedirects(t *testing.T) {
	const (
		adminKey  = "sk-ant-admin03-x"
		authToken = "sk-ant-oat01-x"
		identity  = "jwt-A"
	)
	tests := []struct {
		name      string
		attrs     func(t *testing.T) map[string]tftypes.Value
		request   func(pd *providerdata.ProviderData) error
		wantInErr string
	}{
		{
			name: "admin client",
			attrs: func(*testing.T) map[string]tftypes.Value {
				return map[string]tftypes.Value{"admin_api_key": str(adminKey)}
			},
			request: func(pd *providerdata.ProviderData) error {
				_, err := pd.AdminClient.DoRequest(context.Background(), http.MethodPost, "/v1/organizations/workspaces", map[string]string{"name": "x"})
				return err
			},
			wantInErr: "refused to follow the redirect to",
		},
		{
			name: "sdk client",
			attrs: func(*testing.T) map[string]tftypes.Value {
				return map[string]tftypes.Value{"auth_token": str(authToken)}
			},
			request: func(pd *providerdata.ProviderData) error {
				return pd.OAuthClient.Post(context.Background(), "/v1/organizations/service_accounts", map[string]any{"name": "x"}, nil)
			},
			wantInErr: "refused to follow the 307 redirect to",
		},
		{
			name: "token exchange",
			attrs: func(t *testing.T) map[string]tftypes.Value {
				return federationAttrs(writeIdentityToken(t, identity))
			},
			request: func(pd *providerdata.ProviderData) error {
				return pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil)
			},
			wantInErr: "307",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := newVictim(t)
			origin := redirectingServer(t, v.URL)
			clearCredentialEnv(t)
			attrs := tc.attrs(t)
			attrs["base_url"] = str(origin.URL)
			pd := providerDataFrom(t, configureProviderSettings(t, trust(t, origin, v), attrs))

			err := tc.request(pd)
			if err == nil {
				t.Fatal("request succeeded: the redirect was followed")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("err = %q, want it to contain %q", err, tc.wantInErr)
			}
			for _, secret := range []string{adminKey, authToken, identity} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("err = %q carries a credential", err)
				}
			}
			if seen := v.snapshot(); len(seen) != 0 {
				t.Errorf("%d request(s) followed the redirect to the second host: %+v", len(seen), seen)
			}
		})
	}
}

// TestHTTPClientTimesOutOnAStalledServer: a stalled origin fails within the
// client's own budget, long before the stall ends and without a deadline on
// the request context, for both clients and whether the stall is before the
// status line or in the body.
func TestHTTPClientTimesOutOnAStalledServer(t *testing.T) {
	const stall = 10 * time.Second
	done := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Consuming the body lets the server notice the client hanging up
		// and cancel r.Context(); an unread body leaves the handler blind.
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/stall-body" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
		}
		select {
		case <-r.Context().Done():
		case <-done:
		case <-time.After(stall):
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) }) // runs before srv.Close, releasing any handler still stalled

	settings := trust(t, srv)
	settings.timeout = 500 * time.Millisecond
	settings.responseHeaderTimeout = 200 * time.Millisecond

	clearCredentialEnv(t)
	pd := providerDataFrom(t, configureProviderSettings(t, settings, map[string]tftypes.Value{
		"base_url":      str(srv.URL),
		"admin_api_key": str("sk-ant-admin03-x"),
		"auth_token":    str("sk-ant-oat01-x"),
	}))

	requests := map[string]func(path string) error{
		// POST: the admin client does not retry a non-idempotent request that
		// got no response, so one attempt is timed.
		"admin client": func(path string) error {
			_, err := pd.AdminClient.DoRequest(context.Background(), http.MethodPost, path, map[string]string{})
			return err
		},
		// A destination makes the SDK read the body; with none it returns
		// as soon as the headers arrive.
		"sdk client": func(path string) error {
			var out map[string]any
			return pd.OAuthClient.Get(context.Background(), path, nil, &out)
		},
	}
	for name, request := range requests {
		for _, path := range []string{"/stall-headers", "/stall-body"} {
			t.Run(name+" "+strings.TrimPrefix(path, "/"), func(t *testing.T) {
				start := time.Now()
				err := request(path)
				elapsed := time.Since(start)
				if err == nil {
					t.Fatal("request succeeded against a stalled server")
				}
				if !isTimeout(err) {
					t.Errorf("err = %v, want a timeout", err)
				}
				if elapsed >= stall/2 {
					t.Errorf("request took %s, want the client's timeout to cut it short", elapsed)
				}
			})
		}
	}
}

// isTimeout reports whether err is a net/http client timeout, which surfaces
// as a net.Error with Timeout() true before the status line and as
// context.DeadlineExceeded while reading the body.
func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

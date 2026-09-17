// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// sdkError performs one SDK request against a server answering with status
// and body, and returns the error the SDK produced.
func sdkError(t *testing.T, status int, body string) error {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "req_123")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	client := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithAuthToken("sk-ant-oat01-x"),
		option.WithMaxRetries(0),
	)
	err := client.Get(context.Background(), "/v1/organizations/service_accounts", nil, nil)
	if err == nil {
		t.Fatal("expected the SDK to return an error")
	}
	return err
}

func TestDetailCapsTheSDKResponseBody(t *testing.T) {
	body := `{"type":"error","error":{"type":"api_error","message":"` + strings.Repeat("x", 2048) + `"}}`
	err := sdkError(t, http.StatusInternalServerError, body)
	if !strings.Contains(err.Error(), body) {
		t.Fatalf("precondition: the SDK error no longer echoes the raw body: %q", err)
	}

	got := Detail(fmt.Errorf("Unable to read service account: %w", err))

	for _, want := range []string{
		"Unable to read service account: ",
		`"` + "https://",
		"500 Internal Server Error",
		"Request-ID: req_123",
		body[:admin.MaxErrorBodyBytes] + "... [",
		"more bytes truncated]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Detail = %q\n does not contain %q", got, want)
		}
	}
	if strings.Contains(got, body) {
		t.Error("Detail still carries the whole body")
	}
	if overhead := len(got) - (len(err.Error()) - len(body)); overhead > admin.MaxErrorBodyBytes+64 {
		t.Errorf("Detail spends %d bytes on the body, want about %d", overhead, admin.MaxErrorBodyBytes)
	}
}

func TestDetailLeavesOtherErrorsAlone(t *testing.T) {
	short := sdkError(t, http.StatusNotFound, `{"type":"error","error":{"type":"not_found_error","message":"no such service account"}}`)

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"plain error", stderrors.New("dial tcp: connection refused"), "dial tcp: connection refused"},
		{"admin APIError", &admin.APIError{StatusCode: 404, ErrType: "not_found", Message: "gone"}, "API error (404 not_found): gone"},
		{"SDK error with a short body", short, short.Error()},
		{"wrapped SDK error with a short body", fmt.Errorf("Unable to read: %w", short), "Unable to read: " + short.Error()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detail(tc.err); got != tc.want {
				t.Errorf("Detail = %q, want %q", got, tc.want)
			}
		})
	}
}

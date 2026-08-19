package adminbff_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/adminbff"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestSessionAuthenticatorRequiresBoundCSRFOnWrites(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	key, csrfKey := make([]byte, 32), make([]byte, 32)
	for index := range key {
		key[index], csrfKey[index] = 1, 2
	}
	codec, _ := platformauth.NewSessionCodec("a", map[string][]byte{"a": key}, func() time.Time { return now })
	cookie, _ := codec.Seal(platformauth.Session{SessionID: "session", Subject: "admin", DisplayName: "Admin", Roles: []string{"CONFIG_ADMIN"}, ExpiresAt: now.Add(time.Minute)})
	csrf, _ := platformauth.CSRFToken(csrfKey, "session")
	authenticator, err := adminbff.NewSessionAuthenticator(adminbff.SessionAuthConfig{Codec: codec, CSRFKey: csrfKey, AllowedOrigins: []string{"https://admin.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://admin.example.com/api/v1/models", nil)
	request.AddCookie(&http.Cookie{Name: "finconfig_session", Value: cookie})
	request.AddCookie(&http.Cookie{Name: "finconfig_csrf", Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Origin", "https://admin.example.com")
	principal, err := authenticator.Authenticate(request)
	if err != nil || principal.Subject != "admin" {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
	request.Header.Set("Origin", "https://evil.example.com")
	if _, err := authenticator.Authenticate(request); err == nil {
		t.Fatal("write with hostile origin authenticated")
	}
	read := httptest.NewRequest(http.MethodGet, "https://admin.example.com/api/v1/models", nil)
	read.AddCookie(&http.Cookie{Name: "finconfig_session", Value: cookie})
	if _, err := authenticator.Authenticate(read); err != nil {
		t.Fatalf("safe read requires CSRF: %v", err)
	}
}

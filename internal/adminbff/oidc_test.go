package adminbff_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/adminbff"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestOIDCLoginCallbackSessionAndLogout(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	key, csrfKey, stateKey := bytesOf(1), bytesOf(2), bytesOf(3)
	codec, _ := platformauth.NewSessionCodec("session-key", map[string][]byte{"session-key": key}, func() time.Time { return now })
	sessionAuth, _ := adminbff.NewSessionAuthenticator(adminbff.SessionAuthConfig{Codec: codec, CSRFKey: csrfKey, AllowedOrigins: []string{"https://admin.example.com"}})
	exchanger := &oidcExchangeStub{}
	flow, err := adminbff.NewOIDCFlow(adminbff.OIDCConfig{
		AuthorizationEndpoint: "https://identity.example.com/authorize",
		TokenEndpoint:         "https://identity.example.com/token",
		ClientID:              "finconfig",
		ClientSecret:          "client-secret",
		RedirectURI:           "https://admin.example.com/api/v1/auth/callback",
		SuccessRedirect:       "/collections",
		StateKey:              stateKey,
		SessionDuration:       30 * time.Minute,
		TransactionTTL:        5 * time.Minute,
	}, sessionAuth, exchanger, oidcVerifierStub{}, func(identity platformauth.OIDCIdentity) (adminbff.Principal, error) {
		return adminbff.Principal{Subject: identity.Subject, DisplayName: identity.DisplayName, Roles: []string{"CONFIG_ADMIN"}}, nil
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	login := httptest.NewRecorder()
	flow.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "https://admin.example.com/api/v1/auth/login", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login=%d %s", login.Code, login.Body.String())
	}
	if login.Header().Get("Content-Security-Policy") == "" || login.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("browser security headers missing: %+v", login.Header())
	}
	location, _ := url.Parse(login.Header().Get("Location"))
	if location.Host != "identity.example.com" || location.Query().Get("code_challenge_method") != "S256" || location.Query().Get("nonce") == "" || location.Query().Get("state") == "" {
		t.Fatalf("authorization redirect=%s", location)
	}
	transactionCookie := findCookie(t, login.Result().Cookies(), "finconfig_oidc")
	callbackURL := "https://admin.example.com/api/v1/auth/callback?code=authorization-code&state=" + url.QueryEscape(location.Query().Get("state"))
	callbackRequest := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callbackRequest.AddCookie(transactionCookie)
	callback := httptest.NewRecorder()
	flow.ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/collections" || exchanger.verifier == "" {
		t.Fatalf("callback=%d location=%q verifier=%q body=%s", callback.Code, callback.Header().Get("Location"), exchanger.verifier, callback.Body.String())
	}
	sessionCookie := findCookie(t, callback.Result().Cookies(), "finconfig_session")
	csrfCookie := findCookie(t, callback.Result().Cookies(), "finconfig_csrf")
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || csrfCookie.HttpOnly || !csrfCookie.Secure {
		t.Fatalf("unsafe cookies: session=%+v csrf=%+v", sessionCookie, csrfCookie)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "https://admin.example.com/api/v1/session", nil)
	sessionRequest.AddCookie(sessionCookie)
	sessionResponse := httptest.NewRecorder()
	flow.ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK || !strings.Contains(sessionResponse.Body.String(), `"subject":"user-a"`) {
		t.Fatalf("session=%d %s", sessionResponse.Code, sessionResponse.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "https://admin.example.com/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(sessionCookie)
	logoutRequest.AddCookie(csrfCookie)
	logoutRequest.Header.Set("Origin", "https://admin.example.com")
	logoutRequest.Header.Set("X-CSRF-Token", csrfCookie.Value)
	logoutResponse := httptest.NewRecorder()
	flow.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent || findCookie(t, logoutResponse.Result().Cookies(), "finconfig_session").MaxAge != -1 {
		t.Fatalf("logout=%d cookies=%+v", logoutResponse.Code, logoutResponse.Result().Cookies())
	}
}

func TestOIDCCallbackRejectsStateSubstitution(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	codec, _ := platformauth.NewSessionCodec("key", map[string][]byte{"key": bytesOf(1)}, func() time.Time { return now })
	sessionAuth, _ := adminbff.NewSessionAuthenticator(adminbff.SessionAuthConfig{Codec: codec, CSRFKey: bytesOf(2), AllowedOrigins: []string{"https://admin.example.com"}})
	flow, _ := adminbff.NewOIDCFlow(adminbff.OIDCConfig{AuthorizationEndpoint: "https://id.example.com/auth", TokenEndpoint: "https://id.example.com/token", ClientID: "client", ClientSecret: "secret", RedirectURI: "https://admin.example.com/api/v1/auth/callback", SuccessRedirect: "/", StateKey: bytesOf(3), SessionDuration: time.Minute, TransactionTTL: time.Minute}, sessionAuth, &oidcExchangeStub{}, oidcVerifierStub{}, func(identity platformauth.OIDCIdentity) (adminbff.Principal, error) {
		return adminbff.Principal{Subject: identity.Subject}, nil
	}, func() time.Time { return now })
	login := httptest.NewRecorder()
	flow.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "https://admin.example.com/api/v1/auth/login", nil))
	request := httptest.NewRequest(http.MethodGet, "https://admin.example.com/api/v1/auth/callback?code=code&state=attacker", nil)
	request.AddCookie(findCookie(t, login.Result().Cookies(), "finconfig_oidc"))
	response := httptest.NewRecorder()
	flow.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("state substitution status=%d", response.Code)
	}
}

type oidcExchangeStub struct{ verifier string }

func (stub *oidcExchangeStub) Exchange(_ context.Context, _ string, _ string, _ string, verifier string) (string, error) {
	stub.verifier = verifier
	return "id-token", nil
}

type oidcVerifierStub struct{}

func (oidcVerifierStub) VerifyIDToken(_ context.Context, _ string, nonce string) (platformauth.OIDCIdentity, error) {
	return platformauth.OIDCIdentity{Subject: "user-a", DisplayName: "User A", Nonce: nonce}, nil
}

func bytesOf(value byte) []byte {
	result := make([]byte, 32)
	for index := range result {
		result[index] = value
	}
	return result
}

func findCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %s not found in %+v", name, cookies)
	return nil
}

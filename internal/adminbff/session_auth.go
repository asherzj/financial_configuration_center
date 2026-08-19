package adminbff

import (
	"errors"
	"net/http"
	"strings"
	"time"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

type SessionAuthenticator struct {
	codec          *platformauth.SessionCodec
	cookieName     string
	csrfCookieName string
	csrfHeaderName string
	csrfKey        []byte
	allowedOrigins []string
}

func (authenticator *SessionAuthenticator) Issue(writer http.ResponseWriter, session platformauth.Session, maxAge time.Duration) error {
	if authenticator == nil || maxAge <= 0 {
		return errors.New("session authenticator and positive max age are required")
	}
	sealed, err := authenticator.codec.Seal(session)
	if err != nil {
		return err
	}
	csrf, err := platformauth.CSRFToken(authenticator.csrfKey, session.SessionID)
	if err != nil {
		return err
	}
	seconds := int(maxAge / time.Second)
	http.SetCookie(writer, &http.Cookie{Name: authenticator.cookieName, Value: sealed, Path: "/", MaxAge: seconds, Expires: session.ExpiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.SetCookie(writer, &http.Cookie{Name: authenticator.csrfCookieName, Value: csrf, Path: "/", MaxAge: seconds, Expires: session.ExpiresAt, Secure: true, HttpOnly: false, SameSite: http.SameSiteStrictMode})
	return nil
}

func (authenticator *SessionAuthenticator) Clear(writer http.ResponseWriter) {
	if authenticator == nil {
		return
	}
	for _, cookie := range []http.Cookie{
		{Name: authenticator.cookieName, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode},
		{Name: authenticator.csrfCookieName, Path: "/", Secure: true, SameSite: http.SameSiteStrictMode},
	} {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
		http.SetCookie(writer, &cookie)
	}
}

type SessionAuthConfig struct {
	Codec          *platformauth.SessionCodec
	CookieName     string
	CSRFCookieName string
	CSRFHeaderName string
	CSRFKey        []byte
	AllowedOrigins []string
}

func NewSessionAuthenticator(config SessionAuthConfig) (*SessionAuthenticator, error) {
	if config.Codec == nil || len(config.CSRFKey) < 32 || len(config.AllowedOrigins) == 0 {
		return nil, errors.New("session authenticator requires codec, CSRF key, and allowed origins")
	}
	if config.CookieName == "" {
		config.CookieName = "finconfig_session"
	}
	if config.CSRFCookieName == "" {
		config.CSRFCookieName = "finconfig_csrf"
	}
	if config.CSRFHeaderName == "" {
		config.CSRFHeaderName = "X-CSRF-Token"
	}
	return &SessionAuthenticator{codec: config.Codec, cookieName: config.CookieName, csrfCookieName: config.CSRFCookieName, csrfHeaderName: config.CSRFHeaderName, csrfKey: append([]byte(nil), config.CSRFKey...), allowedOrigins: append([]string(nil), config.AllowedOrigins...)}, nil
}

func (authenticator *SessionAuthenticator) Authenticate(request *http.Request) (Principal, error) {
	cookie, err := request.Cookie(authenticator.cookieName)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	session, err := authenticator.codec.Open(cookie.Value)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	if unsafeMethod(request.Method) {
		csrfCookie, err := request.Cookie(authenticator.csrfCookieName)
		if err != nil || platformauth.ValidateCSRF(authenticator.csrfKey, session.SessionID, cookieValue(csrfCookie), strings.TrimSpace(request.Header.Get(authenticator.csrfHeaderName)), strings.TrimSpace(request.Header.Get("Origin")), authenticator.allowedOrigins) != nil {
			return Principal{}, ErrUnauthenticated
		}
	}
	return Principal{Subject: session.Subject, DisplayName: session.DisplayName, Roles: append([]string(nil), session.Roles...), AllowedScopes: append([]platformauth.ScopePattern(nil), session.Scopes...)}, nil
}

func unsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func cookieValue(cookie *http.Cookie) string {
	if cookie == nil {
		return ""
	}
	return cookie.Value
}

var _ Authenticator = (*SessionAuthenticator)(nil)

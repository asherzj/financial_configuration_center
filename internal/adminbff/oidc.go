package adminbff

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

type OIDCConfig struct {
	AuthorizationEndpoint string
	TokenEndpoint         string
	ClientID              string
	ClientSecret          string
	RedirectURI           string
	SuccessRedirect       string
	StateKey              []byte
	SessionDuration       time.Duration
	TransactionTTL        time.Duration
	Scopes                []string
	TransactionCookieName string
}

type OIDCTokenExchanger interface {
	Exchange(context.Context, string, string, string, string) (string, error)
}

type OIDCIDTokenVerifier interface {
	VerifyIDToken(context.Context, string, string) (platformauth.OIDCIdentity, error)
}

type OIDCPrincipalMapper func(platformauth.OIDCIdentity) (Principal, error)

type OIDCFlow struct {
	config       OIDCConfig
	sessions     *SessionAuthenticator
	exchanger    OIDCTokenExchanger
	verifier     OIDCIDTokenVerifier
	mapPrincipal OIDCPrincipalMapper
	clock        func() time.Time
	stateAEAD    cipher.AEAD
	mux          *http.ServeMux
}

type loginTransaction struct {
	State        string    `json:"state"`
	Nonce        string    `json:"nonce"`
	CodeVerifier string    `json:"codeVerifier"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func NewOIDCFlow(config OIDCConfig, sessions *SessionAuthenticator, exchanger OIDCTokenExchanger, verifier OIDCIDTokenVerifier, mapper OIDCPrincipalMapper, clock func() time.Time) (*OIDCFlow, error) {
	if sessions == nil || exchanger == nil || verifier == nil || mapper == nil || clock == nil {
		return nil, errors.New("OIDC flow dependencies are required")
	}
	if len(config.StateKey) != 32 || config.ClientID == "" || config.ClientSecret == "" || config.SessionDuration <= 0 || config.TransactionTTL <= 0 {
		return nil, errors.New("OIDC client credentials, state key, and durations are required")
	}
	for _, endpoint := range []string{config.AuthorizationEndpoint, config.TokenEndpoint, config.RedirectURI} {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Fragment != "" {
			return nil, errors.New("OIDC endpoints and redirect URI must be absolute HTTPS URLs")
		}
	}
	if config.SuccessRedirect == "" || !strings.HasPrefix(config.SuccessRedirect, "/") || strings.HasPrefix(config.SuccessRedirect, "//") {
		return nil, errors.New("OIDC success redirect must be a local absolute path")
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"openid", "profile"}
	}
	if !containsString(config.Scopes, "openid") {
		return nil, errors.New("OIDC scopes must include openid")
	}
	if config.TransactionCookieName == "" {
		config.TransactionCookieName = "finconfig_oidc"
	}
	block, err := aes.NewCipher(config.StateKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	flow := &OIDCFlow{config: config, sessions: sessions, exchanger: exchanger, verifier: verifier, mapPrincipal: mapper, clock: clock, stateAEAD: aead, mux: http.NewServeMux()}
	flow.mux.HandleFunc("GET /api/v1/auth/login", flow.login)
	flow.mux.HandleFunc("GET /api/v1/auth/callback", flow.callback)
	flow.mux.HandleFunc("POST /api/v1/auth/logout", flow.logout)
	flow.mux.HandleFunc("GET /api/v1/session", flow.session)
	return flow, nil
}

func (flow *OIDCFlow) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	SecurityHeaders(flow.mux).ServeHTTP(writer, request)
}

func (flow *OIDCFlow) login(writer http.ResponseWriter, request *http.Request) {
	state, err := randomURLString(32)
	if err != nil {
		writeOIDCError(writer, http.StatusInternalServerError, "OIDC_LOGIN_FAILED")
		return
	}
	nonce, err := randomURLString(32)
	if err != nil {
		writeOIDCError(writer, http.StatusInternalServerError, "OIDC_LOGIN_FAILED")
		return
	}
	verifier, err := randomURLString(48)
	if err != nil {
		writeOIDCError(writer, http.StatusInternalServerError, "OIDC_LOGIN_FAILED")
		return
	}
	transaction := loginTransaction{State: state, Nonce: nonce, CodeVerifier: verifier, ExpiresAt: flow.clock().Add(flow.config.TransactionTTL)}
	sealed, err := flow.sealTransaction(transaction)
	if err != nil {
		writeOIDCError(writer, http.StatusInternalServerError, "OIDC_LOGIN_FAILED")
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: flow.config.TransactionCookieName, Value: sealed, Path: "/api/v1/auth/callback", MaxAge: int(flow.config.TransactionTTL / time.Second), Expires: transaction.ExpiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	digest := sha256.Sum256([]byte(verifier))
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {flow.config.ClientID},
		"redirect_uri":          {flow.config.RedirectURI},
		"scope":                 {strings.Join(flow.config.Scopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(digest[:])},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(writer, request, flow.config.AuthorizationEndpoint+"?"+values.Encode(), http.StatusFound)
}

func (flow *OIDCFlow) callback(writer http.ResponseWriter, request *http.Request) {
	flow.clearTransaction(writer)
	if request.URL.Query().Get("error") != "" {
		writeOIDCError(writer, http.StatusUnauthorized, "OIDC_AUTHORIZATION_REJECTED")
		return
	}
	cookie, err := request.Cookie(flow.config.TransactionCookieName)
	if err != nil {
		writeOIDCError(writer, http.StatusBadRequest, "OIDC_STATE_INVALID")
		return
	}
	transaction, err := flow.openTransaction(cookie.Value)
	if err != nil || !transaction.ExpiresAt.After(flow.clock()) || subtle.ConstantTimeCompare([]byte(transaction.State), []byte(request.URL.Query().Get("state"))) != 1 {
		writeOIDCError(writer, http.StatusBadRequest, "OIDC_STATE_INVALID")
		return
	}
	code := strings.TrimSpace(request.URL.Query().Get("code"))
	if code == "" {
		writeOIDCError(writer, http.StatusBadRequest, "OIDC_CODE_REQUIRED")
		return
	}
	idToken, err := flow.exchanger.Exchange(request.Context(), flow.config.TokenEndpoint, code, flow.config.RedirectURI, transaction.CodeVerifier)
	if err != nil {
		writeOIDCError(writer, http.StatusBadGateway, "OIDC_TOKEN_EXCHANGE_FAILED")
		return
	}
	identity, err := flow.verifier.VerifyIDToken(request.Context(), idToken, transaction.Nonce)
	if err != nil {
		writeOIDCError(writer, http.StatusUnauthorized, "OIDC_ID_TOKEN_INVALID")
		return
	}
	principal, err := flow.mapPrincipal(identity)
	if err != nil || principal.Subject == "" {
		writeOIDCError(writer, http.StatusForbidden, "OIDC_PRINCIPAL_REJECTED")
		return
	}
	sessionID, err := randomURLString(32)
	if err != nil {
		writeOIDCError(writer, http.StatusInternalServerError, "OIDC_SESSION_FAILED")
		return
	}
	now := flow.clock()
	authTime := identity.AuthTime
	if authTime.IsZero() {
		authTime = now
	}
	if err := flow.sessions.Issue(writer, platformauth.Session{SessionID: sessionID, Subject: principal.Subject, DisplayName: principal.DisplayName, Roles: append([]string(nil), principal.Roles...), Scopes: append([]platformauth.ScopePattern(nil), principal.AllowedScopes...), AuthTime: authTime, ExpiresAt: now.Add(flow.config.SessionDuration)}, flow.config.SessionDuration); err != nil {
		writeOIDCError(writer, http.StatusInternalServerError, "OIDC_SESSION_FAILED")
		return
	}
	http.Redirect(writer, request, flow.config.SuccessRedirect, http.StatusFound)
}

func (flow *OIDCFlow) logout(writer http.ResponseWriter, request *http.Request) {
	if _, err := flow.sessions.Authenticate(request); err != nil {
		writeOIDCError(writer, http.StatusUnauthorized, "UNAUTHENTICATED")
		return
	}
	flow.sessions.Clear(writer)
	writer.WriteHeader(http.StatusNoContent)
}

func (flow *OIDCFlow) session(writer http.ResponseWriter, request *http.Request) {
	principal, err := flow.sessions.Authenticate(request)
	if err != nil {
		writeOIDCError(writer, http.StatusUnauthorized, "UNAUTHENTICATED")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{"subject": principal.Subject, "displayName": principal.DisplayName, "roles": principal.Roles, "allowedScopes": principal.AllowedScopes, "featureFlags": map[string]bool{}})
}

func (flow *OIDCFlow) sealTransaction(transaction loginTransaction) (string, error) {
	plaintext, err := json.Marshal(transaction)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, flow.stateAEAD.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(nonce, flow.stateAEAD.Seal(nil, nonce, plaintext, nil)...)), nil
}

func (flow *OIDCFlow) openTransaction(value string) (loginTransaction, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(data) < flow.stateAEAD.NonceSize() {
		return loginTransaction{}, errors.New("invalid OIDC transaction")
	}
	plaintext, err := flow.stateAEAD.Open(nil, data[:flow.stateAEAD.NonceSize()], data[flow.stateAEAD.NonceSize():], nil)
	if err != nil {
		return loginTransaction{}, errors.New("invalid OIDC transaction")
	}
	var transaction loginTransaction
	if json.Unmarshal(plaintext, &transaction) != nil || transaction.State == "" || transaction.Nonce == "" || transaction.CodeVerifier == "" {
		return loginTransaction{}, errors.New("invalid OIDC transaction")
	}
	return transaction, nil
}

func (flow *OIDCFlow) clearTransaction(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{Name: flow.config.TransactionCookieName, Path: "/api/v1/auth/callback", MaxAge: -1, Expires: time.Unix(1, 0), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func randomURLString(size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func writeOIDCError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"code": code, "message": "authentication request failed"})
}

type HTTPTokenExchanger struct {
	client       *http.Client
	clientID     string
	clientSecret string
}

func NewHTTPTokenExchanger(client *http.Client, clientID, clientSecret string) (*HTTPTokenExchanger, error) {
	if client == nil || clientID == "" || clientSecret == "" {
		return nil, errors.New("OIDC HTTP exchanger requires client credentials")
	}
	return &HTTPTokenExchanger{client: client, clientID: clientID, clientSecret: clientSecret}, nil
}

func (exchanger *HTTPTokenExchanger) Exchange(ctx context.Context, endpoint, code, redirectURI, verifier string) (string, error) {
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(exchanger.clientID, exchanger.clientSecret)
	response, err := exchanger.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("OIDC token endpoint rejected request")
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if decoder.Decode(&body) != nil || body.IDToken == "" {
		return "", errors.New("OIDC token response is invalid")
	}
	return body.IDToken, nil
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(writer, request)
	})
}

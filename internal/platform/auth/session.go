package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

type Session struct {
	SessionID   string         `json:"sessionId"`
	Subject     string         `json:"subject"`
	DisplayName string         `json:"displayName"`
	Roles       []string       `json:"roles"`
	Scopes      []ScopePattern `json:"scopes"`
	AuthTime    time.Time      `json:"authTime"`
	ExpiresAt   time.Time      `json:"expiresAt"`
}

type SessionCodec struct {
	currentKeyID string
	keys         map[string][]byte
	clock        func() time.Time
	random       io.Reader
}

func NewSessionCodec(currentKeyID string, keys map[string][]byte, clock func() time.Time) (*SessionCodec, error) {
	if currentKeyID == "" || len(keys[currentKeyID]) != 32 || clock == nil {
		return nil, errors.New("session codec requires a current 32-byte key and clock")
	}
	cloned := make(map[string][]byte, len(keys))
	for id, key := range keys {
		if id == "" || len(key) != 32 {
			return nil, errors.New("every session key must have an ID and be 32 bytes")
		}
		cloned[id] = append([]byte(nil), key...)
	}
	return &SessionCodec{currentKeyID: currentKeyID, keys: cloned, clock: clock, random: rand.Reader}, nil
}

func (codec *SessionCodec) Seal(session Session) (string, error) {
	if session.SessionID == "" || session.Subject == "" || !session.ExpiresAt.After(codec.clock()) {
		return "", errors.New("session identity and future expiry are required")
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	aead, err := sessionAEAD(codec.keys[codec.currentKeyID])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(codec.random, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, encoded, []byte(codec.currentKeyID))
	return codec.currentKeyID + "." + base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func (codec *SessionCodec) Open(value string) (Session, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return Session{}, ErrTokenInvalid
	}
	key := codec.keys[parts[0]]
	aead, err := sessionAEAD(key)
	if err != nil {
		return Session{}, ErrTokenInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(data) < aead.NonceSize() {
		return Session{}, ErrTokenInvalid
	}
	plaintext, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(parts[0]))
	if err != nil {
		return Session{}, ErrTokenInvalid
	}
	var session Session
	if err := json.Unmarshal(plaintext, &session); err != nil || session.SessionID == "" || session.Subject == "" {
		return Session{}, ErrTokenInvalid
	}
	if !session.ExpiresAt.After(codec.clock()) {
		return Session{}, ErrTokenExpired
	}
	return session, nil
}

func sessionAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid session key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func CSRFToken(key []byte, sessionID string) (string, error) {
	if len(key) < 32 || sessionID == "" {
		return "", errors.New("CSRF key and session ID are required")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func ValidateCSRF(key []byte, sessionID, cookieToken, headerToken, origin string, allowedOrigins []string) error {
	expected, err := CSRFToken(key, sessionID)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 || subtle.ConstantTimeCompare([]byte(expected), []byte(headerToken)) != 1 {
		return errors.New("CSRF token mismatch")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("CSRF origin is invalid")
	}
	normalized := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	for _, allowed := range allowedOrigins {
		if normalized == allowed {
			return nil
		}
	}
	return errors.New("CSRF origin is not allowed")
}

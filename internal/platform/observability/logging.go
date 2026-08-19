package observability

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var (
	credentialURLPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^:/@\s]+:)[^@\s]+@`)
	bearerPattern        = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;]+`)
	jwtPattern           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
)

func NewRedactingJSONHandler(writer io.Writer, options *slog.HandlerOptions) slog.Handler {
	configured := slog.HandlerOptions{}
	if options != nil {
		configured = *options
	}
	callerReplace := configured.ReplaceAttr
	configured.ReplaceAttr = func(groups []string, attribute slog.Attr) slog.Attr {
		if sensitivePath(groups, attribute.Key) {
			return slog.String(attribute.Key, RedactedValue)
		}
		attribute = sanitizeAttribute(attribute)
		if callerReplace != nil {
			return callerReplace(groups, attribute)
		}
		return attribute
	}
	return &redactingHandler{inner: slog.NewJSONHandler(writer, &configured)}
}

type redactingHandler struct {
	inner slog.Handler
}

func (handler *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.inner.Enabled(ctx, level)
}

func (handler *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	return handler.inner.Handle(ctx, record)
}

func (handler *redactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	return &redactingHandler{inner: handler.inner.WithAttrs(attributes)}
}

func (handler *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: handler.inner.WithGroup(name)}
}

func sensitivePath(groups []string, key string) bool {
	for _, group := range groups {
		if sensitiveKey(group) {
			return true
		}
	}
	return sensitiveKey(key)
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	for _, fragment := range []string{
		"token", "cookie", "password", "passwd", "secret", "dsn", "payload",
		"before", "after", "configurationdata", "configdata", "privatekey", "certificate",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return normalized == "value" || normalized == "filtervalue" || normalized == "optionlabel"
}

func sanitizeAttribute(attribute slog.Attr) slog.Attr {
	switch attribute.Value.Kind() {
	case slog.KindString:
		return slog.String(attribute.Key, sanitizeText(attribute.Value.String()))
	case slog.KindAny:
		if err, ok := attribute.Value.Any().(error); ok {
			return slog.String(attribute.Key, sanitizeText(err.Error()))
		}
	}
	return attribute
}

func sanitizeText(value string) string {
	value = credentialURLPattern.ReplaceAllString(value, `${1}`+RedactedValue+`@`)
	value = bearerPattern.ReplaceAllString(value, `${1}`+RedactedValue)
	return jwtPattern.ReplaceAllString(value, RedactedValue)
}

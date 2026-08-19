package observability_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/platform/observability"
)

func TestRedactingHandlerRemovesSecretsFromNestedAttributesAndErrors(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(observability.NewRedactingJSONHandler(&output, nil))
	logger.Info("request",
		"token", "token-value-should-not-appear",
		"request", slog.GroupValue(
			slog.String("configurationData", "config-body-should-not-appear"),
			slog.String("safe", "visible"),
		),
		"error", errors.New("dial mysql://root:password-should-not-appear@mysql/finconfig"),
	)

	text := output.String()
	for _, secret := range []string{"token-value-should-not-appear", "config-body-should-not-appear", "password-should-not-appear"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret %q leaked in %s", secret, text)
		}
	}
	if !strings.Contains(text, "visible") || !strings.Contains(text, observability.RedactedValue) {
		t.Fatalf("safe value or redaction marker missing from %s", text)
	}
}

func TestRedactingHandlerPreservesWithAttrsAndWithGroup(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(observability.NewRedactingJSONHandler(&output, nil)).With("service", "control-plane", "cookie", "hidden").WithGroup("http")
	logger.Info("served", "route", "/healthz")
	text := output.String()
	if !strings.Contains(text, "control-plane") || !strings.Contains(text, "/healthz") || strings.Contains(text, "hidden") {
		t.Fatalf("unexpected structured log: %s", text)
	}
}

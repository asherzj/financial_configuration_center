package observability_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/observability"
)

func TestMetricsExposeOnlyDeclaredBoundedLabels(t *testing.T) {
	t.Parallel()
	metrics, err := observability.NewMetrics(observability.Vocabulary{
		Services:   []string{"control-plane"},
		RPCMethods: []string{"CatalogAdminService/CreateCollection"},
		EventTypes: []string{"catalog.changed"},
		Regions:    []string{"cn"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveRPC("control-plane", "CatalogAdminService/CreateCollection", observability.CodeOK, 25*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObservePageQuery(observability.QueryAll, observability.ResultSuccess, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := metrics.SetOutboxEvents(observability.OutboxPending, 3); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveRPC("control-plane", "release-123", observability.CodeOK, time.Millisecond); err == nil {
		t.Fatal("unregistered high-cardinality RPC method label accepted")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, expected := range []string{
		`finconfig_rpc_requests_total{code="ok",method="CatalogAdminService/CreateCollection",service="control-plane"} 1`,
		`finconfig_pagequery_total{query_type="all",result="success"} 1`,
		`finconfig_outbox_events{status="pending"} 3`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metrics do not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "release-123") {
		t.Fatalf("rejected high-cardinality label was emitted:\n%s", text)
	}
}

func TestMetricVocabularyIsBoundedAndValidated(t *testing.T) {
	t.Parallel()
	tooMany := make([]string, observability.MaxVocabularyValues+1)
	for index := range tooMany {
		tooMany[index] = "method"
	}
	if _, err := observability.NewMetrics(observability.Vocabulary{RPCMethods: tooMany}); err == nil {
		t.Fatal("unbounded vocabulary accepted")
	}
	if _, err := observability.NewMetrics(observability.Vocabulary{Services: []string{"service\nlabel"}}); err == nil {
		t.Fatal("invalid label value accepted")
	}
}

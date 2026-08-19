package observability

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const MaxVocabularyValues = 256

type Result string

const (
	ResultSuccess  Result = "success"
	ResultError    Result = "error"
	ResultConflict Result = "conflict"
	ResultRejected Result = "rejected"
)

type RPCCode string

const (
	CodeOK               RPCCode = "ok"
	CodeInvalidArgument  RPCCode = "invalid_argument"
	CodePermissionDenied RPCCode = "permission_denied"
	CodeConflict         RPCCode = "conflict"
	CodeUnavailable      RPCCode = "unavailable"
	CodeCanceled         RPCCode = "canceled"
	CodeInternal         RPCCode = "internal"
)

type QueryType string

const (
	QueryAll      QueryType = "all"
	QueryOnlyData QueryType = "only_data"
	QueryOptions  QueryType = "options"
)

type OutboxStatus string

const (
	OutboxPending    OutboxStatus = "pending"
	OutboxProcessing OutboxStatus = "processing"
	OutboxDelivered  OutboxStatus = "delivered"
	OutboxDeadLetter OutboxStatus = "dead_letter"
)

type SnapshotMode string

const (
	SnapshotFull        SnapshotMode = "full"
	SnapshotIncremental SnapshotMode = "incremental"
)

type SnapshotTrigger string

const (
	TriggerStartup     SnapshotTrigger = "startup"
	TriggerHint        SnapshotTrigger = "hint"
	TriggerVersionPoll SnapshotTrigger = "version_poll"
)

type Vocabulary struct {
	Services   []string
	RPCMethods []string
	Modules    []string
	EventTypes []string
	Regions    []string
}

type Metrics struct {
	registry *prometheus.Registry
	allowed  vocabularySets

	rpcRequests        *prometheus.CounterVec
	rpcDuration        *prometheus.HistogramVec
	mysqlTransactions  *prometheus.CounterVec
	mysqlDuration      *prometheus.HistogramVec
	snapshotRefreshes  *prometheus.CounterVec
	snapshotDuration   *prometheus.HistogramVec
	snapshotGeneration prometheus.Gauge
	snapshotFailures   prometheus.Gauge
	watchConnections   *prometheus.GaugeVec
	watchDropped       *prometheus.CounterVec
	outboxEvents       *prometheus.GaugeVec
	outboxDeliveries   *prometheus.CounterVec
	releaseActions     *prometheus.CounterVec
	pageQueries        *prometheus.CounterVec
	pageQueryDuration  *prometheus.HistogramVec
	sdkRefreshes       *prometheus.CounterVec
	sdkCallbacks       *prometheus.CounterVec
}

type vocabularySets struct {
	services   map[string]struct{}
	methods    map[string]struct{}
	modules    map[string]struct{}
	eventTypes map[string]struct{}
	regions    map[string]struct{}
}

var labelValuePattern = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)

func NewMetrics(vocabulary Vocabulary) (*Metrics, error) {
	allowed, err := compileVocabulary(vocabulary)
	if err != nil {
		return nil, err
	}
	metrics := &Metrics{
		registry:           prometheus.NewRegistry(),
		allowed:            allowed,
		rpcRequests:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_rpc_requests_total", Help: "FinConfig RPC requests by bounded service, method and result code."}, []string{"service", "method", "code"}),
		rpcDuration:        prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "finconfig_rpc_duration_seconds", Help: "FinConfig RPC duration by bounded service and method."}, []string{"service", "method"}),
		mysqlTransactions:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_mysql_tx_total", Help: "FinConfig MySQL transactions by bounded module and result."}, []string{"module", "result"}),
		mysqlDuration:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "finconfig_mysql_tx_duration_seconds", Help: "FinConfig MySQL transaction duration by bounded module."}, []string{"module"}),
		snapshotRefreshes:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_snapshot_refresh_total", Help: "FinConfig snapshot refreshes by mode, trigger and result."}, []string{"mode", "trigger", "result"}),
		snapshotDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "finconfig_snapshot_refresh_duration_seconds", Help: "FinConfig snapshot refresh duration by mode."}, []string{"mode"}),
		snapshotGeneration: prometheus.NewGauge(prometheus.GaugeOpts{Name: "finconfig_snapshot_generation", Help: "Current process-local snapshot generation."}),
		snapshotFailures:   prometheus.NewGauge(prometheus.GaugeOpts{Name: "finconfig_snapshot_collection_failures", Help: "Number of collections retaining their last-known-good snapshot after refresh failures."}),
		watchConnections:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "finconfig_watch_connections", Help: "Current watch connections by bounded service."}, []string{"service"}),
		watchDropped:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_watch_dropped_events_total", Help: "Dropped watch events by bounded reason."}, []string{"reason"}),
		outboxEvents:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "finconfig_outbox_events", Help: "Outbox events by status."}, []string{"status"}),
		outboxDeliveries:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_outbox_delivery_total", Help: "Outbox delivery attempts by bounded event type and result."}, []string{"event_type", "result"}),
		releaseActions:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_release_actions_total", Help: "Release actions by bounded action, step type and result."}, []string{"action", "step_type", "result"}),
		pageQueries:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_pagequery_total", Help: "Page queries by query type and result."}, []string{"query_type", "result"}),
		pageQueryDuration:  prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "finconfig_pagequery_duration_seconds", Help: "Page query duration by query type."}, []string{"query_type"}),
		sdkRefreshes:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_sdk_refresh_total", Help: "SDK refreshes by bounded region and result."}, []string{"region", "result"}),
		sdkCallbacks:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "finconfig_sdk_callback_total", Help: "SDK callbacks by result."}, []string{"result"}),
	}
	metrics.registry.MustRegister(
		metrics.rpcRequests, metrics.rpcDuration,
		metrics.mysqlTransactions, metrics.mysqlDuration,
		metrics.snapshotRefreshes, metrics.snapshotDuration, metrics.snapshotGeneration, metrics.snapshotFailures,
		metrics.watchConnections, metrics.watchDropped,
		metrics.outboxEvents, metrics.outboxDeliveries,
		metrics.releaseActions,
		metrics.pageQueries, metrics.pageQueryDuration,
		metrics.sdkRefreshes, metrics.sdkCallbacks,
	)
	return metrics, nil
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveRPC(service, method string, code RPCCode, duration time.Duration) error {
	if !contains(m.allowed.services, service) || !contains(m.allowed.methods, method) || !validRPCCode(code) {
		return errors.New("RPC metric contains an undeclared label value")
	}
	m.rpcRequests.WithLabelValues(service, method, string(code)).Inc()
	m.rpcDuration.WithLabelValues(service, method).Observe(duration.Seconds())
	return nil
}

func (m *Metrics) ObserveMySQLTransaction(module string, result Result, duration time.Duration) error {
	if !contains(m.allowed.modules, module) || !validResult(result) {
		return errors.New("MySQL transaction metric contains an undeclared label value")
	}
	m.mysqlTransactions.WithLabelValues(module, string(result)).Inc()
	m.mysqlDuration.WithLabelValues(module).Observe(duration.Seconds())
	return nil
}

func (m *Metrics) ObserveSnapshotRefresh(mode SnapshotMode, trigger SnapshotTrigger, result Result, duration time.Duration) error {
	if !validSnapshotMode(mode) || !validSnapshotTrigger(trigger) || !validResult(result) {
		return errors.New("snapshot refresh metric contains an undeclared label value")
	}
	m.snapshotRefreshes.WithLabelValues(string(mode), string(trigger), string(result)).Inc()
	m.snapshotDuration.WithLabelValues(string(mode)).Observe(duration.Seconds())
	return nil
}

func (m *Metrics) SetSnapshotGeneration(generation uint64) {
	m.snapshotGeneration.Set(float64(generation))
}

func (m *Metrics) SetSnapshotCollectionFailures(count int) error {
	if count < 0 {
		return errors.New("snapshot collection failure count cannot be negative")
	}
	m.snapshotFailures.Set(float64(count))
	return nil
}

func (m *Metrics) SetWatchConnections(service string, count int) error {
	if !contains(m.allowed.services, service) || count < 0 {
		return errors.New("watch connection metric contains an invalid value")
	}
	m.watchConnections.WithLabelValues(service).Set(float64(count))
	return nil
}

func (m *Metrics) ObserveWatchDrop(reason string) error {
	switch reason {
	case "queue_full", "resync_required", "client_closed", "server_shutdown":
		m.watchDropped.WithLabelValues(reason).Inc()
		return nil
	default:
		return errors.New("watch drop metric contains an undeclared reason")
	}
}

func (m *Metrics) SetOutboxEvents(status OutboxStatus, count int) error {
	if !validOutboxStatus(status) || count < 0 {
		return errors.New("outbox event metric contains an invalid value")
	}
	m.outboxEvents.WithLabelValues(string(status)).Set(float64(count))
	return nil
}

func (m *Metrics) ObserveOutboxDelivery(eventType string, result Result) error {
	if !contains(m.allowed.eventTypes, eventType) || !validResult(result) {
		return errors.New("outbox delivery metric contains an undeclared label value")
	}
	m.outboxDeliveries.WithLabelValues(eventType, string(result)).Inc()
	return nil
}

func (m *Metrics) ObserveReleaseAction(action, stepType string, result Result) error {
	if !validReleaseAction(action) || !validStepType(stepType) || !validResult(result) {
		return errors.New("release action metric contains an undeclared label value")
	}
	m.releaseActions.WithLabelValues(action, stepType, string(result)).Inc()
	return nil
}

func (m *Metrics) ObservePageQuery(queryType QueryType, result Result, duration time.Duration) error {
	if !validQueryType(queryType) || !validResult(result) {
		return errors.New("page query metric contains an undeclared label value")
	}
	m.pageQueries.WithLabelValues(string(queryType), string(result)).Inc()
	m.pageQueryDuration.WithLabelValues(string(queryType)).Observe(duration.Seconds())
	return nil
}

func (m *Metrics) ObserveSDKRefresh(region string, result Result) error {
	if !contains(m.allowed.regions, region) || !validResult(result) {
		return errors.New("SDK refresh metric contains an undeclared label value")
	}
	m.sdkRefreshes.WithLabelValues(region, string(result)).Inc()
	return nil
}

func (m *Metrics) ObserveSDKCallback(result Result) error {
	if !validResult(result) {
		return errors.New("SDK callback metric contains an undeclared result")
	}
	m.sdkCallbacks.WithLabelValues(string(result)).Inc()
	return nil
}

func compileVocabulary(vocabulary Vocabulary) (vocabularySets, error) {
	services, err := compileValues("services", vocabulary.Services)
	if err != nil {
		return vocabularySets{}, err
	}
	methods, err := compileValues("RPC methods", vocabulary.RPCMethods)
	if err != nil {
		return vocabularySets{}, err
	}
	modules, err := compileValues("modules", vocabulary.Modules)
	if err != nil {
		return vocabularySets{}, err
	}
	eventTypes, err := compileValues("event types", vocabulary.EventTypes)
	if err != nil {
		return vocabularySets{}, err
	}
	regions, err := compileValues("regions", vocabulary.Regions)
	if err != nil {
		return vocabularySets{}, err
	}
	return vocabularySets{services: services, methods: methods, modules: modules, eventTypes: eventTypes, regions: regions}, nil
}

func compileValues(name string, values []string) (map[string]struct{}, error) {
	if len(values) > MaxVocabularyValues {
		return nil, fmt.Errorf("metric %s vocabulary exceeds %d values", name, MaxVocabularyValues)
	}
	compiled := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !labelValuePattern.MatchString(value) {
			return nil, fmt.Errorf("metric %s contains invalid label value", name)
		}
		if _, exists := compiled[value]; exists {
			return nil, fmt.Errorf("metric %s contains duplicate label value", name)
		}
		compiled[value] = struct{}{}
	}
	return compiled, nil
}

func contains(values map[string]struct{}, value string) bool {
	_, ok := values[value]
	return ok
}

func validResult(value Result) bool {
	return value == ResultSuccess || value == ResultError || value == ResultConflict || value == ResultRejected
}

func validRPCCode(value RPCCode) bool {
	switch value {
	case CodeOK, CodeInvalidArgument, CodePermissionDenied, CodeConflict, CodeUnavailable, CodeCanceled, CodeInternal:
		return true
	default:
		return false
	}
}

func validQueryType(value QueryType) bool {
	return value == QueryAll || value == QueryOnlyData || value == QueryOptions
}

func validOutboxStatus(value OutboxStatus) bool {
	return value == OutboxPending || value == OutboxProcessing || value == OutboxDelivered || value == OutboxDeadLetter
}

func validSnapshotMode(value SnapshotMode) bool {
	return value == SnapshotFull || value == SnapshotIncremental
}

func validSnapshotTrigger(value SnapshotTrigger) bool {
	return value == TriggerStartup || value == TriggerHint || value == TriggerVersionPoll
}

func validReleaseAction(value string) bool {
	switch value {
	case "create", "approve", "reject", "execute", "advance", "rollback", "compensate":
		return true
	default:
		return false
	}
}

func validStepType(value string) bool {
	switch value {
	case "review", "base_apply", "overlay_apply", "wait", "complete", "compensate":
		return true
	default:
		return false
	}
}

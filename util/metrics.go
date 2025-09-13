package util

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (

	// Connection metrics
	openConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_open_connections_total",
			Help: "Current number of open connections between the proxy and the application",
		},
		[]string{"type"},
	)

	connectionOpenedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_connections_opened_total",
			Help: "Total number of connections opened with the application",
		},
		[]string{},
	)

	connectionClosedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_connections_closed_total",
			Help: "Total number of connections closed with the application",
		},
		[]string{},
	)

	// Message handling metrics
	messageHandleDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_message_handle_duration_seconds",
			Help:    "End-to-end time handling an incoming message from the application",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
		[]string{"success"},
	)

	roundTripDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_round_trip_duration_seconds",
			Help:    "Round trip time sending a request and receiving a response from MongoDB",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
		[]string{},
	)

	// Message size metrics
	requestSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_request_size_bytes",
			Help:    "Request size to MongoDB",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"database"},
	)

	responseSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_response_size_bytes",
			Help:    "Response size from MongoDB",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"database"},
	)

	// Cursor and transaction tracking
	cursorsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_cursors_active_total",
			Help: "Number of open cursors being tracked (for cursor -> server mapping)",
		},
		[]string{},
	)

	transactionsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_transactions_active_total",
			Help: "Number of transactions being tracked (for client sessions -> server mapping)",
		},
		[]string{},
	)

	// MongoDB driver metrics
	serverSelectionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_server_selection_duration_seconds",
			Help:    "Go driver server selection timing",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
		[]string{},
	)

	checkoutConnectionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_checkout_connection_duration_seconds",
			Help:    "Go driver connection checkout timing",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
		[]string{},
	)

	// Connection pool metrics
	poolCheckedOutConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_checked_out_connections_total",
			Help: "Number of connections checked out from the Go driver connection pool",
		},
		[]string{},
	)

	poolOpenConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_open_connections_total",
			Help: "Number of open connections from the Go driver to MongoDB",
		},
		[]string{},
	)

	// Pool events
	poolEventCounters = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_pool_events_total",
			Help: "Go driver connection pool events",
		},
		[]string{"event_type"},
	)

	// Pool statistics
	poolWaitTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_pool_wait_duration_seconds",
			Help:    "Time spent waiting for connections from the pool",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"database"},
	)

	poolConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_connections_total",
			Help: "Current number of connections in the pool",
		},
		[]string{"database", "state"}, // state: available, in_use
	)

	// Additional metrics for comprehensive dashboard
	serverConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_server_connections_total",
			Help: "Current number of server connections by state",
		},
		[]string{"state"}, // state: active, idle, used, testing, login
	)

	clientConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_client_connections_total",
			Help: "Current number of client connections by state",
		},
		[]string{"state"}, // state: active, waiting
	)

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_requests_total",
			Help: "Total number of requests processed",
		},
		[]string{"database", "operation"},
	)

	errorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_errors_total",
			Help: "Total number of errors by type",
		},
		[]string{"database", "error_type"},
	)

	authenticationFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_authentication_failures_total",
			Help: "Total number of authentication failures",
		},
		[]string{},
	)

	// MongoDB driver pool metrics
	mongodbPoolActiveConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_mongodb_pool_active_connections",
			Help: "Number of active connections in MongoDB driver pool",
		},
		[]string{"database"},
	)

	mongodbPoolTotalConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_mongodb_pool_total_connections",
			Help: "Total number of connections in MongoDB driver pool",
		},
		[]string{"database"},
	)

	mongodbPoolMaxConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_mongodb_pool_max_connections",
			Help: "Maximum number of connections allowed in MongoDB driver pool",
		},
		[]string{"database"},
	)

	mongodbPoolUtilizationRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_mongodb_pool_utilization_ratio",
			Help: "MongoDB driver pool utilization ratio (active/max)",
		},
		[]string{"database"},
	)

	mongodbPoolCheckoutTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_mongodb_pool_checkout_total",
			Help: "Total number of connection checkouts from MongoDB driver pool",
		},
		[]string{"database"},
	)

	mongodbPoolCheckinTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_mongodb_pool_checkin_total",
			Help: "Total number of connection checkins to MongoDB driver pool",
		},
		[]string{"database"},
	)

	mongodbPoolCheckoutFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_mongodb_pool_checkout_failures_total",
			Help: "Total number of connection checkout failures from MongoDB driver pool",
		},
		[]string{"database"},
	)

	poolTimeoutsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_pool_timeouts_total",
			Help: "Total number of connection pool timeouts",
		},
		[]string{"database"},
	)

	bytesTransferredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_bytes_total",
			Help: "Total bytes transferred by direction",
		},
		[]string{"direction"}, // direction: sent, received
	)

	poolQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_queue_depth",
			Help: "Current number of requests waiting for connections",
		},
		[]string{"database"},
	)

	poolMaxConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_max_connections_total",
			Help: "Maximum number of connections allowed in pool",
		},
		[]string{"database"},
	)

	// Pool exhaustion and capacity metrics
	clientsWaitingForServer = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_clients_waiting_for_server_total",
			Help: "Number of client connections waiting for server connections",
		},
		[]string{"database"},
	)

	poolConnectionWaitTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_pool_connection_wait_duration_seconds",
			Help:    "Time spent waiting for connections from pool by database",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"database"},
	)

	poolUtilization = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_utilization_ratio",
			Help: "Pool utilization ratio (active connections / max connections)",
		},
		[]string{"database"},
	)

	poolExhaustionEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_pool_exhaustion_events_total",
			Help: "Total number of pool exhaustion events",
		},
		[]string{"database"},
	)

	poolConnectionCheckoutDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_pool_checkout_duration_seconds",
			Help:    "Time spent checking out connections from pool",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"database"},
	)

	sessionsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_sessions_active_total",
			Help: "Number of active client sessions",
		},
		[]string{},
	)

	cursorsOpened = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_cursors_opened_total",
			Help: "Total number of cursors opened",
		},
		[]string{"database"},
	)

	cursorsClosed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_cursors_closed_total",
			Help: "Total number of cursors closed",
		},
		[]string{"database"},
	)

	ismasterCommandsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_ismaster_commands_total",
			Help: "Total number of ismaster/hello commands processed",
		},
		[]string{},
	)

	serverSelectionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_server_selections_total",
			Help: "Total number of MongoDB server selections",
		},
		[]string{"database"},
	)

	transactionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_transactions_total",
			Help: "Total number of transactions processed",
		},
		[]string{"database", "result"}, // result: committed, aborted
	)

	maxClientConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_max_client_connections_total",
			Help: "Maximum allowed client connections",
		},
		[]string{},
	)

	maxUserConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_max_user_connections_total",
			Help: "Maximum allowed connections per user",
		},
		[]string{"user"},
	)

	clientWaitTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_client_wait_duration_seconds",
			Help:    "Time clients spend waiting for available connections",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
		[]string{"database"},
	)
)

// MetricsClient provides Prometheus-compatible interface for metrics
type MetricsClient struct {
	logger   *zap.Logger
	server   *http.Server
	registry *prometheus.Registry
}

// NewMetricsClient creates a new Prometheus metrics client
func NewMetricsClient(logger *zap.Logger, metricsAddr string) (*MetricsClient, error) {
	// Create a new registry for this client instance
	registry := prometheus.NewRegistry()

	// Register all metrics to this instance's registry
	registry.MustRegister(
		openConnections,
		connectionOpenedTotal,
		connectionClosedTotal,
		messageHandleDuration,
		roundTripDuration,
		requestSizeBytes,
		responseSizeBytes,
		cursorsActive,
		transactionsActive,
		serverSelectionDuration,
		checkoutConnectionDuration,
		poolCheckedOutConnections,
		poolOpenConnections,
		poolEventCounters,
		poolWaitTime,
		poolConnections,
		// New comprehensive metrics
		serverConnections,
		clientConnections,
		requestsTotal,
		errorsTotal,
		authenticationFailuresTotal,
		poolTimeoutsTotal,
		bytesTransferredTotal,
		poolQueueDepth,
		poolMaxConnections,
		// New pool monitoring metrics
		clientsWaitingForServer,
		poolConnectionWaitTime,
		poolUtilization,
		poolExhaustionEvents,
		poolConnectionCheckoutDuration,
		sessionsActive,
		cursorsOpened,
		cursorsClosed,
		ismasterCommandsTotal,
		serverSelectionTotal,
		transactionsTotal,
		maxClientConnections,
		maxUserConnections,
		clientWaitTime,
		// MongoDB driver pool metrics
		mongodbPoolActiveConnections,
		mongodbPoolTotalConnections,
		mongodbPoolMaxConnections,
		mongodbPoolUtilizationRatio,
		mongodbPoolCheckoutTotal,
		mongodbPoolCheckinTotal,
		mongodbPoolCheckoutFailuresTotal,
	)

	// Create HTTP server for metrics endpoint
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    metricsAddr,
		Handler: mux,
	}

	client := &MetricsClient{
		logger:   logger,
		server:   server,
		registry: registry,
	}

	// Start metrics server in background
	go func() {
		logger.Info("Starting Prometheus metrics server", zap.String("address", metricsAddr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Metrics server failed", zap.Error(err))
		}
	}()

	return client, nil
}

// Shutdown gracefully shuts down the metrics server
func (m *MetricsClient) Shutdown(ctx context.Context) error {
	return m.server.Shutdown(ctx)
}

// Timing records a duration metric
func (m *MetricsClient) Timing(name string, duration time.Duration, tags []string, rate float64) error {
	success := parseSuccessTag(tags)

	switch name {
	case "handle_message":
		messageHandleDuration.WithLabelValues(success).Observe(duration.Seconds())
	case "round_trip":
		roundTripDuration.WithLabelValues().Observe(duration.Seconds())
	case "server_selection":
		serverSelectionDuration.WithLabelValues().Observe(duration.Seconds())
	case "checkout_connection":
		checkoutConnectionDuration.WithLabelValues().Observe(duration.Seconds())
	case "client_wait":
		database := parseDatabaseTag(tags)
		clientWaitTime.WithLabelValues(database).Observe(duration.Seconds())
	}

	return nil
}

// Incr increments a counter
func (m *MetricsClient) Incr(name string, tags []string, rate float64) error {
	switch name {
	case "connection_opened":
		connectionOpenedTotal.WithLabelValues().Inc()
	case "connection_closed":
		connectionClosedTotal.WithLabelValues().Inc()
	case "request":
		database, operation := parseRequestTags(tags)
		requestsTotal.WithLabelValues(database, operation).Inc()
	case "error":
		database := parseDatabaseTag(tags)
		errorType := parseErrorTag(tags)
		errorsTotal.WithLabelValues(database, errorType).Inc()
	case "authentication_failure":
		authenticationFailuresTotal.WithLabelValues().Inc()
	case "pool_timeout":
		database := parseDatabaseTag(tags)
		poolTimeoutsTotal.WithLabelValues(database).Inc()
	case "bytes_sent":
		bytesTransferredTotal.WithLabelValues("sent").Add(rate)
	case "bytes_received":
		bytesTransferredTotal.WithLabelValues("received").Add(rate)
	case "cursor_opened":
		database := parseDatabaseTag(tags)
		cursorsOpened.WithLabelValues(database).Inc()
	case "cursor_closed":
		database := parseDatabaseTag(tags)
		cursorsClosed.WithLabelValues(database).Inc()
	case "ismaster_command":
		ismasterCommandsTotal.WithLabelValues().Inc()
	case "server_selection":
		database := parseDatabaseTag(tags)
		serverSelectionTotal.WithLabelValues(database).Inc()
	case "transaction_committed":
		database := parseDatabaseTag(tags)
		transactionsTotal.WithLabelValues(database, "committed").Inc()
	case "transaction_aborted":
		database := parseDatabaseTag(tags)
		transactionsTotal.WithLabelValues(database, "aborted").Inc()
	case "pool_exhaustion":
		database := parseDatabaseTag(tags)
		poolExhaustionEvents.WithLabelValues(database).Inc()
	default:
		// Handle pool events
		poolEventCounters.WithLabelValues(name).Inc()
	}

	return nil
}

// Gauge sets a gauge value
func (m *MetricsClient) Gauge(name string, value float64, tags []string, rate float64) error {
	switch name {
	case "open_connections":
		openConnections.WithLabelValues("client").Set(value)
	case "cursors":
		cursorsActive.WithLabelValues().Set(value)
	case "transactions":
		transactionsActive.WithLabelValues().Set(value)
	case "pool.checked_out_connections":
		poolCheckedOutConnections.WithLabelValues().Set(value)
	case "pool.open_connections":
		poolOpenConnections.WithLabelValues().Set(value)
	case "server_connections":
		state := parseStateTag(tags)
		serverConnections.WithLabelValues(state).Set(value)
	case "client_connections":
		state := parseStateTag(tags)
		clientConnections.WithLabelValues(state).Set(value)
	case "pool_queue_depth":
		database := parseDatabaseTag(tags)
		poolQueueDepth.WithLabelValues(database).Set(value)
	case "pool_max_connections":
		database := parseDatabaseTag(tags)
		poolMaxConnections.WithLabelValues(database).Set(value)
	case "clients_waiting_for_server":
		database := parseDatabaseTag(tags)
		clientsWaitingForServer.WithLabelValues(database).Set(value)
	case "pool_utilization":
		database := parseDatabaseTag(tags)
		poolUtilization.WithLabelValues(database).Set(value)
	case "sessions_active":
		sessionsActive.WithLabelValues().Set(value)
	case "max_client_connections":
		maxClientConnections.WithLabelValues().Set(value)
	case "max_user_connections":
		user := parseUserTag(tags)
		maxUserConnections.WithLabelValues(user).Set(value)
	case "mongodb_pool_active_connections":
		database := parseDatabaseTag(tags)
		mongodbPoolActiveConnections.WithLabelValues(database).Set(value)
	case "mongodb_pool_total_connections":
		database := parseDatabaseTag(tags)
		mongodbPoolTotalConnections.WithLabelValues(database).Set(value)
	case "mongodb_pool_max_connections":
		database := parseDatabaseTag(tags)
		mongodbPoolMaxConnections.WithLabelValues(database).Set(value)
	case "mongodb_pool_utilization_ratio":
		database := parseDatabaseTag(tags)
		mongodbPoolUtilizationRatio.WithLabelValues(database).Set(value)
	case "mongodb_pool_checkout_total":
		database := parseDatabaseTag(tags)
		mongodbPoolCheckoutTotal.WithLabelValues(database).Add(value)
	case "mongodb_pool_checkin_total":
		database := parseDatabaseTag(tags)
		mongodbPoolCheckinTotal.WithLabelValues(database).Add(value)
	case "mongodb_pool_checkout_failures_total":
		database := parseDatabaseTag(tags)
		mongodbPoolCheckoutFailuresTotal.WithLabelValues(database).Add(value)
	}

	return nil
}

// Distribution records a distribution value
func (m *MetricsClient) Distribution(name string, value float64, tags []string, rate float64) error {
	switch name {
	case "request_size":
		database := parseDatabaseTag(tags)
		requestSizeBytes.WithLabelValues(database).Observe(value)
	case "response_size":
		database := parseDatabaseTag(tags)
		responseSizeBytes.WithLabelValues(database).Observe(value)
	case "pool_connection_wait":
		database := parseDatabaseTag(tags)
		poolConnectionWaitTime.WithLabelValues(database).Observe(value)
	case "pool_checkout_duration":
		database := parseDatabaseTag(tags)
		poolConnectionCheckoutDuration.WithLabelValues(database).Observe(value)
	}

	return nil
}

// BackgroundGaugeCallback is a function type for background gauge updates
type BackgroundGaugeCallback func(name string, tags []string)

// BackgroundGauge creates increment/decrement callbacks for a gauge metric
func (m *MetricsClient) BackgroundGauge(name string, tags []string) (increment, decrement BackgroundGaugeCallback) {
	var gauge prometheus.Gauge
	switch name {
	case "open_connections":
		gauge = openConnections.WithLabelValues("client")
	default:
		// Create a generic gauge if needed
		gauge = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mongobouncer_" + name + "_total",
			Help: "Background tracked gauge: " + name,
		})
	}

	inc := make(chan bool, 100) // Buffered channel
	dec := make(chan bool, 100) // Buffered channel

	increment = func(metricName string, metricTags []string) {
		// Also increment counter for the specific metric
		m.Incr(metricName, metricTags, 1)

		select {
		case inc <- true:
		default:
			// Channel full, ignore
		}
	}

	decrement = func(metricName string, metricTags []string) {
		// Also increment counter for the specific metric
		m.Incr(metricName, metricTags, 1)

		select {
		case dec <- true:
		default:
			// Channel full, ignore
		}
	}

	// Background goroutine to update gauge
	go func() {
		count := float64(0)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-inc:
				count++
			case <-dec:
				if count > 0 {
					count--
				}
			case <-ticker.C:
				gauge.Set(count)
			}
		}
	}()

	return
}

// parseSuccessTag extracts success status from tags, defaults to "true"
func parseSuccessTag(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "success:") {
			return tag[8:] // Remove "success:" prefix
		}
	}
	return "true"
}

// parseTagsToLabels extracts multiple labels from tags (for backward compatibility)
func parseTagsToLabels(tags []string) []string {
	var labels []string

	for _, tag := range tags {
		// Handle tags in format "key:value"
		if idx := strings.Index(tag, ":"); idx != -1 {
			labels = append(labels, tag[idx+1:]) // Just the value part
		} else {
			labels = append(labels, tag)
		}
	}

	// Ensure we have at least one label (cluster)
	if len(labels) == 0 {
		labels = append(labels, "default")
	}

	return labels
}

// Additional helper functions for pool metrics
func (m *MetricsClient) RecordPoolWaitTime(poolName string, duration time.Duration) {
	poolWaitTime.WithLabelValues(poolName).Observe(duration.Seconds())
}

func (m *MetricsClient) SetPoolConnections(poolName, state string, count float64) {
	poolConnections.WithLabelValues(poolName, state).Set(count)
}

func (m *MetricsClient) Flush() error {
	// No-op for Prometheus (metrics are pushed via HTTP scraping)
	return nil
}

func (m *MetricsClient) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.Shutdown(ctx)
}

// Additional tag parsing functions for new metrics
func parseRequestTags(tags []string) (database, operation string) {
	database = "default"
	operation = "unknown"

	for _, tag := range tags {
		if strings.HasPrefix(tag, "database:") {
			database = tag[9:]
		} else if strings.HasPrefix(tag, "operation:") {
			operation = tag[10:]
		}
	}

	return database, operation
}

func parseErrorTag(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "error_type:") {
			return tag[11:]
		}
	}
	return "unknown"
}

func parseDatabaseTag(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "database:") {
			return tag[9:]
		}
	}
	return "default"
}

func parseStateTag(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "state:") {
			return tag[6:]
		}
	}
	return "unknown"
}

func parseUserTag(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "user:") {
			return tag[5:]
		}
	}
	return "default"
}

// Tags property for compatibility (not used in Prometheus)
var Tags []string

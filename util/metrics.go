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
			Help: "Number of open connections between the proxy and the application",
		},
		[]string{"cluster", "type"},
	)

	connectionOpenedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_connections_opened_total",
			Help: "Total number of connections opened with the application",
		},
		[]string{"cluster"},
	)

	connectionClosedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_connections_closed_total",
			Help: "Total number of connections closed with the application",
		},
		[]string{"cluster"},
	)

	// Message handling metrics
	messageHandleDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_message_handle_duration_seconds",
			Help:    "End-to-end time handling an incoming message from the application",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"cluster", "success"},
	)

	roundTripDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_round_trip_duration_seconds",
			Help:    "Round trip time sending a request and receiving a response from MongoDB",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"cluster"},
	)

	// Message size metrics
	requestSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_request_size_bytes",
			Help:    "Request size to MongoDB",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"cluster"},
	)

	responseSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_response_size_bytes",
			Help:    "Response size from MongoDB",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"cluster"},
	)

	// Cursor and transaction tracking
	cursorsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_cursors_active_total",
			Help: "Number of open cursors being tracked (for cursor -> server mapping)",
		},
		[]string{"cluster"},
	)

	transactionsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_transactions_active_total",
			Help: "Number of transactions being tracked (for client sessions -> server mapping)",
		},
		[]string{"cluster"},
	)

	// MongoDB driver metrics
	serverSelectionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_server_selection_duration_seconds",
			Help:    "Go driver server selection timing",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"cluster"},
	)

	checkoutConnectionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_checkout_connection_duration_seconds",
			Help:    "Go driver connection checkout timing",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"cluster"},
	)

	// Connection pool metrics
	poolCheckedOutConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_checked_out_connections_total",
			Help: "Number of connections checked out from the Go driver connection pool",
		},
		[]string{"cluster"},
	)

	poolOpenConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_open_connections_total",
			Help: "Number of open connections from the Go driver to MongoDB",
		},
		[]string{"cluster"},
	)

	// Pool events
	poolEventCounters = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mongobouncer_pool_events_total",
			Help: "Go driver connection pool events",
		},
		[]string{"cluster", "event_type"},
	)

	// Pool statistics
	poolWaitTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mongobouncer_pool_wait_duration_seconds",
			Help:    "Time spent waiting for connections from the pool",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"cluster", "pool_name"},
	)

	poolConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mongobouncer_pool_connections_total",
			Help: "Current number of connections in the pool",
		},
		[]string{"cluster", "pool_name", "state"}, // state: available, in_use
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
	cluster, success := parseTimingTags(tags)

	switch name {
	case "handle_message":
		messageHandleDuration.WithLabelValues(cluster, success).Observe(duration.Seconds())
	case "round_trip":
		roundTripDuration.WithLabelValues(cluster).Observe(duration.Seconds())
	case "server_selection":
		serverSelectionDuration.WithLabelValues(cluster).Observe(duration.Seconds())
	case "checkout_connection":
		checkoutConnectionDuration.WithLabelValues(cluster).Observe(duration.Seconds())
	}

	return nil
}

// Incr increments a counter
func (m *MetricsClient) Incr(name string, tags []string, rate float64) error {
	cluster := parseClusterTag(tags)

	switch name {
	case "connection_opened":
		connectionOpenedTotal.WithLabelValues(cluster).Inc()
	case "connection_closed":
		connectionClosedTotal.WithLabelValues(cluster).Inc()
	default:
		// Handle pool events
		poolEventCounters.WithLabelValues(cluster, name).Inc()
	}

	return nil
}

// Gauge sets a gauge value
func (m *MetricsClient) Gauge(name string, value float64, tags []string, rate float64) error {
	cluster := parseClusterTag(tags)

	switch name {
	case "open_connections":
		openConnections.WithLabelValues(cluster, "client").Set(value)
	case "cursors":
		cursorsActive.WithLabelValues(cluster).Set(value)
	case "transactions":
		transactionsActive.WithLabelValues(cluster).Set(value)
	case "pool.checked_out_connections":
		poolCheckedOutConnections.WithLabelValues(cluster).Set(value)
	case "pool.open_connections":
		poolOpenConnections.WithLabelValues(cluster).Set(value)
	}

	return nil
}

// Distribution records a distribution value
func (m *MetricsClient) Distribution(name string, value float64, tags []string, rate float64) error {
	cluster := parseClusterTag(tags)

	switch name {
	case "request_size":
		requestSizeBytes.WithLabelValues(cluster).Observe(value)
	case "response_size":
		responseSizeBytes.WithLabelValues(cluster).Observe(value)
	}

	return nil
}

// BackgroundGaugeCallback is a function type for background gauge updates
type BackgroundGaugeCallback func(name string, tags []string)

// BackgroundGauge creates increment/decrement callbacks for a gauge metric
func (m *MetricsClient) BackgroundGauge(name string, tags []string) (increment, decrement BackgroundGaugeCallback) {
	cluster := parseClusterTag(tags)

	var gauge prometheus.Gauge
	switch name {
	case "open_connections":
		gauge = openConnections.WithLabelValues(cluster, "client")
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

// parseClusterTag extracts cluster name from tags, defaults to "default"
func parseClusterTag(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "cluster:") {
			return tag[8:] // Remove "cluster:" prefix
		}
	}
	return "default"
}

// parseTimingTags extracts cluster and success from tags for timing metrics
func parseTimingTags(tags []string) (cluster, success string) {
	cluster = "default"
	success = "true"

	for _, tag := range tags {
		if strings.HasPrefix(tag, "cluster:") {
			cluster = tag[8:]
		} else if strings.HasPrefix(tag, "success:") {
			success = tag[8:]
		}
	}

	return cluster, success
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
func (m *MetricsClient) RecordPoolWaitTime(cluster, poolName string, duration time.Duration) {
	poolWaitTime.WithLabelValues(cluster, poolName).Observe(duration.Seconds())
}

func (m *MetricsClient) SetPoolConnections(cluster, poolName, state string, count float64) {
	poolConnections.WithLabelValues(cluster, poolName, state).Set(count)
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

// Tags property for compatibility (not used in Prometheus)
var Tags []string

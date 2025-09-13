package mongo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/sameer-m-dev/mongobouncer/util"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/address"
	"go.mongodb.org/mongo-driver/mongo/description"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/topology"
	"go.uber.org/zap"
)

const pingTimeout = 60 * time.Second
const disconnectTimeout = 10 * time.Second

// PoolStats tracks MongoDB driver connection pool statistics
type PoolStats struct {
	mu                sync.RWMutex
	activeConnections int64
	totalConnections  int64
	maxPoolSize       int64
	checkoutCount     int64
	checkinCount      int64
	checkoutFailures  int64
	lastUpdated       time.Time
}

// HealthStats tracks MongoDB connection health
type HealthStats struct {
	mu               sync.RWMutex
	isHealthy        bool
	lastHealthCheck  time.Time
	healthCheckCount int64
	healthFailures   int64
	lastError        error
	avgResponseTime  time.Duration
}

type Mongo struct {
	log     *zap.Logger
	metrics *util.MetricsClient
	opts    *options.ClientOptions

	mu           sync.RWMutex
	client       *mongo.Client
	topology     *topology.Topology
	cursors      *cursorCache
	transactions *transactionCache
	databaseName string // Database name from connection string

	roundTripCtx    context.Context
	roundTripCancel func()

	// Pool monitoring
	poolStats   *PoolStats
	healthStats *HealthStats
}

func extractTopology(c *mongo.Client) *topology.Topology {
	e := reflect.ValueOf(c).Elem()
	d := e.FieldByName("deployment")
	d = reflect.NewAt(d.Type(), unsafe.Pointer(d.UnsafeAddr())).Elem() // #nosec G103
	return d.Interface().(*topology.Topology)
}

// createPoolMonitor creates a pool monitor that tracks connection pool events
func createPoolMonitor(log *zap.Logger, metrics *util.MetricsClient, databaseName string, poolStats *PoolStats) *event.PoolMonitor {
	return &event.PoolMonitor{
		Event: func(evt *event.PoolEvent) {
			poolStats.mu.Lock()
			defer poolStats.mu.Unlock()

			poolStats.lastUpdated = time.Now()

			switch evt.Type {
			case "ConnectionPoolCreated":
				// Extract max pool size from pool options
				if evt.PoolOptions != nil {
					poolStats.maxPoolSize = int64(evt.PoolOptions.MaxPoolSize)
				}
				log.Debug("Connection pool created",
					zap.String("database", databaseName),
					zap.Int64("max_pool_size", poolStats.maxPoolSize))

			case "ConnectionCreated":
				poolStats.totalConnections++
				log.Debug("Connection created",
					zap.String("database", databaseName),
					zap.Int64("total_connections", poolStats.totalConnections))

			case "ConnectionClosed":
				poolStats.totalConnections--
				if poolStats.totalConnections < 0 {
					poolStats.totalConnections = 0
				}
				log.Debug("Connection closed",
					zap.String("database", databaseName),
					zap.Int64("total_connections", poolStats.totalConnections))

			case "ConnectionCheckedOut":
				poolStats.activeConnections++
				poolStats.checkoutCount++
				log.Debug("Connection checked out",
					zap.String("database", databaseName),
					zap.Int64("active_connections", poolStats.activeConnections))

			case "ConnectionCheckedIn":
				poolStats.activeConnections--
				poolStats.checkinCount++
				if poolStats.activeConnections < 0 {
					poolStats.activeConnections = 0
				}
				log.Debug("Connection checked in",
					zap.String("database", databaseName),
					zap.Int64("active_connections", poolStats.activeConnections))

			case "ConnectionCheckOutFailed":
				poolStats.checkoutFailures++
				log.Debug("Connection checkout failed",
					zap.String("database", databaseName),
					zap.Int64("checkout_failures", poolStats.checkoutFailures))
			}

			// Update metrics - only for non-dynamic database names
			// Dynamic database names will be handled by operation-level metrics
			if metrics != nil && databaseName != "dynamic" {
				tags := []string{fmt.Sprintf("database:%s", databaseName)}

				// Calculate utilization ratio
				utilization := 0.0
				if poolStats.maxPoolSize > 0 {
					utilization = float64(poolStats.activeConnections) / float64(poolStats.maxPoolSize)
				}

				log.Debug("Updating MongoDB pool metrics",
					zap.String("database", databaseName),
					zap.Int64("active_connections", poolStats.activeConnections),
					zap.Int64("max_pool_size", poolStats.maxPoolSize),
					zap.Float64("utilization", utilization))

				_ = metrics.Gauge("mongodb_pool_active_connections", float64(poolStats.activeConnections), tags, 1)
				_ = metrics.Gauge("mongodb_pool_total_connections", float64(poolStats.totalConnections), tags, 1)
				_ = metrics.Gauge("mongodb_pool_max_connections", float64(poolStats.maxPoolSize), tags, 1)
				_ = metrics.Gauge("mongodb_pool_utilization_ratio", utilization, tags, 1)
				_ = metrics.Gauge("mongodb_pool_checkout_total", float64(poolStats.checkoutCount), tags, 1)
				_ = metrics.Gauge("mongodb_pool_checkin_total", float64(poolStats.checkinCount), tags, 1)
				_ = metrics.Gauge("mongodb_pool_checkout_failures_total", float64(poolStats.checkoutFailures), tags, 1)
			}
		},
	}
}

func extractDatabaseName(opts *options.ClientOptions) string {
	// Extract database name from connection string
	uri := opts.GetURI()
	if uri == "" {
		return "default"
	}

	// Parse the URI to extract database name
	// Format: mongodb://[username:password@]host[:port]/database[?options]
	if strings.Contains(uri, "/") {
		parts := strings.Split(uri, "/")
		if len(parts) >= 2 {
			dbPart := parts[len(parts)-1]
			// Remove query parameters
			if strings.Contains(dbPart, "?") {
				dbPart = strings.Split(dbPart, "?")[0]
			}
			// Check if this looks like a host:port instead of a database name
			// If it contains a colon and no dots, it's likely host:port
			if strings.Contains(dbPart, ":") && !strings.Contains(dbPart, ".") {
				return "default"
			}
			if dbPart != "" {
				return dbPart
			}
		}
	}

	return "default"
}

func Connect(log *zap.Logger, metrics *util.MetricsClient, opts *options.ClientOptions, ping bool) (*Mongo, error) {
	// timeout shouldn't be hit if ping == false, as Connect doesn't block the current goroutine
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	// Create pool stats and health stats
	poolStats := &PoolStats{}
	healthStats := &HealthStats{}

	// Add pool monitor to client options
	// For wildcard routes, we'll use a dynamic approach to track database names
	databaseName := extractDatabaseName(opts)
	if databaseName == "default" || databaseName == "" {
		// For wildcard routes, we'll track database names dynamically
		databaseName = "dynamic"
	}
	poolMonitor := createPoolMonitor(log, metrics, databaseName, poolStats)
	opts = opts.SetPoolMonitor(poolMonitor)

	var err error
	log.Info("Connect")
	c, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, err
	}

	if ping {
		log.Info("Ping")
		err = c.Ping(ctx, readpref.Primary())
		if err != nil {
			return nil, err
		}
		log.Info("Pong")
	}

	t := extractTopology(c)
	go topologyMonitor(log, t)

	rtCtx, rtCancel := context.WithCancel(context.Background())
	m := Mongo{
		log:             log,
		metrics:         metrics,
		opts:            opts,
		client:          c,
		topology:        t,
		cursors:         newCursorCache(),
		transactions:    newTransactionCache(),
		databaseName:    databaseName,
		roundTripCtx:    rtCtx,
		roundTripCancel: rtCancel,
		poolStats:       poolStats,
		healthStats:     healthStats,
	}
	go m.cacheMonitor()

	return &m, nil
}

func (m *Mongo) Description() description.Topology {
	return m.topology.Description()
}

func (m *Mongo) DatabaseName() string {
	return m.databaseName
}

// GetPoolStats returns the current connection pool statistics
func (m *Mongo) GetPoolStats() map[string]interface{} {
	if m.poolStats == nil {
		return map[string]interface{}{}
	}

	m.poolStats.mu.RLock()
	defer m.poolStats.mu.RUnlock()

	utilization := 0.0
	if m.poolStats.maxPoolSize > 0 {
		utilization = float64(m.poolStats.activeConnections) / float64(m.poolStats.maxPoolSize)
	}

	return map[string]interface{}{
		"active_connections": m.poolStats.activeConnections,
		"total_connections":  m.poolStats.totalConnections,
		"max_pool_size":      m.poolStats.maxPoolSize,
		"utilization_ratio":  utilization,
		"checkout_count":     m.poolStats.checkoutCount,
		"checkin_count":      m.poolStats.checkinCount,
		"checkout_failures":  m.poolStats.checkoutFailures,
		"last_updated":       m.poolStats.lastUpdated,
	}
}

// GetHealthStats returns the current health statistics
func (m *Mongo) GetHealthStats() map[string]interface{} {
	if m.healthStats == nil {
		return map[string]interface{}{}
	}

	m.healthStats.mu.RLock()
	defer m.healthStats.mu.RUnlock()

	return map[string]interface{}{
		"is_healthy":         m.healthStats.isHealthy,
		"last_health_check":  m.healthStats.lastHealthCheck,
		"health_check_count": m.healthStats.healthCheckCount,
		"health_failures":    m.healthStats.healthFailures,
		"last_error":         m.healthStats.lastError,
		"avg_response_time":  m.healthStats.avgResponseTime,
	}
}

// CheckHealth performs a health check on the MongoDB connection
func (m *Mongo) CheckHealth() error {
	if m.healthStats == nil {
		return fmt.Errorf("health stats not initialized")
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.healthStats.mu.Lock()
	defer m.healthStats.mu.Unlock()

	m.healthStats.healthCheckCount++

	// Perform ping
	err := m.client.Ping(ctx, readpref.Primary())
	responseTime := time.Since(start)

	if err != nil {
		m.healthStats.isHealthy = false
		m.healthStats.healthFailures++
		m.healthStats.lastError = err
		m.healthStats.lastHealthCheck = time.Now()

		m.log.Debug("Health check failed",
			zap.String("database", m.databaseName),
			zap.Error(err),
			zap.Duration("response_time", responseTime))

		return err
	}

	// Update health stats
	m.healthStats.isHealthy = true
	m.healthStats.lastError = nil
	m.healthStats.lastHealthCheck = time.Now()

	// Update average response time (simple moving average)
	if m.healthStats.avgResponseTime == 0 {
		m.healthStats.avgResponseTime = responseTime
	} else {
		m.healthStats.avgResponseTime = (m.healthStats.avgResponseTime + responseTime) / 2
	}

	m.log.Debug("Health check passed",
		zap.String("database", m.databaseName),
		zap.Duration("response_time", responseTime),
		zap.Duration("avg_response_time", m.healthStats.avgResponseTime))

	return nil
}

// IsHealthy returns whether the MongoDB connection is currently healthy
func (m *Mongo) IsHealthy() bool {
	if m.healthStats == nil {
		return false
	}

	m.healthStats.mu.RLock()
	defer m.healthStats.mu.RUnlock()

	// Consider unhealthy if no health check in last 30 seconds
	if time.Since(m.healthStats.lastHealthCheck) > 30*time.Second {
		return false
	}

	return m.healthStats.isHealthy
}

// GetClientOptions returns the client options used to create this MongoDB client
func (m *Mongo) GetClientOptions() *options.ClientOptions {
	return m.opts
}

// SetClientOptions sets the client options (for testing purposes)
func (m *Mongo) SetClientOptions(opts *options.ClientOptions) {
	m.opts = opts
}

func (m *Mongo) cacheGauge(name string, count float64) {
	if m.metrics != nil {
		_ = m.metrics.Gauge(name, count, []string{}, 1)
	}
}

func (m *Mongo) cacheMonitor() {
	for {
		m.cacheGauge("cursors", float64(m.cursors.count()))
		m.cacheGauge("transactions", float64(m.transactions.count()))
		time.Sleep(1 * time.Second)
	}
}

func (m *Mongo) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client == nil {
		// already closed
		return
	}

	m.roundTripCancel()

	m.log.Info("Disconnect")
	ctx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
	defer cancel()
	err := m.client.Disconnect(ctx)
	m.client = nil
	if err != nil {
		m.log.Info("Error disconnecting", zap.Error(err))
	}
}

// UpdatePoolMetricsForDatabase updates pool metrics for a specific database
func (m *Mongo) UpdatePoolMetricsForDatabase(databaseName string) {
	if m.metrics == nil || m.poolStats == nil {
		return
	}

	m.poolStats.mu.RLock()
	defer m.poolStats.mu.RUnlock()

	tags := []string{fmt.Sprintf("database:%s", databaseName)}

	// Calculate utilization ratio
	utilization := 0.0
	if m.poolStats.maxPoolSize > 0 {
		utilization = float64(m.poolStats.activeConnections) / float64(m.poolStats.maxPoolSize)
	}

	m.log.Debug("Updating database-specific pool metrics",
		zap.String("database", databaseName),
		zap.Int64("active_connections", m.poolStats.activeConnections),
		zap.Int64("max_pool_size", m.poolStats.maxPoolSize),
		zap.Float64("utilization", utilization))

	_ = m.metrics.Gauge("mongodb_pool_active_connections", float64(m.poolStats.activeConnections), tags, 1)
	_ = m.metrics.Gauge("mongodb_pool_total_connections", float64(m.poolStats.totalConnections), tags, 1)
	_ = m.metrics.Gauge("mongodb_pool_max_connections", float64(m.poolStats.maxPoolSize), tags, 1)
	_ = m.metrics.Gauge("mongodb_pool_utilization_ratio", utilization, tags, 1)
	_ = m.metrics.Gauge("mongodb_pool_checkout_total", float64(m.poolStats.checkoutCount), tags, 1)
	_ = m.metrics.Gauge("mongodb_pool_checkin_total", float64(m.poolStats.checkinCount), tags, 1)
	_ = m.metrics.Gauge("mongodb_pool_checkout_failures_total", float64(m.poolStats.checkoutFailures), tags, 1)
}

func (m *Mongo) RoundTrip(msg *Message, tags []string) (_ *Message, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var addr address.Address
	defer func() {
		if err != nil {
			cursorID, _ := msg.Op.CursorID()
			command, collection := msg.Op.CommandAndCollection()
			m.log.Error(
				"Round trip error",
				zap.Error(err),
				zap.Int64("cursor_id", cursorID),
				zap.Int32("op_code", int32(msg.Op.OpCode())),
				zap.String("address", addr.String()),
				zap.String("command", string(command)),
				zap.String("collection", collection),
			)
		}
	}()

	if m.client == nil {
		return nil, errors.New("connection closed")
	}

	// Check health before operation
	if !m.IsHealthy() {
		m.log.Debug("Connection unhealthy, performing health check",
			zap.String("database", m.databaseName))
		if healthErr := m.CheckHealth(); healthErr != nil {
			m.log.Warn("Health check failed, proceeding with operation",
				zap.String("database", m.databaseName),
				zap.Error(healthErr))
		}
	}

	// Retry logic with exponential backoff
	maxRetries := 3
	baseDelay := 100 * time.Millisecond

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// CursorID is pinned to a server by CursorID-collection name key
		// Transaction is pinned to a server by the issued lsid
		requestCursorID, _ := msg.Op.CursorID()
		requestCommand, collection := msg.Op.CommandAndCollection()
		transactionDetails := msg.Op.TransactionDetails()

		server, err := m.selectServer(requestCursorID, collection, transactionDetails)
		if err != nil {
			// Record server selection error
			if m.metrics != nil {
				// Extract database name from collection
				databaseName := "default"
				if collection != "" {
					parts := strings.SplitN(collection, ".", 2)
					if len(parts) >= 2 {
						databaseName = parts[0]
					}
				}
				errorTags := []string{
					fmt.Sprintf("database:%s", databaseName),
					fmt.Sprintf("error_type:%s", "server_selection_error"),
				}
				_ = m.metrics.Incr("error", errorTags, 1)
			}

			if attempt < maxRetries {
				delay := baseDelay * time.Duration(1<<uint(attempt)) // Exponential backoff
				m.log.Debug("Server selection failed, retrying",
					zap.String("database", m.databaseName),
					zap.Int("attempt", attempt+1),
					zap.Duration("delay", delay),
					zap.Error(err))
				time.Sleep(delay)
				continue
			}
			return nil, err
		}

		// Record server selection success
		if m.metrics != nil {
			// Extract database name from the operation
			databaseName := "default"
			if msg.Op != nil {
				// Try to extract database name from the operation
				if dbName := msg.Op.DatabaseName(); dbName != "" {
					databaseName = dbName
				} else if collection != "" {
					// Fallback to collection name parsing
					parts := strings.SplitN(collection, ".", 2)
					if len(parts) >= 2 {
						databaseName = parts[0]
					}
				}
			}
			_ = m.metrics.Incr("server_selection", []string{fmt.Sprintf("database:%s", databaseName)}, 1)
			
			// Update pool metrics with the actual database name
			// This ensures pool metrics show the actual database names instead of hardcoded values
			m.UpdatePoolMetricsForDatabase(databaseName)
		}

		conn, err := m.checkoutConnection(server)
		if err != nil {
			// Record connection checkout error
			if m.metrics != nil {
				// Extract database name from collection
				databaseName := "default"
				if collection != "" {
					parts := strings.SplitN(collection, ".", 2)
					if len(parts) >= 2 {
						databaseName = parts[0]
					}
				}
				errorTags := []string{
					fmt.Sprintf("database:%s", databaseName),
					fmt.Sprintf("error_type:%s", "connection_checkout_error"),
				}
				_ = m.metrics.Incr("error", errorTags, 1)
			}

			if attempt < maxRetries {
				delay := baseDelay * time.Duration(1<<uint(attempt))
				m.log.Debug("Connection checkout failed, retrying",
					zap.String("database", m.databaseName),
					zap.Int("attempt", attempt+1),
					zap.Duration("delay", delay),
					zap.Error(err))
				time.Sleep(delay)
				continue
			}
			return nil, err
		}

		addr = conn.Address()
		tags = append(
			tags,
			fmt.Sprintf("address:%s", conn.Address().String()),
		)

		defer func() {
			err := conn.Close()
			if err != nil {
				m.log.Error("Error closing Mongo connection", zap.Error(err), zap.String("address", addr.String()))
			}
		}()

		// see https://github.com/mongodb/mongo-go-driver/blob/v1.7.2/x/mongo/driver/operation.go#L430-L432
		ep, ok := server.(driver.ErrorProcessor)
		if !ok {
			if attempt < maxRetries {
				delay := baseDelay * time.Duration(1<<uint(attempt))
				m.log.Debug("Server ErrorProcessor assertion failed, retrying",
					zap.String("database", m.databaseName),
					zap.Int("attempt", attempt+1),
					zap.Duration("delay", delay))
				time.Sleep(delay)
				continue
			}
			return nil, errors.New("server ErrorProcessor type assertion failed")
		}

		unacknowledged := msg.Op.Unacknowledged()
		wm, err := m.roundTrip(conn, msg.Wm, unacknowledged, tags)
		if err != nil {
			m.processError(err, ep, addr, conn)

			// Check if this is a retryable error
			if attempt < maxRetries && m.isRetryableError(err) {
				delay := baseDelay * time.Duration(1<<uint(attempt))
				m.log.Debug("Round trip failed with retryable error, retrying",
					zap.String("database", m.databaseName),
					zap.Int("attempt", attempt+1),
					zap.Duration("delay", delay),
					zap.Error(err))
				time.Sleep(delay)
				continue
			}
			return nil, err
		}

		if unacknowledged {
			return &Message{}, nil
		}

		op, err := Decode(wm)
		if err != nil {
			if attempt < maxRetries {
				delay := baseDelay * time.Duration(1<<uint(attempt))
				m.log.Debug("Message decode failed, retrying",
					zap.String("database", m.databaseName),
					zap.Int("attempt", attempt+1),
					zap.Duration("delay", delay),
					zap.Error(err))
				time.Sleep(delay)
				continue
			}
			return nil, err
		}

		// check if an error is returned in the server response
		opErr := op.Error()
		if opErr != nil {
			// process the error, but don't return it as we still want to forward the response to the client
			m.processError(opErr, ep, addr, conn)
		}

		if responseCursorID, ok := op.CursorID(); ok {
			if responseCursorID != 0 {
				m.cursors.add(responseCursorID, collection, server)
				// Record cursor opened
				if m.metrics != nil {
					// Extract database name from the operation
					databaseName := "default"
					if msg.Op != nil {
						// Try to extract database name from the operation
						if dbName := msg.Op.DatabaseName(); dbName != "" {
							databaseName = dbName
						} else if collection != "" {
							// Fallback to collection name parsing
							parts := strings.SplitN(collection, ".", 2)
							if len(parts) >= 2 {
								databaseName = parts[0]
							}
						}
					}
					_ = m.metrics.Incr("cursor_opened", []string{fmt.Sprintf("database:%s", databaseName)}, 1)
				}
			} else if requestCursorID != 0 {
				m.cursors.remove(requestCursorID, collection)
				// Record cursor closed
				if m.metrics != nil {
					// Extract database name from the operation
					databaseName := "default"
					if msg.Op != nil {
						// Try to extract database name from the operation
						if dbName := msg.Op.DatabaseName(); dbName != "" {
							databaseName = dbName
						} else if collection != "" {
							// Fallback to collection name parsing
							parts := strings.SplitN(collection, ".", 2)
							if len(parts) >= 2 {
								databaseName = parts[0]
							}
						}
					}
					_ = m.metrics.Incr("cursor_closed", []string{fmt.Sprintf("database:%s", databaseName)}, 1)
				}
			}
		}

		if transactionDetails != nil {
			if transactionDetails.IsStartTransaction {
				m.transactions.add(transactionDetails.LsID, server)
			} else {
				if requestCommand == AbortTransaction || requestCommand == CommitTransaction {
					m.log.Debug("Removing transaction from the cache", zap.String("reqCommand", string(requestCommand)))
					m.transactions.remove(transactionDetails.LsID)

					// Record transaction metrics
					if m.metrics != nil {
						// Extract database name from the operation
						databaseName := "default"
						if msg.Op != nil {
							// Try to extract database name from the operation
							if dbName := msg.Op.DatabaseName(); dbName != "" {
								databaseName = dbName
							} else if collection != "" {
								// Fallback to collection name parsing
								parts := strings.SplitN(collection, ".", 2)
								if len(parts) >= 2 {
									databaseName = parts[0]
								}
							}
						}
						if requestCommand == CommitTransaction {
							_ = m.metrics.Incr("transaction_committed", []string{fmt.Sprintf("database:%s", databaseName)}, 1)
						} else if requestCommand == AbortTransaction {
							_ = m.metrics.Incr("transaction_aborted", []string{fmt.Sprintf("database:%s", databaseName)}, 1)
						}
					}
				}
			}
		}

		// Success! Return the response
		return &Message{
			Wm: wm,
			Op: op,
		}, nil
	}

	// This should never be reached, but just in case
	return nil, errors.New("max retries exceeded")
}

// isRetryableError determines if an error is retryable
func (m *Mongo) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Network-related errors that are typically retryable
	retryableErrors := []string{
		"connection refused",
		"connection reset",
		"connection timeout",
		"network is unreachable",
		"no route to host",
		"temporary failure",
		"server selection error",
		"context deadline exceeded",
		"EOF",
		"socket was unexpectedly closed",
	}

	for _, retryableErr := range retryableErrors {
		if strings.Contains(strings.ToLower(errStr), retryableErr) {
			return true
		}
	}

	return false
}

func (m *Mongo) selectServer(requestCursorID int64, collection string, transDetails *TransactionDetails) (server driver.Server, err error) {
	defer func(start time.Time) {
		if m.metrics != nil {
			_ = m.metrics.Timing("server_selection", time.Since(start), []string{fmt.Sprintf("success:%v", err == nil)}, 1)
		}
	}(time.Now())

	// Check for a pinned server based on current transaction lsid first
	if transDetails != nil {
		if server, ok := m.transactions.peek(transDetails.LsID); ok {
			m.log.Debug("found cached transaction", zap.String("lsid", fmt.Sprintf("%+v", transDetails)))
			return server, nil
		}
	}

	// Search for pinned cursor then
	if requestCursorID != 0 {
		server, ok := m.cursors.peek(requestCursorID, collection)
		if ok {
			m.log.Debug("Cached cursorID has been found", zap.Int64("cursor", requestCursorID), zap.String("collection", collection))
			return server, nil
		}
	}

	// Select a server
	selector := description.CompositeSelector([]description.ServerSelector{
		description.ReadPrefSelector(readpref.Primary()),   // ignored by sharded clusters
		description.LatencySelector(15 * time.Millisecond), // default localThreshold for the client
	})
	return m.topology.SelectServer(m.roundTripCtx, selector)
}

func (m *Mongo) checkoutConnection(server driver.Server) (conn driver.Connection, err error) {
	defer func(start time.Time) {
		addr := ""
		if conn != nil {
			addr = conn.Address().String()
		}
		if m.metrics != nil {
			_ = m.metrics.Timing("checkout_connection", time.Since(start), []string{
				fmt.Sprintf("address:%s", addr),
				fmt.Sprintf("success:%v", err == nil),
			}, 1)
		}
	}(time.Now())

	conn, err = server.Connection(m.roundTripCtx)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// see https://github.com/mongodb/mongo-go-driver/blob/v1.7.2/x/mongo/driver/operation.go#L664-L681
func (m *Mongo) roundTrip(conn driver.Connection, req []byte, unacknowledged bool, tags []string) (res []byte, err error) {
	defer func(start time.Time) {
		if m.metrics != nil {
			tags = append(tags, fmt.Sprintf("success:%v", err == nil))

			_ = m.metrics.Distribution("request_size", float64(len(req)), tags, 1)
			if err == nil && !unacknowledged {
				// There is no response size for unacknowledged writes.
				_ = m.metrics.Distribution("response_size", float64(len(res)), tags, 1)
			}

			_ = m.metrics.Timing("round_trip", time.Since(start), tags, 1)
		}
	}(time.Now())

	if err = conn.WriteWireMessage(m.roundTripCtx, req); err != nil {
		return nil, wrapNetworkError(err)
	}

	if unacknowledged {
		return nil, nil
	}

	if res, err = conn.ReadWireMessage(m.roundTripCtx); err != nil {
		return nil, wrapNetworkError(err)
	}

	return res, nil
}

func wrapNetworkError(err error) error {
	labels := []string{driver.NetworkError}
	return driver.Error{Message: err.Error(), Labels: labels, Wrapped: err}
}

// Process the error with the given ErrorProcessor, returning true if processing causes the topology to change
func (m *Mongo) processError(err error, ep driver.ErrorProcessor, addr address.Address, conn driver.Connection) {
	last := m.Description()

	// gather fields for logging
	fields := []zap.Field{
		zap.String("address", addr.String()),
		zap.Error(err),
	}
	if derr, ok := err.(driver.Error); ok {
		fields = append(fields, zap.Int32("error_code", derr.Code))
		fields = append(fields, zap.Strings("error_labels", derr.Labels))
		fields = append(fields, zap.NamedError("error_wrapped", derr.Wrapped))
	}
	if werr, ok := err.(driver.WriteConcernError); ok {
		fields = append(fields, zap.Int64("error_code", werr.Code))
	}

	// process the error
	ep.ProcessError(err, conn)

	// log if the error changed the topology
	if errorChangesTopology(err) {
		desc := m.Description()

		fields = append(fields, topologyChangedFields(&last, &desc)...)
		m.log.Error("Topology changing error", fields...)
	}
}

// see https://github.com/mongodb/mongo-go-driver/blob/v1.7.2/x/mongo/driver/topology/server.go#L432-L505
func errorChangesTopology(err error) bool {
	if cerr, ok := err.(driver.Error); ok && (cerr.NodeIsRecovering() || cerr.NotPrimary()) {
		return true
	}
	if wcerr, ok := err.(driver.WriteConcernError); ok && (wcerr.NodeIsRecovering() || wcerr.NotPrimary()) {
		return true
	}

	wrappedConnErr := unwrapConnectionError(err)
	if wrappedConnErr == nil {
		return false
	}

	// Ignore transient timeout errors.
	if netErr, ok := wrappedConnErr.(net.Error); ok && netErr.Timeout() {
		return false
	}
	if wrappedConnErr == context.Canceled || wrappedConnErr == context.DeadlineExceeded {
		return false
	}

	return true
}

// see https://github.com/mongodb/mongo-go-driver/blob/v1.7.2/x/mongo/driver/topology/server.go#L949-L969
func unwrapConnectionError(err error) error {
	connErr, ok := err.(topology.ConnectionError)
	if ok {
		return connErr.Wrapped
	}

	driverErr, ok := err.(driver.Error)
	if !ok || !driverErr.NetworkError() {
		return nil
	}

	connErr, ok = driverErr.Wrapped.(topology.ConnectionError)
	if ok {
		return connErr.Wrapped
	}

	return nil
}

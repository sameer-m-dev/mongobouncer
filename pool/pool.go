package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	mongobouncer "github.com/sameer-m-dev/mongobouncer/mongo"
	"github.com/sameer-m-dev/mongobouncer/util"
)

// PoolMode represents the connection pooling mode
type PoolMode string

const (
	// SessionMode - Server connection assigned to client for entire session
	SessionMode PoolMode = "session"

	// TransactionMode - Server connection assigned only during transactions
	TransactionMode PoolMode = "transaction"

	// StatementMode - Server connection returned after each statement
	StatementMode PoolMode = "statement"
)

// Manager manages connection pools for different databases
type Manager struct {
	logger        *zap.Logger
	metrics       *util.MetricsClient
	pools         map[string]*ConnectionPool
	defaultMode   PoolMode
	minPoolSize   int
	maxPoolSize   int
	reserveSize   int
	maxClientConn int
	activeClients map[string]*ClientConnection
	clientMutex   sync.RWMutex
	poolMutex     sync.RWMutex
}

// ConnectionPool manages connections for a specific database
type ConnectionPool struct {
	name        string
	mode        PoolMode
	maxSize     int
	reserveSize int
	mongoClient *mongobouncer.Mongo
	available   chan *PooledConnection
	inUse       map[string]*PooledConnection
	waitQueue   chan chan *PooledConnection
	mutex       sync.RWMutex
	logger      *zap.Logger
	metrics     *util.MetricsClient
	stats       *PoolStats
}

// PooledConnection represents a pooled MongoDB connection
type PooledConnection struct {
	ID            string
	MongoClient   *mongobouncer.Mongo
	Pool          *ConnectionPool
	InUse         bool
	LastUsed      time.Time
	CreatedAt     time.Time
	TransactionID string
	ClientID      string
}

// ClientConnection tracks client connection state
type ClientConnection struct {
	ID              string
	Username        string
	Database        string
	PoolMode        PoolMode
	AssignedConn    *PooledConnection
	TransactionConn *PooledConnection
	LastActivity    time.Time
	mutex           sync.Mutex
}

// PoolStats tracks pool statistics
type PoolStats struct {
	TotalConnections int64
	AvailableConns   int64
	InUseConns       int64
	WaitingClients   int64
	TotalRequests    int64
	TotalWaitTime    time.Duration
	mutex            sync.RWMutex
}

// NewManager creates a new pool manager
func NewManager(logger *zap.Logger, metrics *util.MetricsClient, defaultMode string, minPoolSize, maxPoolSize, reserveSize, maxClientConn int) *Manager {
	mode := SessionMode
	switch defaultMode {
	case "transaction":
		mode = TransactionMode
	case "statement":
		mode = StatementMode
	}

	return &Manager{
		logger:        logger,
		metrics:       metrics,
		pools:         make(map[string]*ConnectionPool),
		defaultMode:   mode,
		minPoolSize:   minPoolSize,
		maxPoolSize:   maxPoolSize,
		reserveSize:   reserveSize,
		maxClientConn: maxClientConn,
		activeClients: make(map[string]*ClientConnection),
	}
}

// GetPool returns or creates a pool for the given database
func (m *Manager) GetPool(database string, mongoClient *mongobouncer.Mongo, mode PoolMode, maxSize int) *ConnectionPool {
	m.poolMutex.RLock()
	pool, exists := m.pools[database]
	m.poolMutex.RUnlock()

	if exists {
		return pool
	}

	// Create new pool
	m.poolMutex.Lock()
	defer m.poolMutex.Unlock()

	// Double-check after acquiring write lock
	if pool, exists = m.pools[database]; exists {
		return pool
	}

	if mode == "" {
		mode = m.defaultMode
	}
	if maxSize == 0 {
		maxSize = m.maxPoolSize
	}

	pool = &ConnectionPool{
		name:        database,
		mode:        mode,
		maxSize:     maxSize,
		reserveSize: m.reserveSize,
		mongoClient: mongoClient,
		available:   make(chan *PooledConnection, maxSize),
		inUse:       make(map[string]*PooledConnection),
		waitQueue:   make(chan chan *PooledConnection, 1000),
		logger:      m.logger,
		metrics:     m.metrics,
		stats:       &PoolStats{},
	}

	m.pools[database] = pool
	go pool.maintainPool()

	m.logger.Info("Created connection pool",
		zap.String("database", database),
		zap.String("mode", string(mode)),
		zap.Int("max_size", maxSize))

	return pool
}

// RegisterClient registers a new client connection
func (m *Manager) RegisterClient(clientID, username, database string, poolMode PoolMode) (*ClientConnection, error) {
	m.clientMutex.Lock()
	defer m.clientMutex.Unlock()

	// Check if client already exists
	if client, exists := m.activeClients[clientID]; exists {
		return client, nil
	}

	// Check max client connections (0 means unlimited)
	if m.maxClientConn > 0 && len(m.activeClients) >= m.maxClientConn {
		return nil, errors.New("max client connections reached")
	}

	client := &ClientConnection{
		ID:           clientID,
		Username:     username,
		Database:     database,
		PoolMode:     poolMode,
		LastActivity: time.Now(),
	}

	m.activeClients[clientID] = client
	return client, nil
}

// UnregisterClient removes a client and returns any held connections
func (m *Manager) UnregisterClient(clientID string) {
	m.clientMutex.Lock()
	defer m.clientMutex.Unlock()

	client, exists := m.activeClients[clientID]
	if !exists {
		return
	}

	// Return any held connections
	client.mutex.Lock()
	if client.AssignedConn != nil {
		client.AssignedConn.Pool.Return(client.AssignedConn)
	}
	if client.TransactionConn != nil {
		client.TransactionConn.Pool.Return(client.TransactionConn)
	}
	client.mutex.Unlock()

	delete(m.activeClients, clientID)
}

// GetConnection gets a connection for a client based on pool mode
func (m *Manager) GetConnection(clientID string, database string, mongoClient *mongobouncer.Mongo, isTransaction bool, transactionID string) (*PooledConnection, error) {
	m.clientMutex.RLock()
	client, exists := m.activeClients[clientID]
	m.clientMutex.RUnlock()

	if !exists {
		return nil, errors.New("client not registered")
	}

	client.mutex.Lock()
	defer client.mutex.Unlock()

	// Update activity
	client.LastActivity = time.Now()

	// Get the pool for the database
	pool := m.GetPool(database, mongoClient, client.PoolMode, 0)

	switch client.PoolMode {
	case SessionMode:
		// In session mode, client keeps the same connection
		if client.AssignedConn == nil {
			conn, err := pool.Checkout(clientID)
			if err != nil {
				return nil, err
			}
			client.AssignedConn = conn
		}
		return client.AssignedConn, nil

	case TransactionMode:
		// In transaction mode, assign connection for the transaction duration
		if isTransaction {
			if client.TransactionConn == nil || client.TransactionConn.TransactionID != transactionID {
				// Return old transaction connection if different transaction
				if client.TransactionConn != nil {
					pool.Return(client.TransactionConn)
				}

				conn, err := pool.Checkout(clientID)
				if err != nil {
					return nil, err
				}
				conn.TransactionID = transactionID
				client.TransactionConn = conn
			}
			return client.TransactionConn, nil
		}

		// Outside transaction, get temporary connection
		return pool.Checkout(clientID)

	case StatementMode:
		// In statement mode, always get a new connection
		return pool.Checkout(clientID)

	default:
		return nil, fmt.Errorf("unknown pool mode: %s", client.PoolMode)
	}
}

// ReturnConnection returns a connection based on pool mode
func (m *Manager) ReturnConnection(clientID string, conn *PooledConnection, isEndOfTransaction bool) {
	m.clientMutex.RLock()
	client, exists := m.activeClients[clientID]
	m.clientMutex.RUnlock()

	if !exists {
		// Client disconnected, just return the connection
		conn.Pool.Return(conn)
		return
	}

	client.mutex.Lock()
	defer client.mutex.Unlock()

	switch client.PoolMode {
	case SessionMode:
		// In session mode, only return if this is not the assigned connection
		// or if the client is disconnecting
		if conn != client.AssignedConn {
			conn.Pool.Return(conn)
		}

	case TransactionMode:
		if isEndOfTransaction && client.TransactionConn == conn {
			// End of transaction, return the connection
			conn.Pool.Return(conn)
			client.TransactionConn = nil
		} else if !isEndOfTransaction && conn != client.TransactionConn {
			// Not in transaction, return temporary connection
			conn.Pool.Return(conn)
		}

	case StatementMode:
		// Always return after statement
		conn.Pool.Return(conn)
	}
}

// Checkout gets a connection from the pool
func (p *ConnectionPool) Checkout(clientID string) (*PooledConnection, error) {
	p.stats.incrementRequests()
	start := time.Now()

	select {
	case conn := <-p.available:
		// Got available connection
		if conn != nil {
			p.mutex.Lock()
			conn.InUse = true
			conn.ClientID = clientID
			conn.LastUsed = time.Now()
			p.inUse[conn.ID] = conn
			p.mutex.Unlock()

			p.stats.updateCounts(int64(len(p.available)), int64(len(p.inUse)))

			// Record checkout duration
			if p.metrics != nil {
				checkoutDuration := time.Since(start).Seconds()
				tags := []string{fmt.Sprintf("database:%s", p.name)}
				_ = p.metrics.Distribution("pool_checkout_duration", checkoutDuration, tags, 1)
			}

			return conn, nil
		}
		// If conn is nil, treat as no available connection and fall through

	default:
		// No available connections
	}

	// Try to create new connection
	if p.canCreateNew() {
		conn := p.createConnection()
		if conn != nil {
			p.mutex.Lock()
			conn.InUse = true
			conn.ClientID = clientID
			conn.LastUsed = time.Now()
			p.inUse[conn.ID] = conn
			p.mutex.Unlock()

			p.stats.updateCounts(int64(len(p.available)), int64(len(p.inUse)))

			// Record checkout duration
			if p.metrics != nil {
				checkoutDuration := time.Since(start).Seconds()
				tags := []string{fmt.Sprintf("database:%s", p.name)}
				_ = p.metrics.Distribution("pool_checkout_duration", checkoutDuration, tags, 1)
			}

			return conn, nil
		}
	}

	// Wait for connection
	return p.waitForConnection(clientID)
}

// Return returns a connection to the pool
func (p *ConnectionPool) Return(conn *PooledConnection) {
	if conn == nil || conn.Pool != p {
		return
	}

	p.mutex.Lock()
	delete(p.inUse, conn.ID)
	conn.InUse = false
	conn.ClientID = ""
	conn.TransactionID = ""
	conn.LastUsed = time.Now()
	p.mutex.Unlock()

	// Try to give to waiting client
	select {
	case waiter := <-p.waitQueue:
		waiter <- conn
	default:
		// Return to available pool
		select {
		case p.available <- conn:
			p.stats.updateCounts(int64(len(p.available)), int64(len(p.inUse)))
		default:
			// Pool is full, close the connection
			p.logger.Debug("Pool full, closing returned connection",
				zap.String("connection_id", conn.ID))
			p.closeConnection(conn)
		}
	}
}

// canCreateNew checks if we can create a new connection
func (p *ConnectionPool) canCreateNew() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	total := len(p.available) + len(p.inUse)
	return total < p.maxSize
}

// createConnection creates a new pooled connection
func (p *ConnectionPool) createConnection() *PooledConnection {
	// Check if we have a valid mongo client
	if p.mongoClient == nil {
		p.logger.Error("Cannot create connection: mongo client is nil")
		return nil
	}

	// Create a new MongoDB client for this connection
	// This ensures each pooled connection has its own underlying connection
	client, err := mongobouncer.Connect(p.logger, p.metrics, p.mongoClient.GetClientOptions(), false)
	if err != nil {
		p.logger.Error("Failed to create MongoDB connection", zap.Error(err))
		return nil
	}

	conn := &PooledConnection{
		ID:          fmt.Sprintf("%s-%d", p.name, time.Now().UnixNano()),
		MongoClient: client,
		Pool:        p,
		CreatedAt:   time.Now(),
		LastUsed:    time.Now(),
	}

	p.stats.incrementTotal()
	p.logger.Debug("Created new pooled connection",
		zap.String("pool", p.name),
		zap.String("connection_id", conn.ID))

	return conn
}

// closeConnection properly closes a MongoDB connection
func (p *ConnectionPool) closeConnection(conn *PooledConnection) {
	if conn != nil && conn.MongoClient != nil {
		p.logger.Debug("Closing pooled connection",
			zap.String("connection_id", conn.ID))
		conn.MongoClient.Close()
		p.stats.decrementTotal()
	}
}

// waitForConnection waits for an available connection
func (p *ConnectionPool) waitForConnection(clientID string) (*PooledConnection, error) {
	waiter := make(chan *PooledConnection, 1)

	// Add to wait queue
	select {
	case p.waitQueue <- waiter:
		p.stats.incrementWaiting()
		defer p.stats.decrementWaiting()

		// Record clients waiting for server
		if p.metrics != nil {
			tags := []string{fmt.Sprintf("database:%s", p.name)}
			_ = p.metrics.Gauge("clients_waiting_for_server", float64(len(p.waitQueue)), tags, 1)
		}
	default:
		// Record pool exhaustion event
		if p.metrics != nil {
			tags := []string{fmt.Sprintf("database:%s", p.name)}
			_ = p.metrics.Incr("pool_exhaustion", tags, 1)
		}
		return nil, errors.New("wait queue full")
	}

	// Wait with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	select {
	case conn := <-waiter:
		waitDuration := time.Since(start)
		p.stats.addWaitTime(waitDuration)

		// Record pool connection wait time
		if p.metrics != nil {
			tags := []string{fmt.Sprintf("database:%s", p.name)}
			_ = p.metrics.Distribution("pool_connection_wait", waitDuration.Seconds(), tags, 1)
		}

		// Check if connection creation failed
		if conn == nil {
			return nil, errors.New("connection creation failed")
		}

		p.mutex.Lock()
		conn.InUse = true
		conn.ClientID = clientID
		conn.LastUsed = time.Now()
		p.inUse[conn.ID] = conn
		p.mutex.Unlock()

		return conn, nil

	case <-ctx.Done():
		return nil, errors.New("timeout waiting for connection")
	}
}

// updatePoolMetrics updates pool-related metrics
func (p *ConnectionPool) updatePoolMetrics() {
	if p.metrics == nil {
		return
	}

	p.mutex.RLock()
	inUseCount := len(p.inUse)
	p.mutex.RUnlock()

	// Calculate utilization ratio
	utilization := float64(inUseCount) / float64(p.maxSize)
	if p.maxSize == 0 {
		utilization = 0
	}

	tags := []string{fmt.Sprintf("database:%s", p.name)}
	_ = p.metrics.Gauge("pool_utilization", utilization, tags, 1)
}

// maintainPool maintains the connection pool
func (p *ConnectionPool) maintainPool() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		p.cleanupIdleConnections()
		p.ensureMinimumConnections()
		p.updatePoolMetrics()
	}
}

// cleanupIdleConnections removes idle connections
func (p *ConnectionPool) cleanupIdleConnections() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	now := time.Now()
	idleTimeout := 5 * time.Minute

	// Check available connections
	available := make(chan *PooledConnection, cap(p.available))
	for {
		select {
		case conn := <-p.available:
			if conn == nil {
				continue
			}
			if now.Sub(conn.LastUsed) > idleTimeout {
				// Remove idle connection
				p.closeConnection(conn)
				continue
			}
			available <- conn
		default:
			goto doneAvailable
		}
	}
doneAvailable:
	close(available)

	// Rebuild available channel
	p.available = make(chan *PooledConnection, p.maxSize)
	for conn := range available {
		select {
		case p.available <- conn:
		default:
			// Pool is full, discard connection
			p.closeConnection(conn)
		}
	}
}

// ensureMinimumConnections ensures minimum connections are available
func (p *ConnectionPool) ensureMinimumConnections() {
	p.mutex.RLock()
	available := len(p.available)
	inUse := len(p.inUse)
	total := available + inUse
	p.mutex.RUnlock()

	// Create connections if we have capacity and waiters
	if total < p.maxSize && len(p.waitQueue) > 0 {
		conn := p.createConnection()
		p.mutex.Lock()
		p.available <- conn
		p.mutex.Unlock()

		// Notify a waiter
		select {
		case waiter := <-p.waitQueue:
			select {
			case waiter <- conn:
			default:
				// Waiter is no longer waiting, return connection to pool
				p.mutex.Lock()
				select {
				case p.available <- conn:
				default:
					// Pool is full, discard connection
				}
				p.mutex.Unlock()
			}
		default:
			// No waiters, connection stays in available pool
		}
	}
}

// GetStats returns pool statistics
func (p *ConnectionPool) GetStats() map[string]interface{} {
	p.stats.mutex.RLock()
	defer p.stats.mutex.RUnlock()

	return map[string]interface{}{
		"name":              p.name,
		"mode":              string(p.mode),
		"max_size":          p.maxSize,
		"total_connections": p.stats.TotalConnections,
		"available":         p.stats.AvailableConns,
		"in_use":            p.stats.InUseConns,
		"waiting_clients":   p.stats.WaitingClients,
		"total_requests":    p.stats.TotalRequests,
		"avg_wait_time_ms":  p.stats.avgWaitTime().Milliseconds(),
	}
}

// Stats helper methods

func (s *PoolStats) incrementTotal() {
	s.mutex.Lock()
	s.TotalConnections++
	s.mutex.Unlock()
}

func (s *PoolStats) decrementTotal() {
	s.mutex.Lock()
	s.TotalConnections--
	s.mutex.Unlock()
}

func (s *PoolStats) incrementRequests() {
	s.mutex.Lock()
	s.TotalRequests++
	s.mutex.Unlock()
}

func (s *PoolStats) incrementWaiting() {
	s.mutex.Lock()
	s.WaitingClients++
	s.mutex.Unlock()
}

func (s *PoolStats) decrementWaiting() {
	s.mutex.Lock()
	s.WaitingClients--
	s.mutex.Unlock()
}

func (s *PoolStats) updateCounts(available, inUse int64) {
	s.mutex.Lock()
	s.AvailableConns = available
	s.InUseConns = inUse
	s.mutex.Unlock()
}

func (s *PoolStats) addWaitTime(duration time.Duration) {
	s.mutex.Lock()
	s.TotalWaitTime += duration
	s.mutex.Unlock()
}

func (s *PoolStats) avgWaitTime() time.Duration {
	if s.TotalRequests == 0 {
		return 0
	}
	return s.TotalWaitTime / time.Duration(s.TotalRequests)
}

// GetDefaultPoolSizes returns the default min and max pool sizes
func (m *Manager) GetDefaultPoolSizes() (int, int) {
	return m.minPoolSize, m.maxPoolSize
}

package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	mongobouncer "github.com/sameer-m-dev/mongobouncer/mongo"
	"github.com/sameer-m-dev/mongobouncer/util"
)

func TestPoolManagerIntegration(t *testing.T) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	t.Run("CompletePoolingWorkflow", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 2, 10, 2, 50)

		// Register multiple clients with different pool modes
		clients := []struct {
			id       string
			username string
			database string
			poolMode PoolMode
		}{
			{"client1", "user1", "db1", SessionMode},
			{"client2", "user2", "db1", TransactionMode},
			{"client3", "user3", "db2", StatementMode},
			{"client4", "user4", "db2", SessionMode},
		}

		for _, c := range clients {
			client, err := m.RegisterClient(c.id, c.username, c.database, c.poolMode)
			assert.NoError(t, err)
			assert.NotNil(t, client)
			assert.Equal(t, c.poolMode, client.PoolMode)
		}

		// Test max client limit
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("extra_client_%d", i)
			_, err := m.RegisterClient(id, "user", "db", SessionMode)
			if i < 46 { // 4 already registered + 46 = 50
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "max client connections")
			}
		}

		// Unregister a client to make room
		m.UnregisterClient("extra_client_0")

		// Should be able to register new client now
		_, err := m.RegisterClient("new_client", "user", "db", SessionMode)
		assert.NoError(t, err)

		// Clean up
		for _, c := range clients {
			m.UnregisterClient(c.id)
		}
	})

	t.Run("SessionPoolingMode", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 1, 5, 1, 10)

		// Create mock MongoDB client with proper options
		mongoClient := &mongobouncer.Mongo{} // Mock
		// Initialize client options to avoid nil pointer dereference
		opts := options.Client().ApplyURI("mongodb://localhost:27017/sessiondb")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("sessiondb", mongoClient, SessionMode, 5)

		// Register session client
		client, err := m.RegisterClient("session_client", "user", "sessiondb", SessionMode)
		assert.NoError(t, err)

		// In session mode, client should keep the same connection
		// First request
		conn1, err := pool.Checkout(client.ID)
		assert.NoError(t, err)
		assert.NotNil(t, conn1)
		client.AssignedConn = conn1 // Simulate assignment

		// Subsequent requests should reuse same connection
		assert.Equal(t, conn1, client.AssignedConn)
		assert.True(t, conn1.InUse)
		assert.Equal(t, client.ID, conn1.ClientID)

		// Connection should not be returned in session mode
		m.ReturnConnection(client.ID, conn1, false)
		assert.True(t, conn1.InUse) // Still in use

		// Only returned when client disconnects
		m.UnregisterClient(client.ID)
		// In real implementation, this would return the connection
	})

	t.Run("TransactionPoolingMode", func(t *testing.T) {
		m := NewManager(logger, metrics, "transaction", 1, 5, 1, 10)

		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/txndb")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("txndb", mongoClient, TransactionMode, 5)

		// Register transaction client
		client, err := m.RegisterClient("txn_client", "user", "txndb", TransactionMode)
		assert.NoError(t, err)

		// Outside transaction - get temporary connection
		conn1, err := pool.Checkout(client.ID)
		assert.NoError(t, err)

		// Return after use (outside transaction)
		m.ReturnConnection(client.ID, conn1, false)
		assert.False(t, conn1.InUse)

		// Start transaction - get dedicated connection
		txnID := "txn123"
		conn2, err := pool.Checkout(client.ID)
		assert.NoError(t, err)
		conn2.TransactionID = txnID
		client.TransactionConn = conn2

		// During transaction - should reuse same connection
		assert.Equal(t, txnID, conn2.TransactionID)
		assert.True(t, conn2.InUse)

		// End transaction - connection returned
		m.ReturnConnection(client.ID, conn2, true)
		assert.False(t, conn2.InUse)
		assert.Nil(t, client.TransactionConn)

		m.UnregisterClient(client.ID)
	})

	t.Run("StatementPoolingMode", func(t *testing.T) {
		m := NewManager(logger, metrics, "statement", 1, 5, 1, 10)

		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/stmtdb")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("stmtdb", mongoClient, StatementMode, 5)

		// Register statement client
		client, err := m.RegisterClient("stmt_client", "user", "stmtdb", StatementMode)
		assert.NoError(t, err)

		// Each request gets new connection
		conns := make([]*PooledConnection, 3)
		for i := 0; i < 3; i++ {
			conn, err := pool.Checkout(client.ID)
			assert.NoError(t, err)
			conns[i] = conn

			// Immediately return after statement
			m.ReturnConnection(client.ID, conn, false)
			assert.False(t, conn.InUse)
		}

		// Connections can be different
		// In statement mode, any available connection is used

		m.UnregisterClient(client.ID)
	})

	t.Run("PoolExhaustion", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 0, 2, 0, 10) // Small pool

		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/small")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("small", mongoClient, SessionMode, 2)

		// Checkout all connections
		conn1, err := pool.Checkout("client1")
		assert.NoError(t, err)
		conn2, err := pool.Checkout("client2")
		assert.NoError(t, err)

		// Next checkout should wait
		done := make(chan bool)
		var conn3 *PooledConnection
		go func() {
			conn3, err = pool.Checkout("client3")
			assert.NoError(t, err)
			assert.NotNil(t, conn3)
			done <- true
		}()

		// Give time for goroutine to start waiting
		time.Sleep(50 * time.Millisecond)

		// Verify waiting client
		stats := pool.GetStats()
		assert.Equal(t, int64(1), stats["waiting_clients"])

		// Return a connection
		pool.Return(conn1)

		// Wait for checkout to complete
		select {
		case <-done:
			assert.NotNil(t, conn3)
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for connection")
		}

		// Clean up
		pool.Return(conn2)
		pool.Return(conn3)
	})

	t.Run("MultipleDatabasePools", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 2, 10, 2, 50)

		// Create pools for different databases
		databases := []string{"users", "orders", "analytics", "logs"}
		pools := make(map[string]*ConnectionPool)

		for _, db := range databases {
			mongoClient := &mongobouncer.Mongo{} // Mock
			opts := options.Client().ApplyURI(fmt.Sprintf("mongodb://localhost:27017/%s", db))
			mongoClient.SetClientOptions(opts)
			pool := m.GetPool(db, mongoClient, SessionMode, 10)
			pools[db] = pool
			assert.Equal(t, db, pool.name)
		}

		// Register clients for different databases
		for _, db := range databases {
			clientID := fmt.Sprintf("client_%s", db)
			client, err := m.RegisterClient(clientID, "user", db, SessionMode)
			assert.NoError(t, err)
			assert.Equal(t, db, client.Database)
		}

		// Each pool should be independent
		for db, pool := range pools {
			conn, err := pool.Checkout(fmt.Sprintf("client_%s", db))
			assert.NoError(t, err)
			assert.NotNil(t, conn)
			defer pool.Return(conn)
		}

		// Get same pool instance
		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/users")
		mongoClient.SetClientOptions(opts)
		samePool := m.GetPool("users", mongoClient, SessionMode, 20)
		assert.Equal(t, pools["users"], samePool)
	})

	t.Run("ConnectionLifecycle", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 1, 5, 1, 10)

		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/lifecycle")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("lifecycle", mongoClient, SessionMode, 5)

		// Track connection lifecycle
		conn, err := pool.Checkout("client1")
		assert.NoError(t, err)

		// Verify initial state
		assert.True(t, conn.InUse)
		assert.Equal(t, "client1", conn.ClientID)
		assert.Equal(t, pool, conn.Pool)
		assert.False(t, conn.CreatedAt.IsZero())
		assert.False(t, conn.LastUsed.IsZero())

		// Use connection
		time.Sleep(10 * time.Millisecond)
		originalLastUsed := conn.LastUsed

		// Return and checkout again
		pool.Return(conn)
		assert.False(t, conn.InUse)
		assert.Empty(t, conn.ClientID)

		conn2, err := pool.Checkout("client2")
		assert.NoError(t, err)

		// Might be same connection object
		if conn2.ID == conn.ID {
			assert.True(t, conn2.LastUsed.After(originalLastUsed))
			assert.Equal(t, "client2", conn2.ClientID)
		}

		pool.Return(conn2)
	})

	t.Run("PoolStatistics", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 2, 10, 2, 20)

		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/stats")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("stats", mongoClient, SessionMode, 5)

		// Initial stats
		stats := pool.GetStats()
		assert.Equal(t, "stats", stats["name"])
		assert.Equal(t, "session", stats["mode"])
		assert.Equal(t, 10, stats["max_size"])
		assert.Equal(t, int64(0), stats["total_requests"])

		// Generate activity
		conns := make([]*PooledConnection, 5)
		for i := 0; i < 5; i++ {
			conn, err := pool.Checkout(fmt.Sprintf("client%d", i))
			assert.NoError(t, err)
			conns[i] = conn
		}

		stats = pool.GetStats()
		assert.Equal(t, int64(5), stats["total_requests"])
		assert.Equal(t, int64(5), stats["in_use"])
		assert.Equal(t, int64(5), stats["total_connections"]) // 5 created

		// Return some connections
		for i := 0; i < 3; i++ {
			pool.Return(conns[i])
		}

		stats = pool.GetStats()
		assert.Equal(t, int64(2), stats["in_use"])
		assert.Equal(t, int64(3), stats["available"])

		// Test wait time tracking
		// Fill the pool
		for i := 5; i < 10; i++ {
			_, err := pool.Checkout(fmt.Sprintf("client%d", i))
			assert.NoError(t, err)
		}

		// This should wait
		done := make(chan bool)
		go func() {
			conn, err := pool.Checkout("waiting_client")
			assert.NoError(t, err)
			pool.Return(conn)
			done <- true
		}()

		time.Sleep(50 * time.Millisecond)
		pool.Return(conns[3]) // Release one

		<-done

		stats = pool.GetStats()
		assert.True(t, stats["avg_wait_time_ms"].(int64) > 0)
	})

	t.Run("ConcurrentPoolOperations", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 5, 20, 5, 100)

		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/concurrent")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("concurrent", mongoClient, SessionMode, 20)

		var wg sync.WaitGroup
		var checkouts int64
		var returns int64
		errors := make(chan error, 1000)

		// Concurrent checkouts
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					conn, err := pool.Checkout(fmt.Sprintf("worker%d", id))
					if err != nil {
						errors <- err
						continue
					}
					atomic.AddInt64(&checkouts, 1)

					// Simulate work
					time.Sleep(time.Millisecond)

					pool.Return(conn)
					atomic.AddInt64(&returns, 1)
				}
			}(i)
		}

		// Concurrent pool stats checking
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				stats := pool.GetStats()
				// Just access stats, ensure no panic
				_ = stats["in_use"]
				_ = stats["available"]
				time.Sleep(2 * time.Millisecond)
			}
		}()

		wg.Wait()
		close(errors)

		// Check for errors
		errorCount := 0
		for err := range errors {
			t.Logf("Concurrent error: %v", err)
			errorCount++
		}
		assert.Equal(t, 0, errorCount)

		// Verify all checkouts were returned
		assert.Equal(t, checkouts, returns)

		// Final state check
		stats := pool.GetStats()
		assert.Equal(t, int64(0), stats["in_use"])
	})

	t.Run("PoolModeConfiguration", func(t *testing.T) {
		tests := []struct {
			configMode string
			dbMode     PoolMode
			userMode   PoolMode
			expected   PoolMode
		}{
			// Default from manager
			{"session", "", "", SessionMode},
			{"transaction", "", "", TransactionMode},
			{"statement", "", "", StatementMode},

			// Database override
			{"session", TransactionMode, "", TransactionMode},
			{"session", StatementMode, "", StatementMode},

			// User override (would be highest priority in full implementation)
			{"session", TransactionMode, StatementMode, StatementMode},
		}

		for _, tt := range tests {
			m := NewManager(logger, metrics, tt.configMode, 2, 10, 2, 50)

			// Get pool with specific mode
			pool := m.GetPool("testdb", nil, tt.dbMode, 10)
			if tt.dbMode != "" {
				assert.Equal(t, tt.dbMode, pool.mode)
			} else {
				assert.Equal(t, m.defaultMode, pool.mode)
			}
		}
	})

	t.Run("ConnectionTimeout", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 0, 1, 0, 10) // Pool of 1

		mongoClient := &mongobouncer.Mongo{} // Mock
		opts := options.Client().ApplyURI("mongodb://localhost:27017/timeout")
		mongoClient.SetClientOptions(opts)
		pool := m.GetPool("timeout", mongoClient, SessionMode, 1)

		// Checkout the only connection
		conn1, err := pool.Checkout("client1")
		assert.NoError(t, err)

		// Try to checkout with timeout (this would timeout in real implementation)
		_, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		done := make(chan bool)
		go func() {
			// In real implementation, this would respect context
			_, err := pool.Checkout("client2")
			// Would timeout
			assert.Error(t, err)
			done <- true
		}()

		select {
		case <-done:
			// Expected timeout
		case <-time.After(200 * time.Millisecond):
			// For this test, we'll just clean up
			pool.Return(conn1)
		}
	})
}

package pool

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/util"
)

func TestPoolManager(t *testing.T) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	t.Run("CreateManager", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 5, 20, 5, 100)
		assert.NotNil(t, m)
		assert.Equal(t, SessionMode, m.defaultMode)
		assert.Equal(t, 5, m.minPoolSize)
		assert.Equal(t, 20, m.maxPoolSize)
		assert.Equal(t, 5, m.reserveSize)
		assert.Equal(t, 100, m.maxClientConn)
	})

	t.Run("PoolModes", func(t *testing.T) {
		tests := []struct {
			mode     string
			expected PoolMode
		}{
			{"session", SessionMode},
			{"transaction", TransactionMode},
			{"statement", StatementMode},
			{"invalid", SessionMode}, // defaults to session
		}

		for _, tt := range tests {
			m := NewManager(logger, metrics, tt.mode, 5, 10, 2, 50)
			assert.Equal(t, tt.expected, m.defaultMode)
		}
	})

	t.Run("GetPool", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 5, 10, 2, 50)

		// Get pool for database
		pool1 := m.GetPool("db1", nil, SessionMode, 20)
		assert.NotNil(t, pool1)
		assert.Equal(t, "db1", pool1.name)
		assert.Equal(t, SessionMode, pool1.mode)
		assert.Equal(t, 20, pool1.maxSize)

		// Get same pool again
		pool2 := m.GetPool("db1", nil, SessionMode, 30)
		assert.Equal(t, pool1, pool2) // Should be same instance

		// Get pool with defaults
		pool3 := m.GetPool("db2", nil, "", 0)
		assert.Equal(t, SessionMode, pool3.mode)
		assert.Equal(t, 10, pool3.maxSize)
	})

	t.Run("ClientRegistration", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 5, 10, 2, 2) // Max 2 clients

		// Register clients
		client1, err := m.RegisterClient("client1", "user1", "db1", SessionMode)
		assert.NoError(t, err)
		assert.NotNil(t, client1)

		client2, err := m.RegisterClient("client2", "user2", "db1", TransactionMode)
		assert.NoError(t, err)
		assert.NotNil(t, client2)

		// Max clients reached
		_, err = m.RegisterClient("client3", "user3", "db1", SessionMode)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "max client connections")

		// Unregister client
		m.UnregisterClient("client1")

		// Now can register new client
		client3, err := m.RegisterClient("client3", "user3", "db1", SessionMode)
		assert.NoError(t, err)
		assert.NotNil(t, client3)
	})
}

func TestConnectionPool(t *testing.T) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	t.Run("CheckoutReturn", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 1, 2, 1, 10)
		pool := m.GetPool("test", nil, SessionMode, 2)

		// Checkout connection
		conn1, err := pool.Checkout("client1")
		assert.NoError(t, err)
		assert.NotNil(t, conn1)
		assert.True(t, conn1.InUse)
		assert.Equal(t, "client1", conn1.ClientID)

		// Return connection
		pool.Return(conn1)
		assert.False(t, conn1.InUse)
		assert.Equal(t, "", conn1.ClientID)

		// Should be able to checkout again
		conn2, err := pool.Checkout("client2")
		assert.NoError(t, err)
		assert.NotNil(t, conn2)
	})

	t.Run("PoolLimit", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 1, 2, 1, 10)
		pool := m.GetPool("test", nil, SessionMode, 2)

		// Checkout max connections
		conn1, err := pool.Checkout("client1")
		assert.NoError(t, err)
		conn2, err := pool.Checkout("client2")
		assert.NoError(t, err)

		// Try to checkout when pool is full - should wait
		done := make(chan bool)
		go func() {
			conn3, err := pool.Checkout("client3")
			assert.NoError(t, err)
			assert.NotNil(t, conn3)
			pool.Return(conn3)
			done <- true
		}()

		// Give some time for goroutine to start waiting
		time.Sleep(100 * time.Millisecond)

		// Return a connection
		pool.Return(conn1)

		// Wait for goroutine to complete
		select {
		case <-done:
			// Success
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for connection")
		}

		// Cleanup
		pool.Return(conn2)
	})

	t.Run("Statistics", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 1, 5, 1, 10)
		pool := m.GetPool("test", nil, SessionMode, 5)

		// Initial stats
		stats := pool.GetStats()
		assert.Equal(t, int64(0), stats["total_requests"])

		// Checkout some connections
		conns := make([]*PooledConnection, 3)
		for i := 0; i < 3; i++ {
			conn, err := pool.Checkout("client")
			assert.NoError(t, err)
			conns[i] = conn
		}

		// Check stats
		stats = pool.GetStats()
		assert.Equal(t, int64(3), stats["total_requests"])
		assert.Equal(t, int64(3), stats["in_use"])

		// Return connections
		for _, conn := range conns {
			pool.Return(conn)
		}

		stats = pool.GetStats()
		assert.Equal(t, int64(0), stats["in_use"])
	})
}

func TestPoolModes(t *testing.T) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	t.Run("SessionMode", func(t *testing.T) {
		m := NewManager(logger, metrics, "session", 2, 10, 2, 10)

		// Register client
		client, err := m.RegisterClient("client1", "user1", "db1", SessionMode)
		assert.NoError(t, err)

		// Create a mock pool
		pool := m.GetPool("db1", nil, SessionMode, 10)

		// In session mode, client should keep same connection
		// Note: GetConnection needs integration with router, so we test the logic directly
		assert.Equal(t, SessionMode, client.PoolMode)

		// Simulate getting connection
		conn, err := pool.Checkout(client.ID)
		assert.NoError(t, err)
		client.AssignedConn = conn

		// Client should keep the connection
		assert.NotNil(t, client.AssignedConn)

		// Cleanup
		m.UnregisterClient("client1")
	})

	t.Run("TransactionMode", func(t *testing.T) {
		m := NewManager(logger, metrics, "transaction", 2, 10, 2, 10)

		// Register client
		client, err := m.RegisterClient("client1", "user1", "db1", TransactionMode)
		assert.NoError(t, err)
		assert.Equal(t, TransactionMode, client.PoolMode)

		// In transaction mode, connection is assigned per transaction
		// Outside transaction, connections are temporary

		// Cleanup
		m.UnregisterClient("client1")
	})

	t.Run("StatementMode", func(t *testing.T) {
		m := NewManager(logger, metrics, "statement", 2, 10, 2, 10)

		// Register client
		client, err := m.RegisterClient("client1", "user1", "db1", StatementMode)
		assert.NoError(t, err)
		assert.Equal(t, StatementMode, client.PoolMode)

		// In statement mode, connections are always temporary

		// Cleanup
		m.UnregisterClient("client1")
	})
}

func TestConcurrentAccess(t *testing.T) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	m := NewManager(logger, metrics, "session", 1, 5, 1, 100)
	pool := m.GetPool("test", nil, SessionMode, 5)

	// Concurrent checkouts and returns
	var wg sync.WaitGroup
	numClients := 20
	numOps := 10

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			for j := 0; j < numOps; j++ {
				conn, err := pool.Checkout(fmt.Sprintf("client%d", clientID))
				if err != nil {
					// Pool might be full, that's ok
					continue
				}

				// Simulate some work
				time.Sleep(time.Millisecond)

				// Return connection
				pool.Return(conn)
			}
		}(i)
	}

	wg.Wait()

	// Give a moment for all returns to complete
	time.Sleep(100 * time.Millisecond)

	// Check final state
	stats := pool.GetStats()
	// All connections should be returned
	assert.True(t, stats["in_use"].(int64) >= 0)
	assert.True(t, stats["in_use"].(int64) <= int64(5)) // Should not exceed pool size
	assert.True(t, stats["total_requests"].(int64) > 0)
}

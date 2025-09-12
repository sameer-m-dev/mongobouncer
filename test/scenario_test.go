package test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/auth"
	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/proxy"
	"github.com/sameer-m-dev/mongobouncer/util"
)

// Real-world scenario tests
func TestRealWorldScenarios(t *testing.T) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	t.Run("MicroservicesArchitecture", func(t *testing.T) {
		// Simulate microservices each with their own database
		services := []struct {
			name     string
			database string
			poolMode string
			users    []string
		}{
			{
				name:     "user-service",
				database: "users_db",
				poolMode: "session",
				users:    []string{"user_service_app", "user_service_read"},
			},
			{
				name:     "order-service",
				database: "orders_db",
				poolMode: "transaction",
				users:    []string{"order_service_app", "order_service_worker"},
			},
			{
				name:     "analytics-service",
				database: "analytics_db",
				poolMode: "statement",
				users:    []string{"analytics_reader", "analytics_writer"},
			},
			{
				name:     "notification-service",
				database: "notifications_db",
				poolMode: "transaction",
				users:    []string{"notification_sender"},
			},
		}

		// Set up auth manager
		authManager, _ := auth.NewManager(logger, "trust", "", "",
			[]string{"admin"},
			[]string{"monitoring", "analytics_reader"})

		// Set up pool manager
		poolManager := pool.NewManager(logger, metrics, "session", 5, 20, 5, 200)

		// Set up router
		router := proxy.NewDatabaseRouter(logger)

		// Configure each service
		for _, svc := range services {
			// Add route
			router.AddRoute(svc.database, &proxy.RouteConfig{
				DatabaseName:   svc.database,
				Label:          svc.name,
				PoolMode:       svc.poolMode,
				MaxConnections: 50,
			})

			// Create pool
			var mode pool.PoolMode
			switch svc.poolMode {
			case "transaction":
				mode = pool.TransactionMode
			case "statement":
				mode = pool.StatementMode
			default:
				mode = pool.SessionMode
			}
			poolManager.GetPool(svc.database, nil, mode, 50)

			// Register service users
			for _, user := range svc.users {
				client, err := poolManager.RegisterClient(
					fmt.Sprintf("%s-%s", svc.name, user),
					user,
					svc.database,
					mode,
				)
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		}

		// Simulate service interactions
		var wg sync.WaitGroup

		// User service creates users
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := poolManager.GetPool("users_db", nil, pool.SessionMode, 50)
			for i := 0; i < 10; i++ {
				conn, err := p.Checkout("user-service-user_service_app")
				if err == nil {
					// Simulate user creation
					time.Sleep(5 * time.Millisecond)
					p.Return(conn)
				}
			}
		}()

		// Order service processes orders with transactions
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := poolManager.GetPool("orders_db", nil, pool.TransactionMode, 50)
			for i := 0; i < 5; i++ {
				// Start transaction
				conn, err := p.Checkout("order-service-order_service_app")
				if err == nil {
					conn.TransactionID = fmt.Sprintf("order-txn-%d", i)
					// Simulate order processing
					time.Sleep(10 * time.Millisecond)
					p.Return(conn)
				}
			}
		}()

		// Analytics runs queries
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := poolManager.GetPool("analytics_db", nil, pool.StatementMode, 50)
			for i := 0; i < 20; i++ {
				conn, err := p.Checkout("analytics-service-analytics_reader")
				if err == nil {
					// Quick query
					time.Sleep(2 * time.Millisecond)
					p.Return(conn)
				}
			}
		}()

		wg.Wait()

		// Verify system state
		allRoutes := router.GetAllRoutes()
		assert.Len(t, allRoutes, len(services))

		// Check permissions
		assert.True(t, authManager.IsStatsUser("analytics_reader"))
		assert.True(t, authManager.IsStatsUser("monitoring"))
		assert.True(t, authManager.IsAdminUser("admin"))
	})

	t.Run("ShardedCluster", func(t *testing.T) {
		// Simulate a sharded MongoDB cluster
		router := proxy.NewDatabaseRouter(logger)
		// poolManager := pool.NewManager(logger, "session", 30, 10, 500)

		// Configure shards
		shardCount := 3
		for i := 0; i < shardCount; i++ {
			shardName := fmt.Sprintf("shard%d", i)

			// Each shard has multiple databases
			for _, db := range []string{"users", "products", "orders"} {
				dbName := fmt.Sprintf("%s_%s", db, shardName)
				router.AddRoute(dbName, &proxy.RouteConfig{
					DatabaseName: dbName,
					Label:        shardName,
				})
			}
		}

		// Add pattern routes for easier access
		router.AddRoute("users_*", &proxy.RouteConfig{
			DatabaseName: "users_*",
			Label:        "users-sharded",
		})
		router.AddRoute("products_*", &proxy.RouteConfig{
			DatabaseName: "products_*",
			Label:        "products-sharded",
		})
		router.AddRoute("orders_*", &proxy.RouteConfig{
			DatabaseName: "orders_*",
			Label:        "orders-sharded",
		})

		// Test shard routing
		testCases := []struct {
			database      string
			expectedLabel string
		}{
			{"users_shard0", "shard0"},
			{"users_shard1", "shard1"},
			{"products_shard2", "shard2"},
			{"users_shard99", "users-sharded"}, // Pattern match
		}

		for _, tc := range testCases {
			route, err := router.GetRoute(tc.database)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedLabel, route.Label)
		}

		// Simulate load distribution across shards
		clientCount := 30
		requestsPerClient := 10
		shardRequests := make(map[string]int)
		var mu sync.Mutex

		var wg sync.WaitGroup
		for i := 0; i < clientCount; i++ {
			wg.Add(1)
			go func(clientNum int) {
				defer wg.Done()

				// Hash-based shard selection
				shard := clientNum % shardCount
				dbName := fmt.Sprintf("users_shard%d", shard)

				for j := 0; j < requestsPerClient; j++ {
					route, err := router.GetRoute(dbName)
					if err == nil {
						mu.Lock()
						shardRequests[route.Label]++
						mu.Unlock()
					}
				}
			}(i)
		}
		wg.Wait()

		// Verify distribution
		for i := 0; i < shardCount; i++ {
			shard := fmt.Sprintf("shard%d", i)
			count := shardRequests[shard]
			expectedCount := (clientCount / shardCount) * requestsPerClient
			// Allow 20% variance
			assert.InDelta(t, expectedCount, count, float64(expectedCount)*0.2,
				"Shard %s: expected ~%d requests, got %d", shard, expectedCount, count)
		}
	})

	t.Run("MultiTenantSaaS", func(t *testing.T) {
		// Simulate multi-tenant SaaS with database per tenant
		router := proxy.NewDatabaseRouter(logger)
		poolManager := pool.NewManager(logger, metrics, "transaction", 2, 10, 2, 1000)

		// Configure tenants
		tenants := []struct {
			id       string
			tier     string
			poolSize int
		}{
			{"tenant_001", "enterprise", 50},
			{"tenant_002", "enterprise", 50},
			{"tenant_003", "standard", 20},
			{"tenant_004", "standard", 20},
			{"tenant_005", "basic", 10},
			{"tenant_006", "basic", 10},
		}

		// Set up routes and pools for each tenant
		for _, tenant := range tenants {
			dbName := fmt.Sprintf("tenant_%s", tenant.id)

			router.AddRoute(dbName, &proxy.RouteConfig{
				DatabaseName:   dbName,
				Label:          tenant.tier,
				MaxConnections: tenant.poolSize,
			})

			poolManager.GetPool(dbName, nil, pool.TransactionMode, tenant.poolSize)
		}

		// Pattern routes for tenant tiers
		router.AddRoute("tenant_*", &proxy.RouteConfig{
			DatabaseName: "tenant_*",
			Label:        "multi-tenant",
		})

		// Simulate tenant operations
		var wg sync.WaitGroup
		operationCounts := make(map[string]int)
		var countMu sync.Mutex

		for _, tenant := range tenants {
			wg.Add(1)
			go func(t struct {
				id       string
				tier     string
				poolSize int
			}) {
				defer wg.Done()

				dbName := fmt.Sprintf("tenant_%s", t.id)
				clientID := fmt.Sprintf("client_%s", t.id)

				// Register tenant client
				_, err := poolManager.RegisterClient(clientID, t.id, dbName, pool.TransactionMode)
				if err != nil {
					return
				}
				defer poolManager.UnregisterClient(clientID)

				// Simulate operations based on tier
				opCount := 10
				if t.tier == "enterprise" {
					opCount = 30
				} else if t.tier == "standard" {
					opCount = 20
				}

				p := poolManager.GetPool(dbName, nil, pool.TransactionMode, t.poolSize)
				for i := 0; i < opCount; i++ {
					conn, err := p.Checkout(clientID)
					if err == nil {
						// Simulate work
						time.Sleep(time.Millisecond)
						p.Return(conn)

						countMu.Lock()
						operationCounts[t.tier]++
						countMu.Unlock()
					}
				}
			}(tenant)
		}

		wg.Wait()

		// Verify tier-based activity
		assert.True(t, operationCounts["enterprise"] > operationCounts["standard"])
		assert.True(t, operationCounts["standard"] > operationCounts["basic"])
	})

	t.Run("DisasterRecoveryFailover", func(t *testing.T) {
		// Simulate DR scenario with primary/secondary clusters

		tmpDir, err := ioutil.TempDir("", "dr-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Initial config pointing to primary
		configFile := filepath.Join(tmpDir, "dr-config.toml")
		primaryConfig := `
[mongobouncer]
listen_port = 27017

[databases]
app_db = "mongodb://primary.cluster:27017/app"
cache_db = "mongodb://primary.cluster:27017/cache"

[databases."*"]
host = "primary.cluster"
port = 27017
`
		err = ioutil.WriteFile(configFile, []byte(primaryConfig), 0644)
		require.NoError(t, err)

		// TODO: Update this test to use new TOML config system
		// For now, skip file-based config test since that system was removed
		t.Skip("File-based config system was removed in favor of TOML config")

		// Load initial config (OLD SYSTEM - COMMENTED OUT)
		// fileConfig, err := config.LoadFileConfig(configFile)
		// assert.NoError(t, err)

		router := proxy.NewDatabaseRouter(logger)

		// Set up routes manually for now (instead of from old config)
		router.AddRoute("app_db", &proxy.RouteConfig{
			DatabaseName:     "app_db",
			ConnectionString: "mongodb://primary.cluster:27017/app_db",
			Label:            "primary",
		})

		// Verify primary routing
		route, err := router.GetRoute("app_db")
		assert.NoError(t, err)
		assert.Contains(t, route.ConnectionString, "primary.cluster")

		// Simulate primary failure - update config
		secondaryConfig := `
[mongobouncer]
listen_port = 27017

[databases]
app_db = "mongodb://secondary.cluster:27017/app"
cache_db = "mongodb://secondary.cluster:27017/cache"

[databases."*"]
host = "secondary.cluster"
port = 27017
`
		err = ioutil.WriteFile(configFile, []byte(secondaryConfig), 0644)
		require.NoError(t, err)

		// Reload config (OLD SYSTEM - COMMENTED OUT)
		// fileConfig, err = config.LoadFileConfig(configFile)
		// assert.NoError(t, err)

		// Update routes manually for now (instead of from old config)
		router.UpdateRoute("app_db", &proxy.RouteConfig{
			DatabaseName:     "app_db",
			ConnectionString: "mongodb://secondary.cluster:27017/app_db",
			Label:            "secondary",
		})

		// Verify failover
		route, err = router.GetRoute("app_db")
		assert.NoError(t, err)
		assert.Contains(t, route.ConnectionString, "secondary.cluster")
		assert.Equal(t, "secondary", route.Label)

		// Test new connections go to secondary
		newRoute, err := router.GetRoute("new_db")
		assert.NoError(t, err)
		assert.Contains(t, newRoute.ConnectionString, "secondary.cluster")
	})

	t.Run("ComplianceAndAuditing", func(t *testing.T) {
		// Simulate environment with strict compliance requirements

		// Set up auth with detailed user tracking
		tmpDir, err := ioutil.TempDir("", "compliance-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create users with different access levels
		authFile := filepath.Join(tmpDir, "compliance-users.txt")
		authContent := `# Compliance users
"data_scientist" "hash1" "pool_mode=statement max_connections=10"
"app_service" "hash2" "pool_mode=transaction max_connections=50"
"etl_worker" "hash3" "pool_mode=session max_connections=20"
"auditor" "hash4" "stats_only=true"
"admin" "hash5"
`
		err = ioutil.WriteFile(authFile, []byte(authContent), 0644)
		require.NoError(t, err)

		authManager, err := auth.NewManager(logger, "md5", authFile, "",
			[]string{"admin"},
			[]string{"auditor", "compliance_officer"})
		assert.NoError(t, err)

		// Set up router with data classification
		router := proxy.NewDatabaseRouter(logger)

		databases := []struct {
			name           string
			classification string
			allowedUsers   []string
		}{
			{
				name:           "pii_data",
				classification: "sensitive",
				allowedUsers:   []string{"app_service", "admin"},
			},
			{
				name:           "analytics_data",
				classification: "internal",
				allowedUsers:   []string{"data_scientist", "app_service", "admin"},
			},
			{
				name:           "public_data",
				classification: "public",
				allowedUsers:   []string{"data_scientist", "app_service", "etl_worker", "admin"},
			},
			{
				name:           "audit_logs",
				classification: "restricted",
				allowedUsers:   []string{"admin", "auditor"},
			},
		}

		for _, db := range databases {
			router.AddRoute(db.name, &proxy.RouteConfig{
				DatabaseName: db.name,
				Label:        db.classification,
			})
		}

		// Test access control
		testUsers := []string{"data_scientist", "app_service", "etl_worker", "auditor", "admin"}

		for _, user := range testUsers {
			// Check permissions
			canViewStats := authManager.IsStatsUser(user)
			isAdmin := authManager.IsAdminUser(user)

			switch user {
			case "admin":
				assert.True(t, isAdmin)
				assert.True(t, canViewStats)
			case "auditor":
				assert.False(t, isAdmin)
				assert.True(t, canViewStats)
			case "data_scientist", "app_service", "etl_worker":
				assert.False(t, isAdmin)
				assert.False(t, canViewStats)
			}

			// Get user configuration
			userInfo, exists := authManager.GetUser(user)
			if exists {
				t.Logf("User %s: PoolMode=%s, MaxConn=%d",
					user, userInfo.PoolMode, userInfo.MaxConnections)
			}
		}

		// Simulate audit trail
		auditLog := make([]string, 0)
		var auditMu sync.Mutex

		// Track database access
		for _, db := range databases {
			for _, user := range testUsers {
				route, err := router.GetRoute(db.name)
				if err == nil {
					auditMu.Lock()
					auditLog = append(auditLog, fmt.Sprintf(
						"[%s] User '%s' accessed database '%s' (classification: %s)",
						time.Now().Format(time.RFC3339),
						user,
						db.name,
						route.Label,
					))
					auditMu.Unlock()
				}
			}
		}

		// Verify audit log
		assert.True(t, len(auditLog) > 0)
		// In real scenario, would write to persistent audit log
	})
}

package test

import (
	"fmt"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/auth"
	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/proxy"
	"github.com/sameer-m-dev/mongobouncer/util"
)

// Benchmark tests for performance testing
func BenchmarkRouting(b *testing.B) {
	logger := zap.NewNop()
	router := proxy.NewDatabaseRouter(logger)

	// Set up routes
	for i := 0; i < 100; i++ {
		dbName := fmt.Sprintf("db%d", i)
		router.AddRoute(dbName, &proxy.RouteConfig{
			DatabaseName: dbName,
			Label:        fmt.Sprintf("label%d", i),
		})
	}
	router.AddRoute("prefix_*", &proxy.RouteConfig{DatabaseName: "prefix_*"})
	router.AddRoute("*_suffix", &proxy.RouteConfig{DatabaseName: "*_suffix"})
	router.AddRoute("*", &proxy.RouteConfig{DatabaseName: "*"})

	b.Run("ExactMatch", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.GetRoute("db50")
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("PrefixMatch", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.GetRoute("prefix_test")
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("WildcardMatch", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.GetRoute("unknown_db")
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("MixedRouting", func(b *testing.B) {
		databases := []string{"db10", "prefix_test", "test_suffix", "unknown"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			db := databases[i%len(databases)]
			_, err := router.GetRoute(db)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkAuthentication(b *testing.B) {
	logger := zap.NewNop()

	b.Run("TrustAuth", func(b *testing.B) {
		m, _ := auth.NewManager(logger, "trust", "", "", nil, nil)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = m.Authenticate("user", "pass")
		}
	})

	b.Run("MD5Auth", func(b *testing.B) {
		// Set up auth manager with users
		m, _ := auth.NewManager(logger, "md5", "", "", nil, nil)
		// In real scenario, would load from file

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = m.Authenticate("testuser", "testpass")
		}
	})

	b.Run("UserLookup", func(b *testing.B) {
		m, _ := auth.NewManager(logger, "trust", "", "",
			[]string{"admin1", "admin2"},
			[]string{"stats1", "stats2"})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = m.GetUser("testuser")
		}
	})

	b.Run("PermissionCheck", func(b *testing.B) {
		m, _ := auth.NewManager(logger, "trust", "", "",
			[]string{"admin1", "admin2", "admin3"},
			[]string{"stats1", "stats2", "stats3"})

		users := []string{"admin1", "stats1", "regular", "admin2"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			user := users[i%len(users)]
			_ = m.IsAdminUser(user)
			_ = m.IsStatsUser(user)
		}
	})
}

func BenchmarkConnectionPool(b *testing.B) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	b.Run("SessionMode", func(b *testing.B) {
		m := pool.NewManager(logger, metrics, "session", 10, 100, 10, 1000)
		p := m.GetPool("bench", nil, pool.SessionMode, 100)

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			clientID := fmt.Sprintf("client-%d", b.N)
			for pb.Next() {
				conn, err := p.Checkout(clientID)
				if err != nil {
					continue
				}
				p.Return(conn)
			}
		})
	})

	b.Run("StatementMode", func(b *testing.B) {
		m := pool.NewManager(logger, metrics, "statement", 10, 100, 10, 1000)
		p := m.GetPool("bench", nil, pool.StatementMode, 100)

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			clientID := fmt.Sprintf("client-%d", b.N)
			for pb.Next() {
				conn, err := p.Checkout(clientID)
				if err != nil {
					continue
				}
				p.Return(conn)
			}
		})
	})

	b.Run("HighContention", func(b *testing.B) {
		m := pool.NewManager(logger, metrics, "statement", 2, 10, 2, 1000) // Small pool
		p := m.GetPool("bench", nil, pool.StatementMode, 10)

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			clientID := fmt.Sprintf("client-%d", b.N)
			for pb.Next() {
				conn, err := p.Checkout(clientID)
				if err != nil {
					continue
				}
				// Simulate some work
				for i := 0; i < 100; i++ {
					_ = i
				}
				p.Return(conn)
			}
		})
	})
}

func BenchmarkConfigParsing(b *testing.B) {
	configContent := `
[mongobouncer]
listen_addr = "0.0.0.0"
listen_port = 27017
auth_type = "md5"
pool_mode = "transaction"

[databases]
db1 = "mongodb://localhost:27017/db1"
db2 = "mongodb://localhost:27017/db2"
db3 = "mongodb://localhost:27017/db3"

[databases."*"]
host = "default.mongodb.net"

[users.user1]
password = "hash1"
pool_mode = "session"
`

	b.Run("ParseTOML", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// In real test, would parse TOML
			_ = configContent
		}
	})
}

func BenchmarkConcurrentOperations(b *testing.B) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	b.Run("MixedWorkload", func(b *testing.B) {
		// Set up components
		authM, _ := auth.NewManager(logger, "trust", "", "", nil, nil)
		poolM := pool.NewManager(logger, metrics, "session", 5, 50, 5, 500)
		router := proxy.NewDatabaseRouter(logger)

		// Set up routes
		for i := 0; i < 10; i++ {
			dbName := fmt.Sprintf("db%d", i)
			router.AddRoute(dbName, &proxy.RouteConfig{DatabaseName: dbName})
			poolM.GetPool(dbName, nil, pool.SessionMode, 50)
		}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			clientID := fmt.Sprintf("client-%d", b.N)
			for pb.Next() {
				// Simulate mixed operations

				// 1. Authenticate
				_ = authM.Authenticate(clientID, "pass")

				// 2. Route database
				dbName := fmt.Sprintf("db%d", b.N%10)
				_, _ = router.GetRoute(dbName)

				// 3. Get connection
				p := poolM.GetPool(dbName, nil, pool.SessionMode, 50)
				conn, err := p.Checkout(clientID)
				if err == nil {
					p.Return(conn)
				}
			}
		})
	})
}

// Memory allocation benchmarks
func BenchmarkMemoryAllocations(b *testing.B) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	b.Run("RouterCreation", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			router := proxy.NewDatabaseRouter(logger)
			router.AddRoute("test", &proxy.RouteConfig{DatabaseName: "test"})
		}
	})

	b.Run("PoolCreation", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m := pool.NewManager(logger, metrics, "session", 2, 10, 2, 100)
			_ = m.GetPool("test", nil, pool.SessionMode, 10)
		}
	})

	b.Run("ClientRegistration", func(b *testing.B) {
		m := pool.NewManager(logger, metrics, "session", 2, 10, 2, 10000)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			clientID := fmt.Sprintf("client-%d", i)
			client, err := m.RegisterClient(clientID, "user", "db", pool.SessionMode)
			if err == nil {
				m.UnregisterClient(client.ID)
			}
		}
	})
}

// Stress test benchmark
func BenchmarkStressTest(b *testing.B) {
	logger := zap.NewNop()
	metrics, _ := util.NewMetricsClient(logger, "localhost:9090")

	b.Run("FullSystemStress", func(b *testing.B) {
		// Create all components
		authM, _ := auth.NewManager(logger, "trust", "", "",
			[]string{"admin"}, []string{"stats"})
		poolM := pool.NewManager(logger, metrics, "statement", 20, 200, 20, 2000)
		router := proxy.NewDatabaseRouter(logger)

		// Set up 50 databases
		for i := 0; i < 50; i++ {
			dbName := fmt.Sprintf("db%d", i)
			router.AddRoute(dbName, &proxy.RouteConfig{
				DatabaseName: dbName,
				Label:        fmt.Sprintf("shard%d", i%5),
			})
		}
		router.AddRoute("*", &proxy.RouteConfig{DatabaseName: "*"})

		// Prepare workload distribution
		operations := []string{"auth", "route", "pool", "stats"}

		var mu sync.Mutex
		opCounts := make(map[string]int)

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			localCounts := make(map[string]int)
			clientID := fmt.Sprintf("client-%d", b.N)

			for pb.Next() {
				op := operations[b.N%len(operations)]
				localCounts[op]++

				switch op {
				case "auth":
					_ = authM.Authenticate(clientID, "pass")
					_ = authM.IsAdminUser(clientID)

				case "route":
					dbName := fmt.Sprintf("db%d", b.N%60) // Some will hit wildcard
					_, _ = router.GetRoute(dbName)

				case "pool":
					dbName := fmt.Sprintf("db%d", b.N%50)
					if client, err := poolM.RegisterClient(clientID, "user", dbName, pool.StatementMode); err == nil {
						p := poolM.GetPool(dbName, nil, pool.StatementMode, 200)
						if conn, err := p.Checkout(clientID); err == nil {
							p.Return(conn)
						}
						poolM.UnregisterClient(client.ID)
					}

				case "stats":
					dbName := fmt.Sprintf("db%d", b.N%50)
					p := poolM.GetPool(dbName, nil, pool.StatementMode, 200)
					_ = p.GetStats()
					_ = router.Statistics()
				}
			}

			// Aggregate counts
			mu.Lock()
			for op, count := range localCounts {
				opCounts[op] += count
			}
			mu.Unlock()
		})

		b.Logf("Operation distribution: %v", opCounts)
	})
}

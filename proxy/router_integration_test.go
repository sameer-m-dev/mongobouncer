package proxy

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/x/mongo/driver/wiremessage"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/mongo"
)

// Integration tests for router with more complex scenarios
func TestRouterIntegration(t *testing.T) {
	logger := zap.NewNop()

	t.Run("ComplexRoutingScenarios", func(t *testing.T) {
		router := NewDatabaseRouter(logger)

		// Set up complex routing rules
		router.AddRoute("prod_users", &RouteConfig{Label: "prod-users"})
		router.AddRoute("prod_*", &RouteConfig{Label: "prod-any"})
		router.AddRoute("*_test", &RouteConfig{Label: "any-test"})
		router.AddRoute("*staging*", &RouteConfig{Label: "staging"})
		router.AddRoute("analytics_*_v2", &RouteConfig{Label: "analytics-v2"})
		router.AddRoute("*", &RouteConfig{Label: "default"})

		tests := []struct {
			database string
			expected string
		}{
			// Exact matches
			{"prod_users", "prod-users"},

			// Prefix matches
			{"prod_orders", "prod-any"},
			{"prod_inventory", "prod-any"},

			// Suffix matches
			{"integration_test", "any-test"},
			{"unit_test", "any-test"},

			// Contains matches
			{"pre_staging_env", "staging"},
			{"staging", "staging"},

			// Complex pattern
			{"analytics_metrics_v2", "analytics-v2"},

			// Wildcard fallback
			{"random_database", "default"},
			{"development", "default"},
		}

		for _, tt := range tests {
			route, err := router.GetRoute(tt.database)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, route.Label, "Database: %s", tt.database)
		}
	})

	t.Run("RouteUpdates", func(t *testing.T) {
		router := NewDatabaseRouter(logger)

		// Initial routes
		router.AddRoute("db1", &RouteConfig{Label: "initial"})
		router.AddRoute("*", &RouteConfig{Label: "wildcard"})

		// Verify initial state
		route, _ := router.GetRoute("db1")
		assert.Equal(t, "initial", route.Label)

		// Update existing route
		router.UpdateRoute("db1", &RouteConfig{Label: "updated"})
		route, _ = router.GetRoute("db1")
		assert.Equal(t, "updated", route.Label)

		// Add new route
		router.AddRoute("db2", &RouteConfig{Label: "new"})
		route, _ = router.GetRoute("db2")
		assert.Equal(t, "new", route.Label)

		// Remove route
		router.RemoveRoute("db1")
		route, _ = router.GetRoute("db1")
		assert.Equal(t, "wildcard", route.Label) // Falls back to wildcard

		// Remove wildcard
		router.RemoveRoute("*")
		_, err := router.GetRoute("db1")
		assert.Error(t, err)
	})

	t.Run("ConcurrentRouting", func(t *testing.T) {
		router := NewDatabaseRouter(logger)

		// Add initial routes
		for i := 0; i < 10; i++ {
			dbName := fmt.Sprintf("db%d", i)
			router.AddRoute(dbName, &RouteConfig{
				DatabaseName: dbName,
				Label:        fmt.Sprintf("route%d", i),
			})
		}
		router.AddRoute("*", &RouteConfig{Label: "wildcard"})

		// Concurrent reads and writes
		done := make(chan bool)

		// Reader goroutines
		for i := 0; i < 5; i++ {
			go func(id int) {
				for j := 0; j < 100; j++ {
					dbName := fmt.Sprintf("db%d", j%10)
					route, err := router.GetRoute(dbName)
					assert.NoError(t, err)
					assert.NotNil(t, route)
				}
				done <- true
			}(i)
		}

		// Writer goroutines
		for i := 0; i < 2; i++ {
			go func(id int) {
				for j := 0; j < 50; j++ {
					dbName := fmt.Sprintf("dynamic%d", j)
					router.AddRoute(dbName, &RouteConfig{
						DatabaseName: dbName,
						Label:        fmt.Sprintf("dynamic%d", j),
					})
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 7; i++ {
			<-done
		}

		// Verify final state
		allRoutes := router.GetAllRoutes()
		assert.True(t, len(allRoutes) >= 11) // At least initial 10 + wildcard
	})

	t.Run("MessageExtraction", func(t *testing.T) {
		// Test database extraction from different message types
		tests := []struct {
			name     string
			setup    func() *mongo.Message
			expected string
		}{
			{
				name: "AdminCommand",
				setup: func() *mongo.Message {
					// Create a mock message that returns ismaster
					return &mongo.Message{
						Op: &mockOperation{
							ismaster:   true,
							collection: "admin.$cmd",
						},
					}
				},
				expected: "admin",
			},
			{
				name: "DatabaseCollection",
				setup: func() *mongo.Message {
					return &mongo.Message{
						Op: &mockOperation{
							collection: "mydb.users",
						},
					}
				},
				expected: "mydb",
			},
			{
				name: "NoDatabase",
				setup: func() *mongo.Message {
					return &mongo.Message{
						Op: &mockOperation{
							collection: "",
						},
					}
				},
				expected: "admin", // Default to admin
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				msg := tt.setup()
				db, err := ExtractDatabaseName(msg)
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, db)
			})
		}
	})

	t.Run("RouteStatistics", func(t *testing.T) {
		router := NewDatabaseRouter(logger)

		// Add various routes
		router.AddRoute("db1", &RouteConfig{
			DatabaseName: "db1",
			Label:        "primary",
			PoolMode:     "session",
		})
		router.AddRoute("db2", &RouteConfig{
			DatabaseName: "db2",
			Label:        "secondary",
			PoolMode:     "transaction",
		})
		router.AddRoute("test_*", &RouteConfig{
			DatabaseName: "test_*",
			Label:        "test",
			PoolMode:     "statement",
		})
		router.AddRoute("*", &RouteConfig{
			DatabaseName: "*",
			Label:        "default",
			PoolMode:     "session",
		})

		stats := router.Statistics()

		assert.Equal(t, 2, stats["exact_routes"])
		assert.Equal(t, 1, stats["pattern_routes"])
		assert.Equal(t, true, stats["has_wildcard"])

		routes := stats["routes"].([]map[string]string)
		assert.Len(t, routes, 2) // Only exact routes in the routes list

		// Verify route details
		foundPrimary := false
		foundSecondary := false
		for _, route := range routes {
			if route["database"] == "db1" {
				assert.Equal(t, "primary", route["label"])
				assert.Equal(t, "session", route["pool_mode"])
				foundPrimary = true
			}
			if route["database"] == "db2" {
				assert.Equal(t, "secondary", route["label"])
				assert.Equal(t, "transaction", route["pool_mode"])
				foundSecondary = true
			}
		}
		assert.True(t, foundPrimary)
		assert.True(t, foundSecondary)
	})

	t.Run("EdgeCases", func(t *testing.T) {
		router := NewDatabaseRouter(logger)

		// Empty database name
		router.AddRoute("", &RouteConfig{Label: "empty"})
		route, err := router.GetRoute("")
		assert.NoError(t, err)
		assert.Equal(t, "empty", route.Label)

		// Special characters in database names
		specialDBs := []string{
			"db-with-dash",
			"db.with.dots",
			"db_with_underscore",
			"db$special",
			"123numeric",
		}

		for _, db := range specialDBs {
			router.AddRoute(db, &RouteConfig{Label: db})
			route, err := router.GetRoute(db)
			assert.NoError(t, err)
			assert.Equal(t, db, route.Label)
		}

		// Very long database name
		longDB := "very_long_database_name_that_exceeds_normal_length_expectations_and_tests_buffer_limits"
		router.AddRoute(longDB, &RouteConfig{Label: "long"})
		route, err = router.GetRoute(longDB)
		assert.NoError(t, err)
		assert.Equal(t, "long", route.Label)
	})
}

// Mock operation for testing
type mockOperation struct {
	ismaster   bool
	collection string
	command    string
}

func (m *mockOperation) OpCode() wiremessage.OpCode          { return 0 }
func (m *mockOperation) Encode(responseTo int32) []byte      { return nil }
func (m *mockOperation) IsIsMaster() bool                    { return m.ismaster }
func (m *mockOperation) CursorID() (cursorID int64, ok bool) { return 0, false }
func (m *mockOperation) RequestID() int32                    { return 0 }
func (m *mockOperation) Error() error                        { return nil }
func (m *mockOperation) Unacknowledged() bool                { return false }
func (m *mockOperation) CommandAndCollection() (mongo.Command, string) {
	return mongo.Command(m.command), m.collection
}
func (m *mockOperation) TransactionDetails() *mongo.TransactionDetails { return nil }
func (m *mockOperation) String() string                                { return "mock" }

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Test configuration
type TestConfig struct {
	Name          string
	ConnectionURI string
	Database      string
	Collection    string
	ExpectedError bool
	TestType      string // "read", "write", "auth", "replica_set"
	Description   string
}

// Test results
type TestResult struct {
	Name        string
	Passed      bool
	Error       error
	Duration    time.Duration
	Description string
}

var testResults []TestResult

// MongoDB connection configurations with unique names
var testConfigs = []TestConfig{
	// MongoDB 6 Replica Set Tests (rs0)
	{
		Name:          "MongoDB 6 RS - AppUser Auth - Read app_prod_rs6",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs6?&authSource=admin",
		Database:      "app_prod_rs6",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test appuser_rs6 read access to app_prod_rs6 database",
	},
	{
		Name:          "MongoDB 6 RS - AppUser Auth - Write app_prod_rs6",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs6?&authSource=admin",
		Database:      "app_prod_rs6",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test appuser_rs6 write access to app_prod_rs6 database",
	},
	{
		Name:          "MongoDB 6 RS - ReadOnly Auth - Read app_prod_rs6",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs6?&authSource=admin",
		Database:      "app_prod_rs6",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test readonly_rs6 user read access to app_prod_rs6 database",
	},
	{
		Name:          "MongoDB 6 RS - ReadOnly Auth - Write app_prod_rs6 (Should Fail)",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs6?&authSource=admin",
		Database:      "app_prod_rs6",
		Collection:    "users",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test readonly_rs6 user write access should fail",
	},
	{
		Name:          "MongoDB 6 RS - Analytics Auth - Read analytics_rs6",
		ConnectionURI: "mongodb://analytics_rs6:analytics_rs6_123@localhost:27017/analytics_rs6?&authSource=analytics_rs6",
		Database:      "analytics_rs6",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_rs6 user read access to analytics_rs6 database",
	},
	{
		Name:          "MongoDB 6 RS - Analytics Auth - Write analytics_rs6",
		ConnectionURI: "mongodb://analytics_rs6:analytics_rs6_123@localhost:27017/analytics_rs6?&authSource=analytics_rs6",
		Database:      "analytics_rs6",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test analytics_rs6 user write access to analytics_rs6 database",
	},
	{
		Name:          "MongoDB 6 RS - Analytics Auth - Read app_prod_rs6",
		ConnectionURI: "mongodb://analytics_rs6:analytics_rs6_123@localhost:27017/app_prod_rs6?&authSource=analytics_rs6",
		Database:      "app_prod_rs6",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_rs6 user read access to app_prod_rs6 database",
	},
	{
		Name:          "MongoDB 6 RS - Analytics Auth - Write app_prod_rs6 (Should Fail)",
		ConnectionURI: "mongodb://analytics_rs6:analytics_rs6_123@localhost:27017/app_prod_rs6?&authSource=analytics_rs6",
		Database:      "app_prod_rs6",
		Collection:    "users",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test analytics_rs6 user write access to app_prod_rs6 should fail",
	},

	// MongoDB 8 Replica Set Tests (rs1)
	{
		Name:          "MongoDB 8 RS - AppUser Auth - Read app_prod_rs8",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs8?authSource=admin",
		Database:      "app_prod_rs8",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test appuser_rs8 read access to app_prod_rs8 database",
	},
	{
		Name:          "MongoDB 8 RS - AppUser Auth - Write app_prod_rs8",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs8?authSource=admin",
		Database:      "app_prod_rs8",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test appuser_rs8 write access to app_prod_rs8 database",
	},
	{
		Name:          "MongoDB 8 RS - ReadOnly Auth - Read app_prod_rs8",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs8?authSource=admin",
		Database:      "app_prod_rs8",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test readonly_rs8 user read access to app_prod_rs8 database",
	},
	{
		Name:          "MongoDB 8 RS - ReadOnly Auth - Write app_prod_rs8 (Should Fail)",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs8?authSource=admin",
		Database:      "app_prod_rs8",
		Collection:    "users",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test readonly_rs8 user write access should fail",
	},
	{
		Name:          "MongoDB 8 RS - Analytics Auth - Read analytics_rs8",
		ConnectionURI: "mongodb://localhost:27017/analytics_rs8?authSource=analytics_rs8",
		Database:      "analytics_rs8",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_rs8 user read access to analytics_rs8 database",
	},
	{
		Name:          "MongoDB 8 RS - Analytics Auth - Write analytics_rs8",
		ConnectionURI: "mongodb://localhost:27017/analytics_rs8?authSource=analytics_rs8",
		Database:      "analytics_rs8",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test analytics_rs8 user write access to analytics_rs8 database",
	},
	{
		Name:          "MongoDB 8 RS - Analytics Auth - Read app_prod_rs8",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs8?authSource=analytics_rs8",
		Database:      "app_prod_rs8",
		Collection:    "users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_rs8 user read access to app_prod_rs8 database",
	},
	{
		Name:          "MongoDB 8 RS - Analytics Auth - Write app_prod_rs8 (Should Fail)",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs8?authSource=analytics_rs8",
		Database:      "app_prod_rs8",
		Collection:    "users",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test analytics_rs8 user write access to app_prod_rs8 should fail",
	},

	// MongoDB 6 Standalone Tests
	{
		Name:          "MongoDB 6 Standalone - AppUser Auth - Read standalone_v6_db",
		ConnectionURI: "mongodb://localhost:27017/standalone_v6_db?authSource=admin&retryWrites=false&retryReads=false",
		Database:      "standalone_v6_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test appuser_s6 read access to standalone_v6_db database",
	},
	{
		Name:          "MongoDB 6 Standalone - AppUser Auth - Write standalone_v6_db",
		ConnectionURI: "mongodb://localhost:27017/standalone_v6_db?authSource=admin&retryWrites=false&retryReads=false",
		Database:      "standalone_v6_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test appuser_s6 write access to standalone_v6_db database",
	},
	{
		Name:          "MongoDB 6 Standalone - ReadOnly Auth - Read standalone_v6_db",
		ConnectionURI: "mongodb://localhost:27017/standalone_v6_db?authSource=admin",
		Database:      "standalone_v6_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test readonly_s6 user read access to standalone_v6_db database",
	},
	{
		Name:          "MongoDB 6 Standalone - ReadOnly Auth - Write standalone_v6_db (Should Fail)",
		ConnectionURI: "mongodb://localhost:27017/standalone_v6_db?authSource=admin",
		Database:      "standalone_v6_db",
		Collection:    "documents",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test readonly_s6 user write access should fail",
	},
	{
		Name:          "MongoDB 6 Standalone - Analytics Auth - Read analytics_s6",
		ConnectionURI: "mongodb://analytics_s6:analytics_s6_123@localhost:27017/analytics_s6?authSource=analytics_s6&retryWrites=false&retryReads=false",
		Database:      "analytics_s6",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_s6 user read access to analytics_s6 database",
	},
	{
		Name:          "MongoDB 6 Standalone - Analytics Auth - Write analytics_s6",
		ConnectionURI: "mongodb://analytics_s6:analytics_s6_123@localhost:27017/analytics_s6?authSource=analytics_s6&retryWrites=false&retryReads=false",
		Database:      "analytics_s6",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test analytics_s6 user write access to analytics_s6 database",
	},
	{
		Name:          "MongoDB 6 Standalone - Analytics Auth - Read standalone_v6_db",
		ConnectionURI: "mongodb://analytics_s6:analytics_s6_123@localhost:27017/standalone_v6_db?authSource=analytics_s6",
		Database:      "standalone_v6_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_s6 user read access to standalone_v6_db database",
	},
	{
		Name:          "MongoDB 6 Standalone - Analytics Auth - Write standalone_v6_db (Should Fail)",
		ConnectionURI: "mongodb://analytics_s6:analytics_s6_123@localhost:27017/standalone_v6_db?authSource=analytics_s6",
		Database:      "standalone_v6_db",
		Collection:    "documents",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test analytics_s6 user write access to standalone_v6_db should fail",
	},
	{
		Name:          "MongoDB 6 Standalone - Test Database Access",
		ConnectionURI: "mongodb://localhost:27017/test_v6_db?authSource=admin",
		Database:      "test_v6_db",
		Collection:    "test_data",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test appuser_s6 access to test_v6_db database",
	},

	// MongoDB 8 Standalone Tests
	{
		Name:          "MongoDB 8 Standalone - AppUser Auth - Read standalone_db",
		ConnectionURI: "mongodb://localhost:27017/standalone_db?authSource=admin",
		Database:      "standalone_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test appuser_s8 read access to standalone_db database",
	},
	{
		Name:          "MongoDB 8 Standalone - AppUser Auth - Write standalone_db",
		ConnectionURI: "mongodb://localhost:27017/standalone_db?authSource=admin&retryWrites=false&retryReads=false",
		Database:      "standalone_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test appuser_s8 write access to standalone_db database",
	},
	{
		Name:          "MongoDB 8 Standalone - ReadOnly Auth - Read standalone_db",
		ConnectionURI: "mongodb://localhost:27017/standalone_db?authSource=admin&retryWrites=false&retryReads=false",
		Database:      "standalone_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test readonly_s8 user read access to standalone_db database",
	},
	{
		Name:          "MongoDB 8 Standalone - ReadOnly Auth - Write standalone_db (Should Fail)",
		ConnectionURI: "mongodb://localhost:27017/standalone_db?authSource=admin&retryWrites=false&retryReads=false",
		Database:      "standalone_db",
		Collection:    "documents",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test readonly_s8 user write access should fail",
	},
	{
		Name:          "MongoDB 8 Standalone - Analytics Auth - Read analytics_s8",
		ConnectionURI: "mongodb://analytics_s8:analytics_s8_123@localhost:27017/analytics_s8?authSource=analytics_s8&retryWrites=false&retryReads=false",
		Database:      "analytics_s8",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_s8 user read access to analytics_s8 database",
	},
	{
		Name:          "MongoDB 8 Standalone - Analytics Auth - Write analytics_s8",
		ConnectionURI: "mongodb://analytics_s8:analytics_s8_123@localhost:27017/analytics_s8?authSource=analytics_s8&retryWrites=false&retryReads=false",
		Database:      "analytics_s8",
		Collection:    "events",
		ExpectedError: false,
		TestType:      "write",
		Description:   "Test analytics_s8 user write access to analytics_s8 database",
	},
	{
		Name:          "MongoDB 8 Standalone - Analytics Auth - Read standalone_db",
		ConnectionURI: "mongodb://analytics_s8:analytics_s8_123@localhost:27017/standalone_db?authSource=analytics_s8",
		Database:      "standalone_db",
		Collection:    "documents",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics_s8 user read access to standalone_db database",
	},
	{
		Name:          "MongoDB 8 Standalone - Analytics Auth - Write standalone_db (Should Fail)",
		ConnectionURI: "mongodb://analytics_s8:analytics_s8_123@localhost:27017/standalone_db?authSource=analytics_s8",
		Database:      "standalone_db",
		Collection:    "documents",
		ExpectedError: true,
		TestType:      "write",
		Description:   "Test analytics_s8 user write access to standalone_db should fail",
	},
	{
		Name:          "MongoDB 8 Standalone - Test Database Access",
		ConnectionURI: "mongodb://localhost:27017/test_db?authSource=admin",
		Database:      "test_db",
		Collection:    "test_data",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test appuser_s8 access to test_db database",
	},

	// Additional comprehensive tests
	{
		Name:          "MongoDB 6 RS - Cross Database Access",
		ConnectionURI: "mongodb://localhost:27017/app_test_rs6?&authSource=admin",
		Database:      "app_test_rs6",
		Collection:    "test_users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test cross database access on MongoDB 6 replica set",
	},
	{
		Name:          "MongoDB 8 RS - Cross Database Access",
		ConnectionURI: "mongodb://localhost:27017/app_test_rs8?authSource=admin",
		Database:      "app_test_rs8",
		Collection:    "test_users",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test cross database access on MongoDB 8 replica set",
	},
	{
		Name:          "MongoDB 6 RS - Collection Count",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs6?&authSource=admin",
		Database:      "app_prod_rs6",
		Collection:    "products",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test collection count on MongoDB 6 replica set",
	},
	{
		Name:          "MongoDB 8 RS - Collection Count",
		ConnectionURI: "mongodb://localhost:27017/app_prod_rs8?authSource=admin",
		Database:      "app_prod_rs8",
		Collection:    "products",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test collection count on MongoDB 8 replica set",
	},
	{
		Name:          "MongoDB 6 Standalone - Collection Count",
		ConnectionURI: "mongodb://localhost:27017/standalone_v6_db?authSource=admin",
		Database:      "standalone_v6_db",
		Collection:    "metadata",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test collection count on MongoDB 6 standalone",
	},
	{
		Name:          "MongoDB 8 Standalone - Collection Count",
		ConnectionURI: "mongodb://localhost:27017/standalone_db?authSource=admin",
		Database:      "standalone_db",
		Collection:    "metadata",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test collection count on MongoDB 8 standalone",
	},
	{
		Name:          "MongoDB 6 RS - Analytics Metrics Access",
		ConnectionURI: "mongodb://analytics_rs6:analytics_rs6_123@localhost:27017/analytics_rs6?&authSource=analytics_rs6",
		Database:      "analytics_rs6",
		Collection:    "metrics",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics metrics access on MongoDB 6 replica set",
	},
	{
		Name:          "MongoDB 8 RS - Analytics Metrics Access",
		ConnectionURI: "mongodb://localhost:27017/analytics_rs8?authSource=analytics_rs8",
		Database:      "analytics_rs8",
		Collection:    "metrics",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics metrics access on MongoDB 8 replica set",
	},
	{
		Name:          "MongoDB 6 Standalone - Analytics Metrics Access",
		ConnectionURI: "mongodb://analytics_s6:analytics_s6_123@localhost:27017/analytics_s6?authSource=analytics_s6",
		Database:      "analytics_s6",
		Collection:    "metrics",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics metrics access on MongoDB 6 standalone",
	},
	{
		Name:          "MongoDB 8 Standalone - Analytics Metrics Access",
		ConnectionURI: "mongodb://analytics_s8:analytics_s8_123@localhost:27017/analytics_s8?authSource=analytics_s8",
		Database:      "analytics_s8",
		Collection:    "metrics",
		ExpectedError: false,
		TestType:      "read",
		Description:   "Test analytics metrics access on MongoDB 8 standalone",
	},
	{
		Name:          "MongoDB 6 RS - Wrong Auth Source (Should Fail)",
		ConnectionURI: "mongodb://analytics_rs6:analytics_rs6_123@localhost:27017/analytics_rs6?&authSource=admin",
		Database:      "analytics_rs6",
		Collection:    "events",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong auth source should fail",
	},
	{
		Name:          "MongoDB 8 RS - Wrong Auth Source (Should Fail)",
		ConnectionURI: "mongodb://localhost:27017/analytics_rs8?authSource=admin",
		Database:      "analytics_rs8",
		Collection:    "events",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong auth source should fail",
	},
	{
		Name:          "MongoDB 6 Standalone - Wrong Auth Source (Should Fail)",
		ConnectionURI: "mongodb://analytics_s6:analytics_s6_123@localhost:27017/analytics_s6?authSource=admin",
		Database:      "analytics_s6",
		Collection:    "events",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong auth source should fail",
	},
	{
		Name:          "MongoDB 8 Standalone - Wrong Auth Source (Should Fail)",
		ConnectionURI: "mongodb://analytics_s8:analytics_s8_123@localhost:27017/analytics_s8?authSource=admin",
		Database:      "analytics_s8",
		Collection:    "events",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong auth source should fail",
	},
	{
		Name:          "MongoDB 6 RS - Wrong Password (Should Fail)",
		ConnectionURI: "mongodb://appuser_rs6:wrongpassword@localhost:27017/app_prod_rs6?&authSource=admin",
		Database:      "app_prod_rs6",
		Collection:    "users",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong password should fail",
	},
	{
		Name:          "MongoDB 8 RS - Wrong Password (Should Fail)",
		ConnectionURI: "mongodb://appuser_rs8:wrongpassword@localhost:27017/app_prod_rs8?authSource=admin",
		Database:      "app_prod_rs8",
		Collection:    "users",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong password should fail",
	},
	{
		Name:          "MongoDB 6 Standalone - Wrong Password (Should Fail)",
		ConnectionURI: "mongodb://appuser_s6:wrongpassword@localhost:27017/standalone_v6_db?authSource=admin",
		Database:      "standalone_v6_db",
		Collection:    "documents",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong password should fail",
	},
	{
		Name:          "MongoDB 8 Standalone - Wrong Password (Should Fail)",
		ConnectionURI: "mongodb://appuser_s8:wrongpassword@localhost:27017/standalone_db?authSource=admin",
		Database:      "standalone_db",
		Collection:    "documents",
		ExpectedError: true,
		TestType:      "auth",
		Description:   "Test wrong password should fail",
	},
}

func main() {
	fmt.Println("🚀 Starting MongoDB Comprehensive Test Suite with Unique Names")
	fmt.Println(strings.Repeat("=", 70))

	totalTests := len(testConfigs)
	passedTests := 0
	failedTests := 0

	for i, config := range testConfigs {
		fmt.Printf("\n[%d/%d] Running: %s\n", i+1, totalTests, config.Name)
		fmt.Printf("Description: %s\n", config.Description)

		result := runTest(config)
		testResults = append(testResults, result)

		if result.Passed {
			fmt.Printf("✅ PASSED (%v)\n", result.Duration)
			passedTests++
		} else {
			fmt.Printf("❌ FAILED (%v)\n", result.Duration)
			if result.Error != nil {
				fmt.Printf("   Error: %v\n", result.Error)
			}
			failedTests++
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Printf("📊 Test Results Summary:\n")
	fmt.Printf("   Total Tests: %d\n", totalTests)
	fmt.Printf("   Passed: %d\n", passedTests)
	fmt.Printf("   Failed: %d\n", failedTests)
	fmt.Printf("   Success Rate: %.1f%%\n", float64(passedTests)/float64(totalTests)*100)

	if failedTests > 0 {
		fmt.Println("\n❌ Failed Tests:")
		for _, result := range testResults {
			if !result.Passed {
				fmt.Printf("   - %s: %v\n", result.Name, result.Error)
			}
		}
		os.Exit(1)
	} else {
		fmt.Println("\n🎉 All tests passed successfully!")
	}
}

func runTest(config TestConfig) TestResult {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(config.ConnectionURI))
	if err != nil {
		duration := time.Since(start)
		if config.ExpectedError {
			return TestResult{
				Name:        config.Name,
				Passed:      true,
				Error:       nil,
				Duration:    duration,
				Description: config.Description,
			}
		}
		return TestResult{
			Name:        config.Name,
			Passed:      false,
			Error:       err,
			Duration:    duration,
			Description: config.Description,
		}
	}
	defer client.Disconnect(ctx)

	// Test based on type
	switch config.TestType {
	case "auth":
		err = testAuth(ctx, client, config)
	case "read":
		err = testRead(ctx, client, config)
	case "write":
		err = testWrite(ctx, client, config)
	case "replica_set":
		err = testReplicaSet(ctx, client, config)
	default:
		err = fmt.Errorf("unknown test type: %s", config.TestType)
	}

	duration := time.Since(start)

	if config.ExpectedError {
		// If we expected an error and got one, test passes
		if err != nil {
			return TestResult{
				Name:        config.Name,
				Passed:      true,
				Error:       nil,
				Duration:    duration,
				Description: config.Description,
			}
		}
		// If we expected an error but didn't get one, test fails
		return TestResult{
			Name:        config.Name,
			Passed:      false,
			Error:       fmt.Errorf("expected error but got none"),
			Duration:    duration,
			Description: config.Description,
		}
	} else {
		// If we didn't expect an error and didn't get one, test passes
		if err == nil {
			return TestResult{
				Name:        config.Name,
				Passed:      true,
				Error:       nil,
				Duration:    duration,
				Description: config.Description,
			}
		}
		// If we didn't expect an error but got one, test fails
		return TestResult{
			Name:        config.Name,
			Passed:      false,
			Error:       err,
			Duration:    duration,
			Description: config.Description,
		}
	}
}

func testAuth(ctx context.Context, client *mongo.Client, config TestConfig) error {
	if config.Database == "admin" {
		var result bson.M
		return client.Database("admin").RunCommand(ctx, bson.D{{"ping", 1}}).Decode(&result)
	}
	// For non-admin auth tests, try to access a collection
	collection := client.Database(config.Database).Collection(config.Collection)
	_, err := collection.CountDocuments(ctx, bson.D{})
	return err
}

func testRead(ctx context.Context, client *mongo.Client, config TestConfig) error {
	collection := client.Database(config.Database).Collection(config.Collection)
	cursor, err := collection.Find(ctx, bson.D{}, options.Find().SetLimit(1))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err = cursor.All(ctx, &results); err != nil {
		return err
	}

	return nil
}

func testWrite(ctx context.Context, client *mongo.Client, config TestConfig) error {
	collection := client.Database(config.Database).Collection(config.Collection)
	testDoc := bson.M{
		"test":        true,
		"timestamp":   time.Now(),
		"test_id":     fmt.Sprintf("test_%d", time.Now().UnixNano()),
		"description": config.Description,
	}

	_, err := collection.InsertOne(ctx, testDoc)
	return err
}

func testReplicaSet(ctx context.Context, client *mongo.Client, config TestConfig) error {
	var result bson.M
	return client.Database("admin").RunCommand(ctx, bson.D{{"replSetGetStatus", 1}}).Decode(&result)
}

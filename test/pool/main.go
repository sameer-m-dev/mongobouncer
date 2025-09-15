package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	fmt.Println("🔍 MongoBouncer Comprehensive Connection Pool Test")
	fmt.Println("=================================================")

	// Test multiple scenarios - comprehensive scaling up to 5K workers
	testScenarios := []struct {
		name     string
		workers  int
		expected string
	}{
		// Baseline scenarios
		{"Under Pool Limit", 5, "SUCCESS"},
		{"At Pool Limit", 10, "SUCCESS"},
		{"Just Over Pool Limit", 11, "SUCCESS"},

		// Light overload scenarios
		{"With 15", 15, "SUCCESS"},
		{"With 20", 20, "SUCCESS"},
		{"With 30", 30, "SUCCESS"},

		// Medium scale scenarios
		{"With 50", 50, "SUCCESS"},
		{"With 100", 100, "SUCCESS"},
		{"With 200", 200, "SUCCESS"},
		{"With 300", 300, "SUCCESS"},

		// High scale scenarios
		{"With 500", 500, "SUCCESS"},
		{"With 750", 750, "SUCCESS"},
		{"With 1000", 1000, "SUCCESS"},

		// Enterprise scale scenarios
		{"With 1500", 1500, "SUCCESS"},
		{"With 2000", 2000, "SUCCESS"},
		{"With 2500", 2500, "SUCCESS"},
		{"With 3000", 3000, "SUCCESS"},

		// Extreme scale scenarios
		{"With 3500", 3500, "SUCCESS"},
		{"With 4000", 4000, "SUCCESS"},
		{"With 4500", 4500, "SUCCESS"},
		{"With 5000", 5000, "SUCCESS"},
	}

	allPassed := true

	for _, scenario := range testScenarios {
		fmt.Printf("\n🧪 Testing: %s (%d workers)\n", scenario.name, scenario.workers)
		fmt.Println("----------------------------------------")

		success := testConcurrentConnections(scenario.workers)
		if success {
			fmt.Printf("✅ %s: PASSED\n", scenario.name)
		} else {
			fmt.Printf("❌ %s: FAILED\n", scenario.name)
			allPassed = false
		}

		// Small delay between tests
		time.Sleep(1 * time.Second)
	}

	fmt.Println("\n" + strings.Repeat("=", 50))
	if allPassed {
		fmt.Println("🎉 ALL TESTS PASSED! Connection pool is working correctly.")
	} else {
		fmt.Println("⚠️  SOME TESTS FAILED! Check the implementation.")
	}
}

func testConcurrentConnections(numWorkers int) bool {
	// Connect to MongoBouncer
	client, err := mongo.Connect(context.Background(),
		options.Client().ApplyURI("mongodb://localhost:27017/pool_test?retryWrites=false"))
	if err != nil {
		log.Printf("Failed to connect: %v", err)
		return false
	}
	defer client.Disconnect(context.Background())

	db := client.Database("pool_test")
	coll := db.Collection("test_collection")

	// Clean up
	coll.Drop(context.Background())

	// Test concurrent operations
	var wg sync.WaitGroup
	// Adjust operations per worker based on scale to keep test duration reasonable
	opsPerWorker := 5
	if numWorkers > 1000 {
		opsPerWorker = 2 // Reduce ops for very high worker counts
	} else if numWorkers > 500 {
		opsPerWorker = 3
	}
	successCount := 0
	var successMutex sync.Mutex

	start := time.Now()

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < opsPerWorker; j++ {
				// Insert operation
				_, err := coll.InsertOne(context.Background(), bson.M{
					"worker": workerID,
					"op":     j,
					"time":   time.Now(),
				})
				if err != nil {
					log.Printf("Worker %d, Op %d failed: %v", workerID, j, err)
					continue
				}

				successMutex.Lock()
				successCount++
				successMutex.Unlock()

				// Find operation
				var result bson.M
				err = coll.FindOne(context.Background(), bson.M{
					"worker": workerID,
					"op":     j,
				}).Decode(&result)
				if err != nil {
					log.Printf("Worker %d, Find %d failed: %v", workerID, j, err)
					continue
				}

				successMutex.Lock()
				successCount++
				successMutex.Unlock()

				// Small delay to simulate real workload (reduced for high worker counts)
				if numWorkers > 1000 {
					time.Sleep(1 * time.Millisecond)
				} else if numWorkers > 500 {
					time.Sleep(5 * time.Millisecond)
				} else {
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(i)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Adjust timeout based on worker count
	timeout := 30 * time.Second
	if numWorkers > 1000 {
		timeout = 60 * time.Second // Longer timeout for high worker counts
	} else if numWorkers > 500 {
		timeout = 45 * time.Second
	}

	select {
	case <-done:
		// All workers completed
	case <-time.After(timeout):
		log.Printf("Test timed out after %v", timeout)
		return false
	}

	duration := time.Since(start)

	// Count documents
	count, err := coll.CountDocuments(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Failed to count documents: %v", err)
		return false
	}

	totalOps := numWorkers * opsPerWorker * 2 // insert + find per operation
	successRate := float64(successCount) / float64(totalOps) * 100

	fmt.Printf("  ✅ Completed: %d/%d operations (%.1f%% success rate)\n",
		successCount, totalOps, successRate)
	fmt.Printf("  📊 Documents created: %d\n", count)
	fmt.Printf("  ⏱️ Duration: %v\n", duration)
	fmt.Printf("  ⚡ Rate: %.2f ops/sec\n", float64(successCount)/duration.Seconds())

	// Clean up
	coll.Drop(context.Background())

	// Consider test passed if success rate is >= 95%
	return successRate >= 95.0
}

package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	fmt.Println("🔍 MongoBouncer Dynamic Multi-Database Load Test")
	fmt.Println("================================================")
	fmt.Println("Generating random load across dynamically named databases")

	// Initialize random seed
	rand.New(rand.NewSource(time.Now().UnixNano()))

	// Get test count from command line or use default
	testCount := 100 // Default number of test scenarios
	if len(os.Args) > 1 {
		if count, err := strconv.Atoi(os.Args[1]); err == nil && count > 0 {
			testCount = count
		}
	}

	fmt.Printf("🎯 Running %d test scenarios with random database names and operation counts\n", testCount)
	fmt.Printf("📊 Each test will use a unique database name and 100-1500 random operations\n\n")

	allPassed := true
	totalOperations := 0
	totalDuration := time.Duration(0)

	for i := 0; i < testCount; i++ {
		// Generate random database name based on loop number
		databaseName := fmt.Sprintf("test_db_%d", i)

		// Generate random operation count
		operationCount := rand.Intn(1451) + 100

		fmt.Printf("\n🧪 Test %d/%d: Database '%s' with %d operations\n",
			i+1, testCount, databaseName, operationCount)
		fmt.Println("----------------------------------------")

		success, opsCount, duration := testConcurrentConnectionsDynamic(databaseName, operationCount)
		totalOperations += opsCount
		totalDuration += duration

		if success {
			fmt.Printf("✅ Test %d: PASSED\n", i+1)
		} else {
			fmt.Printf("❌ Test %d: FAILED\n", i+1)
			allPassed = false
		}

		// Small delay between tests
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Printf("📈 SUMMARY:\n")
	fmt.Printf("   Tests Run: %d\n", testCount)
	fmt.Printf("   Total Operations: %d\n", totalOperations)
	fmt.Printf("   Total Duration: %v\n", totalDuration)
	fmt.Printf("   Overall Rate: %.2f ops/sec\n", float64(totalOperations)/totalDuration.Seconds())

	if allPassed {
		fmt.Println("🎉 ALL TESTS PASSED! Dynamic multi-database load test completed successfully.")
	} else {
		fmt.Println("⚠️  SOME TESTS FAILED! Check the implementation.")
	}
}

func testConcurrentConnectionsDynamic(databaseName string, operationCount int) (bool, int, time.Duration) {
	// Connect to MongoBouncer
	client, err := mongo.Connect(context.Background(),
		options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Printf("Failed to connect: %v", err)
		return false, 0, 0
	}
	defer client.Disconnect(context.Background())

	db := client.Database(databaseName)
	coll := db.Collection("test_collection")

	// Clean up any existing data
	coll.Drop(context.Background())

	// Test concurrent operations
	var wg sync.WaitGroup
	successCount := 0
	var successMutex sync.Mutex

	start := time.Now()

	// Create workers based on operation count (1 worker per 10 operations, min 1, max 50)
	numWorkers := operationCount / 10
	if numWorkers < 1 {
		numWorkers = 1
	}
	if numWorkers > 50 {
		numWorkers = 50
	}

	opsPerWorker := operationCount / numWorkers
	remainingOps := operationCount % numWorkers

	fmt.Printf("  🔧 Using %d workers, %d operations per worker\n", numWorkers, opsPerWorker)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Some workers get an extra operation if there's a remainder
			workerOps := opsPerWorker
			if workerID < remainingOps {
				workerOps++
			}

			for j := 0; j < workerOps; j++ {
				// Random operation type
				operationType := rand.Intn(4) // 0: insert, 1: find, 2: update, 3: delete

				switch operationType {
				case 0: // Insert
					_, err := coll.InsertOne(context.Background(), bson.M{
						"worker":      workerID,
						"op":          j,
						"timestamp":   time.Now(),
						"operation":   "insert",
						"random_data": rand.Intn(1000),
					})
					if err != nil {
						log.Printf("Worker %d, Insert %d failed: %v", workerID, j, err)
						continue
					}

				case 1: // Find
					var result bson.M
					err := coll.FindOne(context.Background(), bson.M{
						"worker": workerID,
					}).Decode(&result)
					if err != nil && err != mongo.ErrNoDocuments {
						log.Printf("Worker %d, Find %d failed: %v", workerID, j, err)
						continue
					}

				case 2: // Update
					_, err := coll.UpdateOne(context.Background(),
						bson.M{"worker": workerID},
						bson.M{"$set": bson.M{
							"updated_at": time.Now(),
							"random_val": rand.Intn(1000),
						}})
					if err != nil {
						log.Printf("Worker %d, Update %d failed: %v", workerID, j, err)
						continue
					}

				case 3: // Delete (only delete documents from this worker to avoid conflicts)
					_, err := coll.DeleteOne(context.Background(), bson.M{
						"worker": workerID,
						"op":     j,
					})
					if err != nil {
						log.Printf("Worker %d, Delete %d failed: %v", workerID, j, err)
						continue
					}
				}

				successMutex.Lock()
				successCount++
				successMutex.Unlock()

				// Small random delay to simulate real workload
				delay := time.Duration(rand.Intn(10)) * time.Millisecond
				time.Sleep(delay)
			}
		}(i)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Dynamic timeout based on operation count
	timeout := time.Duration(operationCount/10) * time.Second
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}

	select {
	case <-done:
		// All workers completed
	case <-time.After(timeout):
		log.Printf("Test timed out after %v", timeout)
		return false, successCount, time.Since(start)
	}

	duration := time.Since(start)

	// Count documents
	count, err := coll.CountDocuments(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Failed to count documents: %v", err)
	}

	successRate := float64(successCount) / float64(operationCount) * 100

	fmt.Printf("  ✅ Completed: %d/%d operations (%.1f%% success rate)\n",
		successCount, operationCount, successRate)
	fmt.Printf("  📊 Documents in database: %d\n", count)
	fmt.Printf("  ⏱️ Duration: %v\n", duration)
	fmt.Printf("  ⚡ Rate: %.2f ops/sec\n", float64(successCount)/duration.Seconds())

	// Clean up
	coll.Drop(context.Background())

	// Consider test passed if success rate is >= 90%
	return successRate >= 90.0, successCount, duration
}

package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	// Configuration
	NUM_CONCURRENT_CLIENTS = 50  // Number of concurrent client connections
	OPERATIONS_PER_CLIENT  = 100 // Operations per client
	OPERATION_DELAY_MS     = 100 // Delay between operations (ms)
	LONG_RUNNING_DURATION  = 30  // Duration for long-running connections (seconds)
	MONITORING_INTERVAL    = 5   // Metrics monitoring interval (seconds)
)

type ClientStats struct {
	ID            int
	Database      string
	Operations    int
	Errors        int
	StartTime     time.Time
	EndTime       time.Time
	IsLongRunning bool
}

func main() {
	fmt.Println("🚀 MongoBouncer Sessions Load Test")
	fmt.Println("===================================")
	fmt.Printf("📊 Configuration:\n")
	fmt.Printf("   • Concurrent Clients: %d\n", NUM_CONCURRENT_CLIENTS)
	fmt.Printf("   • Operations per Client: %d\n", OPERATIONS_PER_CLIENT)
	fmt.Printf("   • Operation Delay: %dms\n", OPERATION_DELAY_MS)
	fmt.Printf("   • Long Running Duration: %ds\n", LONG_RUNNING_DURATION)
	fmt.Printf("   • Monitoring Interval: %ds\n", MONITORING_INTERVAL)
	fmt.Println()

	// Start metrics monitoring in background
	go monitorMetrics()

	// Test 2: Sustained connections (medium-lived)
	fmt.Println("\n🧪 Test 2: Sustained Connections (Medium-lived)")
	testSustainedConnections()

	// Test 3: Long-running connections
	fmt.Println("\n🧪 Test 3: Long-running Connections")
	testLongRunningConnections()

	// Test 4: Mixed workload
	fmt.Println("\n🧪 Test 4: Mixed Workload (All Types)")
	testMixedWorkload()

	fmt.Println("\n🎉 Load test completed!")
	fmt.Println("📊 Check final metrics: curl -s http://localhost:9090/metrics | grep mongobouncer_sessions_active_total")
}

func testSustainedConnections() {
	fmt.Println("   Creating sustained medium-lived connections...")

	var wg sync.WaitGroup
	statsChan := make(chan ClientStats, NUM_CONCURRENT_CLIENTS/2)

	for i := 0; i < NUM_CONCURRENT_CLIENTS/2; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			stats := runSustainedClient(clientID)
			statsChan <- stats
		}(i)
	}

	wg.Wait()
	close(statsChan)

	// Collect and display stats
	var totalOps, totalErrors int
	for stats := range statsChan {
		totalOps += stats.Operations
		totalErrors += stats.Errors
	}

	fmt.Printf("   ✅ Sustained test completed: %d operations, %d errors\n", totalOps, totalErrors)
}

func testLongRunningConnections() {
	fmt.Println("   Creating long-running connections...")

	var wg sync.WaitGroup
	statsChan := make(chan ClientStats, 10) // Fewer long-running clients

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			stats := runLongRunningClient(clientID)
			statsChan <- stats
		}(i)
	}

	wg.Wait()
	close(statsChan)

	// Collect and display stats
	var totalOps, totalErrors int
	for stats := range statsChan {
		totalOps += stats.Operations
		totalErrors += stats.Errors
	}

	fmt.Printf("   ✅ Long-running test completed: %d operations, %d errors\n", totalOps, totalErrors)
}

func testMixedWorkload() {
	fmt.Println("   Creating mixed workload with all connection types...")

	var wg sync.WaitGroup
	statsChan := make(chan ClientStats, NUM_CONCURRENT_CLIENTS+20)

	// Sustained clients
	for i := 0; i < NUM_CONCURRENT_CLIENTS/3; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			stats := runSustainedClient(clientID + 1000)
			statsChan <- stats
		}(i)
	}

	// Long-running clients
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			stats := runLongRunningClient(clientID + 2000)
			statsChan <- stats
		}(i)
	}

	wg.Wait()
	close(statsChan)

	// Collect and display stats
	var totalOps, totalErrors int
	for stats := range statsChan {
		totalOps += stats.Operations
		totalErrors += stats.Errors
	}

	fmt.Printf("   ✅ Mixed workload completed: %d operations, %d errors\n", totalOps, totalErrors)
}

func runSustainedClient(clientID int) ClientStats {
	stats := ClientStats{
		ID:        clientID,
		StartTime: time.Now(),
		Database:  fmt.Sprintf("sustained_db_%d", clientID%3), // Use 3 different databases
	}

	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(fmt.Sprintf("mongodb://localhost:27017/%s?retryWrites=false", stats.Database)))
	if err != nil {
		log.Printf("Client %d failed to connect: %v", clientID, err)
		stats.Errors++
		return stats
	}
	defer client.Disconnect(context.TODO())

	collection := client.Database(stats.Database).Collection("sustained_test")

	// Run sustained operations with regular delays
	for i := 0; i < OPERATIONS_PER_CLIENT; i++ {
		_, err := collection.InsertOne(context.TODO(), map[string]interface{}{
			"client_id": clientID,
			"operation": i,
			"type":      "sustained",
			"timestamp": time.Now(),
		})
		if err != nil {
			stats.Errors++
		} else {
			stats.Operations++
		}

		// Regular delay
		time.Sleep(time.Duration(OPERATION_DELAY_MS) * time.Millisecond)
	}

	stats.EndTime = time.Now()
	return stats
}

func runLongRunningClient(clientID int) ClientStats {
	stats := ClientStats{
		ID:            clientID,
		StartTime:     time.Now(),
		Database:      fmt.Sprintf("longrun_db_%d", clientID%2), // Use 2 different databases
		IsLongRunning: true,
	}

	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(fmt.Sprintf("mongodb://localhost:27017/%s?retryWrites=false", stats.Database)))
	if err != nil {
		log.Printf("Client %d failed to connect: %v", clientID, err)
		stats.Errors++
		return stats
	}
	defer client.Disconnect(context.TODO())

	collection := client.Database(stats.Database).Collection("longrun_test")

	// Run operations for the specified duration
	endTime := time.Now().Add(time.Duration(LONG_RUNNING_DURATION) * time.Second)
	operationCount := 0

	for time.Now().Before(endTime) {
		_, err := collection.InsertOne(context.TODO(), map[string]interface{}{
			"client_id": clientID,
			"operation": operationCount,
			"type":      "long_running",
			"timestamp": time.Now(),
		})
		if err != nil {
			stats.Errors++
		} else {
			stats.Operations++
		}
		operationCount++

		// Longer delay for long-running operations
		time.Sleep(time.Duration(OPERATION_DELAY_MS*2) * time.Millisecond)
	}

	stats.EndTime = time.Now()
	return stats
}

func monitorMetrics() {
	ticker := time.NewTicker(time.Duration(MONITORING_INTERVAL) * time.Second)
	defer ticker.Stop()

	fmt.Println("📊 Starting metrics monitoring...")
	fmt.Println("   (Check metrics manually: curl -s http://localhost:9090/metrics | grep mongobouncer_sessions_active_total)")
	fmt.Println()

	for {
		select {
		case <-ticker.C:
			fmt.Printf("⏰ [%s] Monitoring checkpoint - Check metrics now!\n", time.Now().Format("15:04:05"))
			fmt.Println("   Command: curl -s http://localhost:9090/metrics | grep mongobouncer_sessions_active_total")
			fmt.Println()
		}
	}
}

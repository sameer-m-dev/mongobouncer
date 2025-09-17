package mongo

import (
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// RequestForwarder handles forwarding requests between pods for session routing
type RequestForwarder struct {
	distributedCache *DistributedCache
	connections      map[string]*PodConnection // podIP:port -> connection
	mutex            sync.RWMutex
	logger           *zap.Logger
	config           *RequestForwarderConfig
}

// RequestForwarderConfig holds configuration for request forwarding
type RequestForwarderConfig struct {
	MaxConnections    int           `toml:"max_connections"`
	ConnectionTimeout time.Duration `toml:"connection_timeout"`
	KeepAlive         bool          `toml:"keep_alive"`
	KeepAliveInterval time.Duration `toml:"keep_alive_interval"`
}

// PodConnection represents a connection to another pod
type PodConnection struct {
	podIP        string
	podPort      int
	conn         net.Conn
	lastUsed     time.Time
	createdAt    time.Time
	requestCount int64
	mutex        sync.RWMutex
}

// NewRequestForwarder creates a new request forwarder
func NewRequestForwarder(config *RequestForwarderConfig, distributedCache *DistributedCache, logger *zap.Logger) *RequestForwarder {
	return &RequestForwarder{
		distributedCache: distributedCache,
		connections:      make(map[string]*PodConnection),
		logger:           logger,
		config:           config,
	}
}

// ForwardRequest forwards a MongoDB request to the correct pod
func (rf *RequestForwarder) ForwardRequest(sessionID string, request []byte, clientIP string) ([]byte, error) {
	// Check if session exists locally first
	if rf.distributedCache != nil && rf.distributedCache.IsEnabled() {
		if rf.hasLocalSession(sessionID) {
			rf.logger.Debug("Session exists locally, processing locally",
				zap.String("session_id", sessionID))
			return nil, nil // Signal to process locally
		}
	}

	// Check if session exists on another pod
	podIP, podPort, exists := rf.getSessionLocation(sessionID)
	if !exists {
		rf.logger.Debug("Session not found anywhere, will create new session",
			zap.String("session_id", sessionID))
		return nil, nil // Signal to create new session locally
	}

	// Forward request to the correct pod
	rf.logger.Debug("Forwarding request to pod",
		zap.String("session_id", sessionID),
		zap.String("target_pod", fmt.Sprintf("%s:%d", podIP, podPort)),
		zap.String("client_ip", clientIP))

	return rf.forwardToPod(sessionID, request, podIP, podPort, clientIP)
}

// forwardToPod forwards a request to a specific pod using the same MongoDB port
func (rf *RequestForwarder) forwardToPod(sessionID string, request []byte, podIP string, podPort int, clientIP string) ([]byte, error) {
	// Get or create connection to target pod (using same MongoDB port)
	conn, err := rf.getPodConnection(podIP, podPort)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection to pod %s:%d: %w", podIP, podPort, err)
	}

	// Send the raw MongoDB request directly to the target pod
	conn.mutex.Lock()
	defer conn.mutex.Unlock()

	// Send request data directly (no custom protocol wrapper)
	if _, err := conn.conn.Write(request); err != nil {
		return nil, fmt.Errorf("failed to send request data: %w", err)
	}

	// Update connection stats
	conn.lastUsed = time.Now()
	conn.requestCount++

	// Read response (assuming it's a complete MongoDB response)
	// In practice, we'd need to parse the MongoDB wire protocol to know the response size
	response := make([]byte, 1024*1024) // 1MB buffer for response
	n, err := conn.conn.Read(response)
	if err != nil {
		return nil, fmt.Errorf("failed to read response data: %w", err)
	}

	rf.logger.Debug("Successfully forwarded request to pod",
		zap.String("session_id", sessionID),
		zap.String("target_pod", fmt.Sprintf("%s:%d", podIP, podPort)),
		zap.Int("response_size", n))

	return response[:n], nil
}

// getPodConnection gets or creates a connection to a specific pod
func (rf *RequestForwarder) getPodConnection(podIP string, podPort int) (*PodConnection, error) {
	key := fmt.Sprintf("%s:%d", podIP, podPort)

	rf.mutex.RLock()
	conn, exists := rf.connections[key]
	rf.mutex.RUnlock()

	if exists && conn.isHealthy() {
		return conn, nil
	}

	// Create new connection
	rf.mutex.Lock()
	defer rf.mutex.Unlock()

	// Double-check after acquiring write lock
	if conn, exists := rf.connections[key]; exists && conn.isHealthy() {
		return conn, nil
	}

	// Create new connection
	newConn, err := rf.createConnection(podIP, podPort)
	if err != nil {
		return nil, err
	}

	rf.connections[key] = newConn
	rf.logger.Debug("Created new connection to pod",
		zap.String("pod", key))

	return newConn, nil
}

// createConnection creates a new connection to a pod
func (rf *RequestForwarder) createConnection(podIP string, podPort int) (*PodConnection, error) {
	address := fmt.Sprintf("%s:%d", podIP, podPort)

	conn, err := net.DialTimeout("tcp", address, rf.config.ConnectionTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", address, err)
	}

	// Set keep-alive if enabled
	if rf.config.KeepAlive {
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(rf.config.KeepAliveInterval)
		}
	}

	podConn := &PodConnection{
		podIP:     podIP,
		podPort:   podPort,
		conn:      conn,
		lastUsed:  time.Now(),
		createdAt: time.Now(),
	}

	return podConn, nil
}

// isHealthy checks if a connection is still healthy
func (pc *PodConnection) isHealthy() bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	if pc.conn == nil {
		return false
	}

	// Check if connection is still alive by setting a read deadline
	pc.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	one := []byte{0}
	_, err := pc.conn.Read(one)
	pc.conn.SetReadDeadline(time.Time{}) // Clear deadline

	return err != nil // If we get an error, connection is likely dead
}

// getSessionLocation gets the location of a session from distributed cache
func (rf *RequestForwarder) getSessionLocation(sessionID string) (string, int, bool) {
	if rf.distributedCache == nil || !rf.distributedCache.IsEnabled() {
		return "", 0, false
	}

	return rf.distributedCache.GetSessionLocation(sessionID)
}

// hasLocalSession checks if a session exists locally
func (rf *RequestForwarder) hasLocalSession(sessionID string) bool {
	if rf.distributedCache == nil || !rf.distributedCache.IsEnabled() {
		return false
	}

	// This would need to be implemented in the session manager
	// For now, we'll assume it doesn't exist locally if we're forwarding
	return false
}

// StoreSessionLocation stores the location of a session
func (rf *RequestForwarder) StoreSessionLocation(sessionID, podIP string, podPort int) error {
	if rf.distributedCache == nil || !rf.distributedCache.IsEnabled() {
		return nil
	}

	return rf.distributedCache.StoreSessionLocation(sessionID, podIP, podPort)
}

// GetStats returns statistics about the pod forwarder
func (rf *RequestForwarder) GetStats() map[string]interface{} {
	rf.mutex.RLock()
	defer rf.mutex.RUnlock()

	stats := map[string]interface{}{
		"enabled":     rf.distributedCache != nil && rf.distributedCache.IsEnabled(),
		"connections": len(rf.connections),
	}

	connectionStats := make([]map[string]interface{}, 0, len(rf.connections))
	for key, conn := range rf.connections {
		conn.mutex.RLock()
		connStats := map[string]interface{}{
			"pod":           key,
			"created_at":    conn.createdAt,
			"last_used":     conn.lastUsed,
			"request_count": conn.requestCount,
			"healthy":       conn.isHealthy(),
		}
		conn.mutex.RUnlock()
		connectionStats = append(connectionStats, connStats)
	}

	stats["connection_details"] = connectionStats
	return stats
}

package mongo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/groupcache/groupcache-go/v3"
	"github.com/groupcache/groupcache-go/v3/transport"
	"github.com/groupcache/groupcache-go/v3/transport/peer"
	"github.com/udhos/kube/kubeclient"
	"github.com/udhos/kubegroup/kubegroup"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.uber.org/zap"
)

// DistributedCacheConfig holds configuration for the distributed cache
type DistributedCacheConfig struct {
	Enabled           bool          `toml:"enabled"`
	ListenAddr        string        `toml:"listen_addr"`
	ListenPort        int           `toml:"listen_port"`
	CacheSizeBytes    int64         `toml:"cache_size_bytes"`
	SessionExpiry     time.Duration `toml:"session_expiry"`
	TransactionExpiry time.Duration `toml:"transaction_expiry"`
	CursorExpiry      time.Duration `toml:"cursor_expiry"`
	Debug             bool          `toml:"debug"`
	LabelSelector     string        `toml:"label_selector"` // Kubernetes label selector for peer discovery
	// Manual peer configuration for non-Kubernetes environments
	PeerURLs []string `toml:"peer_urls"`
}

// DistributedCache manages distributed caching for sessions, transactions, and cursors
type DistributedCache struct {
	config           *DistributedCacheConfig
	logger           *zap.Logger
	daemon           *groupcache.Daemon
	discoveryGroup   *kubegroup.Group
	sessionGroup     transport.Group
	transactionGroup transport.Group
	cursorGroup      transport.Group
	enabled          bool
	myAddress        string
}

// SessionData represents session data for distributed storage
type SessionData struct {
	ID               string           `json:"id"`
	LsID             []byte           `json:"lsid"`
	ClientID         string           `json:"client_id"`
	State            SessionState     `json:"state"`
	CreatedAt        time.Time        `json:"created_at"`
	LastActivity     time.Time        `json:"last_activity"`
	TransactionState TransactionState `json:"transaction_state"`
	CurrentTxnNumber int64            `json:"current_txn_number"`
	PinnedServerID   string           `json:"pinned_server_id,omitempty"`
	PinnedConnID     string           `json:"pinned_conn_id,omitempty"`
}

// TransactionData represents transaction data for distributed storage
type TransactionData struct {
	LsID           []byte    `json:"lsid"`
	ServerID       string    `json:"server_id"`
	TransactionNum int64     `json:"transaction_num"`
	CreatedAt      time.Time `json:"created_at"`
}

// CursorData represents cursor data for distributed storage
type CursorData struct {
	CursorID     int64     `json:"cursor_id"`
	Collection   string    `json:"collection"`
	ServerID     string    `json:"server_id"`
	DatabaseName string    `json:"database_name"`
	CreatedAt    time.Time `json:"created_at"`
}

// NewDistributedCache creates a new distributed cache instance
func NewDistributedCache(config *DistributedCacheConfig, logger *zap.Logger) *DistributedCache {
	if !config.Enabled {
		return &DistributedCache{
			config:  config,
			logger:  logger,
			enabled: false,
		}
	}

	dc := &DistributedCache{
		config:  config,
		logger:  logger,
		enabled: true,
	}

	dc.myAddress = fmt.Sprintf("%s:%d", config.ListenAddr, config.ListenPort)

	// Start groupcache daemon
	daemon, err := groupcache.ListenAndServe(context.Background(), dc.myAddress, groupcache.Options{})
	if err != nil {
		logger.Error("Failed to start groupcache daemon", zap.Error(err))
		return &DistributedCache{
			config:  config,
			logger:  logger,
			enabled: false,
		}
	}

	dc.daemon = daemon

	// Initialize Kubernetes client for peer discovery (only if not using manual peers)
	if len(config.PeerURLs) == 0 {
		clientsetOpt := kubeclient.Options{DebugLog: config.Debug}
		clientset, err := kubeclient.New(clientsetOpt)
		if err != nil {
			logger.Warn("Failed to create Kubernetes client, falling back to single-node mode", zap.Error(err))
			// Continue without peer discovery - will work as single-node cache
		} else {
			// Start peer discovery using configurable label selector
			labelSelector := config.LabelSelector
			if labelSelector == "" {
				logger.Warn("No label selector configured, using default label selector", zap.String("label_selector", "app.kubernetes.io/name=mongobouncer"))
				// Default to Helm chart auto-generated labels
				labelSelector = "app.kubernetes.io/name=mongobouncer"
			}

			options := kubegroup.Options{
				Client:         clientset,
				Peers:          daemon,
				LabelSelector:  labelSelector,
				GroupCachePort: fmt.Sprintf(":%d", config.ListenPort),
				Debug:          config.Debug,
			}

			discoveryGroup, err := kubegroup.UpdatePeers(options)
			if err != nil {
				logger.Warn("Failed to start peer discovery, falling back to single-node mode", zap.Error(err))
				// Continue without peer discovery - will work as single-node cache
			} else {
				dc.discoveryGroup = discoveryGroup
				logger.Info("Kubernetes peer discovery enabled", zap.String("label_selector", labelSelector))
			}
		}
	} else {
		// Manual peer configuration for non-Kubernetes environments
		if len(config.PeerURLs) > 0 {
			logger.Info("Using manual peer configuration", zap.Strings("peer_urls", config.PeerURLs))

			// Convert peer URLs to peer.Info structures
			peers := make([]peer.Info, 0, len(config.PeerURLs)+1)

			// Add self as a peer
			peers = append(peers, peer.Info{
				Address: dc.myAddress,
				IsSelf:  true,
			})

			// Add manual peers
			for _, peerURL := range config.PeerURLs {
				// Extract host:port from URL (remove http:// prefix if present)
				peerAddr := peerURL
				if len(peerAddr) > 7 && peerAddr[:7] == "http://" {
					peerAddr = peerAddr[7:]
				}

				peers = append(peers, peer.Info{
					Address: peerAddr,
					IsSelf:  false,
				})

				logger.Info("Added manual peer", zap.String("peer_url", peerURL), zap.String("peer_addr", peerAddr))
			}

			// Set peers in the daemon
			if err := daemon.SetPeers(context.Background(), peers); err != nil {
				logger.Error("Failed to set manual peers", zap.Error(err))
			} else {
				logger.Info("Manual peer configuration completed", zap.Int("peer_count", len(peers)))
			}
		} else {
			logger.Info("No manual peers configured, using single-node mode")
		}
	}

	// Create cache groups for different data types
	dc.sessionGroup = dc.createGroup("sessions", dc.sessionGetter)
	dc.transactionGroup = dc.createGroup("transactions", dc.transactionGetter)
	dc.cursorGroup = dc.createGroup("cursors", dc.cursorGetter)

	logger.Info("Distributed cache initialized",
		zap.String("my_address", dc.myAddress),
		zap.String("label_selector", config.LabelSelector),
		zap.Int64("cache_size_bytes", config.CacheSizeBytes))

	return dc
}

// createGroup creates a groupcache group with the specified name and getter
func (dc *DistributedCache) createGroup(name string, getter groupcache.GetterFunc) transport.Group {
	group, err := dc.daemon.NewGroup(name, dc.config.CacheSizeBytes/3, getter)
	if err != nil {
		dc.logger.Error("Failed to create groupcache group",
			zap.String("name", name),
			zap.Error(err))
		return nil
	}
	return group
}

// Start starts the distributed cache (no-op for kubegroup as it starts automatically)
func (dc *DistributedCache) Start() error {
	if !dc.enabled {
		dc.logger.Info("Distributed cache is disabled, skipping start")
		return nil
	}

	dc.logger.Info("Distributed cache started", zap.String("my_address", dc.myAddress))
	return nil
}

// Stop stops the distributed cache
func (dc *DistributedCache) Stop() error {
	if !dc.enabled {
		return nil
	}

	// Stop peer discovery
	if dc.discoveryGroup != nil {
		dc.discoveryGroup.Close()
	}

	// Stop groupcache daemon
	if dc.daemon != nil {
		dc.daemon.Shutdown(context.Background())
	}

	dc.logger.Info("Distributed cache stopped")
	return nil
}

// StoreSession stores session data in the distributed cache
func (dc *DistributedCache) StoreSession(session *Session) error {
	if !dc.enabled || dc.sessionGroup == nil {
		return nil
	}

	sessionData := &SessionData{
		ID:               session.ID,
		LsID:             session.LsID,
		ClientID:         session.ClientID,
		State:            session.State,
		CreatedAt:        session.CreatedAt,
		LastActivity:     session.LastActivity,
		TransactionState: session.TransactionState,
		CurrentTxnNumber: session.CurrentTxnNumber,
	}

	// Convert server/connection references to IDs for serialization
	if session.PinnedServer != nil {
		sessionData.PinnedServerID = fmt.Sprintf("server_%p", session.PinnedServer)
	}
	if session.PinnedConnection != nil {
		sessionData.PinnedConnID = fmt.Sprintf("conn_%p", session.PinnedConnection)
	}

	data, err := json.Marshal(sessionData)
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	// Store in groupcache with session ID as key
	expire := time.Now().Add(dc.config.SessionExpiry)
	err = dc.sessionGroup.Set(context.Background(), session.ID, data, expire, false)
	if err != nil {
		dc.logger.Warn("Failed to store session in distributed cache",
			zap.String("session_id", session.ID),
			zap.Error(err))
	}

	return nil
}

// GetSession retrieves session data from the distributed cache
func (dc *DistributedCache) GetSession(sessionID string) (*Session, bool) {
	if !dc.enabled || dc.sessionGroup == nil {
		return nil, false
	}

	var data []byte
	err := dc.sessionGroup.Get(context.Background(), sessionID, transport.AllocatingByteSliceSink(&data))
	if err != nil {
		dc.logger.Debug("Session not found in distributed cache",
			zap.String("session_id", sessionID),
			zap.Error(err))
		return nil, false
	}

	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err != nil {
		dc.logger.Error("Failed to unmarshal session data",
			zap.String("session_id", sessionID),
			zap.Error(err))
		return nil, false
	}

	// Convert back to Session object
	session := &Session{
		ID:               sessionData.ID,
		LsID:             sessionData.LsID,
		ClientID:         sessionData.ClientID,
		State:            sessionData.State,
		CreatedAt:        sessionData.CreatedAt,
		LastActivity:     sessionData.LastActivity,
		TransactionState: sessionData.TransactionState,
		CurrentTxnNumber: sessionData.CurrentTxnNumber,
		// Note: PinnedServer and PinnedConnection will need to be resolved separately
		// as they can't be serialized across the network
	}

	return session, true
}

// StoreTransaction stores transaction data in the distributed cache
func (dc *DistributedCache) StoreTransaction(lsid []byte, server driver.Server, txnNumber int64) error {
	if !dc.enabled || dc.transactionGroup == nil {
		return nil
	}

	transactionData := &TransactionData{
		LsID:           lsid,
		ServerID:       fmt.Sprintf("server_%p", server),
		TransactionNum: txnNumber,
		CreatedAt:      time.Now(),
	}

	data, err := json.Marshal(transactionData)
	if err != nil {
		return fmt.Errorf("failed to marshal transaction data: %w", err)
	}

	key := fmt.Sprintf("txn_%x", lsid)
	expire := time.Now().Add(dc.config.TransactionExpiry)
	err = dc.transactionGroup.Set(context.Background(), key, data, expire, false)
	if err != nil {
		dc.logger.Warn("Failed to store transaction in distributed cache",
			zap.String("key", key),
			zap.Error(err))
	}

	return nil
}

// GetTransaction retrieves transaction data from the distributed cache
func (dc *DistributedCache) GetTransaction(lsid []byte) (driver.Server, bool) {
	if !dc.enabled || dc.transactionGroup == nil {
		return nil, false
	}

	key := fmt.Sprintf("txn_%x", lsid)
	var data []byte
	err := dc.transactionGroup.Get(context.Background(), key, transport.AllocatingByteSliceSink(&data))
	if err != nil {
		return nil, false
	}

	var transactionData TransactionData
	if err := json.Unmarshal(data, &transactionData); err != nil {
		dc.logger.Error("Failed to unmarshal transaction data", zap.Error(err))
		return nil, false
	}

	// Note: We can't deserialize the actual server object, so we return nil
	// The caller will need to handle server resolution separately
	return nil, true
}

// StoreCursor stores cursor data in the distributed cache
func (dc *DistributedCache) StoreCursor(cursorID int64, collection string, server driver.Server, databaseName string) error {
	if !dc.enabled || dc.cursorGroup == nil {
		return nil
	}

	cursorData := &CursorData{
		CursorID:     cursorID,
		Collection:   collection,
		ServerID:     fmt.Sprintf("server_%p", server),
		DatabaseName: databaseName,
		CreatedAt:    time.Now(),
	}

	data, err := json.Marshal(cursorData)
	if err != nil {
		return fmt.Errorf("failed to marshal cursor data: %w", err)
	}

	key := fmt.Sprintf("cursor_%d_%s", cursorID, collection)
	expire := time.Now().Add(dc.config.CursorExpiry)
	err = dc.cursorGroup.Set(context.Background(), key, data, expire, false)
	if err != nil {
		dc.logger.Warn("Failed to store cursor in distributed cache",
			zap.String("key", key),
			zap.Error(err))
	}

	return nil
}

// GetCursor retrieves cursor data from the distributed cache
func (dc *DistributedCache) GetCursor(cursorID int64, collection string) (driver.Server, string, bool) {
	if !dc.enabled || dc.cursorGroup == nil {
		return nil, "", false
	}

	key := fmt.Sprintf("cursor_%d_%s", cursorID, collection)
	var data []byte
	err := dc.cursorGroup.Get(context.Background(), key, transport.AllocatingByteSliceSink(&data))
	if err != nil {
		return nil, "", false
	}

	var cursorData CursorData
	if err := json.Unmarshal(data, &cursorData); err != nil {
		dc.logger.Error("Failed to unmarshal cursor data", zap.Error(err))
		return nil, "", false
	}

	// Note: We can't deserialize the actual server object, so we return nil
	// The caller will need to handle server resolution separately
	return nil, cursorData.DatabaseName, true
}

// RemoveSession removes session data from the distributed cache
func (dc *DistributedCache) RemoveSession(sessionID string) error {
	if !dc.enabled || dc.sessionGroup == nil {
		return nil
	}

	err := dc.sessionGroup.Remove(context.Background(), sessionID)
	if err != nil {
		dc.logger.Warn("Failed to remove session from distributed cache",
			zap.String("session_id", sessionID),
			zap.Error(err))
	}

	return err
}

// RemoveTransaction removes transaction data from the distributed cache
func (dc *DistributedCache) RemoveTransaction(lsid []byte) error {
	if !dc.enabled || dc.transactionGroup == nil {
		return nil
	}

	key := fmt.Sprintf("txn_%x", lsid)
	err := dc.transactionGroup.Remove(context.Background(), key)
	if err != nil {
		dc.logger.Warn("Failed to remove transaction from distributed cache",
			zap.String("key", key),
			zap.Error(err))
	}

	return err
}

// RemoveCursor removes cursor data from the distributed cache
func (dc *DistributedCache) RemoveCursor(cursorID int64, collection string) error {
	if !dc.enabled || dc.cursorGroup == nil {
		return nil
	}

	key := fmt.Sprintf("cursor_%d_%s", cursorID, collection)
	err := dc.cursorGroup.Remove(context.Background(), key)
	if err != nil {
		dc.logger.Warn("Failed to remove cursor from distributed cache",
			zap.String("key", key),
			zap.Error(err))
	}

	return err
}

// IsEnabled returns whether the distributed cache is enabled
func (dc *DistributedCache) IsEnabled() bool {
	return dc.enabled
}

// GetStats returns cache statistics
func (dc *DistributedCache) GetStats() map[string]interface{} {
	if !dc.enabled {
		return map[string]interface{}{
			"enabled": false,
		}
	}

	stats := map[string]interface{}{
		"enabled":    true,
		"my_address": dc.myAddress,
	}

	if dc.sessionGroup != nil {
		usedBytes, totalBytes := dc.sessionGroup.UsedBytes()
		stats["session_group"] = map[string]interface{}{
			"name":        dc.sessionGroup.Name(),
			"used_bytes":  usedBytes,
			"total_bytes": totalBytes,
		}
	}
	if dc.transactionGroup != nil {
		usedBytes, totalBytes := dc.transactionGroup.UsedBytes()
		stats["transaction_group"] = map[string]interface{}{
			"name":        dc.transactionGroup.Name(),
			"used_bytes":  usedBytes,
			"total_bytes": totalBytes,
		}
	}
	if dc.cursorGroup != nil {
		usedBytes, totalBytes := dc.cursorGroup.UsedBytes()
		stats["cursor_group"] = map[string]interface{}{
			"name":        dc.cursorGroup.Name(),
			"used_bytes":  usedBytes,
			"total_bytes": totalBytes,
		}
	}

	return stats
}

// Getter functions for groupcache (these are called when data is not found locally)

func (dc *DistributedCache) sessionGetter(ctx context.Context, key string, dest transport.Sink) error {
	// This is called when a session is not found in the local cache
	// In a real implementation, this might query a database or other storage
	dc.logger.Debug("Session getter called for key", zap.String("key", key))
	return fmt.Errorf("session not found")
}

func (dc *DistributedCache) transactionGetter(ctx context.Context, key string, dest transport.Sink) error {
	// This is called when a transaction is not found in the local cache
	dc.logger.Debug("Transaction getter called for key", zap.String("key", key))
	return fmt.Errorf("transaction not found")
}

func (dc *DistributedCache) cursorGetter(ctx context.Context, key string, dest transport.Sink) error {
	// This is called when a cursor is not found in the local cache
	dc.logger.Debug("Cursor getter called for key", zap.String("key", key))
	return fmt.Errorf("cursor not found")
}

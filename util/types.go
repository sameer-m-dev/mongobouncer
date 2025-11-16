package util

import (
	"time"

	"go.mongodb.org/mongo-driver/mongo/description"
)

// MongoDBClientConfig represents MongoDB client configuration settings
// This is shared between config and proxy packages to avoid duplication
type MongoDBClientConfig struct {
	MaxPoolSize            int                       `toml:"max_pool_size"`
	MinPoolSize            int                       `toml:"min_pool_size"`
	MaxConnIdleTime        time.Duration             `toml:"max_conn_idle_time"`
	ServerSelectionTimeout time.Duration             `toml:"server_selection_timeout"`
	ConnectTimeout         time.Duration             `toml:"connect_timeout"`
	SocketTimeout          time.Duration             `toml:"socket_timeout"`
	HeartbeatInterval      time.Duration             `toml:"heartbeat_interval"`
	RetryWrites            *bool                     `toml:"retry_writes"`
	Topology               *description.TopologyKind `toml:"topology"`
	
	// Connection refresh/cleanup settings
	EnableConnectionRefresh bool          `toml:"enable_connection_refresh"` // Enable automatic connection refresh
	MaxConnectionAge         time.Duration `toml:"max_connection_age"`        // Maximum age before refreshing connections (0 = disabled)
	ConnectionRefreshOnExhaustion bool     `toml:"connection_refresh_on_exhaustion"` // Refresh connections when pool is exhausted
	ConnectionRefreshInterval time.Duration `toml:"connection_refresh_interval"`     // Interval for periodic connection refresh (0 = disabled)
}

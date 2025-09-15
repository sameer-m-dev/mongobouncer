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
}

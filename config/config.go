package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sameer-m-dev/mongobouncer/mongo"
	"github.com/sameer-m-dev/mongobouncer/proxy"
	"github.com/sameer-m-dev/mongobouncer/util"
)

const defaultMetricsAddress = "localhost:9090"

// TOMLConfig represents the complete configuration structure
type TOMLConfig struct {
	Mongobouncer MongobouncerConfig     `toml:"mongobouncer"`
	Databases    map[string]interface{} `toml:"databases"` // Can be string or Database struct
	Users        map[string]User        `toml:"users"`
}

// MongobouncerConfig represents the main mongobouncer configuration
type MongobouncerConfig struct {
	ListenAddr           string   `toml:"listen_addr"`
	ListenPort           int      `toml:"listen_port"`
	UnixSocketPath       string   `toml:"unix_socket_path"`
	LogLevel             string   `toml:"log_level"`
	LogFile              string   `toml:"logfile"`
	PidFile              string   `toml:"pidfile"`
	AuthType             string   `toml:"auth_type"`
	AuthFile             string   `toml:"auth_file"`
	AuthQuery            string   `toml:"auth_query"`
	AdminUsers           []string `toml:"admin_users"`
	StatsUsers           []string `toml:"stats_users"`
	PoolMode             string   `toml:"pool_mode"`
	DefaultPoolSize      int      `toml:"default_pool_size"`
	ReservePoolSize      int      `toml:"reserve_pool_size"`
	MaxClientConn        int      `toml:"max_client_conn"`
	ServerIdleTimeout    int      `toml:"server_idle_timeout"`
	ServerLifetime       int      `toml:"server_lifetime"`
	ServerConnectTimeout int      `toml:"server_connect_timeout"`
	ServerLoginRetry     int      `toml:"server_login_retry"`
	QueryTimeout         int      `toml:"query_timeout"`
	QueryWaitTimeout     int      `toml:"query_wait_timeout"`
	ClientIdleTimeout    int      `toml:"client_idle_timeout"`
	ClientLoginTimeout   int      `toml:"client_login_timeout"`
	MetricsAddress       string   `toml:"metrics_address"`
	MetricsEnabled       bool     `toml:"metrics_enabled"`
	Network              string   `toml:"network"`
	Unlink               bool     `toml:"unlink"`
	Ping                 bool     `toml:"ping"`
	EnableSDAMMetrics    bool     `toml:"enable_sdam_metrics"`
	EnableSDAMLogging    bool     `toml:"enable_sdam_logging"`
	DynamicConfig        string   `toml:"dynamic_config"`
}

// Database represents database configuration
// Can be either a string (connection string) or a struct (detailed config)
type Database struct {
	// Connection string format (when database is just a string)
	ConnectionString string

	// Individual settings (when database is a table)
	Host             string `toml:"host"`
	Port             int    `toml:"port"`
	DBName           string `toml:"dbname"`
	User             string `toml:"user"`
	Password         string `toml:"password"`
	AuthDB           string `toml:"auth_db"`
	PoolMode         string `toml:"pool_mode"`
	PoolSize         int    `toml:"pool_size"`
	MaxDBConnections int    `toml:"max_db_connections"`
	Label            string `toml:"label"`

	// Connection options
	MaxPoolSize              int `toml:"maxPoolSize"`
	MinPoolSize              int `toml:"minPoolSize"`
	MaxIdleTimeMS            int `toml:"maxIdleTimeMS"`
	ServerSelectionTimeoutMS int `toml:"serverSelectionTimeoutMS"`
}

// User represents user configuration
type User struct {
	Password           string `toml:"password"`
	PoolMode           string `toml:"pool_mode"`
	MaxUserConnections int    `toml:"max_user_connections"`
}

// Config represents the runtime configuration
type Config struct {
	tomlConfig *TOMLConfig
	logger     *zap.Logger
	metrics    *util.MetricsClient
	clients    []client
	network    string
	unlink     bool
	ping       bool
	dynamic    string
}

type client struct {
	address string
	label   string
	opts    *options.ClientOptions
}

// LoadConfig loads configuration from TOML file
func LoadConfig(configPath string, verbose bool) (*Config, error) {
	// Set default values
	config := &TOMLConfig{
		Mongobouncer: MongobouncerConfig{
			ListenAddr:      "0.0.0.0",
			ListenPort:      27017,
			LogLevel:        "info",
			Network:         "tcp4",
			PoolMode:        "session",
			DefaultPoolSize: 20,
			ReservePoolSize: 5,
			MaxClientConn:   100,
			MetricsAddress:  "localhost:9090",
			MetricsEnabled:  true,
		},
		Databases: make(map[string]interface{}),
		Users:     make(map[string]User),
	}

	// Load from file if provided
	if configPath != "" {
		if _, err := os.Stat(configPath); err != nil {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}

		var rawConfig map[string]interface{}
		if _, err := toml.DecodeFile(configPath, &rawConfig); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}

		// Parse the raw config into our structured config
		if err := parseRawConfig(rawConfig, config); err != nil {
			return nil, fmt.Errorf("failed to process config: %w", err)
		}
	}

	// Override log level if verbose
	if verbose {
		config.Mongobouncer.LogLevel = "debug"
	}

	// Create logger
	logger, err := createLogger(config.Mongobouncer.LogLevel, config.Mongobouncer.LogFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	// Create Metrics client
	var metricsClient *util.MetricsClient
	if config.Mongobouncer.MetricsEnabled {
		metricsClient, err = util.NewMetricsClient(logger, config.Mongobouncer.MetricsAddress)
		if err != nil {
			logger.Warn("Failed to create metrics client", zap.Error(err))
			// Continue without metrics
		}
	}

	// Build client configurations
	clients, err := buildClients(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to build clients: %w", err)
	}

	return &Config{
		tomlConfig: config,
		logger:     logger,
		metrics:    metricsClient,
		clients:    clients,
		network:    config.Mongobouncer.Network,
		unlink:     config.Mongobouncer.Unlink,
		ping:       config.Mongobouncer.Ping,
		dynamic:    config.Mongobouncer.DynamicConfig,
	}, nil
}

// parseRawConfig parses the raw TOML configuration into our structured config
func parseRawConfig(rawConfig map[string]interface{}, config *TOMLConfig) error {
	// Parse mongobouncer section
	if mb, ok := rawConfig["mongobouncer"].(map[string]interface{}); ok {
		if listenAddr, ok := mb["listen_addr"].(string); ok {
			config.Mongobouncer.ListenAddr = listenAddr
		}
		if listenPort, ok := mb["listen_port"].(int64); ok {
			config.Mongobouncer.ListenPort = int(listenPort)
		}
		if unixSocketPath, ok := mb["unix_socket_path"].(string); ok {
			config.Mongobouncer.UnixSocketPath = unixSocketPath
		}
		if logLevel, ok := mb["log_level"].(string); ok {
			config.Mongobouncer.LogLevel = logLevel
		}
		if logFile, ok := mb["logfile"].(string); ok {
			config.Mongobouncer.LogFile = logFile
		}
		if pidFile, ok := mb["pidfile"].(string); ok {
			config.Mongobouncer.PidFile = pidFile
		}
		if authType, ok := mb["auth_type"].(string); ok {
			config.Mongobouncer.AuthType = authType
		}
		if authFile, ok := mb["auth_file"].(string); ok {
			config.Mongobouncer.AuthFile = authFile
		}
		if authQuery, ok := mb["auth_query"].(string); ok {
			config.Mongobouncer.AuthQuery = authQuery
		}
		if adminUsers, ok := mb["admin_users"].([]interface{}); ok {
			for _, user := range adminUsers {
				if userStr, ok := user.(string); ok {
					config.Mongobouncer.AdminUsers = append(config.Mongobouncer.AdminUsers, userStr)
				}
			}
		}
		if statsUsers, ok := mb["stats_users"].([]interface{}); ok {
			for _, user := range statsUsers {
				if userStr, ok := user.(string); ok {
					config.Mongobouncer.StatsUsers = append(config.Mongobouncer.StatsUsers, userStr)
				}
			}
		}
		if poolMode, ok := mb["pool_mode"].(string); ok {
			config.Mongobouncer.PoolMode = poolMode
		}
		if defaultPoolSize, ok := mb["default_pool_size"].(int64); ok {
			config.Mongobouncer.DefaultPoolSize = int(defaultPoolSize)
		}
		if reservePoolSize, ok := mb["reserve_pool_size"].(int64); ok {
			config.Mongobouncer.ReservePoolSize = int(reservePoolSize)
		}
		if maxClientConn, ok := mb["max_client_conn"].(int64); ok {
			config.Mongobouncer.MaxClientConn = int(maxClientConn)
		}
		if metricsAddress, ok := mb["metrics_address"].(string); ok {
			config.Mongobouncer.MetricsAddress = metricsAddress
		}
		if metricsEnabled, ok := mb["metrics_enabled"].(bool); ok {
			config.Mongobouncer.MetricsEnabled = metricsEnabled
		}
		if network, ok := mb["network"].(string); ok {
			config.Mongobouncer.Network = network
		}
		if unlink, ok := mb["unlink"].(bool); ok {
			config.Mongobouncer.Unlink = unlink
		}
		if ping, ok := mb["ping"].(bool); ok {
			config.Mongobouncer.Ping = ping
		}
		if enableSDAMMetrics, ok := mb["enable_sdam_metrics"].(bool); ok {
			config.Mongobouncer.EnableSDAMMetrics = enableSDAMMetrics
		}
		if enableSDAMLogging, ok := mb["enable_sdam_logging"].(bool); ok {
			config.Mongobouncer.EnableSDAMLogging = enableSDAMLogging
		}
		if dynamicConfig, ok := mb["dynamic_config"].(string); ok {
			config.Mongobouncer.DynamicConfig = dynamicConfig
		}
	}

	// Parse databases section
	if databases, ok := rawConfig["databases"].(map[string]interface{}); ok {
		config.Databases = databases
	}

	// Parse users section
	if users, ok := rawConfig["users"].(map[string]interface{}); ok {
		for username, userConfig := range users {
			if userMap, ok := userConfig.(map[string]interface{}); ok {
				user := User{}
				if password, ok := userMap["password"].(string); ok {
					user.Password = password
				}
				if poolMode, ok := userMap["pool_mode"].(string); ok {
					user.PoolMode = poolMode
				}
				if maxUserConnections, ok := userMap["max_user_connections"].(int64); ok {
					user.MaxUserConnections = int(maxUserConnections)
				}
				config.Users[username] = user
			}
		}
	}

	return nil
}

// buildClients creates client configurations from the TOML config
func buildClients(config *TOMLConfig, logger *zap.Logger) ([]client, error) {
	var clients []client

	// If no databases configured, create a default one
	if len(config.Databases) == 0 {
		// Create default local database connection
		opts := options.Client().ApplyURI("mongodb://localhost:27017")

		// Create listening address
		address := fmt.Sprintf("%s:%d", config.Mongobouncer.ListenAddr, config.Mongobouncer.ListenPort)
		if config.Mongobouncer.UnixSocketPath != "" {
			address = config.Mongobouncer.UnixSocketPath
		}

		clients = append(clients, client{
			address: address,
			label:   "default",
			opts:    opts,
		})

		logger.Info("No databases configured, using default local MongoDB")
		return clients, nil
	}

	// For TOML config, we typically have one proxy listening address but multiple backend databases
	// Create a single client that will handle all database connections

	// Create listening address
	address := fmt.Sprintf("%s:%d", config.Mongobouncer.ListenAddr, config.Mongobouncer.ListenPort)
	if config.Mongobouncer.UnixSocketPath != "" {
		address = config.Mongobouncer.UnixSocketPath
	}

	// Process database configurations to determine the primary connection
	// For now, we'll use the first configured database as the main connection
	// In a full implementation, this would be handled by a router

	var primaryURI string
	var primaryLabel string

	for dbName, dbConfigInterface := range config.Databases {
		var uri string
		var label string

		switch dbConfig := dbConfigInterface.(type) {
		case string:
			// Simple connection string format
			uri = dbConfig
			label = dbName
		case map[string]interface{}:
			// Structured format - build URI
			host := "localhost"
			port := 27017

			if h, ok := dbConfig["host"].(string); ok {
				host = h
			}
			if p, ok := dbConfig["port"].(int64); ok {
				port = int(p)
			}

			var user, password string
			if u, ok := dbConfig["user"].(string); ok {
				user = u
			}
			if p, ok := dbConfig["password"].(string); ok {
				password = p
			}

			// Build URI
			if user != "" && password != "" {
				uri = fmt.Sprintf("mongodb://%s:%s@%s:%d", user, password, host, port)
			} else {
				uri = fmt.Sprintf("mongodb://%s:%d", host, port)
			}

			// Add database name if specified
			if dbname, ok := dbConfig["dbname"].(string); ok && dbname != "" {
				uri += "/" + dbname
			}

			// Add connection options
			params := []string{}
			if maxPoolSize, ok := dbConfig["maxPoolSize"].(int64); ok && maxPoolSize > 0 {
				params = append(params, fmt.Sprintf("maxPoolSize=%d", maxPoolSize))
			} else if poolSize, ok := dbConfig["pool_size"].(int64); ok && poolSize > 0 {
				params = append(params, fmt.Sprintf("maxPoolSize=%d", poolSize))
			}
			if minPoolSize, ok := dbConfig["minPoolSize"].(int64); ok && minPoolSize > 0 {
				params = append(params, fmt.Sprintf("minPoolSize=%d", minPoolSize))
			}
			if maxIdleTimeMS, ok := dbConfig["maxIdleTimeMS"].(int64); ok && maxIdleTimeMS > 0 {
				params = append(params, fmt.Sprintf("maxIdleTimeMS=%d", maxIdleTimeMS))
			}
			if serverSelectionTimeoutMS, ok := dbConfig["serverSelectionTimeoutMS"].(int64); ok && serverSelectionTimeoutMS > 0 {
				params = append(params, fmt.Sprintf("serverSelectionTimeoutMS=%d", serverSelectionTimeoutMS))
			}

			if len(params) > 0 {
				uri += "?" + strings.Join(params, "&")
			}

			// Set label
			if l, ok := dbConfig["label"].(string); ok {
				label = l
			} else {
				label = dbName
			}
		}

		// Use the first database as primary (for simplicity)
		if primaryURI == "" {
			primaryURI = uri
			primaryLabel = label
		}

		logger.Info("Configured database",
			zap.String("name", dbName),
			zap.String("uri", sanitizeURI(uri)),
			zap.String("label", label))
	}

	// Create client options using the primary URI
	if primaryURI == "" {
		primaryURI = "mongodb://localhost:27017"
		primaryLabel = "default"
	}

	opts := options.Client().ApplyURI(primaryURI)

	clients = append(clients, client{
		address: address,
		label:   primaryLabel,
		opts:    opts,
	})

	return clients, nil
}

// createLogger creates a zap logger with the specified level and output
func createLogger(level, logFile string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	switch strings.ToLower(level) {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn", "warning":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	if logFile != "" {
		config.OutputPaths = []string{logFile}
	}

	return config.Build()
}

// sanitizeURI removes sensitive information from URI for logging
func sanitizeURI(uri string) string {
	// Replace password in URI with ***
	if strings.Contains(uri, "@") {
		parts := strings.Split(uri, "@")
		if len(parts) == 2 && strings.Contains(parts[0], "://") {
			userInfo := strings.Split(parts[0], "://")[1]
			if strings.Contains(userInfo, ":") {
				userParts := strings.Split(userInfo, ":")
				sanitized := userParts[0] + ":***"
				return strings.Replace(uri, userInfo, sanitized, 1)
			}
		}
	}
	return uri
}

// Interface methods for compatibility with existing code
func (c *Config) Logger() *zap.Logger {
	return c.logger
}

func (c *Config) Metrics() *util.MetricsClient {
	return c.metrics
}

func (c *Config) Network() string {
	return c.network
}

func (c *Config) Unlink() bool {
	return c.unlink
}

func (c *Config) Ping() bool {
	return c.ping
}

func (c *Config) Dynamic() string {
	return c.dynamic
}

func (c *Config) Proxies(log *zap.Logger) ([]*proxy.Proxy, error) {
	d, err := proxy.NewDynamic(c.dynamic, log)
	if err != nil {
		return nil, err
	}

	mongos := make(map[string]*mongo.Mongo)
	for _, client := range c.clients {
		m, err := mongo.Connect(log, c.metrics, client.opts, c.ping)
		if err != nil {
			return nil, err
		}
		mongos[client.address] = m
	}

	mongoLookup := func(address string) *mongo.Mongo {
		return mongos[address]
	}

	var proxies []*proxy.Proxy
	for _, client := range c.clients {
		p, err := proxy.NewProxy(log, c.metrics, client.label, c.network, client.address, c.unlink, mongoLookup, d)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, p)
	}

	return proxies, nil
}

// GetDatabases returns the database configurations
func (c *Config) GetDatabases() map[string]Database {
	result := make(map[string]Database)

	for name, dbConfig := range c.tomlConfig.Databases {
		switch v := dbConfig.(type) {
		case string:
			// Simple connection string format
			result[name] = Database{
				ConnectionString: v,
			}
		case map[string]interface{}:
			// Structured format - convert to Database struct
			db := Database{}
			if host, ok := v["host"].(string); ok {
				db.Host = host
			}
			if port, ok := v["port"].(int64); ok {
				db.Port = int(port)
			}
			if dbname, ok := v["dbname"].(string); ok {
				db.DBName = dbname
			}
			if user, ok := v["user"].(string); ok {
				db.User = user
			}
			if password, ok := v["password"].(string); ok {
				db.Password = password
			}
			if authdb, ok := v["auth_db"].(string); ok {
				db.AuthDB = authdb
			}
			if poolmode, ok := v["pool_mode"].(string); ok {
				db.PoolMode = poolmode
			}
			if poolsize, ok := v["pool_size"].(int64); ok {
				db.PoolSize = int(poolsize)
			}
			if maxdbconn, ok := v["max_db_connections"].(int64); ok {
				db.MaxDBConnections = int(maxdbconn)
			}
			if label, ok := v["label"].(string); ok {
				db.Label = label
			}
			if maxpoolsize, ok := v["maxPoolSize"].(int64); ok {
				db.MaxPoolSize = int(maxpoolsize)
			}
			if minpoolsize, ok := v["minPoolSize"].(int64); ok {
				db.MinPoolSize = int(minpoolsize)
			}
			if maxidletime, ok := v["maxIdleTimeMS"].(int64); ok {
				db.MaxIdleTimeMS = int(maxidletime)
			}
			if seltimeout, ok := v["serverSelectionTimeoutMS"].(int64); ok {
				db.ServerSelectionTimeoutMS = int(seltimeout)
			}
			result[name] = db
		}
	}

	return result
}

// GetUsers returns the user configurations
func (c *Config) GetUsers() map[string]User {
	return c.tomlConfig.Users
}

// GetMainConfig returns the main configuration
func (c *Config) GetMainConfig() MongobouncerConfig {
	return c.tomlConfig.Mongobouncer
}

package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sameer-m-dev/mongobouncer/mongo"
	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/proxy"
	"github.com/sameer-m-dev/mongobouncer/util"
)

const defaultMetricsAddress = "localhost:9090"

// TOMLConfig represents the complete configuration structure
type TOMLConfig struct {
	Mongobouncer MongobouncerConfig     `toml:"mongobouncer"`
	Databases    map[string]interface{} `toml:"databases"` // Can be string or Database struct
}

// Database represents database configuration
// Can be either a string (connection string) or a struct (detailed config)
type Database struct {
	ConnectionString string `toml:"connection_string"`
	Host             string `toml:"host"`
	Port             int    `toml:"port"`
	Database         string `toml:"database"`
	DBName           string `toml:"dbname"` // Alias for database
	User             string `toml:"user"`
	Password         string `toml:"password"`
	AuthDB           string `toml:"auth_db"`
	MaxConnections   int    `toml:"max_connections"`
	PoolMode         string `toml:"pool_mode"`
	PoolSize         int    `toml:"pool_size"`
	MaxDBConnections int    `toml:"max_db_connections"`
	Label            string `toml:"label"`

	// MongoDB client pool overrides (optional)
	MongoDBMaxPoolSize            int           `toml:"max_pool_size"`
	MongoDBMinPoolSize            int           `toml:"min_pool_size"`
	MongoDBMaxConnIdleTime        time.Duration `toml:"max_conn_idle_time"`
	MongoDBServerSelectionTimeout time.Duration `toml:"server_selection_timeout"`
	MongoDBConnectTimeout         time.Duration `toml:"connect_timeout"`
	MongoDBSocketTimeout          time.Duration `toml:"socket_timeout"`
	MongoDBHeartbeatInterval      time.Duration `toml:"heartbeat_interval"`

	// Legacy connection options (for backward compatibility)
	MaxPoolSize              int `toml:"maxPoolSize"`
	MinPoolSize              int `toml:"minPoolSize"`
	MaxIdleTimeMS            int `toml:"maxIdleTimeMS"`
	ServerSelectionTimeoutMS int `toml:"serverSelectionTimeoutMS"`
}
type MongobouncerConfig struct {
	// Core server settings
	ListenAddr string `toml:"listen_addr"`
	ListenPort int    `toml:"listen_port"`
	LogLevel   string `toml:"log_level"`
	LogFile    string `toml:"logfile"`

	// Connection pooling (implemented)
	PoolMode      string `toml:"pool_mode"`
	MaxClientConn int    `toml:"max_client_conn"`

	// Metrics (implemented)
	MetricsAddress string `toml:"metrics_address"`
	MetricsEnabled bool   `toml:"metrics_enabled"`

	// Authentication settings
	AuthEnabled bool `toml:"auth_enabled"`

	// Wildcard/Regex credential passthrough settings
	RegexCredentialPassthrough bool `toml:"regex_credential_passthrough"`

	// Network settings (implemented)
	Network string `toml:"network"`
	Unlink  bool   `toml:"unlink"`

	// MongoDB client pool settings (implemented)
	MongoDBMaxPoolSize            int           `toml:"max_pool_size"`
	MongoDBMinPoolSize            int           `toml:"min_pool_size"`
	MongoDBMaxConnIdleTime        time.Duration `toml:"max_conn_idle_time"`
	MongoDBServerSelectionTimeout time.Duration `toml:"server_selection_timeout"`
	MongoDBConnectTimeout         time.Duration `toml:"connect_timeout"`
	MongoDBSocketTimeout          time.Duration `toml:"socket_timeout"`
	MongoDBHeartbeatInterval      time.Duration `toml:"heartbeat_interval"`
	Ping                          bool          `toml:"ping"`
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
			ListenAddr:                 "0.0.0.0",
			ListenPort:                 27017,
			LogLevel:                   "info",
			Network:                    "tcp4",
			PoolMode:                   "session",
			MaxClientConn:              100,
			MetricsAddress:             "localhost:9090",
			MetricsEnabled:             true,
			AuthEnabled:                true,
			RegexCredentialPassthrough: true,
		},
		Databases: make(map[string]interface{}),
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
		if logLevel, ok := mb["log_level"].(string); ok {
			config.Mongobouncer.LogLevel = logLevel
		}
		if logFile, ok := mb["logfile"].(string); ok {
			config.Mongobouncer.LogFile = logFile
		}
		if poolMode, ok := mb["pool_mode"].(string); ok {
			config.Mongobouncer.PoolMode = poolMode
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
		if authEnabled, ok := mb["auth_enabled"].(bool); ok {
			config.Mongobouncer.AuthEnabled = authEnabled
		}
		if regexCredentialPassthrough, ok := mb["regex_credential_passthrough"].(bool); ok {
			config.Mongobouncer.RegexCredentialPassthrough = regexCredentialPassthrough
		}
	}

	// Parse databases section
	if databases, ok := rawConfig["databases"].(map[string]interface{}); ok {
		config.Databases = databases
	}

	return nil
}

// validateDatabaseNames checks for conflicting database names/patterns
func validateDatabaseNames(databases map[string]interface{}, logger *zap.Logger) error {
	// Check if databases map is empty
	if len(databases) == 0 {
		return fmt.Errorf("no databases configured - MongoBouncer cannot run without database configuration")
	}

	// Track patterns and exact matches to detect conflicts
	exactMatches := make(map[string]string)     // dbName -> connectionString
	wildcardPatterns := make(map[string]string) // pattern -> connectionString
	prefixPatterns := make(map[string]string)   // pattern -> connectionString
	suffixPatterns := make(map[string]string)   // pattern -> connectionString
	containsPatterns := make(map[string]string) // pattern -> connectionString

	for dbName, dbConfigInterface := range databases {
		var connectionString string

		// Extract connection string for comparison
		switch dbConfig := dbConfigInterface.(type) {
		case string:
			connectionString = dbConfig
		case map[string]interface{}:
			if connStr, ok := dbConfig["connection_string"].(string); ok && connStr != "" {
				connectionString = connStr
			} else {
				// Build URI from individual fields for comparison
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
					connectionString = fmt.Sprintf("mongodb://%s:%s@%s:%d", user, password, host, port)
				} else {
					connectionString = fmt.Sprintf("mongodb://%s:%d", host, port)
				}

				// Add database name if specified
				if dbname, ok := dbConfig["dbname"].(string); ok && dbname != "" {
					connectionString += "/" + dbname
				}
			}
		}

		// Check for wildcard route conflicts
		if dbName == "*" {
			if existing, exists := wildcardPatterns["*"]; exists {
				return fmt.Errorf("conflicting wildcard routes detected: both routes point to different connections (%s vs %s). Only one wildcard route (*) is allowed",
					sanitizeURI(existing), sanitizeURI(connectionString))
			}
			wildcardPatterns["*"] = connectionString
			continue
		}

		// Check for pattern-based conflicts
		if strings.Contains(dbName, "*") {
			// Check for conflicts with exact matches
			for exactName, exactConnStr := range exactMatches {
				if matchesPattern(exactName, dbName) {
					return fmt.Errorf("conflicting database routes detected: pattern '%s' matches exact database '%s' but points to different connections (%s vs %s)",
						dbName, exactName, sanitizeURI(connectionString), sanitizeURI(exactConnStr))
				}
			}

			// Check for conflicts with other patterns
			for pattern, patternConnStr := range prefixPatterns {
				if patternsConflict(dbName, pattern) {
					return fmt.Errorf("conflicting database patterns detected: '%s' and '%s' may match the same databases but point to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}
			for pattern, patternConnStr := range suffixPatterns {
				if patternsConflict(dbName, pattern) {
					return fmt.Errorf("conflicting database patterns detected: '%s' and '%s' may match the same databases but point to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}
			for pattern, patternConnStr := range containsPatterns {
				if patternsConflict(dbName, pattern) {
					return fmt.Errorf("conflicting database patterns detected: '%s' and '%s' may match the same databases but point to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}

			// Categorize the pattern
			if strings.HasPrefix(dbName, "*") && strings.HasSuffix(dbName, "*") {
				containsPatterns[dbName] = connectionString
			} else if strings.HasPrefix(dbName, "*") {
				suffixPatterns[dbName] = connectionString
			} else if strings.HasSuffix(dbName, "*") {
				prefixPatterns[dbName] = connectionString
			} else {
				containsPatterns[dbName] = connectionString
			}
		} else {
			// Exact match - check for conflicts with patterns
			for pattern, patternConnStr := range prefixPatterns {
				if matchesPattern(dbName, pattern) {
					return fmt.Errorf("conflicting database routes detected: exact database '%s' matches pattern '%s' but points to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}
			for pattern, patternConnStr := range suffixPatterns {
				if matchesPattern(dbName, pattern) {
					return fmt.Errorf("conflicting database routes detected: exact database '%s' matches pattern '%s' but points to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}
			for pattern, patternConnStr := range containsPatterns {
				if matchesPattern(dbName, pattern) {
					return fmt.Errorf("conflicting database routes detected: exact database '%s' matches pattern '%s' but points to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}

			// Check for duplicate exact matches
			if existing, exists := exactMatches[dbName]; exists {
				return fmt.Errorf("duplicate database configuration detected: database '%s' is configured multiple times with different connections (%s vs %s)",
					dbName, sanitizeURI(existing), sanitizeURI(connectionString))
			}

			exactMatches[dbName] = connectionString
		}
	}

	logger.Info("Database configuration validation passed",
		zap.Int("exact_matches", len(exactMatches)),
		zap.Int("prefix_patterns", len(prefixPatterns)),
		zap.Int("suffix_patterns", len(suffixPatterns)),
		zap.Int("contains_patterns", len(containsPatterns)),
		zap.Bool("has_wildcard", len(wildcardPatterns) > 0))

	return nil
}

// matchesPattern checks if a database name matches a wildcard pattern
func matchesPattern(dbName, pattern string) bool {
	// Convert pattern to regex
	regexPattern := strings.ReplaceAll(pattern, "*", ".*")
	matched, err := regexp.MatchString("^"+regexPattern+"$", dbName)
	if err != nil {
		return false
	}
	return matched
}

// patternsConflict checks if two patterns could potentially match the same databases
func patternsConflict(pattern1, pattern2 string) bool {
	// Simple heuristic: if both patterns have wildcards in similar positions,
	// they might conflict. This is a conservative check.

	// Convert patterns to regex
	regex1 := strings.ReplaceAll(pattern1, "*", ".*")
	regex2 := strings.ReplaceAll(pattern2, "*", ".*")

	// Test with some common database name patterns to see if they overlap
	testNames := []string{
		"test", "prod", "staging", "dev",
		"test_db", "prod_db", "staging_db", "dev_db",
		"app_test", "app_prod", "app_staging", "app_dev",
		"test_app", "prod_app", "staging_app", "dev_app",
		"myapp_test", "myapp_prod", "myapp_staging", "myapp_dev",
	}

	matches1 := 0
	matches2 := 0

	for _, testName := range testNames {
		if matched, _ := regexp.MatchString("^"+regex1+"$", testName); matched {
			matches1++
		}
		if matched, _ := regexp.MatchString("^"+regex2+"$", testName); matched {
			matches2++
		}
	}

	// If both patterns match a significant number of test names, they might conflict
	return matches1 > 0 && matches2 > 0 && matches1 == matches2
}

// buildClients creates client configurations from the TOML config
func buildClients(config *TOMLConfig, logger *zap.Logger) ([]client, error) {
	var clients []client

	// Validate that databases are configured
	if len(config.Databases) == 0 {
		return nil, fmt.Errorf("no databases configured - MongoBouncer cannot run without database configuration")
	}

	// Validate database name conflicts
	if err := validateDatabaseNames(config.Databases, logger); err != nil {
		return nil, err
	}

	// For TOML config, we typically have one proxy listening address but multiple backend databases
	// Create a single client that will handle all database connections

	// Create listening address
	address := fmt.Sprintf("%s:%d", config.Mongobouncer.ListenAddr, config.Mongobouncer.ListenPort)

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
			// Structured format - check for connection_string first
			if connStr, ok := dbConfig["connection_string"].(string); ok && connStr != "" {
				// Use connection_string if provided
				uri = connStr
			} else {
				// Build URI from individual fields
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

	// Create temporary config instance to apply MongoDB client settings
	tempConfig := &Config{tomlConfig: config, logger: logger}
	opts = tempConfig.applyMongoDBClientSettings(opts, "default", nil)

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

// applyMongoDBClientSettings applies MongoDB client pool settings from config
func (c *Config) applyMongoDBClientSettings(opts *options.ClientOptions, dbName string, dbConfig *Database) *options.ClientOptions {
	// Get defaults from mongobouncer config
	defaults := c.tomlConfig.Mongobouncer

	// Start with defaults
	maxPoolSize := defaults.MongoDBMaxPoolSize
	minPoolSize := defaults.MongoDBMinPoolSize
	maxConnIdleTime := defaults.MongoDBMaxConnIdleTime
	serverSelectionTimeout := defaults.MongoDBServerSelectionTimeout
	connectTimeout := defaults.MongoDBConnectTimeout
	socketTimeout := defaults.MongoDBSocketTimeout
	heartbeatInterval := defaults.MongoDBHeartbeatInterval

	// Apply database-level overrides if provided
	overrides := make(map[string]interface{})
	if dbConfig != nil {
		if dbConfig.MongoDBMaxPoolSize > 0 {
			maxPoolSize = dbConfig.MongoDBMaxPoolSize
			overrides["max_pool_size"] = maxPoolSize
		}
		if dbConfig.MongoDBMinPoolSize > 0 {
			minPoolSize = dbConfig.MongoDBMinPoolSize
			overrides["min_pool_size"] = minPoolSize
		}
		if dbConfig.MongoDBMaxConnIdleTime > 0 {
			maxConnIdleTime = dbConfig.MongoDBMaxConnIdleTime
			overrides["max_conn_idle_time"] = maxConnIdleTime
		}
		if dbConfig.MongoDBServerSelectionTimeout > 0 {
			serverSelectionTimeout = dbConfig.MongoDBServerSelectionTimeout
			overrides["server_selection_timeout"] = serverSelectionTimeout
		}
		if dbConfig.MongoDBConnectTimeout > 0 {
			connectTimeout = dbConfig.MongoDBConnectTimeout
			overrides["connect_timeout"] = connectTimeout
		}
		if dbConfig.MongoDBSocketTimeout > 0 {
			socketTimeout = dbConfig.MongoDBSocketTimeout
			overrides["socket_timeout"] = socketTimeout
		}
		if dbConfig.MongoDBHeartbeatInterval > 0 {
			heartbeatInterval = dbConfig.MongoDBHeartbeatInterval
			overrides["heartbeat_interval"] = heartbeatInterval
		}
	}

	// Set defaults ONLY if not configured by user
	if maxPoolSize == 0 {
		maxPoolSize = 10 // Only use default if user didn't provide a value
	}
	if minPoolSize == 0 {
		minPoolSize = 1 // Only use default if user didn't provide a value
	}
	if maxConnIdleTime == 0 {
		maxConnIdleTime = 30 * time.Second // Only use default if user didn't provide a value
	}
	if serverSelectionTimeout == 0 {
		serverSelectionTimeout = 30 * time.Second // Only use default if user didn't provide a value
	}
	if connectTimeout == 0 {
		connectTimeout = 30 * time.Second // Only use default if user didn't provide a value
	}
	if socketTimeout == 0 {
		socketTimeout = 30 * time.Second // Only use default if user didn't provide a value
	}
	if heartbeatInterval == 0 {
		heartbeatInterval = 10 * time.Second // Only use default if user didn't provide a value
	}

	// Log the configuration being used
	if len(overrides) > 0 {
		c.logger.Info("Using database-specific MongoDB client overrides",
			zap.String("database", dbName),
			zap.Any("overrides", overrides))
	} else {
		c.logger.Info("Using default MongoDB client settings",
			zap.String("database", dbName))
	}

	// Apply settings
	opts = opts.SetMaxPoolSize(uint64(maxPoolSize))
	opts = opts.SetMinPoolSize(uint64(minPoolSize))
	opts = opts.SetMaxConnIdleTime(maxConnIdleTime)
	opts = opts.SetServerSelectionTimeout(serverSelectionTimeout)
	opts = opts.SetConnectTimeout(connectTimeout)
	opts = opts.SetSocketTimeout(socketTimeout)
	opts = opts.SetHeartbeatInterval(heartbeatInterval)

	// Log final configuration
	c.logger.Info("MongoDB client configuration applied",
		zap.String("database", dbName),
		zap.Int("max_pool_size", maxPoolSize),
		zap.Int("min_pool_size", minPoolSize),
		zap.Duration("max_conn_idle_time", maxConnIdleTime),
		zap.Duration("server_selection_timeout", serverSelectionTimeout),
		zap.Duration("connect_timeout", connectTimeout),
		zap.Duration("socket_timeout", socketTimeout),
		zap.Duration("heartbeat_interval", heartbeatInterval))

	return opts
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

func (c *Config) Proxies(log *zap.Logger) ([]*proxy.Proxy, error) {
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

	// Create database router for wildcard database support
	databaseRouter := proxy.NewDatabaseRouter(log)

	// Add database routes with precedence: exact match → wildcard match
	databases := c.GetDatabases()
	for dbName, dbConfig := range databases {
		// Create MongoDB client for this database
		var mongoClient *mongo.Mongo
		var err error
		if dbConfig.ConnectionString != "" {
			// Use connection string
			opts := options.Client().ApplyURI(dbConfig.ConnectionString)
			// Apply MongoDB client pool settings from config
			opts = c.applyMongoDBClientSettings(opts, dbName, &dbConfig)
			mongoClient, err = mongo.Connect(log, c.metrics, opts, c.ping)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to database %s: %v", dbName, err)
			}
		} else {
			// Use structured config
			connectionString := fmt.Sprintf("mongodb://%s:%d/%s", dbConfig.Host, dbConfig.Port, dbConfig.DBName)
			opts := options.Client().ApplyURI(connectionString)
			// Apply MongoDB client pool settings from config
			opts = c.applyMongoDBClientSettings(opts, dbName, &dbConfig)
			mongoClient, err = mongo.Connect(log, c.metrics, opts, c.ping)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to database %s: %v", dbName, err)
			}
		}

		// Add route to database router
		var connectionString string
		if dbConfig.ConnectionString != "" {
			connectionString = dbConfig.ConnectionString
		} else {
			connectionString = fmt.Sprintf("mongodb://%s:%d/%s", dbConfig.Host, dbConfig.Port, dbConfig.DBName)
		}

		routeConfig := &proxy.RouteConfig{
			DatabaseName:     dbName,
			Label:            dbName,
			MongoClient:      mongoClient,
			ConnectionString: connectionString,
		}
		databaseRouter.AddRoute(dbName, routeConfig)
	}

	// Create pool manager
	// Use MongoDB driver's pool settings for MongoBouncer's pool metrics
	mongodbMaxPoolSize := c.tomlConfig.Mongobouncer.MongoDBMaxPoolSize
	if mongodbMaxPoolSize == 0 {
		mongodbMaxPoolSize = 20 // Default if not configured
	}
	mongodbMinPoolSize := c.tomlConfig.Mongobouncer.MongoDBMinPoolSize
	if mongodbMinPoolSize == 0 {
		mongodbMinPoolSize = 3 // Default if not configured
	}

	poolManager := pool.NewManager(
		log,
		c.metrics,
		c.tomlConfig.Mongobouncer.PoolMode,
		mongodbMinPoolSize, // Use MongoDB driver's min pool size
		mongodbMaxPoolSize, // Use MongoDB driver's max pool size
		c.tomlConfig.Mongobouncer.MaxClientConn,
	)

	var proxies []*proxy.Proxy
	for _, client := range c.clients {
		p, err := proxy.NewProxy(log, c.metrics, client.label, c.network, client.address, c.unlink, mongoLookup, poolManager, databaseRouter, c.tomlConfig.Mongobouncer.AuthEnabled, c.tomlConfig.Mongobouncer.RegexCredentialPassthrough)
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
			// Simple connection string format is no longer supported
			// This will cause a validation error to help users understand the correct format
			panic(fmt.Sprintf("invalid database configuration for '%s': simple string format is no longer supported. Please use structured format with 'connection_string' field. Example: %s = { connection_string = \"%s\" }", name, name, v))
		case map[string]interface{}:
			// Structured format - convert to Database struct
			db := Database{}

			// connection_string is preferred, but individual fields are also supported
			if connStr, ok := v["connection_string"].(string); ok && connStr != "" {
				db.ConnectionString = connStr
			} else {
				// Fallback: build connection string from individual fields
				host := "localhost"
				port := 27017
				dbname := ""

				if h, ok := v["host"].(string); ok && h != "" {
					host = h
				}
				if p, ok := v["port"].(int64); ok && p > 0 {
					port = int(p)
				}
				if d, ok := v["dbname"].(string); ok && d != "" {
					dbname = d
				}

				// Build basic connection string from individual fields
				if dbname != "" {
					db.ConnectionString = fmt.Sprintf("mongodb://%s:%d/%s", host, port, dbname)
				} else {
					db.ConnectionString = fmt.Sprintf("mongodb://%s:%d", host, port)
				}
			}

			// Parse individual fields (optional overrides)
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

			// Parse MongoDB client pool overrides
			if maxpoolsize, ok := v["max_pool_size"].(int64); ok {
				db.MongoDBMaxPoolSize = int(maxpoolsize)
			}
			if minpoolsize, ok := v["min_pool_size"].(int64); ok {
				db.MongoDBMinPoolSize = int(minpoolsize)
			}
			if maxidletime, ok := v["max_conn_idle_time"].(string); ok {
				if duration, err := time.ParseDuration(maxidletime); err == nil {
					db.MongoDBMaxConnIdleTime = duration
				}
			}
			if seltimeout, ok := v["server_selection_timeout"].(string); ok {
				if duration, err := time.ParseDuration(seltimeout); err == nil {
					db.MongoDBServerSelectionTimeout = duration
				}
			}
			if connecttimeout, ok := v["connect_timeout"].(string); ok {
				if duration, err := time.ParseDuration(connecttimeout); err == nil {
					db.MongoDBConnectTimeout = duration
				}
			}
			if sockettimeout, ok := v["socket_timeout"].(string); ok {
				if duration, err := time.ParseDuration(sockettimeout); err == nil {
					db.MongoDBSocketTimeout = duration
				}
			}
			if heartbeatinterval, ok := v["heartbeat_interval"].(string); ok {
				if duration, err := time.ParseDuration(heartbeatinterval); err == nil {
					db.MongoDBHeartbeatInterval = duration
				}
			}

			// Legacy fields for backward compatibility
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

// GetMainConfig returns the main configuration
func (c *Config) GetMainConfig() MongobouncerConfig {
	return c.tomlConfig.Mongobouncer
}

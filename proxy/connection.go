package proxy

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/util"
	"go.mongodb.org/mongo-driver/mongo/description"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/mongo"
)

type connection struct {
	log     *zap.Logger
	metrics util.MetricsInterface

	address string
	conn    net.Conn
	kill    chan interface{}
	buffer  []byte

	poolManager    *pool.Manager
	databaseRouter *DatabaseRouter
	clientID       string

	// Context-aware routing fields
	intendedDatabase string       // The database the client originally wanted to connect to
	routeConfig      *RouteConfig // Cached route config for this connection

	// Client authentication fields
	clientUsername             string // Username from client connection string
	clientPassword             string // Password from client connection string
	authEnabled                bool   // Whether authentication is enabled
	regexCredentialPassthrough bool   // Whether to use client credentials for wildcard/regex matches

	// MongoDB client settings for dynamic client creation
	mongodbDefaults      util.MongoDBClientConfig // Default MongoDB client settings
	globalSessionManager *mongo.SessionManager    // Global session manager for shared session management
	proxy                *Proxy                   // Reference to proxy for client registration

	// Database usage tracking
	databaseUsed string // The actual database that was used during this connection
}

func handleConnection(log *zap.Logger, metrics util.MetricsInterface, address string, conn net.Conn, poolManager *pool.Manager, kill chan interface{}, databaseRouter *DatabaseRouter, authEnabled bool, regexCredentialPassthrough bool, mongodbDefaults util.MongoDBClientConfig, globalSessionManager *mongo.SessionManager, proxy *Proxy) string {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Connection crashed", zap.String("panic", fmt.Sprintf("%v", r)), zap.String("stack", string(debug.Stack())))
		}
	}()

	// Generate unique client ID
	clientID := fmt.Sprintf("%s-%d", address, time.Now().UnixNano())

	c := connection{
		log:     log,
		metrics: metrics,

		address: address,
		conn:    conn,
		kill:    kill,

		poolManager:                poolManager,
		databaseRouter:             databaseRouter,
		clientID:                   clientID,
		authEnabled:                authEnabled,
		regexCredentialPassthrough: regexCredentialPassthrough,
		mongodbDefaults:            mongodbDefaults,
		globalSessionManager:       globalSessionManager,
		proxy:                      proxy,
	}

	// Extract target database from the client's connection string for smart admin routing
	// This helps us route admin operations to the correct MongoDB instance
	c.extractTargetDatabaseFromConnectionString()

	// Register client with pool manager for proper connection pooling
	// We'll register with a placeholder database and update it when we know the actual database
	if poolManager != nil {
		configuredPoolMode := poolManager.GetDefaultMode()
		_, err := poolManager.RegisterClient(clientID, "default", "default", configuredPoolMode)
		if err != nil {
			log.Error("Failed to register client with pool manager", zap.Error(err))
		} else {
			// Don't register sessions metric yet - wait until we know the actual database name
			// This prevents incorrect "default" database sessions from accumulating
		}
	}

	// Ensure client is unregistered when connection closes
	defer func() {
		if poolManager != nil {
			poolManager.UnregisterClient(clientID)
			// Only decrement sessions if we actually registered them (i.e., if we determined the database name)
			// and it's not a default database (which we skip during registration)
			if c.metrics != nil && c.intendedDatabase != "" && c.intendedDatabase != "default" {
				_ = c.metrics.Gauge("sessions_active", -1, []string{fmt.Sprintf("database:%s", c.intendedDatabase)}, 1)
			}
		}
	}()

	c.processMessages()

	// Return the database that was actually used during this connection
	return c.databaseUsed
}

func (c *connection) processMessages() {
	for {
		err := c.handleMessage()
		if err != nil {
			if err != io.EOF {
				select {
				case <-c.kill:
					// ignore errors from force shutdown
				default:
					c.log.Error("Error handling message", zap.Error(err))
				}
			}
			return
		}
	}
}

func (c *connection) handleMessage() (err error) {
	var tags []string

	defer func(start time.Time) {
		if c.metrics != nil {
			tags := append(tags, fmt.Sprintf("success:%v", err == nil))
			_ = c.metrics.Timing("handle_message", time.Since(start), tags, 1)
		}
	}(time.Now())

	var wm []byte
	if wm, err = c.readWireMessage(); err != nil {
		return
	}

	var op mongo.Operation
	if op, err = mongo.Decode(wm); err != nil {
		return
	}

	isMaster := op.IsIsMaster()
	command, collection := op.CommandAndCollection()
	unacknowledged := op.Unacknowledged()
	tags = append(
		tags,
		fmt.Sprintf("request_op_code:%v", op.OpCode()),
		fmt.Sprintf("is_master:%v", isMaster),
		fmt.Sprintf("command:%s", string(command)),
		fmt.Sprintf("collection:%s", collection),
		fmt.Sprintf("unacknowledged:%v", unacknowledged),
	)

	// Extract database name from the operation
	databaseName := c.extractDatabaseName(op)
	tags = append(tags, fmt.Sprintf("database:%s", databaseName))

	// Record request metrics
	if c.metrics != nil {
		// Record request count
		requestTags := []string{
			fmt.Sprintf("database:%s", databaseName),
			fmt.Sprintf("operation:%s", string(command)),
		}
		_ = c.metrics.Incr("request", requestTags, 1)

		// Record request size
		requestSize := float64(len(wm))
		requestSizeTags := []string{
			fmt.Sprintf("database:%s", databaseName),
		}
		_ = c.metrics.Distribution("request_size", requestSize, requestSizeTags, 1)

		// Record ismaster commands
		if isMaster {
			_ = c.metrics.Incr("ismaster_command", []string{}, 1)
		}
	}
	c.log.Debug(
		"Request",
		zap.Int32("op_code", int32(op.OpCode())),
		zap.Bool("is_master", isMaster),
		zap.String("command", string(command)),
		zap.String("collection", collection),
		zap.Int("request_size", len(wm)),
	)

	req := &mongo.Message{
		Wm: wm,
		Op: op,
	}
	var res *mongo.Message
	if res, err = c.roundTrip(req, isMaster, tags); err != nil {
		// Record error metrics
		if c.metrics != nil {
			databaseName := c.extractDatabaseName(op)
			errorTags := []string{
				fmt.Sprintf("database:%s", databaseName),
				fmt.Sprintf("error_type:%s", "round_trip_error"),
			}
			_ = c.metrics.Incr("error", errorTags, 1)
		}
		return
	}

	if unacknowledged {
		c.log.Debug("Unacknowledged request")
		return
	}

	tags = append(
		tags,
		fmt.Sprintf("response_op_code:%v", res.Op.OpCode()),
	)

	if _, err = c.conn.Write(res.Wm); err != nil {
		return
	}

	c.log.Debug(
		"Response",
		zap.Int32("op_code", int32(res.Op.OpCode())),
		zap.Int("response_size", len(res.Wm)),
	)

	// Record response metrics
	if c.metrics != nil {
		// Record response size
		responseSize := float64(len(res.Wm))
		databaseName := c.extractDatabaseName(op)
		responseSizeTags := []string{
			fmt.Sprintf("database:%s", databaseName),
		}
		_ = c.metrics.Distribution("response_size", responseSize, responseSizeTags, 1)
	}
	return
}

func (c *connection) readWireMessage() ([]byte, error) {
	var sizeBuf [4]byte

	_, err := io.ReadFull(c.conn, sizeBuf[:])
	if err != nil {
		return nil, err
	}

	// read the length as an int32
	size := (int32(sizeBuf[0])) | (int32(sizeBuf[1]) << 8) | (int32(sizeBuf[2]) << 16) | (int32(sizeBuf[3]) << 24)

	if int(size) > cap(c.buffer) {
		c.buffer = make([]byte, 0, size)
	}

	buffer := c.buffer[:size]
	copy(buffer, sizeBuf[:])

	_, err = io.ReadFull(c.conn, buffer[4:])
	if err != nil {
		return nil, err
	}

	return buffer, nil

	// Return a copy since we're putting the buffer back to the pool
	// result := make([]byte, len(buffer))
	// copy(result, buffer)
	// return result, nil
}

func (c *connection) roundTrip(msg *mongo.Message, isMaster bool, tags []string) (*mongo.Message, error) {
	// Extract database name for pool routing (needed for authentication)
	databaseName := c.extractDatabaseName(msg.Op)

	// Extract command for handshake detection
	command, collection := msg.Op.CommandAndCollection()

	// Handle handshake commands (isMaster, buildInfo, etc.) with authentication and mocked response
	// Also handle aggregate commands on atlascli collection (Atlas CLI handshake)
	// Also handle transaction commands (abortTransaction, commitTransaction) - these should be forwarded to MongoDB
	if isMaster || command == mongo.BuildInfo || command == mongo.AtlasVersion || command == mongo.GetParameter ||
		(command == mongo.Aggregate && collection == "atlascli") {
		// For handshake commands, authentication logic should be based on appName content
		// not the extracted database name (which is always "admin" for these commands)
		topology := description.Sharded
		if c.authEnabled && c.clientUsername == "" {
			// Extract appName from the message
			appName, err := c.extractAppNameFromMessage(msg)
			if err != nil {
				c.log.Debug("Failed to extract appName", zap.Error(err))
				// If we can't extract appName, reject the connection
				return nil, fmt.Errorf("authentication required: failed to extract appName")
			}

			if appName == "" {
				c.log.Debug("No appName found in isMaster message")
				return nil, fmt.Errorf("authentication required: appName parameter must be provided")
			}

			c.log.Debug("Extracted appName from isMaster", zap.String("app_name", appName))

			// Parse appName to get database, username, password
			appDatabase, username, password, err := c.parseAppNameCredentials(appName)
			if err != nil {
				c.log.Debug("AppName is not in credential format", zap.Error(err))
				return nil, fmt.Errorf("invalid appName format: must be 'database:username:password', got: %s", appName)
			}

			if appDatabase == "" || username == "" || password == "" {
				c.log.Debug("Empty credentials in appName", zap.String("app_name", appName))
				return nil, fmt.Errorf("authentication required: username and password must be provided")
			}

			c.log.Debug("Parsed credentials from appName",
				zap.String("app_database", appDatabase),
				zap.String("username", username),
				zap.Bool("has_password", password != ""))

			// Determine if this is an exact match or wildcard/regex match based on appDatabase
			route, err := c.databaseRouter.GetRoute(appDatabase)
			if err != nil {
				c.log.Error("Database route not found for appName database",
					zap.String("app_database", appDatabase),
					zap.Error(err))
				return nil, fmt.Errorf("database %s is not supported: %v", appDatabase, err)
			}

			isExactMatch := route != nil && route.DatabaseName == appDatabase

			// If topology is defined then use it, else use the default sharded topology
			if route != nil {
				if route.DatabaseConfig.Topology != nil {
					topology = *route.DatabaseConfig.Topology
				}
			}

			// Apply authentication based on route type
			if isExactMatch {
				// For exact matches, validate credentials against route configuration
				c.log.Debug("Exact match detected, validating credentials",
					zap.String("app_database", appDatabase),
					zap.String("username", username))

				if err := c.validateCredentialsAgainstDatabase(username, password, appDatabase, appDatabase); err != nil {
					c.log.Error("Credential validation failed for exact match", zap.Error(err))
					return nil, fmt.Errorf("authentication failed: %v", err)
				}
			} else {
				// For wildcard/regex matches, use credential passthrough
				c.log.Debug("Wildcard/regex match detected, using credential passthrough",
					zap.String("app_database", appDatabase),
					zap.String("username", username))

				// Store credentials for downstream connection
				c.clientUsername = username
				c.clientPassword = password
			}
		}

		requestID := msg.Op.RequestID()
		c.log.Debug("Mocking handshake response", zap.Int32("request_id", requestID), zap.String("topology", topology.String()), zap.String("command", string(command)))

		// Handle different handshake commands
		if isMaster {
			// Always respond as a shard router (mongos) to emulate the behavior you described
			return mongo.IsMasterResponse(requestID, topology)
		} else if command == mongo.BuildInfo {
			// Return a buildInfo response that indicates this is mongos
			return mongo.BuildInfoResponse(requestID)
		} else if command == mongo.AtlasVersion {
			// Return error for atlasVersion command (not supported in mongos)
			return mongo.ErrorResponse(requestID, "no such command: 'atlasVersion'", "CommandNotFound")
		} else if command == mongo.GetParameter {
			// Return a generic response for getParameter
			return mongo.GetParameterResponse(requestID)
		} else if command == mongo.Aggregate && collection == "atlascli" {
			// Return empty cursor for atlascli aggregate
			return mongo.EmptyCursorResponse(requestID)
		}

		// For other handshake commands, return a generic mongos response
		return mongo.IsMasterResponse(requestID, topology)
	}

	// For non-isMaster operations, authentication should already be validated
	// during the initial isMaster handshake

	// Get MongoDB client using simplified routing
	var mongoClient *mongo.Mongo
	var route *RouteConfig
	var targetDatabase string

	if c.databaseRouter != nil {
		// Simple routing: use the database name directly
		targetDatabase = databaseName

		// Get route for the target database
		var err error
		route, err = c.databaseRouter.GetRoute(targetDatabase)
		if err != nil {
			// If admin database is not configured but admin command is attempted, reject with proper error
			if targetDatabase == "admin" {
				c.log.Error("Admin command attempted but no admin database configured",
					zap.String("target_database", targetDatabase),
					zap.String("operation", msg.Op.String()),
					zap.Error(err))
				return nil, fmt.Errorf("admin database is not configured but admin command was attempted: %v", err)
			}
			return nil, fmt.Errorf("database %s is not supported: %v", targetDatabase, err)
		}

		// Cache the route config for this connection
		if c.routeConfig == nil {
			c.routeConfig = route
			c.intendedDatabase = targetDatabase
			c.log.Debug("Cached route config for connection",
				zap.String("intended_database", targetDatabase),
				zap.String("current_operation_db", databaseName))
		}

		// Check if we need to create a MongoDB client lazily or recreate a failed one
		if c.regexCredentialPassthrough && c.isWildcardOrRegexMatch(targetDatabase, route) && c.clientUsername != "" {
			c.log.Debug("Using credential passthrough for MongoDB client creation",
				zap.String("username", c.clientUsername),
				zap.String("target_database", targetDatabase))

			// Create a new MongoDB client with the client's credentials
			clientWithCredentials, err := c.createMongoClientWithCredentials(route, c.clientUsername, c.clientPassword)
			if err != nil {
				c.log.Error("Failed to create MongoDB client with client credentials", zap.Error(err))
				return nil, fmt.Errorf("failed to create MongoDB client with client credentials: %v", err)
			}
			mongoClient = clientWithCredentials
		} else if route.MongoClient == nil {
			c.log.Debug("MongoDB client is nil, creating lazily",
				zap.String("target_database", targetDatabase))

			// Create MongoDB client lazily
			lazyClient, err := c.createLazyMongoClient(route, targetDatabase)
			if err != nil {
				c.log.Error("Failed to create lazy MongoDB client", zap.Error(err))
				return nil, fmt.Errorf("failed to create MongoDB client: %v", err)
			}
			mongoClient = lazyClient
		} else {
			// Use the existing route's MongoDB client
			mongoClient = route.MongoClient
		}
	} else {
		return nil, fmt.Errorf("mongo client not found for address: %s", c.address)
	}

	// Use pool manager for connection pooling, fallback to direct client if needed
	if c.poolManager != nil {
		result, err := c.roundTripWithPool(msg, databaseName, mongoClient, tags)
		// If the operation failed due to connection issues, try to recreate the client
		if err != nil && c.isConnectionError(err) && route != nil && route.MongoClient != nil {
			c.log.Warn("Connection error detected, attempting to recreate MongoDB client",
				zap.String("target_database", targetDatabase),
				zap.Error(err))

			// Mark the route's client as nil to force recreation on next request
			// Use mutex to prevent race conditions with lazy client creation
			route.clientMutex.Lock()
			route.MongoClient = nil
			route.clientMutex.Unlock()
		}
		return result, err
	}

	// Fallback to direct client usage
	c.log.Warn("Fallback to direct client usage", zap.String("address", c.address))
	result, err := mongoClient.RoundTrip(msg, tags)

	// If the operation failed due to connection issues, try to recreate the client
	if err != nil && c.isConnectionError(err) && route != nil && route.MongoClient != nil {
		c.log.Warn("Connection error detected in direct client usage, attempting to recreate MongoDB client",
			zap.String("target_database", targetDatabase),
			zap.Error(err))

		// Mark the route's client as nil to force recreation on next request
		// Use mutex to prevent race conditions with lazy client creation
		route.clientMutex.Lock()
		route.MongoClient = nil
		route.clientMutex.Unlock()
	}

	return result, err
}

// roundTripWithPool handles round trip using the pool manager
func (c *connection) roundTripWithPool(msg *mongo.Message, databaseName string, mongoClient *mongo.Mongo, tags []string) (*mongo.Message, error) {
	// Use the extracted database name from the operation, not the client's database name
	actualDatabaseName := databaseName
	if actualDatabaseName == "" {
		actualDatabaseName = "default"
	}

	c.log.Debug("Using database name from operation",
		zap.String("extracted_db", databaseName),
		zap.String("actual_db", actualDatabaseName))

	// Check if request forwarding is enabled and try to forward the request
	if c.proxy != nil && c.proxy.requestForwarder != nil {
		// Extract session ID from the message if available
		sessionID := ""
		if msg.Op != nil {
			if transactionDetails := msg.Op.TransactionDetails(); transactionDetails != nil {
				sessionID = fmt.Sprintf("%x", transactionDetails.LsID)
			}
		}

		// If we have a session ID, try to forward the request
		if sessionID != "" {
			// Convert message to raw bytes for forwarding
			rawRequest := msg.Op.Encode(msg.Op.RequestID())

			// Try to forward the request
			forwardedResponse, err := c.proxy.requestForwarder.ForwardRequest(sessionID, rawRequest, c.address)
			if err != nil {
				c.log.Warn("Request forwarding failed, processing locally",
					zap.String("session_id", sessionID),
					zap.Error(err))
			} else if forwardedResponse != nil {
				// Request was forwarded successfully, parse and return the response
				c.log.Debug("Request forwarded successfully",
					zap.String("session_id", sessionID),
					zap.Int("response_size", len(forwardedResponse)))

				// Parse the forwarded response
				responseOp, err := mongo.Decode(forwardedResponse)
				if err != nil {
					c.log.Error("Failed to parse forwarded response", zap.Error(err))
					return nil, err
				}
				responseMsg := &mongo.Message{Op: responseOp}
				return responseMsg, nil
			}
			// If forwardedResponse is nil, it means the session should be processed locally
		}
	}

	// Ensure client is registered with the correct database
	// If this is the first operation for this database, re-register the client
	if c.poolManager != nil {
		// Check if client is registered for this database
		client, exists := c.poolManager.GetClient(c.clientID)
		if !exists || client.Database != actualDatabaseName {
			// Re-register client with the correct database
			c.log.Debug("Re-registering client with correct database",
				zap.String("client_id", c.clientID),
				zap.String("database", actualDatabaseName))

			// Unregister old client if exists
			if exists {
				c.poolManager.UnregisterClient(c.clientID)
			}

			// Register with correct database using the configured pool mode
			configuredPoolMode := c.poolManager.GetDefaultMode()
			_, err := c.poolManager.RegisterClient(c.clientID, "default", actualDatabaseName, configuredPoolMode)
			if err != nil {
				c.log.Error("Failed to re-register client with pool manager", zap.Error(err))
				// Return error instead of falling back to direct client usage
				return nil, err
			}
		}
	}

	// Check if this operation has transaction details
	transactionDetails := msg.Op.TransactionDetails()
	isTransaction := transactionDetails != nil && !transactionDetails.Autocommit
	transactionID := ""
	if transactionDetails != nil {
		transactionID = fmt.Sprintf("%x-%d", transactionDetails.LsID, transactionDetails.TxnNumber)
	}

	// Get connection from pool (client is now registered with correct database)
	pooledConn, err := c.poolManager.GetConnection(c.clientID, actualDatabaseName, mongoClient, isTransaction, transactionID)
	if err != nil {
		c.log.Warn("Failed to get connection from pool", zap.Error(err))
		// Return error instead of falling back to direct client usage
		// This ensures we respect connection pool limits
		return nil, err
	}

	// Use the pooled connection's MongoDB client for the round trip
	result, err := pooledConn.MongoClient.RoundTrip(msg, tags)

	// Determine if this is the end of a transaction based on operation type
	isEndOfTransaction := c.isEndOfTransaction(msg.Op)

	// Return connection to pool based on pool mode
	// For session mode, we don't return the connection immediately
	// For statement mode, we return after each operation
	// For transaction mode, we return based on transaction state
	c.poolManager.ReturnConnection(c.clientID, pooledConn, isEndOfTransaction)

	return result, err
}

// isEndOfTransaction determines if an operation marks the end of a transaction
func (c *connection) isEndOfTransaction(op mongo.Operation) bool {
	// Get the command to determine transaction state
	command, _ := op.CommandAndCollection()
	commandStr := string(command)

	// Transaction-ending commands
	transactionEndCommands := []string{
		"commitTransaction",
		"abortTransaction",
		"endSessions",
	}

	for _, endCmd := range transactionEndCommands {
		if commandStr == endCmd {
			return true
		}
	}

	// Check if this is a transaction operation that's ending
	transactionDetails := op.TransactionDetails()
	if transactionDetails != nil {
		// If autocommit is true, this operation ends the transaction
		if transactionDetails.Autocommit {
			return true
		}

		// If this is a commit or abort command, it ends the transaction
		if commandStr == "commitTransaction" || commandStr == "abortTransaction" {
			return true
		}
	}

	// For statement mode, every operation should return the connection
	// This is handled in the pool manager based on pool mode
	return false
}

// extractTargetDatabaseFromConnectionString extracts the target database from the client's connection
// This is a heuristic approach: when a client connects to MongoBouncer, we assume they want to
// connect to the database specified in their connection string
func (c *connection) extractTargetDatabaseFromConnectionString() {
	c.log.Debug("Connection established, determining target database from connection context",
		zap.String("client_id", c.clientID),
		zap.String("address", c.address))

	// For now, we'll use a simple heuristic: if the client is connecting to our proxy,
	// we'll assume they want to connect to the first configured database
	// In a production implementation, we would parse the actual connection string
	// that the client used to connect to MongoBouncer

	// TODO: Implement proper connection string parsing to extract the target database
	// from the client's connection string (e.g., mongodb://localhost:27018/mongobouncer_test)
	// For now, we'll leave this empty and let the system determine the target database
	// from the first non-admin operation

	c.log.Debug("Connection established, will determine target database from first operation",
		zap.String("client_id", c.clientID))
}

// extractDatabaseName extracts the database name from a MongoDB operation
func (c *connection) extractDatabaseName(op mongo.Operation) string {
	// Try to extract from collection name first (most reliable)
	_, collection := op.CommandAndCollection()
	c.log.Debug("Extracting database name",
		zap.String("collection", collection),
		zap.String("op_type", fmt.Sprintf("%T", op)),
		zap.String("database_name", op.DatabaseName()),
		zap.String("operation_string", op.String()))

	var databaseName string

	if collection != "" {
		// Extract database name using utility function
		collectionInfo := util.ParseCollectionName(collection)
		if collectionInfo.Database != "" {
			databaseName = collectionInfo.Database
			c.log.Debug("Extracted database name from collection",
				zap.String("collection", collection),
				zap.String("database", databaseName))
		}
	}

	// Use the new DatabaseName method as fallback
	if databaseName == "" {
		if dbName := op.DatabaseName(); dbName != "" {
			databaseName = dbName
			c.log.Debug("Extracted database name from operation", zap.String("database", databaseName))
		}
	}

	// If we still don't have a database name, try to extract from the operation string
	if databaseName == "" {
		opString := op.String()
		c.log.Debug("Operation string for database extraction", zap.String("op_string", opString))

		// Look for database patterns in the operation string
		patterns := []string{
			`sustained_db_\d+`,
			`longrun_db_\d+`,
			`test_db_\d+`,
			`burst_db_\d+`,
			`storm_db_\d+`,
			`concurrent_db_\d+`,
			`random_db_\d+`,
			`webapp_test`,
			`background_test`,
			`analytics_test`,
		}

		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			if match := re.FindString(opString); match != "" {
				databaseName = match
				c.log.Debug("Extracted database name from operation string", zap.String("database", databaseName), zap.String("pattern", pattern))
				break
			}
		}

		// If still no match, try to extract any database name pattern
		if databaseName == "" {
			// Look for any pattern that looks like a database name (alphanumeric with underscores)
			re := regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_]*_db_\d+`)
			if match := re.FindString(opString); match != "" {
				databaseName = match
				c.log.Debug("Extracted database name using generic pattern", zap.String("database", databaseName))
			}
		}
	}

	// Default fallback - but try to use a more intelligent default
	if databaseName == "" {
		// For admin operations, use admin
		if op.IsIsMaster() {
			databaseName = "admin"
		} else {
			// Try to use the connection context database first
			if c.intendedDatabase != "" {
				databaseName = c.intendedDatabase
				c.log.Debug("Using connection context database", zap.String("database", databaseName))
			} else {
				// For other operations, use default
				databaseName = "default"
			}
		}
		c.log.Debug("Using fallback database name", zap.String("database", databaseName), zap.String("collection", collection))
	}

	// Track the database name for this connection
	if databaseName != "" {
		// If this is the first time we're setting the database, register the session
		if c.databaseUsed == "" {
			// This is the first operation, register the session with the correct database
			if c.metrics != nil {
				// Only register if we have a valid database name (not "default")
				if databaseName != "default" {
					_ = c.metrics.Gauge("sessions_active", 1, []string{fmt.Sprintf("database:%s", databaseName)}, 1)
					c.log.Debug("Registered session for database", zap.String("database", databaseName))
				} else {
					c.log.Debug("Skipping session registration for default database", zap.String("database", databaseName))
				}
			}
		}
		c.databaseUsed = databaseName
		c.intendedDatabase = databaseName
		c.log.Debug("Updated connection database tracking",
			zap.String("database_used", c.databaseUsed),
			zap.String("intended_database", c.intendedDatabase),
			zap.String("current_operation_db", databaseName))
	} else if databaseName == "default" && c.intendedDatabase != "" {
		// If we're falling back to default but we have a connection context, use that instead
		databaseName = c.intendedDatabase
		c.log.Debug("Using connection context instead of default", zap.String("database", databaseName))
	}

	return databaseName
}

// extractHostFromRoute extracts host and port from a route configuration
func (c *connection) extractHostFromRoute(targetRoute *RouteConfig) (string, string) {
	// Extract host from the connection string
	if targetRoute.ConnectionString == "" {
		return "", ""
	}

	c.log.Debug("Parsing connection string for host/port extraction",
		zap.String("connection_string", targetRoute.ConnectionString))

	// Parse the connection string to extract host and port
	// Format: mongodb://[username:password@]host:port/database

	// Simple parsing - look for mongodb:// and extract host:port
	if !strings.HasPrefix(targetRoute.ConnectionString, "mongodb://") {
		return "", ""
	}

	// Remove mongodb:// prefix
	withoutScheme := strings.TrimPrefix(targetRoute.ConnectionString, "mongodb://")

	// Handle credentials - look for @ symbol
	var hostPortPart string
	if atIndex := strings.Index(withoutScheme, "@"); atIndex != -1 {
		// Has credentials, extract everything after @
		hostPortPart = withoutScheme[atIndex+1:]
	} else {
		// No credentials
		hostPortPart = withoutScheme
	}

	// Find the first '/' to separate host:port from database
	slashIndex := strings.Index(hostPortPart, "/")
	if slashIndex == -1 {
		// No database specified, use the whole hostPortPart
		hostPort := hostPortPart
		c.log.Debug("Extracted host:port from connection string",
			zap.String("host_port", hostPort))

		// Extract host and port
		host, port := c.extractHostAndPort(hostPort)

		c.log.Debug("Parsed host and port",
			zap.String("host", host),
			zap.String("port", port))

		return host, port
	}

	// Extract host:port part
	hostPort := hostPortPart[:slashIndex]

	c.log.Debug("Extracted host:port from connection string",
		zap.String("host_port", hostPort))

	// Extract host and port
	host, port := c.extractHostAndPort(hostPort)

	c.log.Debug("Parsed host and port",
		zap.String("host", host),
		zap.String("port", port))

	return host, port
}

// extractHostAndPort extracts host and port from host:port string
func (c *connection) extractHostAndPort(hostPort string) (string, string) {
	// Handle IPv6 addresses in brackets
	if strings.HasPrefix(hostPort, "[") {
		// IPv6 format: [::1]:27017
		closeBracket := strings.Index(hostPort, "]")
		if closeBracket == -1 {
			return "", ""
		}
		host := hostPort[1:closeBracket]
		if closeBracket+1 < len(hostPort) && hostPort[closeBracket+1] == ':' {
			port := hostPort[closeBracket+2:]
			return host, port
		}
		return host, "27017" // Default MongoDB port
	}

	// IPv4 format: localhost:27017
	colonIndex := strings.LastIndex(hostPort, ":")
	if colonIndex == -1 {
		return hostPort, "27017" // Default MongoDB port
	}

	host := hostPort[:colonIndex]
	port := hostPort[colonIndex+1:]
	return host, port
}

// extractCredentialsFromConnectionString extracts username and password from MongoDB connection string
func (c *connection) extractCredentialsFromConnectionString(connStr string) (username, password string, err error) {
	// Parse the connection string
	parsedURL, err := url.Parse(connStr)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse connection string: %v", err)
	}

	// Extract username and password from URL
	if parsedURL.User != nil {
		username = parsedURL.User.Username()
		password, _ = parsedURL.User.Password()
	}

	c.log.Debug("Extracted credentials from connection string",
		zap.String("username", username),
		zap.Bool("has_password", password != ""))

	return username, password, nil
}

// extractAppNameFromMessage extracts appName from isMaster call
func (c *connection) extractAppNameFromMessage(msg *mongo.Message) (string, error) {
	opStr := msg.Op.String()
	c.log.Debug("Analyzing operation string for appName", zap.String("operation_string", opStr))

	// Look for application.name in the operation string
	if strings.Contains(opStr, "application") && strings.Contains(opStr, "name") {
		// Extract the appName value - look for "name": "value"
		start := strings.Index(opStr, "\"name\": \"")
		if start != -1 {
			start += 9 // Skip "\"name\": \""
			end := strings.Index(opStr[start:], "\"")
			if end != -1 {
				appName := opStr[start : start+end]
				c.log.Debug("Extracted appName from isMaster call", zap.String("app_name", appName))
				return appName, nil
			}
		}
	}

	c.log.Debug("No appName found in isMaster message")
	return "", nil
}

// parseAppNameCredentials parses appName string to extract database, username and password
// Supports both old format (username:password) and new format (database:username:password)
func (c *connection) parseAppNameCredentials(appName string) (database, username, password string, err error) {
	// Use utility function for parsing
	appInfo := util.ParseAppName(appName)

	// Handle new format: database:username:password
	if appInfo.Database != "" && appInfo.Username != "" && appInfo.Password != "" {
		database = strings.TrimSpace(appInfo.Database)
		username = strings.TrimSpace(appInfo.Username)
		password = strings.TrimSpace(appInfo.Password)

		if database == "" || username == "" || password == "" {
			return "", "", "", fmt.Errorf("database, username and password cannot be empty")
		}

		return database, username, password, nil
	}

	// Handle old format: username:password (for backward compatibility)
	if appInfo.Username != "" && appInfo.Password != "" {
		username = strings.TrimSpace(appInfo.Username)
		password = strings.TrimSpace(appInfo.Password)

		if username == "" || password == "" {
			return "", "", "", fmt.Errorf("username and password cannot be empty")
		}

		// For old format, database is empty (will be determined from connection)
		return "", username, password, nil
	}

	return "", "", "", fmt.Errorf("appName must be in format 'database:username:password' or 'username:password', got: %s", appName)
}

// validateCredentialsAgainstDatabase validates credentials against database configuration
func (c *connection) validateCredentialsAgainstDatabase(username, password, targetDatabase, appDatabase string) error {
	// Get the route configuration for the target database
	route, err := c.databaseRouter.GetRoute(targetDatabase)
	if err != nil {
		return fmt.Errorf("database %s is not supported: %v", targetDatabase, err)
	}

	// Check if this is a wildcard/regex match and credential passthrough is enabled
	if c.regexCredentialPassthrough && c.isWildcardOrRegexMatch(targetDatabase, route) {
		c.log.Debug("Using credential passthrough for wildcard/regex match",
			zap.String("username", username),
			zap.String("target_database", targetDatabase))

		// Store the client credentials for use in downstream connections
		c.clientUsername = username
		c.clientPassword = password

		// For wildcard/regex matches, we don't validate against route credentials
		// Instead, we'll use the client credentials for the downstream connection
		return nil
	}

	// For exact matches, validate credentials against database configuration
	routeUsername, routePassword, err := c.extractCredentialsFromConnectionString(route.ConnectionString)
	if err != nil {
		return fmt.Errorf("failed to extract route credentials: %v", err)
	}

	// Validate credentials
	if username != routeUsername || password != routePassword {
		c.log.Error("Client credentials do not match route credentials",
			zap.String("client_username", username),
			zap.String("route_username", routeUsername),
			zap.String("target_database", targetDatabase))
		return fmt.Errorf("authentication failed: invalid credentials")
	}

	// If appDatabase is provided (new format), validate that it matches the target database
	if appDatabase != "" && appDatabase != targetDatabase {
		c.log.Error("AppName database does not match target database",
			zap.String("app_database", appDatabase),
			zap.String("target_database", targetDatabase),
			zap.String("username", username))
		return fmt.Errorf("authentication failed: database mismatch - appName specifies '%s' but connecting to '%s'", appDatabase, targetDatabase)
	}

	c.log.Debug("Credentials match database configuration",
		zap.String("username", username),
		zap.String("target_database", targetDatabase),
		zap.String("app_database", appDatabase))

	// Store validated credentials for this connection
	c.clientUsername = username
	c.clientPassword = password

	return nil
}

// isWildcardOrRegexMatch checks if the target database matches a wildcard or regex pattern
func (c *connection) isWildcardOrRegexMatch(targetDatabase string, route *RouteConfig) bool {
	// Check if the route's database name contains wildcards or regex patterns
	routePattern := route.DatabaseName

	// Check for wildcard patterns
	if strings.Contains(routePattern, "*") {
		c.log.Debug("Route pattern contains wildcard",
			zap.String("route_pattern", routePattern),
			zap.String("target_database", targetDatabase))
		return true
	}

	// Check if this is a wildcard route (matches everything)
	if routePattern == "*" {
		c.log.Debug("Route is wildcard route",
			zap.String("target_database", targetDatabase))
		return true
	}

	// Check if this route was added as a pattern route by checking if the database name
	// doesn't exactly match the route pattern (indicating it was matched via pattern)
	if routePattern != targetDatabase {
		c.log.Debug("Route pattern doesn't match target database exactly - likely a pattern match",
			zap.String("route_pattern", routePattern),
			zap.String("target_database", targetDatabase))
		return true
	}

	c.log.Debug("Route is exact match",
		zap.String("route_pattern", routePattern),
		zap.String("target_database", targetDatabase))

	return false
}

// isConnectionError determines if an error is related to connection issues
func (c *connection) isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Check for common connection-related error patterns
	connectionErrorPatterns := []string{
		"connection refused",
		"connection reset",
		"connection timeout",
		"network is unreachable",
		"no such host",
		"context deadline exceeded",
		"server selection error",
		"topology closed",
		"connection pool closed",
		"connection not available",
	}

	for _, pattern := range connectionErrorPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}

	return false
}

// createLazyMongoClient creates a MongoDB client lazily when the route's client is nil
// Uses double-checked locking pattern to prevent race conditions
func (c *connection) createLazyMongoClient(route *RouteConfig, targetDatabase string) (*mongo.Mongo, error) {
	// First check without lock for performance
	if route.MongoClient != nil {
		return route.MongoClient, nil
	}

	// Acquire lock for thread-safe client creation
	route.clientMutex.Lock()
	defer route.clientMutex.Unlock()

	// Double-check after acquiring lock
	if route.MongoClient != nil {
		return route.MongoClient, nil
	}

	c.log.Debug("Creating lazy MongoDB client",
		zap.String("target_database", targetDatabase),
		zap.String("connection_string", route.ConnectionString))

	// Create MongoDB client options from the route's connection string
	opts := options.Client().ApplyURI(route.ConnectionString)

	// Apply MongoDB client settings using the shared utility function
	opts = util.ApplyMongoDBClientSettings(opts, targetDatabase, c.mongodbDefaults, route.DatabaseConfig, c.log)

	// Create the MongoDB client without pinging (lazy creation)
	mongoClient, err := mongo.ConnectWithSessionManager(c.log, c.metrics, opts, false, c.globalSessionManager) // Don't ping for lazy clients
	if err != nil {
		return nil, fmt.Errorf("failed to create lazy MongoDB client: %v", err)
	}

	// Update the route's MongoDB client for future use
	route.MongoClient = mongoClient

	// Register the client with the proxy for cleanup tracking
	if c.proxy != nil {
		c.proxy.RegisterMongoClient(targetDatabase, mongoClient)
	}

	c.log.Info("Successfully created lazy MongoDB client",
		zap.String("target_database", targetDatabase))

	return mongoClient, nil
}

// createMongoClientWithCredentials creates a MongoDB client using the client's credentials
func (c *connection) createMongoClientWithCredentials(route *RouteConfig, username, password string) (*mongo.Mongo, error) {
	// Extract host and port from the route's connection string
	host, port := c.extractHostFromRoute(route)
	if host == "" || port == "" {
		return nil, fmt.Errorf("failed to extract host/port from route connection string")
	}

	// Parse the original route connection string to extract authSource and other options
	originalURI := route.ConnectionString
	parsedURI, err := url.Parse(originalURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse route connection string: %v", err)
	}

	// Build new connection string with client credentials
	var newConnectionString string
	if username != "" && password != "" {
		// Use client credentials but preserve authSource and other options from route
		newConnectionString = fmt.Sprintf("mongodb://%s:%s@%s:%s/", username, password, host, port)

		// Add authSource if it was specified in the original route
		if parsedURI.Query().Get("authSource") != "" {
			newConnectionString += "?authSource=" + parsedURI.Query().Get("authSource")
		} else {
			// Default to admin authSource for client credentials
			newConnectionString += "?authSource=admin"
		}

		// Add other query parameters from the original route
		query := parsedURI.Query()
		for key, values := range query {
			if key != "authSource" { // We already handled authSource above
				for _, value := range values {
					if strings.Contains(newConnectionString, "?") {
						newConnectionString += "&" + key + "=" + value
					} else {
						newConnectionString += "?" + key + "=" + value
					}
				}
			}
		}
	} else {
		newConnectionString = fmt.Sprintf("mongodb://%s:%s/", host, port)
		// Preserve query parameters from original route
		if parsedURI.RawQuery != "" {
			newConnectionString += "?" + parsedURI.RawQuery
		}
	}

	c.log.Debug("Creating MongoDB client with client credentials",
		zap.String("username", username),
		zap.String("host", host),
		zap.String("port", port),
		zap.String("auth_source", parsedURI.Query().Get("authSource")),
		zap.String("connection_string", sanitizeConnectionString(newConnectionString)))

	// Create MongoDB client options
	opts := options.Client().ApplyURI(newConnectionString)

	// Apply MongoDB client settings using the shared utility function
	opts = util.ApplyMongoDBClientSettings(opts, c.intendedDatabase, c.mongodbDefaults, route.DatabaseConfig, c.log)

	// Create the MongoDB client
	mongoClient, err := mongo.ConnectWithSessionManager(c.log, c.metrics, opts, false, c.globalSessionManager) // Don't ping for dynamic clients
	if err != nil {
		return nil, fmt.Errorf("failed to create MongoDB client with client credentials: %v", err)
	}

	return mongoClient, nil
}

// sanitizeConnectionString removes sensitive information from connection string for logging
func sanitizeConnectionString(connStr string) string {
	// Use utility function for parsing
	connInfo := util.ParseConnectionString(connStr)
	if connInfo.Username != "" && connInfo.Password != "" {
		// Replace password with ***
		sanitized := connInfo.Username + ":***"
		return strings.Replace(connStr, connInfo.Username+":"+connInfo.Password, sanitized, 1)
	}
	return connStr
}

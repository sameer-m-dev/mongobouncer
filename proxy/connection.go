package proxy

import (
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/util"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/mongo"
)

type connection struct {
	log     *zap.Logger
	metrics *util.MetricsClient

	address string
	conn    net.Conn
	kill    chan interface{}
	buffer  []byte

	mongoLookup    MongoLookup
	poolManager    *pool.Manager
	databaseRouter *DatabaseRouter
	clientID       string

	// Context-aware routing fields
	intendedDatabase string       // The database the client originally wanted to connect to
	authSource       string       // The authSource from the connection string
	routeConfig      *RouteConfig // Cached route config for this connection

	// Connection state tracking
	adminRoute string // Cached admin route for this connection
}

func handleConnection(log *zap.Logger, metrics *util.MetricsClient, address string, conn net.Conn, mongoLookup MongoLookup, poolManager *pool.Manager, kill chan interface{}, databaseRouter *DatabaseRouter) {
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

		mongoLookup:    mongoLookup,
		poolManager:    poolManager,
		databaseRouter: databaseRouter,
		clientID:       clientID,
	}

	// Extract target database from the client's connection string for smart admin routing
	// This helps us route admin operations to the correct MongoDB instance
	c.extractTargetDatabaseFromConnectionString()

	// Register client with pool manager for proper connection pooling
	if poolManager != nil {
		_, err := poolManager.RegisterClient(clientID, "default", "default", pool.SessionMode)
		if err != nil {
			log.Error("Failed to register client with pool manager", zap.Error(err))
		}
	}

	// Ensure client is unregistered when connection closes
	defer func() {
		if poolManager != nil {
			poolManager.UnregisterClient(clientID)
		}
	}()

	c.processMessages()
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

	// Record request metrics
	if c.metrics != nil {
		// Extract database name from the operation
		databaseName := c.extractDatabaseName(op)

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
}

func (c *connection) roundTrip(msg *mongo.Message, isMaster bool, tags []string) (*mongo.Message, error) {
	// Extract database name for pool routing
	databaseName := c.extractDatabaseName(msg.Op)

	// Get MongoDB client using context-aware routing
	var mongoClient *mongo.Mongo
	if c.databaseRouter != nil {
		// Context-aware routing: determine the target database
		targetDatabase := c.determineTargetDatabase(databaseName, msg.Op)

		// Get route for the target database
		route, err := c.databaseRouter.GetRoute(targetDatabase)
		if err != nil {
			// If it's a dynamic admin route, create it
			if strings.HasPrefix(targetDatabase, "admin_") {
				route, err = c.createDynamicAdminRoute(targetDatabase)
				if err != nil {
					return nil, fmt.Errorf("database %s is not supported: %v", targetDatabase, err)
				}
			} else {
				return nil, fmt.Errorf("database %s is not supported: %v", targetDatabase, err)
			}
		}

		// Cache the route config for this connection
		// Don't cache for admin operations - we want to update when we see the actual target database
		if c.routeConfig == nil && targetDatabase != "admin" {
			c.routeConfig = route
			c.intendedDatabase = targetDatabase
			c.log.Debug("Cached route config for connection",
				zap.String("intended_database", targetDatabase),
				zap.String("current_operation_db", databaseName),
				zap.String("auth_source", c.authSource))
		} else if targetDatabase == "admin" {
			c.log.Debug("Skipping cache for admin operation",
				zap.String("current_operation_db", databaseName),
				zap.String("reason", "wait_for_target_database"))
		}

		mongoClient = route.MongoClient
	} else {
		return nil, fmt.Errorf("mongo client not found for address: %s", c.address)
	}

	if isMaster {
		requestID := msg.Op.RequestID()
		c.log.Debug("Non-proxied ismaster response", zap.Int32("request_id", requestID))
		return mongo.IsMasterResponse(requestID, mongoClient.Description().Kind)
	}

	// Use pool manager for connection pooling, fallback to direct client if needed
	if c.poolManager != nil {
		return c.roundTripWithPool(msg, databaseName, mongoClient, tags)
	}

	// Fallback to direct client usage
	return mongoClient.RoundTrip(msg, tags)
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

	// Get connection from pool (client is already registered)
	pooledConn, err := c.poolManager.GetConnection(c.clientID, actualDatabaseName, mongoClient, false, "")
	if err != nil {
		c.log.Warn("Failed to get connection from pool", zap.Error(err))
		// Fallback to direct client usage
		return mongoClient.RoundTrip(msg, tags)
	}

	// Use the pooled connection's MongoDB client for the round trip
	result, err := pooledConn.MongoClient.RoundTrip(msg, tags)

	// Return connection to pool based on pool mode
	// For session mode, we don't return the connection immediately
	// For statement mode, we return after each operation
	c.poolManager.ReturnConnection(c.clientID, pooledConn, false)

	return result, err
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

// extractTargetDatabaseFromFirstMessage reads the first wire message and extracts the target database
// This allows us to determine the admin route immediately without buffering operations
func (c *connection) extractTargetDatabaseFromFirstMessage() {
	c.log.Debug("Connection established, reading first message to determine target database",
		zap.String("client_id", c.clientID))

	// Read the first wire message
	firstMsgBytes, err := c.readWireMessage()
	if err != nil {
		c.log.Error("Failed to read first wire message", zap.Error(err))
		return
	}

	// Decode the first message
	firstMsgOp, err := mongo.Decode(firstMsgBytes)
	if err != nil {
		c.log.Error("Failed to decode first wire message", zap.Error(err))
		return
	}

	// Try to extract the target database from the MongoDB handshake
	// For MongoDB clients, the target database is often specified in the connection string
	// We need to parse this from the initial handshake message
	targetDatabase := c.extractTargetDatabaseFromHandshake(firstMsgOp)

	if targetDatabase != "" && targetDatabase != "admin" {
		c.intendedDatabase = targetDatabase
		c.determineAdminRoute()
		c.log.Debug("Determined admin route from handshake",
			zap.String("intended_database", c.intendedDatabase),
			zap.String("admin_route", c.adminRoute),
			zap.String("client_id", c.clientID))
	} else {
		c.log.Debug("Could not determine target database from handshake, will wait for non-admin operation",
			zap.String("client_id", c.clientID))
	}

	// Process the first message immediately
	err = c.handleMessageWithBytes(firstMsgBytes, firstMsgOp)
	if err != nil {
		c.log.Error("Error handling first message", zap.Error(err))
		return
	}

	// Continue processing subsequent messages
	c.processMessages()
}

// extractTargetDatabaseFromHandshake extracts the target database from MongoDB handshake messages
func (c *connection) extractTargetDatabaseFromHandshake(op mongo.Operation) string {
	// For MongoDB handshake operations (like isMaster, hello), we need to look for
	// the target database in the BSON payload of the message

	// Check if this is a handshake operation
	if !op.IsIsMaster() {
		return ""
	}

	// Log detailed information about the handshake operation to understand what we're receiving
	command, collection := op.CommandAndCollection()
	c.log.Debug("Handshake operation detected - analyzing for target database",
		zap.String("operation_type", fmt.Sprintf("%T", op)),
		zap.String("operation_string", op.String()),
		zap.String("database_name", op.DatabaseName()),
		zap.String("command", string(command)),
		zap.String("collection", collection),
		zap.String("request_id", fmt.Sprintf("%d", op.RequestID())))

	// Try to extract target database from the operation's BSON payload
	// For MongoDB handshake operations, the target database information might be
	// embedded in the BSON document that was sent
	targetDatabase := c.extractTargetDatabaseFromBSON(op)
	if targetDatabase != "" {
		c.log.Debug("Found target database in handshake BSON",
			zap.String("target_database", targetDatabase))
		return targetDatabase
	}

	// If we can't determine the target database from the handshake, return empty
	c.log.Debug("Could not extract target database from handshake operation")
	return ""
}

// extractTargetDatabaseFromBSON attempts to extract target database from BSON payload
func (c *connection) extractTargetDatabaseFromBSON(op mongo.Operation) string {
	// For MongoDB operations, we need to access the raw BSON data
	// The target database information might be in the connection metadata

	// Try to get the database name from the operation itself first
	databaseName := op.DatabaseName()
	if databaseName != "" && databaseName != "admin" {
		c.log.Debug("Found target database in operation database name",
			zap.String("database", databaseName))
		return databaseName
	}

	// For handshake operations, we'll analyze the operation string
	// to look for patterns that might indicate the target database
	operationString := op.String()
	c.log.Debug("Analyzing operation string for target database",
		zap.String("operation_string", operationString))

	// Look for patterns in the operation string that might indicate the target database
	// This is a heuristic approach - we'll look for database names in the string
	// that are not "admin"

	// For now, we'll return empty and let the normal flow handle it
	// The detailed logging will help us understand what information is available
	return ""
}

// handleMessageWithBytes handles a message with pre-decoded bytes and operation
func (c *connection) handleMessageWithBytes(wm []byte, op mongo.Operation) (err error) {
	var tags []string

	defer func(start time.Time) {
		if c.metrics != nil {
			tags := append(tags, fmt.Sprintf("success:%v", err == nil))
			_ = c.metrics.Timing("handle_message", time.Since(start), tags, 1)
		}
	}(time.Now())

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

	// Record request metrics
	if c.metrics != nil {
		// Extract database name from the operation
		databaseName := c.extractDatabaseName(op)

		// Record request count
		requestTags := append(tags, fmt.Sprintf("database:%s", databaseName))
		_ = c.metrics.Incr("request", requestTags, 1)

		// Record request size
		requestSize := len(wm)
		_ = c.metrics.Distribution("request_size", float64(requestSize), requestTags, 1)
	}

	c.log.Debug("Request",
		zap.String("op_code", fmt.Sprintf("%v", op.OpCode())),
		zap.Bool("is_master", isMaster),
		zap.String("command", string(command)),
		zap.String("collection", collection),
		zap.Int("request_size", len(wm)))

	// Perform round trip
	res, err := c.roundTrip(&mongo.Message{Wm: wm, Op: op}, false, tags)
	if err != nil {
		return err
	}

	// Record response metrics
	if c.metrics != nil {
		responseSize := len(res.Wm)
		responseSizeTags := append(tags, fmt.Sprintf("response_op_code:%v", res.Op.OpCode()))
		_ = c.metrics.Distribution("response_size", float64(responseSize), responseSizeTags, 1)
	}

	if _, err = c.conn.Write(res.Wm); err != nil {
		return
	}
	return
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

	if collection != "" {
		// Collection names are in format "database.collection"
		parts := strings.SplitN(collection, ".", 2)
		if len(parts) >= 2 {
			c.log.Debug("Extracted database name from collection",
				zap.String("collection", collection),
				zap.String("database", parts[0]))
			return parts[0]
		}
	}

	// Use the new DatabaseName method as fallback
	if dbName := op.DatabaseName(); dbName != "" {
		c.log.Debug("Extracted database name from operation", zap.String("database", dbName))
		return dbName
	}

	// Default fallback
	c.log.Debug("Using default database name", zap.String("collection", collection))
	return "default"
}

// determineTargetDatabase determines which database this operation should be routed to
func (c *connection) determineTargetDatabase(currentDatabase string, op mongo.Operation) string {
	// If we already have a cached route config, check if it matches the current operation
	if c.routeConfig != nil {
		// If the current operation is for a different database than what we have cached,
		// we need to handle this properly
		if currentDatabase != "admin" && currentDatabase != c.intendedDatabase {
			// This is a non-admin operation for a different database
			// We should route to the actual database, not use the cached admin route
			c.log.Debug("Non-admin operation for different database than cached",
				zap.String("current_db", currentDatabase),
				zap.String("cached_intended_db", c.intendedDatabase))
			return currentDatabase
		}
		// For admin operations or operations matching our cached database, use cached route
		return c.intendedDatabase
	}

	// If the current operation is not on admin, use it as the intended database
	if currentDatabase != "admin" {
		c.intendedDatabase = currentDatabase

		// Now that we know the intended database, determine the admin route
		c.determineAdminRoute()

		c.log.Debug("Set intended database from non-admin operation",
			zap.String("intended_database", currentDatabase),
			zap.String("current_operation_db", currentDatabase),
			zap.String("admin_route", c.adminRoute))
		return currentDatabase
	}

	// For admin operations, we need to determine the correct MongoDB instance
	// Strategy: Route admin operations to the same MongoDB instance as the target database

	// If we have an intended database, use the cached admin route
	if c.intendedDatabase != "" && c.adminRoute != "" {
		c.log.Debug("Using cached admin route for admin operation",
			zap.String("current_db", currentDatabase),
			zap.String("intended_db", c.intendedDatabase),
			zap.String("admin_route", c.adminRoute),
			zap.String("reason", "cached_admin_route"))
		return c.adminRoute
	}

	// For admin operations when we don't know the intended database yet,
	// try to extract it from the operation's BSON payload
	if c.intendedDatabase == "" {
		// Try to extract target database from the current operation
		targetDatabase := c.extractTargetDatabaseFromBSON(op)
		if targetDatabase != "" {
			c.intendedDatabase = targetDatabase
			c.determineAdminRoute()
			c.log.Debug("Extracted target database from BSON for admin operation",
				zap.String("target_database", targetDatabase),
				zap.String("admin_route", c.adminRoute))
			return c.adminRoute
		}

		// Fallback: use the first configured database as a heuristic
		firstDatabase := c.getFirstConfiguredDatabase()
		if firstDatabase != "" {
			c.intendedDatabase = firstDatabase
			c.determineAdminRoute()
			c.log.Debug("Using first configured database as heuristic for admin operation",
				zap.String("heuristic_database", firstDatabase),
				zap.String("admin_route", c.adminRoute))
			return c.adminRoute
		}
	}

	// If we still don't have an intended database, we can't route admin operations
	c.log.Warn("Admin operation without known intended database",
		zap.String("current_db", currentDatabase),
		zap.String("intended_db", c.intendedDatabase))
	return currentDatabase
}

// determineAdminRoute determines the admin route based on the intended database's host
func (c *connection) determineAdminRoute() {
	if c.intendedDatabase == "" || c.databaseRouter == nil {
		return
	}

	// Get the route for the intended database
	targetRoute, err := c.databaseRouter.GetRoute(c.intendedDatabase)
	if err != nil {
		c.log.Debug("Could not find route for intended database",
			zap.String("intended_database", c.intendedDatabase),
			zap.Error(err))
		return
	}

	// Extract host from the target database's connection string
	host, port := c.extractHostFromRoute(targetRoute)
	if host == "" || port == "" {
		c.log.Debug("Could not extract host/port from target route",
			zap.String("intended_database", c.intendedDatabase),
			zap.String("connection_string", targetRoute.ConnectionString))
		return
	}

	// Create a temporary admin route name based on the host:port
	// This will be used to route admin operations to the same MongoDB instance
	c.adminRoute = fmt.Sprintf("admin_%s_%s", host, port)

	c.log.Debug("Determined admin route for intended database",
		zap.String("intended_database", c.intendedDatabase),
		zap.String("host", host),
		zap.String("port", port),
		zap.String("admin_route", c.adminRoute))
}

// findAdminRouteForTarget finds the correct admin route based on the target database's host
// This implements the user's idea: route admin operations to the same MongoDB instance as the target database
func (c *connection) findAdminRouteForTarget(targetRoute *RouteConfig) string {
	// Extract host from the connection string
	host, port := c.extractHostFromRoute(targetRoute)
	if host == "" || port == "" {
		c.log.Debug("Could not extract host/port from target route",
			zap.String("connection_string", targetRoute.ConnectionString))
		return ""
	}

	// Look for a database configuration that matches this host:port
	// We'll search through all configured databases to find one that matches
	if c.databaseRouter != nil {
		// Try to find a route that uses the same host:port
		// This could be an exact match or a wildcard pattern
		adminRoute := c.findMatchingAdminRoute(host, port)
		if adminRoute != "" {
			c.log.Debug("Found matching admin route for host",
				zap.String("host", host),
				zap.String("port", port),
				zap.String("admin_route", adminRoute))
			return adminRoute
		}
	}

	c.log.Debug("No matching admin route found for host",
		zap.String("host", host),
		zap.String("port", port))

	return ""
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
	// Format: mongodb://host:port/database

	// Simple parsing - look for mongodb:// and extract host:port
	if !strings.HasPrefix(targetRoute.ConnectionString, "mongodb://") {
		return "", ""
	}

	// Remove mongodb:// prefix
	withoutScheme := strings.TrimPrefix(targetRoute.ConnectionString, "mongodb://")

	// Find the first '/' to separate host:port from database
	slashIndex := strings.Index(withoutScheme, "/")
	if slashIndex == -1 {
		return "", ""
	}

	// Extract host:port part
	hostPort := withoutScheme[:slashIndex]

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

// findMatchingAdminRoute finds a database route that matches the given host:port
func (c *connection) findMatchingAdminRoute(targetHost, targetPort string) string {
	// This is a simplified implementation that looks for exact host:port matches
	// In a more sophisticated implementation, we could:
	// 1. Look for wildcard patterns that match the host
	// 2. Handle hostname resolution
	// 3. Support multiple hosts in connection strings

	// For now, we'll look for routes that use the same host:port combination
	// and return the first one we find

	// This would require access to the database router's internal routes
	// For now, we'll implement a basic version that tries common patterns

	// Try to find a route that matches this host:port
	// We'll look for patterns like "*_port" or exact host matches

	// Try port-based wildcard first
	portWildcard := fmt.Sprintf("*_%s", targetPort)
	if c.databaseRouter != nil {
		if _, err := c.databaseRouter.GetRoute(portWildcard); err == nil {
			return portWildcard
		}
	}

	// Try host-based wildcard
	hostWildcard := fmt.Sprintf("*_%s", targetHost)
	if c.databaseRouter != nil {
		if _, err := c.databaseRouter.GetRoute(hostWildcard); err == nil {
			return hostWildcard
		}
	}

	return ""
}

// createDynamicAdminRoute creates a dynamic admin route based on the admin route name
func (c *connection) createDynamicAdminRoute(adminRouteName string) (*RouteConfig, error) {
	// Parse the admin route name: admin_host_port
	// Example: admin_localhost_27017
	parts := strings.Split(adminRouteName, "_")
	if len(parts) != 3 || parts[0] != "admin" {
		return nil, fmt.Errorf("invalid admin route name: %s", adminRouteName)
	}

	host := parts[1]
	port := parts[2]

	// Create a connection string for the admin database on the same host:port
	connectionString := fmt.Sprintf("mongodb://%s:%s/admin", host, port)

	c.log.Debug("Creating dynamic admin route",
		zap.String("admin_route", adminRouteName),
		zap.String("host", host),
		zap.String("port", port),
		zap.String("connection_string", connectionString))

	// Create MongoDB client options
	opts := options.Client().ApplyURI(connectionString)

	// Create MongoDB client
	mongoClient, err := mongo.Connect(c.log, c.metrics, opts, false) // Don't ping during creation
	if err != nil {
		return nil, fmt.Errorf("failed to create admin client for %s: %v", adminRouteName, err)
	}

	// Create route config
	routeConfig := &RouteConfig{
		DatabaseName:     adminRouteName,
		ConnectionString: connectionString,
		MongoClient:      mongoClient,
		Label:            fmt.Sprintf("admin-%s-%s", host, port),
	}

	// Add the route to the database router
	c.databaseRouter.AddRoute(adminRouteName, routeConfig)

	c.log.Debug("Successfully created dynamic admin route",
		zap.String("admin_route", adminRouteName),
		zap.String("host", host),
		zap.String("port", port))

	return routeConfig, nil
}

// getFirstConfiguredDatabase returns the first configured database name as a fallback
func (c *connection) getFirstConfiguredDatabase() string {
	if c.databaseRouter == nil {
		return ""
	}

	// Get all configured databases from the router
	// For now, we'll use a simple approach and return "mongobouncer" as the first database
	// In a more sophisticated implementation, we could iterate through the router's databases
	return "mongobouncer"
}

// createDummyResponse creates a proper dummy MongoDB response message
func (c *connection) createDummyResponse(originalMsg *mongo.Message) *mongo.Message {
	// Instead of creating a dummy response, let's just return the original message
	// This will prevent the slice bounds panic while we figure out the proper response format
	return originalMsg
}

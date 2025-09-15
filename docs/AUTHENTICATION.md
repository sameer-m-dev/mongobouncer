# MongoBouncer Authentication System

## Overview

MongoBouncer implements a unique authentication system that leverages MongoDB's `isMaster` handshake mechanism to provide proxy-level authentication. This document explains the authentication approach, its rationale, implementation details, and the challenges that led to this solution.

## Table of Contents

1. [Authentication Approach](#authentication-approach)
2. [Why This Method?](#why-this-method)
3. [Implementation Details](#implementation-details)
4. [Configuration](#configuration)
5. [Previous Approaches and Limitations](#previous-approaches-and-limitations)
6. [Security Considerations](#security-considerations)
7. [Usage Examples](#usage-examples)
8. [Troubleshooting](#troubleshooting)

## Authentication Approach

### Core Concept

MongoBouncer uses the `appName` parameter in MongoDB connection strings to pass credentials during the `isMaster` handshake. This approach leverages the fact that:

1. **Every MongoDB operation requires an `isMaster` handshake first**
2. **`appName` is transmitted in the `isMaster` call**
3. **No operations can proceed without successful handshake**

### Authentication Flow

```
Client                    MongoBouncer                MongoDB
  |                           |                        |
  |-- isMaster(appName) ----->|                        |
  |                           |-- Extract credentials -|
  |                           |-- Validate against DB -|
  |                           |-- Allow/Deny ----------|
  |<-- Success/Error --------|                        |
  |                           |                        |
  |-- Operations ------------>|-- Forward ------------>|
```

### Credential Format

Credentials are passed in the `appName` parameter using the format:

```
mongodb://localhost:27017/database?appName=database:username:password
```

The format includes the database name to ensure credential uniqueness and prevent cross-database access vulnerabilities.

## Credential Uniqueness and Database Isolation

### The Problem with Previous Approach

The original authentication approach had a critical security limitation: **credential uniqueness was not enforced**. This meant that if multiple database routes used the same credentials, a client could potentially access multiple databases with the same authentication.

**Example of the Problem:**
```toml
[databases.admin]
connection_string = "mongodb://admin:admin123@host:27019/admin"

[databases.app_db]
connection_string = "mongodb://admin:admin123@host:27018/app_db"  # Same credentials!
```

With the previous approach, a client using `appName=admin:admin123` could access **both** the `admin` and `app_db` databases, creating a security vulnerability.

### The Solution: Database-Specific Credentials

The `database:username:password` format solves this by:

1. **Explicit Database Declaration**: The client must specify which database they intend to access
2. **Database Mismatch Validation**: MongoBouncer validates that the appName database matches the target database
3. **Credential Isolation**: Each database route can have unique credentials without conflicts

**Example with Current Format:**
```bash
# ✅ Works - database matches target
mongosh "mongodb://localhost:27017/admin?appName=admin:admin:admin123"

# ❌ Fails - database mismatch
mongosh "mongodb://localhost:27017/admin?appName=app_db:admin:admin123"
# Error: authentication failed: database mismatch - appName specifies 'app_db' but connecting to 'admin'
```

## Why This Method?

### The Problem

Traditional MongoDB authentication methods face several challenges in a proxy environment:

1. **SASL Complexity**: MongoDB's SASL authentication is complex and requires multi-step handshakes
2. **Wire Protocol Limitations**: Most connection string parameters are not transmitted in wire protocol messages
3. **Connection String Parsing**: Standard MongoDB clients don't send connection strings over the wire
4. **Authentication Timing**: Authentication needs to happen before any operations can proceed

### The Solution

The `appName` approach solves these problems because:

1. **Universal Coverage**: Every MongoDB operation starts with `isMaster`
2. **Always Present**: `appName` is sent in every handshake
3. **Early Authentication**: Can authenticate before any operations proceed
4. **Simple Implementation**: Just parse `appName` in `isMaster` calls
5. **No SASL Complexity**: Bypasses complex SASL handshake entirely

## Implementation Details

### Configuration-Based Authentication

Authentication can be enabled or disabled via TOML configuration:

```toml
[mongobouncer]
auth_enabled = true  # Default: true (secure by default)
```

### Authentication Logic

```go
func (c *connection) validateCredentialsFromAppName(msg *mongo.Message, targetDatabase string) error {
    // If authentication is disabled, skip validation
    if !c.authEnabled {
        c.log.Debug("Authentication is disabled, skipping credential validation")
        return nil
    }

    // Extract appName from isMaster call
    appName, err := c.extractAppNameFromMessage(msg)
    if err != nil {
        return fmt.Errorf("failed to extract appName: %v", err)
    }

    if appName == "" {
        return fmt.Errorf("authentication required: appName parameter must be provided")
    }

    // Parse credentials from appName (format: username:password)
    username, password, err := c.parseAppNameCredentials(appName)
    if err != nil {
        return fmt.Errorf("invalid appName format: must be 'username:password'")
    }

    // Validate against database configuration
    return c.validateCredentialsAgainstDatabase(username, password, targetDatabase)
}
```

### Wire Protocol Analysis

Our analysis revealed what parameters are actually sent in MongoDB wire protocol messages:

#### ✅ Parameters Sent in Wire Protocol
- **`$readPreference`**: In every operation
- **`$db`**: In every operation  
- **`appName`**: In `isMaster` calls (as `application.name`)
- **`lsid`**: Logical Session ID in every operation

#### ❌ Parameters NOT Sent in Wire Protocol
- **`authSource`**: Processed client-side only
- **`retryWrites`**: Client-side setting
- **`writeConcern`**: Applied client-side
- **`readConcern`**: Applied client-side

## Configuration

### TOML Configuration

```toml
[mongobouncer]
listen_addr = "0.0.0.0"
listen_port = 27017
log_level = "debug"
pool_mode = "session"
max_client_conn = 100
metrics_enabled = true
metrics_address = "localhost:9090"
auth_enabled = true  # Enable/disable authentication
```

### Database Configuration

Each database route must have credentials configured:

```toml
[databases.admin]
connection_string = "mongodb://admin:admin123@host.docker.internal:27019/admin?authSource=admin"

[databases.app_db]
connection_string = "mongodb://appuser:apppass@host.docker.internal:27018/app_db?authSource=admin"
```

## Previous Approaches and Limitations

### 1. SASL Authentication Interception

**Approach**: Intercept and handle SASL authentication messages directly.

**Implementation Attempted**:
```go
func (c *connection) handleSaslAuthenticationWithCredentialMapping(msg *mongo.Message) {
    // Extract username from SASL payload
    username, ok := c.extractUsernameFromSaslPayload(msg)
    // Map username to backend credentials
    backendConfig, err := c.mapUsernameToBackendConfig(username)
    // Forward with mapped credentials
}
```

**Limitations**:
- Complex multi-step SASL handshake
- "No SASL session state found" errors
- Difficult to maintain session state across proxy
- High complexity for minimal benefit

**Result**: ❌ Abandoned due to complexity

### 2. Custom Query Parameters

**Approach**: Use custom query parameters like `?username=foo&password=bar`.

**Implementation Attempted**:
```go
func (c *connection) extractCredentialsFromConnectionString(connStr string) {
    // Parse custom parameters from connection string
    parsedURL, err := url.Parse(connStr)
    username := parsedURL.Query().Get("username")
    password := parsedURL.Query().Get("password")
}
```

**Limitations**:
- Custom parameters are not sent in wire protocol
- MongoDB clients don't transmit connection string parameters
- Only processed client-side during connection establishment

**Result**: ❌ Abandoned - parameters not transmitted

### 3. AuthSource Parameter Extraction

**Approach**: Use `authSource=username:password` format.

**Implementation Attempted**:
```go
func (c *connection) extractAuthSourceFromMessage(msg *mongo.Message) {
    // Look for authSource in wire protocol messages
    opStr := msg.Op.String()
    if strings.Contains(opStr, "authSource") {
        // Extract authSource value
    }
}
```

**Limitations**:
- `authSource` is not sent in wire protocol messages
- Only processed during connection establishment
- Cannot extract from individual operations

**Result**: ❌ Abandoned - parameter not transmitted

### 4. AppName Authentication (Current Solution)

**Approach**: Use `appName=username:password` in connection string.

**Why It Works**:
- `appName` is sent in every `isMaster` call
- `isMaster` is required before any operations
- Simple string parsing and validation
- Universal coverage for all operations

**Result**: ✅ Success - implemented and working

## Security Considerations

### Strengths

1. **Early Authentication**: Happens during handshake, before any operations
2. **Universal Coverage**: Every operation requires authentication
3. **Configuration-Based**: Can be enabled/disabled as needed
4. **Secure by Default**: Authentication enabled by default
5. **Clear Logging**: Comprehensive debug logging

### Considerations

1. **Credential Exposure**: Credentials are visible in connection strings
2. **Log Visibility**: Credentials may appear in logs (consider log filtering)
3. **Network Transmission**: Credentials transmitted over network
4. **Client Compatibility**: Requires client support for `appName` parameter

### Recommendations

1. **Use HTTPS/TLS**: Encrypt connections to protect credentials in transit
2. **Log Filtering**: Implement log filtering to hide sensitive data
3. **Network Security**: Ensure secure network infrastructure
4. **Regular Rotation**: Implement credential rotation policies

## Usage Examples

### Secure Mode (Default)

```bash
# Admin database
mongosh "mongodb://localhost:27017/admin?appName=admin:admin:admin123" --eval "db.runCommand({ping: 1})"

# Application database
mongosh "mongodb://localhost:27017/app_db?appName=app_db:appuser:apppass" --eval "db.runCommand({ping: 1})"

# Analytics database
mongosh "mongodb://localhost:27017/analytics?appName=analytics:analytics:analyticspass" --eval "db.runCommand({ping: 1})"
```

### Authentication Failures

```bash
# ❌ Database mismatch
mongosh "mongodb://localhost:27017/admin?appName=app_db:admin:admin123" --eval "db.runCommand({ping: 1})"
# Error: authentication failed: database mismatch - appName specifies 'app_db' but connecting to 'admin'

# ❌ Wrong credentials
mongosh "mongodb://localhost:27017/admin?appName=admin:wrong:password" --eval "db.runCommand({ping: 1})"
# Error: authentication failed: invalid credentials

# ❌ No credentials when required
mongosh "mongodb://localhost:27017/admin" --eval "db.runCommand({ping: 1})"
# Error: MongoServerSelectionError: connection closed
```

### Permissive Mode

```toml
[mongobouncer]
auth_enabled = false
```

```bash
# Allows connections without authentication
mongosh "mongodb://localhost:27017/admin" --eval "db.runCommand({ping: 1})"
# Success: { ok: 1, ... }
```

## Troubleshooting

### Common Issues

#### 1. Authentication Failing with Correct Credentials

**Symptoms**: Connection closed even with correct `appName`

**Debug Steps**:
1. Check logs for authentication messages
2. Verify `auth_enabled = true` in configuration
3. Confirm database route exists in TOML
4. Check credential format: `database:username:password`
5. Verify database name in appName matches target database

**Logs to Check**:
```json
{"level":"debug","msg":"Validating credentials from appName","auth_enabled":true}
{"level":"debug","msg":"Extracted appName","app_name":"admin:admin:admin123"}
{"level":"debug","msg":"Parsed credentials from appName","app_database":"admin","username":"admin"}
{"level":"debug","msg":"Credentials match database configuration","username":"admin","target_database":"admin"}
```

#### 2. Database Mismatch Error

**Symptoms**: `authentication failed: database mismatch - appName specifies 'X' but connecting to 'Y'`

**Debug Steps**:
1. Verify the database name in appName matches the target database
2. Check that you're connecting to the correct database in the connection string
3. Ensure appName format is `database:username:password`

**Example Fix**:
```bash
# ❌ Wrong - database mismatch
mongosh "mongodb://localhost:27017/admin?appName=app_db:admin:admin123"

# ✅ Correct - database matches
mongosh "mongodb://localhost:27017/admin?appName=admin:admin:admin123"
```

#### 3. Authentication Not Required When Expected

**Symptoms**: Connections succeed without `appName` when `auth_enabled = true`

**Debug Steps**:
1. Verify TOML configuration is loaded
2. Check if `auth_enabled` is being parsed correctly
3. Restart MongoBouncer after configuration changes

**Logs to Check**:
```json
{"level":"debug","msg":"Authentication is disabled, skipping credential validation"}
```

#### 4. Wrong Credentials Accepted

**Symptoms**: Authentication succeeds with incorrect credentials

**Debug Steps**:
1. Check database configuration in TOML
2. Verify credential mapping is correct
3. Check if wrong database route is being used

### Debug Commands

```bash
# Check configuration loading
./bin/mongobouncer -config examples/mongobouncer.docker-compose.toml -verbose

# Test with current format
mongosh "mongodb://localhost:27017/admin?appName=admin:admin:admin123" --eval "db.runCommand({ping: 1})"

# Test database mismatch (should fail)
mongosh "mongodb://localhost:27017/admin?appName=app_db:admin:admin123" --eval "db.runCommand({ping: 1})"

# Test with authentication disabled
mongosh "mongodb://localhost:27017/admin" --eval "db.runCommand({ping: 1})"
```

### Log Analysis

Key log patterns to look for:

```bash
# Authentication enabled
grep "auth_enabled.*true" mongobouncer.log

# Authentication disabled  
grep "Authentication is disabled" mongobouncer.log

# Credential parsing
grep "Parsed credentials from appName" mongobouncer.log

# Database mismatch errors
grep "database mismatch" mongobouncer.log

# Credential validation success
grep "Credentials match database configuration" mongobouncer.log

# Authentication failures
grep "authentication failed" mongobouncer.log
```

## Conclusion

The `appName`-based authentication approach provides a robust, simple, and effective solution for proxy-level authentication in MongoBouncer. The `database:username:password` format addresses critical security concerns by ensuring credential uniqueness and database isolation. By leveraging MongoDB's universal `isMaster` handshake mechanism, we achieve:

- **Universal coverage** for all operations
- **Early authentication** before any data access
- **Simple implementation** without complex SASL handling
- **Configuration flexibility** for different deployment scenarios
- **Secure by default** behavior
- **Credential isolation** between database routes
- **Database-specific authentication** preventing cross-database access

This approach successfully addresses the limitations of previous methods while providing a clean, maintainable authentication system that integrates seamlessly with MongoDB's connection flow and ensures proper credential isolation between different database routes.

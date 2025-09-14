# Regex Credential Passthrough Feature

## Overview

The Regex Credential Passthrough feature is a new capability in MongoBouncer that allows wildcard and regex matched databases to use credentials provided by the client (via the `appName` parameter) for downstream MongoDB connections, while maintaining the existing behavior for exact matches.

## Problem Solved

Previously, all database routes (exact matches, wildcard patterns, and regex patterns) required credentials to be pre-configured in the route's connection string. This limitation made it difficult to:

1. **Multi-tenant applications**: Each tenant might have different database credentials
2. **Dynamic database provisioning**: New databases created on-the-fly couldn't use different credentials
3. **User-specific access**: Different users accessing the same wildcard pattern needed different authentication

## Solution

The new feature introduces a configurable behavior where:

- **Exact matches**: Continue to use route-configured credentials (no passthrough)
- **Wildcard/regex matches**: Use client-provided credentials for downstream connections
- **Feature flag**: Can be enabled/disabled via configuration

## Configuration

### Enable the Feature

```toml
[mongobouncer]
# Enable regex credential passthrough (default: true)
regex_credential_passthrough = true
```

### Disable the Feature

```toml
[mongobouncer]
# Disable regex credential passthrough
regex_credential_passthrough = false
```

### Example Configuration

```toml
[mongobouncer]
listen_addr = "0.0.0.0"
listen_port = 27017
auth_enabled = true
regex_credential_passthrough = true

[databases]
# Exact match - uses route credentials
exact_db.connection_string = "mongodb://admin:admin123@localhost:27017/exact_db"

# Wildcard pattern - uses client credentials
"test_*".connection_string = "mongodb://localhost:27017"

# Full wildcard - uses client credentials
"*".connection_string = "mongodb://localhost:27017"
```

## Usage

### Client Connection Format

```bash
mongodb://localhost:27017/database?appName=username:password
```

### Examples

#### 1. Exact Match (No Passthrough)
```bash
# Route: exact_db = "mongodb://admin:admin123@localhost:27017/exact_db"
# Client must provide matching credentials
mongosh "mongodb://localhost:27017/exact_db?appName=admin:admin123"
```

#### 2. Wildcard Pattern (With Passthrough)
```bash
# Route: "test_*" = "mongodb://localhost:27017"
# Client credentials are passed through to MongoDB
mongosh "mongodb://localhost:27017/test_db1?appName=user1:pass1"
mongosh "mongodb://localhost:27017/test_db2?appName=user2:pass2"
```

#### 3. Full Wildcard (With Passthrough)
```bash
# Route: "*" = "mongodb://localhost:27017"
# Any database not matched by other patterns uses client credentials
mongosh "mongodb://localhost:27017/any_database?appName=anyuser:anypass"
```

## Implementation Details

### Authentication Flow

1. **Client Connection**: Client connects with `appName=username:password`
2. **Route Matching**: MongoBouncer determines if the database matches a wildcard/regex pattern
3. **Credential Decision**:
   - **Exact match**: Validate credentials against route configuration
   - **Wildcard/regex match**: Use client credentials for downstream connection
4. **MongoDB Connection**: Create MongoDB client with appropriate credentials
5. **Request Forwarding**: Forward requests using the authenticated connection

### Code Changes

#### Configuration (`config/config.go`)
- Added `RegexCredentialPassthrough bool` field to `MongobouncerConfig`
- Default value: `true`
- Parsing logic for TOML configuration

#### Proxy (`proxy/proxy.go`)
- Updated `NewProxy` function signature to accept the new parameter
- Passed configuration to connection handlers

#### Connection Handling (`proxy/connection.go`)
- Modified `validateCredentialsAgainstDatabase` to check for wildcard/regex matches
- Added `isWildcardOrRegexMatch` function to detect pattern matches
- Added `createMongoClientWithCredentials` function for dynamic client creation
- Updated `roundTrip` logic to use client credentials for wildcard matches
- Enhanced `extractHostFromRoute` to properly handle credentials in connection strings

### Pattern Detection

The feature detects wildcard/regex matches using:

1. **Wildcard patterns**: Routes containing `*` characters
2. **Full wildcard**: Routes matching `*` (catch-all)
3. **Non-exact matches**: Routes where the pattern doesn't exactly match the target database name

## Testing

### Unit Tests

The feature includes comprehensive unit tests:

- **Configuration parsing**: Tests for TOML configuration loading
- **Pattern matching**: Tests for wildcard/regex detection
- **Connection string parsing**: Tests for credential extraction
- **Host/port extraction**: Tests for connection string parsing

### Integration Tests

A test script (`test_wildcard_credential_passthrough.sh`) demonstrates:

1. **Exact match behavior**: Route credentials are used
2. **Wildcard pattern behavior**: Client credentials are passed through
3. **Multiple patterns**: Different wildcard patterns work independently
4. **Full wildcard behavior**: Catch-all pattern uses client credentials

### Running Tests

```bash
# Run unit tests
go test ./config -v -run TestRegexCredentialPassthrough
go test ./proxy -v -run "TestIsWildcardOrRegexMatch|TestSanitizeConnectionString|TestExtractHost"

# Run integration test (requires MongoDB running)
./test_wildcard_credential_passthrough.sh
```

## Security Considerations

### Credential Handling

- **Client credentials**: Passed through to MongoDB (not stored by MongoBouncer)
- **Route credentials**: Used for exact matches (stored in configuration)
- **Logging**: Credentials are sanitized in logs (passwords shown as `***`)

### Authentication Flow

- **Early validation**: Authentication happens during `isMaster` handshake
- **No credential storage**: Client credentials are not persisted
- **Dynamic connections**: MongoDB clients are created per-request for wildcard matches

### Network Security

- **Encryption**: Use TLS/SSL for production deployments
- **Network isolation**: Ensure secure network infrastructure
- **Access control**: Implement proper firewall rules

## Performance Impact

### Connection Management

- **Dynamic clients**: Wildcard matches create new MongoDB clients per request
- **Connection pooling**: MongoDB driver handles connection pooling
- **Memory usage**: Minimal additional memory for client creation
- **Latency**: Slight increase due to dynamic client creation

### Optimization Recommendations

1. **Connection reuse**: MongoDB driver's connection pooling minimizes overhead
2. **Client caching**: Consider implementing client caching for frequently used credentials
3. **Monitoring**: Use Prometheus metrics to monitor performance

## Migration Guide

### From Previous Versions

1. **Configuration update**: Add `regex_credential_passthrough = true` to enable
2. **No breaking changes**: Existing configurations continue to work
3. **Gradual rollout**: Enable feature flag per environment

### Configuration Migration

```toml
# Before (implicit behavior)
[databases]
"test_*" = "mongodb://user:pass@localhost:27017"

# After (explicit behavior)
[mongobouncer]
regex_credential_passthrough = true

[databases]
"test_*" = "mongodb://localhost:27017"  # No credentials - uses client's
```

## Troubleshooting

### Common Issues

#### 1. Authentication Failures
```
Error: authentication failed: invalid credentials
```
**Solution**: Ensure client credentials match MongoDB user configuration

#### 2. Connection String Parsing
```
Error: failed to extract host/port from route connection string
```
**Solution**: Verify connection string format in configuration

#### 3. Pattern Matching
```
Error: database X is not supported
```
**Solution**: Check wildcard pattern configuration and matching logic

### Debug Mode

Enable debug logging to troubleshoot issues:

```toml
[mongobouncer]
log_level = "debug"
```

### Log Analysis

Look for these log messages:

- `"Using credential passthrough for wildcard/regex match"`
- `"Creating MongoDB client with client credentials"`
- `"Route pattern contains wildcard"`
- `"Route is exact match"`

## Future Enhancements

### Potential Improvements

1. **Client caching**: Cache MongoDB clients by credentials to improve performance
2. **Credential validation**: Pre-validate client credentials against MongoDB
3. **Pattern-specific settings**: Different passthrough behavior per pattern
4. **Metrics enhancement**: Add metrics for credential passthrough usage

### Configuration Extensions

```toml
[databases]
"test_*" = {
    connection_string = "mongodb://localhost:27017",
    credential_passthrough = true,  # Per-pattern setting
    credential_validation = true   # Pre-validate credentials
}
```

## Conclusion

The Regex Credential Passthrough feature provides a powerful and flexible way to handle authentication for dynamic database patterns while maintaining backward compatibility. It enables multi-tenant applications, dynamic database provisioning, and user-specific access patterns without compromising security or performance.

The feature is designed to be:
- **Configurable**: Can be enabled/disabled per deployment
- **Secure**: Maintains proper authentication flows
- **Performant**: Minimal overhead with MongoDB driver optimization
- **Compatible**: No breaking changes to existing configurations

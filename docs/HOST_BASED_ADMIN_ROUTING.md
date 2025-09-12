# Host-Based Admin Routing

## Overview

MongoBouncer now includes intelligent host-based admin routing that automatically routes admin database operations to the same MongoDB instance as the target database. This eliminates the need to hardcode admin database details in the TOML configuration file.

## Problem Solved

When MongoDB clients connect to MongoBouncer, they often start with authentication against the `admin` database. Previously, this would fail if the admin database wasn't explicitly configured in the `[databases]` section of the TOML file.

**Example scenario:**
- Client connects: `mongodb://localhost:27017/mongobouncer`
- First operation is against `admin` database for authentication
- Without host-based routing: Connection fails because `admin` database is not configured
- With host-based routing: Admin operation is automatically routed to `localhost:27017` (same host as `mongobouncer` database)

## How It Works

The host-based admin routing works through the following process:

1. **Connection Analysis**: When a client connects, MongoBouncer extracts the target database from the connection string
2. **Host Extraction**: For admin operations, MongoBouncer finds the MongoDB instance that hosts the target database
3. **Smart Routing**: Admin operations are routed to the same MongoDB instance's admin database
4. **Seamless Operation**: The client can authenticate and then use the target database without any configuration changes

## Implementation Details

### Key Methods

- `extractTargetDatabaseFromConnection()`: Extracts the target database from the connection string
- `determineTargetDatabase()`: Determines which database to route operations to
- `findAdminRouteForTarget()`: Finds the correct admin route based on the target database's host
- `extractHostFromRoute()`: Extracts host and port from database configuration
- `extractHostAndPort()`: Parses host:port strings (supports IPv4, IPv6, and default ports)

### Routing Logic

```go
// For admin operations, route to the same MongoDB instance as the target database
if currentDatabase == "admin" && intendedDatabase != "" {
    targetRoute := databaseRouter.GetRoute(intendedDatabase)
    adminRoute := findAdminRouteForTarget(targetRoute)
    return adminRoute
}
```

## Configuration

### Automatic Operation

Host-based admin routing works automatically with no additional configuration required. Simply configure your target databases as usual:

```toml
[databases]
mongobouncer = "mongodb://localhost:27017/mongobouncer"
analytics = "mongodb://localhost:27018/analytics"
```

When a client connects to `mongodb://localhost:27017/mongobouncer`, admin operations will automatically be routed to `localhost:27017`.

### Advanced Configuration (Optional)

For advanced scenarios, you can optionally configure port-specific admin routes:

```toml
[databases]
# Target databases
mongobouncer = "mongodb://localhost:27017/mongobouncer"
analytics = "mongodb://localhost:27018/analytics"

# Optional: Explicit admin routes
[databases.*_27017]  # Admin route for localhost:27017
host = "localhost"
port = 27017
dbname = "admin"
user = "${ADMIN_USER}"
password = "${ADMIN_PASSWORD}"
pool_mode = "session"
label = "admin-27017"

[databases.*_27018]  # Admin route for localhost:27018
host = "localhost"
port = 27018
dbname = "admin"
user = "${ADMIN_USER}"
password = "${ADMIN_PASSWORD}"
pool_mode = "session"
label = "admin-27018"
```

## Benefits

1. **Simplified Configuration**: No need to hardcode admin database details
2. **Automatic Discovery**: MongoBouncer automatically finds the correct MongoDB instance
3. **Reduced Configuration Errors**: Eliminates common configuration mistakes
4. **Backward Compatibility**: Existing configurations continue to work
5. **Flexible Routing**: Supports complex multi-instance deployments

## Testing

The implementation includes comprehensive tests:

```bash
# Run the host-based admin routing tests
go test ./proxy -v -run TestHostBasedAdminRouting

# Run host/port extraction tests
go test ./proxy -v -run TestExtractHostAndPort
```

## Examples

### Basic Usage

```toml
[databases]
app_db = "mongodb://prod-cluster:27017/myapp"
analytics = "mongodb://analytics-cluster:27017/analytics"
```

**Client connections:**
- `mongodb://localhost:27017/app_db` → Admin operations routed to `prod-cluster:27017`
- `mongodb://localhost:27017/analytics` → Admin operations routed to `analytics-cluster:27017`

### Multi-Port Scenario

```toml
[databases]
dev_app = "mongodb://localhost:27017/dev_app"
test_app = "mongodb://localhost:27018/test_app"
prod_app = "mongodb://localhost:27019/prod_app"
```

**Client connections:**
- `mongodb://localhost:27017/dev_app` → Admin operations routed to `localhost:27017`
- `mongodb://localhost:27017/test_app` → Admin operations routed to `localhost:27018`
- `mongodb://localhost:27017/prod_app` → Admin operations routed to `localhost:27019`

## Troubleshooting

### Debug Logging

Enable debug logging to see the host-based routing in action:

```toml
[mongobouncer]
log_level = "debug"
```

Look for log messages like:
```
Using host-based admin routing current_db=admin intended_db=mongobouncer admin_route=*_27017 reason=host_based_routing
```

### Common Issues

1. **No Matching Route**: If no matching admin route is found, MongoBouncer falls back to the catch-all route (`*`)
2. **Connection String Parsing**: Ensure connection strings follow the standard MongoDB format: `mongodb://host:port/database`
3. **Port Extraction**: Default port `27017` is used if no port is specified in the connection string

## Future Enhancements

Potential future improvements could include:

1. **Hostname Resolution**: Support for hostname-to-IP resolution
2. **Multiple Hosts**: Support for replica set connection strings with multiple hosts
3. **Advanced Pattern Matching**: More sophisticated pattern matching for complex deployments
4. **Connection String Caching**: Cache parsed connection strings for better performance

## Conclusion

Host-based admin routing makes MongoBouncer more user-friendly by eliminating the need to configure admin databases explicitly. It automatically routes admin operations to the correct MongoDB instance based on the target database, making deployments simpler and less error-prone.

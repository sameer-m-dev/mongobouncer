# MongoBouncer

`mongobouncer` is a lightweight MongoDB connection pooler written in Golang, inspired by [pgbouncer](https://www.pgbouncer.org/) and based on the original [mongobetween](https://github.com/coinbase/mongobetween) project by Coinbase. It functions as an exact replica of `pgbouncer` but designed specifically for MongoDB, handling a large number of incoming connections and multiplexing them across a smaller connection pool to one or more MongoDB clusters.


`mongobouncer` is actively used in production at [Fynd](https://www.fynd.com/), where it serves as a critical infrastructure component for MongoDB connection management at scale.

**Production Deployment Characteristics:**
- **Scale**: Handles connection storms from multiple applications across microservices architecture
- **Architecture Support**: Seamlessly works with MongoDB deployments in multiple configurations:
  - **Replica Set Mode**: Primary-secondary configurations for high availability
  - **Standalone Servers**: Single MongoDB instances for development and specific workloads  
  - **Sharded Clusters**: Distributed MongoDB architectures supporting entity-based data partitioning
- **Connection Management**: Effectively reduces connection counts that frequently breach MongoDB's maximum connection limits (typically 500-1000+ concurrent connections per application instance)
- **Multi-Application Support**: Deployed as a highly available service using Helm charts, with all applications (Rails, Node.js, Go services) connecting to this centralized MongoDB proxy deployment

The proxy was specifically built to support complex sharded architectures where different business entities (merchants, customers, products, orders, etc) are distributed across multiple MongoDB clusters, requiring intelligent connection pooling and routing to maintain optimal performance while staying within MongoDB's connection limits.

### How it works
`mongobouncer` listens for incoming connections from an application, and proxies any queries to the [MongoDB Go driver](https://github.com/mongodb/mongo-go-driver) which is connected to a MongoDB cluster. It also intercepts any `ismaster` commands from the application, and responds with `"I'm a shard router (mongos)"`, without proxying. This means `mongobouncer` appears to the application as an always-available MongoDB shard router, and any MongoDB connection issues or failovers are handled internally by the Go driver.

### Installation

**From Source:**
```bash
git clone https://github.com/sameer-m-dev/mongobouncer
cd mongobouncer
go build -o bin/mongobouncer .
```

**Using Go Install:**
```bash
go install github.com/sameer-m-dev/mongobouncer
```

### Configuration

`mongobouncer` uses TOML-based configuration for comprehensive setup and management. The configuration system provides fine-grained control over connection pooling, authentication, monitoring, and database routing.

**⚠️ Important: Database Configuration Format**

Database configurations support **two methods**:

**✅ Method 1: Connection String (Preferred):**
```toml
[databases]
app_db = { connection_string = "mongodb://localhost:27017/myapp" }
analytics = { connection_string = "mongodb://analytics-cluster:27017/analytics?replicaSet=rs0" }
```

**✅ Method 2: Individual Fields (Fallback):**
```toml
[databases]
[databases.local_db]
host = "localhost"
port = 27017
dbname = "myapp"
max_pool_size = 20
min_pool_size = 5
```

**❌ Incorrect Format (Will Cause Error):**
```toml
[databases]
app_db = "mongodb://localhost:27017/myapp"  # ❌ Simple string format not supported
```

**Basic Usage:**
```bash
./mongobouncer -config mongobouncer.toml [-verbose]
```

**Command Line Options:**
```
Usage: mongobouncer [OPTIONS]
  --config string
        Path to TOML configuration file (required)
  --verbose
        Enable verbose (debug) logging
  --help
        Show help message
```

**Minimal Configuration Example (`mongobouncer.toml`):**
```toml
[mongobouncer]
listen_addr = "0.0.0.0"
listen_port = 27017
log_level = "info"
pool_mode = "session"
min_pool_size = 5
max_pool_size = 20
max_client_conn = 100
auth_enabled = true

[mongobouncer.metrics]
address = "0.0.0.0:9090"
enabled = true

[databases]
app_db = { connection_string = "mongodb://localhost:27017/myapp" }
analytics = { connection_string = "mongodb://analytics-cluster:27017/analytics?replicaSet=rs0" }
```

See [mongobouncer.example.toml](examples/mongobouncer.example.toml) for a comprehensive configuration with all available options including TLS, authentication, and advanced pool management settings.

**Advanced Configuration Examples:**

*Multiple Database Routing:*
```toml
[databases]
# Production sharded clusters
users = { connection_string = "mongodb://user-cluster:27017/users?replicaSet=users-rs&maxPoolSize=50" }
products = { connection_string = "mongodb://product-cluster:27017/products?replicaSet=products-rs&maxPoolSize=30" }
orders = { connection_string = "mongodb://order-cluster:27017/orders?replicaSet=orders-rs&maxPoolSize=40" }

# Analytics read replicas
analytics = { connection_string = "mongodb://analytics-cluster:27017/analytics?readPreference=secondary&maxPoolSize=20" }
```

*Connection Pool Modes:*
- **Session Mode**: Connection returned to pool after session ends (default, recommended)
- **Transaction Mode**: Connection returned after transaction completes
- **Statement Mode**: Connection returned immediately after statement execution

*Wildcard Database Support:*
```toml
[databases]
# Exact match - specific database
myapp_prod = { connection_string = "mongodb://prod-cluster:27017/myapp_prod" }

# Wildcard patterns - match multiple databases
# Prefix wildcard: matches databases starting with "analytics_"
analytics_* = { connection_string = "mongodb://analytics-cluster:27017/analytics" }

# Suffix wildcard: matches databases ending with "_test"  
*_test = { connection_string = "mongodb://test-cluster:27017/test" }

# Contains wildcard: matches databases containing "staging"
*staging* = { connection_string = "mongodb://staging-cluster:27017/staging" }

# Complex wildcard: matches patterns like "analytics_tenant_v2"
analytics_*_v2 = { connection_string = "mongodb://analytics-v2-cluster:27017/analytics_v2" }

# Catch-all wildcard: matches any database not matched above
* = { connection_string = "mongodb://default-cluster:27017/default" }
```

**Wildcard Database Routing:**
MongoBouncer supports wildcard database patterns for dynamic routing based on database name patterns. This allows you to handle multiple databases with similar naming patterns without configuring each one individually.

**Precedence Logic:**
1. **Exact Match**: Specific database names (e.g., `myapp_prod`) take highest priority
2. **Wildcard Match**: Pattern-based matches (e.g., `analytics_*`, `*_test`) are checked in order
3. **Error**: If no match is found, the connection is rejected with an appropriate error message

**Wildcard Pattern Types:**
- **Prefix**: `analytics_*` matches `analytics_users`, `analytics_orders`, etc.
- **Suffix**: `*_test` matches `users_test`, `products_test`, etc.  
- **Contains**: `*staging*` matches `users_staging`, `staging_orders`, etc.
- **Complex**: `analytics_*_v2` matches `analytics_tenant_v2`, `analytics_customer_v2`, etc.
- **Catch-all**: `*` matches any database not matched by other patterns

*Production Deployment:*
```bash
# Docker deployment
docker run -d \
  -p 27017:27017 \
  -p 9090:9090 \
  -v /path/to/config:/etc/mongobouncer \
  mongobouncer:latest -config /etc/mongobouncer/mongobouncer.toml

# Kubernetes deployment via Helm
helm install mongobouncer ./deploy/helm/mongobouncer
```

### Architecture & Features

**Core Capabilities:**
- ✅ **Connection Multiplexing**: Reduces thousands of application connections to manageable pool sizes
- ✅ **Transaction Server Pinning**: Ensures transaction consistency across operations  
- ✅ **Cursor Tracking**: Maintains cursor-to-server mappings for complex queries
- ✅ **Multi-Cluster Support**: Route different databases to separate MongoDB clusters
- ✅ **Wildcard Database Support**: Dynamic routing based on database name patterns
- ✅ **Proxy-Level Authentication**: Secure authentication using appName-based credentials
- ✅ **Dynamic Configuration**: Runtime configuration updates without restarts
- ✅ **Prometheus Metrics**: Comprehensive monitoring and observability
- ✅ **High Availability**: Graceful handling of MongoDB failovers and network issues

**MongoDB Topology Support:**
- **Standalone**: Single MongoDB instances
- **Replica Sets**: Primary-secondary configurations with automatic failover
- **Sharded Clusters**: Distributed MongoDB with `mongos` routing
- **Mixed Environments**: Simultaneous connections to different topology types

### Authentication

MongoBouncer implements a unique proxy-level authentication system that leverages MongoDB's `isMaster` handshake mechanism. This approach provides secure authentication without the complexity of traditional SASL authentication.

**Key Features:**
- 🔐 **Secure by Default**: Authentication enabled by default (`auth_enabled = true`)
- 🌐 **Universal Coverage**: Every MongoDB operation requires authentication
- ⚡ **Early Authentication**: Happens during handshake, before any operations
- 🔧 **Configuration-Based**: Can be enabled/disabled via TOML configuration
- 📝 **Clear Logging**: Comprehensive debug logging for troubleshooting

**Authentication Method:**
Credentials are passed using the `appName` parameter in MongoDB connection strings:

```bash
# Secure connection with authentication
mongosh "mongodb://localhost:27017/admin?appName=admin:admin:admin123" # The format is database_name:username:password

# Will fail without appName when auth_enabled = true
mongosh "mongodb://localhost:27017/admin"
# Error: MongoServerSelectionError: connection closed
```

**Configuration:**
```toml
[mongobouncer]
auth_enabled = true  # Enable/disable authentication (default: true)

[databases]
admin = { connection_string = "mongodb://admin:admin123@host.docker.internal:27019/admin?authSource=admin" }
app_db = { connection_string = "mongodb://appuser:apppass@host.docker.internal:27018/app_db?authSource=admin" }
```

**Why This Approach?**
- **Universal**: Every MongoDB operation requires `isMaster` handshake first
- **Simple**: No complex SASL handshake handling required
- **Efficient**: Authentication happens once during connection establishment
- **Secure**: Credentials validated against database configuration before any operations

For detailed information about the authentication system, implementation details, and troubleshooting, see [AUTHENTICATION.md](AUTHENTICATION.md).

### Prometheus Metrics
`mongobouncer` supports Prometheus metrics collection with a built-in HTTP endpoint for scraping. By default it serves metrics on `localhost:9090/metrics`. The following metrics are reported:

**Connection Metrics:**
 - `mongobouncer_open_connections_total` (Gauge) - Current number of open connections between the proxy and the application
 - `mongobouncer_connections_opened_total` (Counter) - Total number of connections opened with the application
 - `mongobouncer_connections_closed_total` (Counter) - Total number of connections closed with the application

**Message Handling Metrics:**
 - `mongobouncer_message_handle_duration_seconds` (Histogram) - End-to-end time handling an incoming message from the application
 - `mongobouncer_round_trip_duration_seconds` (Histogram) - Round trip time sending a request and receiving a response from MongoDB
 - `mongobouncer_request_size_bytes` (Histogram) - Request size to MongoDB
 - `mongobouncer_response_size_bytes` (Histogram) - Response size from MongoDB

**Cursor and Transaction Tracking:**
 - `mongobouncer_cursors_active_total` (Gauge) - Number of open cursors being tracked (for cursor -> server mapping)
 - `mongobouncer_transactions_active_total` (Gauge) - Number of transactions being tracked (for client sessions -> server mapping)

**MongoDB Driver Metrics:**
 - `mongobouncer_server_selection_duration_seconds` (Histogram) - Go driver server selection timing
 - `mongobouncer_checkout_connection_duration_seconds` (Histogram) - Go driver connection checkout timing
 - `mongobouncer_pool_checked_out_connections_total` (Gauge) - Number of connections checked out from the Go driver connection pool
 - `mongobouncer_pool_open_connections_total` (Gauge) - Number of open connections from the Go driver to MongoDB
 - `mongobouncer_pool_events_total` (Counter) - Go driver connection pool events (by event type)
 - `mongobouncer_pool_wait_duration_seconds` (Histogram) - Time spent waiting for connections from the pool
 - `mongobouncer_pool_connections_total` (Gauge) - Current number of connections in the pool (by state: available, in_use)

**Endpoints:**
 - `/metrics` - Prometheus metrics endpoint
 - `/health` - Health check endpoint

### Background & Inspiration

`mongobouncer` was originally inspired by Coinbase's [mongobetween](https://github.com/coinbase/mongobetween) project, which addressed connection storm issues in high-scale Rails applications (see their [blog post](https://blog.coinbase.com/scaling-connections-with-ruby-and-mongodb-99204dbf8857)). Building upon this foundation, `mongobouncer` has been enhanced and adapted for high load production requirements.

**The Problem:**
Multi-process web applications (like Ruby Puma workers, Node.js cluster mode, or Go service replicas) create separate MongoDB connection pools per process. In high-scale deployments, this leads to:
- **Connection Storm**: 50,000+ connections overwhelming MongoDB servers
- **Resource Exhaustion**: Exceeding MongoDB's connection limits (typically 65-128,000 depending on configuration)
- **Performance Degradation**: Excessive `isMaster` commands and connection management overhead
- **Scaling Limitations**: Unable to horizontally scale applications due to connection constraints

**The Solution:**
`mongobouncer` acts as a connection multiplexer, reducing connection counts by an order of magnitude:
- **Before**: 30,000+ direct application connections to MongoDB
- **After**: ~1,000 optimized connections through mongobouncer
- **Efficiency**: Single monitor goroutine per proxy process vs. monitor thread per application process
- **Transparency**: Applications connect through standard MongoDB connection strings without modification

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

### Original Attribution
This project builds upon and extends [mongobetween](https://github.com/coinbase/mongobetween) by Coinbase, which is also licensed under Apache License 2.0. We acknowledge and thank the original contributors for their foundational work.

## Documentation

- [Authentication System](./AUTHENTICATION.md) - Complete authentication system documentation, implementation details, and troubleshooting guide
- [Comprehensive Tests](./docs/COMPREHENSIVE_TESTS.md) - Detailed testing documentation
- [Prometheus Migration](./docs/PROMETHEUS_MIGRATION.md) - Metrics migration guide
- [Test Cleanup Report](./docs/TEST_CLEANUP_REPORT.md) - Test cleanup documentation
- [Host-Based Admin Routing](./docs/HOST_BASED_ADMIN_ROUTING.md) - Admin routing guide
- [Connection Pool Architecture](./docs/CONNECTION_POOL_ARCHITECTURE.md) - Complete connection pooling architecture, fixes, and optimization guide

## Contributing

We welcome contributions to `mongobouncer`! Here's how you can help:

### 🤝 Ways to Contribute

- **🐛 Bug Reports**: Found a bug? Please open an issue with detailed reproduction steps
- **💡 Feature Requests**: Have an idea? Share it by opening a feature request issue  
- **📝 Documentation**: Help improve our documentation and examples
- **🔧 Code Contributions**: Submit pull requests for bug fixes or new features
- **🧪 Testing**: Help expand our test coverage or test in different environments

### 📋 Getting Started

1. **Fork the Repository**
   ```bash
   git clone https://github.com/sameer-m-dev/mongobouncer
   cd mongobouncer
   ```

2. **Setup Development Environment**
   ```bash
   go mod tidy
   go build -o bin/mongobouncer .
   ```

3. **Run Tests**
   ```bash
   go test ./...
   go test ./test/ -v  # Integration tests
   ```

4. **Create Your Feature Branch**
   ```bash
   git checkout -b feature/amazing-feature
   ```

5. **Make Your Changes**
   - Write clean, well-documented code
   - Add tests for new functionality
   - Ensure all tests pass

6. **Commit and Push**
   ```bash
   git commit -m 'Add some amazing feature'
   git push origin feature/amazing-feature
   ```

7. **Open a Pull Request**
   - Provide a clear description of your changes
   - Reference any related issues
   - Include test results

### 🧪 Development Guidelines

- **Code Style**: Follow Go best practices and run `go fmt`
- **Testing**: Write unit tests for new functionality
- **Documentation**: Update relevant documentation
- **Backwards Compatibility**: Maintain compatibility when possible
- **Performance**: Consider performance implications of changes

### 🔍 Testing Your Changes

- **Unit Tests**: `go test ./...`
- **Integration Tests**: `go test ./test/ -v`
- **Comprehensive MongoDB Tests**: `go run test/comprehensive/`
- **Configuration Tests**: Test with different TOML configurations
- **Prometheus Metrics**: Verify metrics are properly exposed

### 💬 Community

- **Issues**: Use GitHub Issues for bug reports and feature requests
- **Discussions**: Join discussions for questions and community support
- **Security**: For security vulnerabilities, please email maintainers directly

### 📜 Code of Conduct

We are committed to providing a welcoming and inclusive experience for all contributors. Please be respectful and professional in all interactions.

---

**Thank you for contributing to `mongobouncer`!** 🎉 Your help makes this project better for everyone.

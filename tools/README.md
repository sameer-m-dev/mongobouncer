# MongoDB Setup with Docker Compose

This directory contains the Docker Compose configuration for MongoDB with single-node replica sets, standalone instances, and a no-auth testing setup. All MongoDB instances are automatically initialized with users and test data.

## Services Overview

### MongoDB 6 Single-Node Replica Set (rs0)
- **mongo6-rs**: Port 27018 - Single-node replica set with authentication
- **mongo6-rs-init**: Automatic initialization service

### MongoDB 8 Single-Node Replica Set (rs1)
- **mongo8-rs**: Port 27019 - Single-node replica set with authentication
- **mongo8-rs-init**: Automatic initialization service

### MongoDB 6 Standalone
- **mongo6-standalone**: Port 27020 - Standalone MongoDB 6 instance with authentication
- **mongo6-standalone-init**: Automatic initialization service

### MongoDB 8 Standalone
- **mongo8-standalone**: Port 27021 - Standalone MongoDB 8 instance with authentication
- **mongo8-standalone-init**: Automatic initialization service

### MongoDB 8 No-Auth (Testing)
- **mongo8-noauth**: Port 27022 - Standalone MongoDB 8 instance **without authentication** for testing

### Monitoring Services
- **Prometheus**: Port 8010 - Metrics collection
- **Grafana**: Port 8011 - Metrics visualization

## Authentication

Most MongoDB instances are configured with authentication enabled, except for the testing instance. All users and databases are automatically created by initialization scripts.

### Admin Users (All Authenticated Instances)
- **Username**: `admin`
- **Password**: `admin123`
- **Role**: Root access to all databases

### MongoDB 6 Replica Set Users
- **Application User**: `appuser_rs6` / `apppass_rs6_123`
- **Read-Only User**: `readonly_rs6` / `readonly_rs6_123`
- **Analytics User**: `analytics_rs6` / `analytics_rs6_123`

### MongoDB 8 Replica Set Users
- **Application User**: `appuser_rs8` / `apppass_rs8_123`
- **Read-Only User**: `readonly_rs8` / `readonly_rs8_123`
- **Analytics User**: `analytics_rs8` / `analytics_rs8_123`

### MongoDB 6 Standalone Users
- **Application User**: `appuser_s6` / `apppass_s6_123`
- **Read-Only User**: `readonly_s6` / `readonly_s6_123`

### MongoDB 8 Standalone Users
- **Application User**: `appuser_s8` / `apppass_s8_123`
- **Read-Only User**: `readonly_s8` / `readonly_s8_123`

### No Authentication (Testing Instance)
- **mongo8-noauth**: No authentication required - ideal for testing and development

## Connection Strings

### MongoDB 6 Replica Set (rs0) - Port 27018
```bash
# Admin access
mongodb://admin:admin123@localhost:27018/?replicaSet=rs0&authSource=admin

# Application access
mongodb://appuser_rs6:apppass_rs6_123@localhost:27018/app_prod_rs6?replicaSet=rs0&authSource=admin

# Read-only access
mongodb://readonly_rs6:readonly_rs6_123@localhost:27018/app_prod_rs6?replicaSet=rs0&authSource=admin

# Analytics access
mongodb://analytics_rs6:analytics_rs6_123@localhost:27018/analytics_rs6?replicaSet=rs0&authSource=admin
```

### MongoDB 8 Replica Set (rs1) - Port 27019
```bash
# Admin access
mongodb://admin:admin123@localhost:27019/?replicaSet=rs1&authSource=admin

# Application access
mongodb://appuser_rs8:apppass_rs8_123@localhost:27019/app_prod_rs8?replicaSet=rs1&authSource=admin

# Read-only access
mongodb://readonly_rs8:readonly_rs8_123@localhost:27019/app_prod_rs8?replicaSet=rs1&authSource=admin

# Analytics access
mongodb://analytics_rs8:analytics_rs8_123@localhost:27019/analytics_rs8?replicaSet=rs1&authSource=admin
```

### MongoDB 6 Standalone - Port 27020
```bash
# Admin access
mongodb://admin:admin123@localhost:27020/?authSource=admin

# Application access
mongodb://appuser_s6:apppass_s6_123@localhost:27020/app_prod_s6?authSource=admin

# Read-only access
mongodb://readonly_s6:readonly_s6_123@localhost:27020/app_prod_s6?authSource=admin
```

### MongoDB 8 Standalone - Port 27021
```bash
# Admin access
mongodb://admin:admin123@localhost:27021/?authSource=admin

# Application access
mongodb://appuser_s8:apppass_s8_123@localhost:27021/app_prod_s8?authSource=admin

# Read-only access
mongodb://readonly_s8:readonly_s8_123@localhost:27021/app_prod_s8?authSource=admin
```

### MongoDB 8 No-Auth (Testing) - Port 27022
```bash
# Direct access (no authentication required)
mongodb://localhost:27022/mongobouncer_test
mongodb://localhost:27022/users
mongodb://localhost:27022/orders
mongodb://localhost:27022/inventory
mongodb://localhost:27022/pool_test

# Through MongoBouncer (using examples/mongobouncer.noauth.toml)
mongodb://localhost:27017/mongobouncer_test
mongodb://localhost:27017/users
mongodb://localhost:27017/orders
mongodb://localhost:27017/inventory
mongodb://localhost:27017/pool_test
```

## Quick Start

### Option 1: Start All Services (Recommended)
```bash
cd tools
docker-compose -f docker-compose.yaml up -d
```

This will start all MongoDB instances with automatic initialization. The initialization services will:
- Initialize replica sets (rs0 for MongoDB 6, rs1 for MongoDB 8)
- Create application users with appropriate permissions
- Create test databases and collections
- Insert sample data

### Option 2: Start Only No-Auth MongoDB (for Testing)
```bash
cd tools
docker-compose -f docker-compose.yaml up mongo8-noauth -d
```

### Option 3: Start Specific Services
```bash
# Start MongoDB 8 replica set with initialization
docker-compose -f docker-compose.yaml up mongo8-rs mongo8-rs-init -d

# Start MongoDB 6 standalone with initialization
docker-compose -f docker-compose.yaml up mongo6-standalone mongo6-standalone-init -d

# Start monitoring services only
docker-compose -f docker-compose.yaml up prometheus grafana -d
```

## Automatic Initialization

All MongoDB instances are automatically initialized when you start the services. The initialization scripts handle:

### Replica Set Initialization
- **MongoDB 6**: Initializes replica set `rs0` with single node
- **MongoDB 8**: Initializes replica set `rs1` with single node

### User Creation
- **Admin users**: `admin` / `admin123` (root access)
- **Application users**: `appuser_rs6`, `appuser_rs8`, `appuser_s6`, `appuser_s8`
- **Read-only users**: `readonly_rs6`, `readonly_rs8`, `readonly_s6`, `readonly_s8`
- **Analytics users**: `analytics_rs6`, `analytics_rs8`

### Database and Collection Creation
- **Production databases**: `app_prod_rs6`, `app_prod_rs8`, `app_prod_s6`, `app_prod_s8`
- **Test databases**: `app_test_rs6`, `app_test_rs8`, `app_test_s6`, `app_test_s8`
- **Analytics databases**: `analytics_rs6`, `analytics_rs8`
- **Sample data**: Users, products, orders, events, metrics, reports

## Verification

### Check Service Status
```bash
# Check if all services are running
docker-compose -f docker-compose.yaml ps

# Check initialization logs
docker-compose -f docker-compose.yaml logs mongo6-rs-init
docker-compose -f docker-compose.yaml logs mongo8-rs-init
docker-compose -f docker-compose.yaml logs mongo6-standalone-init
docker-compose -f docker-compose.yaml logs mongo8-standalone-init
```

### Test MongoDB Connectivity
```bash
# Test MongoDB 6 replica set
mongosh "mongodb://admin:admin123@localhost:27018/?replicaSet=rs0&authSource=admin" --eval "db.adminCommand('ping')"

# Test MongoDB 8 replica set
mongosh "mongodb://admin:admin123@localhost:27019/?replicaSet=rs1&authSource=admin" --eval "db.adminCommand('ping')"

# Test MongoDB 6 standalone
mongosh "mongodb://admin:admin123@localhost:27020/?authSource=admin" --eval "db.adminCommand('ping')"

# Test MongoDB 8 standalone
mongosh "mongodb://admin:admin123@localhost:27021/?authSource=admin" --eval "db.adminCommand('ping')"

# Test MongoDB 8 no-auth
mongosh "mongodb://localhost:27022/admin" --eval "db.adminCommand('ping')"
```

### Connect to MongoDB Instances
```bash
# Connect to MongoDB 6 replica set
docker exec -it tools-mongo6-rs-1 mongosh -u admin -p admin123

# Connect to MongoDB 8 replica set
docker exec -it tools-mongo8-rs-1 mongosh -u admin -p admin123

# Connect to MongoDB 6 standalone
docker exec -it tools-mongo6-standalone-1 mongosh -u admin -p admin123

# Connect to MongoDB 8 standalone
docker exec -it tools-mongo8-standalone-1 mongosh -u admin -p admin123

# Connect to MongoDB 8 no-auth (no credentials needed)
docker exec -it tools-mongo8-noauth-1 mongosh
```

## MongoBouncer Configuration

### For Testing (No Authentication)
Use the no-auth configuration for testing MongoBouncer without authentication complexity:

```bash
# Build MongoBouncer
go build -o bin/mongobouncer .

# Run with no-auth configuration
./bin/mongobouncer -config examples/mongobouncer.noauth.toml
```

### For Production (With Authentication)
Use the provided configuration files for authenticated setups:

```bash
# Run with replica set configuration
./bin/mongobouncer -config examples/mongobouncer.example.toml

# Run with Docker Compose configuration
./bin/mongobouncer -config examples/mongobouncer.docker-compose.toml
```

### Testing MongoBouncer
```bash
# Test with no-auth setup
mongosh "mongodb://localhost:27017/mongobouncer_test"

# Test multi-database functionality
mongosh "mongodb://localhost:27017/users"
mongosh "mongodb://localhost:27017/orders"
mongosh "mongodb://localhost:27017/inventory"

# Run comprehensive tests
go run test/comprehensive/main.go test/comprehensive/report.go --generate-html -connection mongodb://localhost:27017 -database mongobouncer_test

# Run multi-database tests
go run test/multidb/main.go

# Run pool tests
go run test/pool/main.go
```

## Test Data

The initialization scripts automatically create comprehensive test data in the following databases:

### MongoDB 6 Replica Set (rs0) - Port 27018
**Databases**: `app_prod_rs6`, `app_test_rs6`, `analytics_rs6`
- **users** collection with sample user data
- **products** collection with sample product data
- **orders** collection (empty, for testing)
- **events** collection with analytics data
- **metrics** collection with performance data
- **reports** collection with sample reports

### MongoDB 8 Replica Set (rs1) - Port 27019
**Databases**: `app_prod_rs8`, `app_test_rs8`, `analytics_rs8`
- **users** collection with sample user data (version 8.0)
- **products** collection with sample product data (version 8.0)
- **orders** collection (empty, for testing)
- **sessions** collection (empty, for testing)
- **events** collection with analytics data
- **metrics** collection with performance data
- **reports** collection with sample reports
- **dashboards** collection (empty, for testing)

### MongoDB 6 Standalone - Port 27020
**Databases**: `app_prod_s6`, `app_test_s6`
- **documents** collection with sample document data (version 6.0)
- **metadata** collection with configuration data (version 6.0)
- **logs** collection (empty, for testing)
- **sessions** collection (empty, for testing)
- **test_data** collection with test items
- **test_users** collection with test user data

### MongoDB 8 Standalone - Port 27021
**Databases**: `app_prod_s8`, `app_test_s8`
- **documents** collection with sample document data
- **metadata** collection with configuration data
- **logs** collection (empty, for testing)
- **test_data** collection with test items
- **test_users** collection with test user data

### MongoDB 8 No-Auth (Testing) - Port 27022
**Databases**: Any database name (no authentication required)
- Perfect for testing MongoBouncer functionality
- No pre-created data - clean slate for testing

## Monitoring

- **Prometheus**: http://localhost:8010
- **Grafana**: http://localhost:8011 (admin/admin)

## Troubleshooting

### Services Not Starting
If services fail to start:
```bash
# Check service logs
docker-compose -f docker-compose.yaml logs mongo6-rs
docker-compose -f docker-compose.yaml logs mongo8-rs
docker-compose -f docker-compose.yaml logs mongo6-standalone
docker-compose -f docker-compose.yaml logs mongo8-standalone
docker-compose -f docker-compose.yaml logs mongo8-noauth

# Check initialization logs
docker-compose -f docker-compose.yaml logs mongo6-rs-init
docker-compose -f docker-compose.yaml logs mongo8-rs-init
docker-compose -f docker-compose.yaml logs mongo6-standalone-init
docker-compose -f docker-compose.yaml logs mongo8-standalone-init
```

### Initialization Issues
If initialization fails:
```bash
# Restart initialization services
docker-compose -f docker-compose.yaml restart mongo6-rs-init
docker-compose -f docker-compose.yaml restart mongo8-rs-init
docker-compose -f docker-compose.yaml restart mongo6-standalone-init
docker-compose -f docker-compose.yaml restart mongo8-standalone-init

# Check if MongoDB instances are healthy
docker-compose -f docker-compose.yaml ps
```

### Authentication Issues
If authentication fails:
```bash
# Check if users exist in MongoDB 6 replica set
docker exec -it tools-mongo6-rs-1 mongosh -u admin -p admin123
use admin
db.getUsers()

# Check if users exist in MongoDB 8 replica set
docker exec -it tools-mongo8-rs-1 mongosh -u admin -p admin123
use admin
db.getUsers()

# Check if users exist in MongoDB 6 standalone
docker exec -it tools-mongo6-standalone-1 mongosh -u admin -p admin123
use admin
db.getUsers()

# Check if users exist in MongoDB 8 standalone
docker exec -it tools-mongo8-standalone-1 mongosh -u admin -p admin123
use admin
db.getUsers()
```

### Clean Restart
To start fresh:
```bash
# Stop all services and remove volumes
docker-compose -f docker-compose.yaml down -v

# Start all services again
docker-compose -f docker-compose.yaml up -d
```

## Port Mapping

| Service | Host Port | Container Port | Purpose |
|---------|-----------|----------------|---------|
| mongo6-rs | 27018 | 27017 | MongoDB 6 replica set (with auth) |
| mongo8-rs | 27019 | 27017 | MongoDB 8 replica set (with auth) |
| mongo6-standalone | 27020 | 27017 | MongoDB 6 standalone (with auth) |
| mongo8-standalone | 27021 | 27017 | MongoDB 8 standalone (with auth) |
| mongo8-noauth | 27022 | 27017 | MongoDB 8 standalone (no auth) |
| prometheus | 8010 | 9090 | Metrics collection |
| grafana | 8011 | 3000 | Metrics visualization |

## File Structure

```
tools/
├── docker-compose.yaml              # Main Docker Compose configuration
├── README.md                        # This documentation
└── config/
    ├── mongodb/
    │   ├── init-replica-set-6.sh     # MongoDB 6 replica set initialization
    │   ├── init-replica-set-8.sh     # MongoDB 8 replica set initialization
    │   ├── init-standalone-6.sh      # MongoDB 6 standalone initialization
    │   ├── init-standalone-8.sh      # MongoDB 8 standalone initialization
    │   └── mongodb-keyfile          # Keyfile for replica set authentication
    ├── prometheus/
    │   └── config.yaml              # Prometheus configuration
    └── grafana/
        ├── grafana.conf             # Grafana environment
        └── provisioning/            # Grafana dashboards and datasources
```

## MongoBouncer Configuration Files

```
examples/
├── mongobouncer.example.toml        # Production example configuration
├── mongobouncer.docker-compose.toml # Docker Compose configuration
├── mongobouncer.test.toml           # Test configuration
└── mongobouncer.noauth.toml         # No-auth testing configuration
```

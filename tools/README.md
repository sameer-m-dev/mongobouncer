# MongoDB Setup with Replica Set and Authentication

This directory contains the Docker Compose configuration for MongoDB with replica set support and authentication.

## Services Overview

### MongoDB 6 Replica Set (rs0)
- **mongo6-rs-1** (Primary): Port 27018 - Primary replica set member
- **mongo6-rs-2** (Secondary): Port 27019 - Secondary replica set member  
- **mongo6-rs-3** (Secondary): Port 27020 - Secondary replica set member

### MongoDB 8 Replica Set (rs1)
- **mongo8-rs-1** (Primary): Port 27021 - Primary replica set member
- **mongo8-rs-2** (Secondary): Port 27022 - Secondary replica set member
- **mongo8-rs-3** (Secondary): Port 27023 - Secondary replica set member

### MongoDB 6 Standalone
- **mongo6-standalone**: Port 27024 - Standalone MongoDB 6 instance with authentication

### MongoDB 8 Standalone
- **mongo8-standalone**: Port 27025 - Standalone MongoDB 8 instance with authentication

### Monitoring Services
- **Prometheus**: Port 8010 - Metrics collection
- **Grafana**: Port 8011 - Metrics visualization

## Authentication

All MongoDB instances are configured with authentication enabled:

### Admin Users
- **Username**: `admin`
- **Password**: `admin123`
- **Role**: Root access to all databases

### Application Users
- **Username**: `appuser`
- **Password**: `apppass123`
- **Role**: Read/Write access to application databases

### Read-Only Users
- **Username**: `readonly`
- **Password**: `readonly123`
- **Role**: Read-only access to application databases

## Connection Strings

### MongoDB 6 Replica Set (rs0)
```bash
# Admin access
mongodb://admin:admin123@localhost:27018/?replicaSet=rs0&authSource=admin

# Application access
mongodb://appuser:apppass123@localhost:27018/app_prod?replicaSet=rs0&authSource=admin

# Read-only access
mongodb://readonly:readonly123@localhost:27018/app_prod?replicaSet=rs0&authSource=admin
```

### MongoDB 8 Replica Set (rs1)
```bash
# Admin access
mongodb://admin:admin123@localhost:27021/?replicaSet=rs1&authSource=admin

# Application access
mongodb://appuser:apppass123@localhost:27021/app_v8_prod?replicaSet=rs1&authSource=admin

# Read-only access
mongodb://readonly:readonly123@localhost:27021/app_v8_prod?replicaSet=rs1&authSource=admin
```

### MongoDB 6 Standalone
```bash
# Admin access
mongodb://admin:admin123@localhost:27024/?authSource=admin

# Application access
mongodb://appuser:apppass123@localhost:27024/standalone_v6_db?authSource=admin

# Read-only access
mongodb://readonly:readonly123@localhost:27024/standalone_v6_db?authSource=admin
```

### MongoDB 8 Standalone
```bash
# Admin access
mongodb://admin:admin123@localhost:27025/?authSource=admin

# Application access
mongodb://appuser:apppass123@localhost:27025/standalone_db?authSource=admin

# Read-only access
mongodb://readonly:readonly123@localhost:27025/standalone_db?authSource=admin
```

## Quick Start

1. **Start all services**:
   ```bash
   cd tools
   docker-compose -f docker-compose.yaml up -d
   ```

2. **Initialize replica sets** (required for first run):
   ```bash
   # Initialize MongoDB 6 replica set
   docker exec -it tools-mongo6-rs-1-1 mongosh -u admin -p admin123 --eval "rs.initiate({_id: 'rs0', members: [{_id: 0, host: 'mongo6-rs-1:27017'}, {_id: 1, host: 'mongo6-rs-2:27017'}, {_id: 2, host: 'mongo6-rs-3:27017'}]})"
   
   # Initialize MongoDB 8 replica set
   docker exec -it tools-mongo8-rs-1-1 mongosh -u admin -p admin123 --eval "rs.initiate({_id: 'rs1', members: [{_id: 0, host: 'mongo8-rs-1:27017'}, {_id: 1, host: 'mongo8-rs-2:27017'}, {_id: 2, host: 'mongo8-rs-3:27017'}]})"
   ```

3. **Create application users**:
   ```bash
   # For MongoDB 6 replica set
   docker exec -it tools-mongo6-rs-1-1 mongosh -u admin -p admin123 --eval "db.createUser({user: 'appuser', pwd: 'apppass123', roles: [{role: 'readWrite', db: 'app_prod'}, {role: 'readWrite', db: 'app_test'}, {role: 'read', db: 'admin'}]})"
   
   # For MongoDB 8 replica set
   docker exec -it tools-mongo8-rs-1-1 mongosh -u admin -p admin123 --eval "db.createUser({user: 'appuser', pwd: 'apppass123', roles: [{role: 'readWrite', db: 'app_v8_prod'}, {role: 'readWrite', db: 'app_v8_test'}, {role: 'read', db: 'admin'}]})"
   
   # For MongoDB 6 standalone
   docker exec -it tools-mongo6-standalone-1 mongosh -u admin -p admin123 --eval "db.createUser({user: 'appuser', pwd: 'apppass123', roles: [{role: 'readWrite', db: 'standalone_v6_db'}, {role: 'readWrite', db: 'test_v6_db'}, {role: 'read', db: 'admin'}]})"
   
   # For MongoDB 8 standalone
   docker exec -it tools-mongo8-standalone-1 mongosh -u admin -p admin123 --eval "db.createUser({user: 'appuser', pwd: 'apppass123', roles: [{role: 'readWrite', db: 'standalone_db'}, {role: 'readWrite', db: 'test_db'}, {role: 'read', db: 'admin'}]})"
   ```

4. **Verify the setup**:
   ```bash
   ./verify-mongodb-setup.sh
   ```

5. **Connect to MongoDB**:
   ```bash
   # Connect to MongoDB 6 replica set
   docker exec -it tools-mongo6-rs-1-1 mongosh -u admin -p admin123
   
   # Connect to MongoDB 8 replica set
   docker exec -it tools-mongo8-rs-1-1 mongosh -u admin -p admin123
   
   # Connect to MongoDB 6 standalone
   docker exec -it tools-mongo6-standalone-1 mongosh -u admin -p admin123
   
   # Connect to MongoDB 8 standalone
   docker exec -it tools-mongo8-standalone-1 mongosh -u admin -p admin123
   ```

## MongoBouncer Configuration

Use the provided configuration file `config/mongobouncer-replica-set.toml` to test MongoBouncer with the new setup:

```bash
# Build MongoBouncer
go build -o bin/mongobouncer .

# Run with replica set configuration
./bin/mongobouncer -config tools/config/mongobouncer-replica-set.toml
```

## Test Data

The initialization scripts create test data in the following databases:

### MongoDB 6 Replica Set (app_prod, app_test)
- **users** collection with sample user data
- **products** collection with sample product data
- **orders** collection (empty, for testing)

### MongoDB 8 Replica Set (app_v8_prod, app_v8_test, analytics_v8)
- **users** collection with sample user data (version 8.0)
- **products** collection with sample product data (version 8.0)
- **orders** collection (empty, for testing)
- **sessions** collection (empty, for testing)
- **events** collection with analytics data
- **metrics** collection (empty, for testing)
- **reports** collection (empty, for testing)

### MongoDB 6 Standalone (standalone_v6_db, test_v6_db)
- **documents** collection with sample document data (version 6.0)
- **metadata** collection with configuration data (version 6.0)
- **logs** collection (empty, for testing)
- **sessions** collection (empty, for testing)
- **test_data** collection with test items
- **test_users** collection with test user data

### MongoDB 8 Standalone (standalone_db, test_db)
- **documents** collection with sample document data
- **metadata** collection with configuration data
- **logs** collection (empty, for testing)

## Monitoring

- **Prometheus**: http://localhost:8010
- **Grafana**: http://localhost:8011 (admin/admin)

## Troubleshooting

### Replica Sets Not Starting
If the replica sets fail to initialize:
```bash
# Check logs
docker-compose logs mongo6-rs-1
docker-compose logs mongo8-rs-1

# Manually initialize MongoDB 6 replica set
docker exec -it tools-mongo6-rs-1-1 mongosh -u admin -p admin123
rs.initiate({
  _id: "rs0",
  members: [
    { _id: 0, host: "mongo6-rs-1:27017" },
    { _id: 1, host: "mongo6-rs-2:27017" },
    { _id: 2, host: "mongo6-rs-3:27017" }
  ]
})

# Manually initialize MongoDB 8 replica set
docker exec -it tools-mongo8-rs-1-1 mongosh -u admin -p admin123
rs.initiate({
  _id: "rs1",
  members: [
    { _id: 0, host: "mongo8-rs-1:27017" },
    { _id: 1, host: "mongo8-rs-2:27017" },
    { _id: 2, host: "mongo8-rs-3:27017" }
  ]
})
```

### Authentication Issues
If authentication fails:
```bash
# Check if users exist in MongoDB 6 replica set
docker exec -it tools-mongo6-rs-1-1 mongosh -u admin -p admin123
use admin
db.getUsers()

# Check if users exist in MongoDB 8 replica set
docker exec -it tools-mongo8-rs-1-1 mongosh -u admin -p admin123
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
docker-compose down -v
docker-compose up -d
```

## File Structure

```
tools/
├── docker-compose.yaml              # Main Docker Compose configuration
├── verify-mongodb-setup.sh          # Comprehensive verification script
└── config/
    ├── mongodb/
    │   ├── init-replica-set-6.js      # MongoDB 6 replica set initialization
    │   ├── init-replica-set-8.js    # MongoDB 8 replica set initialization
    │   ├── init-standalone-8.js       # MongoDB 8 standalone initialization
    │   ├── init-standalone-6.js     # MongoDB 6 standalone initialization
    │   └── mongodb-keyfile          # Keyfile for replica set authentication
    ├── mongobouncer-replica-set.toml # MongoBouncer config example
    ├── prometheus/
    │   └── config.yaml              # Prometheus configuration
    └── grafana/
        ├── grafana.conf             # Grafana environment
        └── provisioning/            # Grafana dashboards and datasources
```

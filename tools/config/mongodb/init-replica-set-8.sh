#!/bin/bash

# MongoDB 8 Single-Node Replica Set Initialization Script
# This script initializes the replica set and creates necessary users

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# MongoDB connection details
MONGO_HOST="host.docker.internal"
MONGO_PORT="27019"
MONGO_USER="admin"
MONGO_PASS="admin123"
MONGO_AUTH_DB="admin"

print_status "Starting MongoDB 8 replica set initialization..."

# Wait for MongoDB to be ready
print_status "Waiting for MongoDB to be ready..."
max_attempts=30
attempt=0
while [ $attempt -lt $max_attempts ]; do
    if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "db.adminCommand('ping')" --quiet > /dev/null 2>&1; then
        print_success "MongoDB is ready"
        break
    fi
    print_status "Waiting for MongoDB... attempt $((attempt + 1))"
    sleep 2
    attempt=$((attempt + 1))
done

if [ $attempt -eq $max_attempts ]; then
    print_error "MongoDB failed to become ready within $((max_attempts * 2)) seconds"
    exit 1
fi

# Initialize replica set
print_status "Initializing replica set rs1..."
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
rs.initiate({
    _id: 'rs1',
    members: [
        {
            _id: 0,
            host: '${MONGO_HOST}:${MONGO_PORT}',
            priority: 1
        }
    ]
})
" --quiet; then
    print_success "Replica set initialization initiated"
else
    print_warning "Replica set might already be initialized"
fi

# Wait for replica set to be ready
print_status "Waiting for replica set to be ready..."
max_attempts=30
attempt=0
while [ $attempt -lt $max_attempts ]; do
    if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "rs.status().ok" --quiet 2>/dev/null | grep -q "1"; then
        print_success "Replica set is ready"
        break
    fi
    print_status "Waiting for replica set... attempt $((attempt + 1))"
    sleep 2
    attempt=$((attempt + 1))
done

if [ $attempt -eq $max_attempts ]; then
    print_error "Replica set failed to become ready within $((max_attempts * 2)) seconds"
    exit 1
fi

# Create application users
print_status "Creating application users..."

# Create appuser
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('admin');
db.createUser({
    user: 'appuser_rs8',
    pwd: 'apppass_rs8_123',
    roles: [
        { role: 'readWrite', db: 'app_prod_rs8' },
        { role: 'readWrite', db: 'app_test_rs8' },
        { role: 'readWrite', db: 'analytics_rs8' },
        { role: 'read', db: 'admin' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Application user created successfully"
else
    print_warning "Application user might already exist"
fi

# Create readonly user
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('admin');
db.createUser({
    user: 'readonly_rs8',
    pwd: 'readonly_rs8_123',
    roles: [
        { role: 'read', db: 'app_prod_rs8' },
        { role: 'read', db: 'app_test_rs8' },
        { role: 'read', db: 'analytics_rs8' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Read-only user created successfully"
else
    print_warning "Read-only user might already exist"
fi

# Create analytics database and user
print_status "Creating analytics database and user..."

# Create analytics_rs8 database first
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_rs8');
db.createCollection('events');
db.createCollection('metrics');
db.createCollection('reports');
db.createCollection('dashboards');

// Insert some analytics data
db.events.insertMany([
    { _id: 1, event_type: 'user_login', timestamp: new Date(), user_id: 1, session_id: 'sess_001', source: 'rs8' },
    { _id: 2, event_type: 'product_view', timestamp: new Date(), user_id: 2, product_id: 1, category: 'electronics', source: 'rs8' },
    { _id: 3, event_type: 'purchase', timestamp: new Date(), user_id: 3, product_id: 2, amount: 199.99, currency: 'USD', source: 'rs8' },
    { _id: 4, event_type: 'page_view', timestamp: new Date(), user_id: 4, page: '/dashboard', duration: 45, source: 'rs8' }
]);

db.metrics.insertMany([
    { _id: 1, metric_name: 'daily_active_users', value: 1500, date: new Date(), source: 'rs8' },
    { _id: 2, metric_name: 'conversion_rate', value: 3.2, date: new Date(), source: 'rs8' },
    { _id: 3, metric_name: 'revenue', value: 12500.75, date: new Date(), source: 'rs8' },
    { _id: 4, metric_name: 'page_load_time', value: 1.2, date: new Date(), source: 'rs8' }
]);

db.reports.insertMany([
    { _id: 1, report_name: 'User Activity', generated_at: new Date(), data_points: 1500, source: 'rs8' },
    { _id: 2, report_name: 'Sales Summary', generated_at: new Date(), data_points: 250, source: 'rs8' }
]);

print('Analytics database and collections created');
" --quiet

# Create analytics user in analytics_rs8 database
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_rs8');
db.createUser({
    user: 'analytics_rs8',
    pwd: 'analytics_rs8_123',
    roles: [
        { role: 'readWrite', db: 'analytics_rs8' },
        { role: 'read', db: 'app_prod_rs8' },
        { role: 'read', db: 'app_test_rs8' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Analytics user created successfully"
else
    print_warning "Analytics user might already exist"
fi

# Create test databases and collections
print_status "Creating test databases and collections..."

# Create app_v8_prod database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('app_prod_rs8');

// Create collections
db.createCollection('users');
db.createCollection('products');
db.createCollection('orders');
db.createCollection('sessions');

// Insert test data
db.users.insertMany([
    { _id: 1, name: 'Alice Johnson', email: 'alice@example.com', role: 'admin', source: 'rs8' },
    { _id: 2, name: 'Charlie Brown', email: 'charlie@example.com', role: 'user', source: 'rs8' },
    { _id: 3, name: 'Diana Prince', email: 'diana@example.com', role: 'user', source: 'rs8' },
    { _id: 4, name: 'Eve Wilson', email: 'eve@example.com', role: 'moderator', source: 'rs8' }
]);

db.products.insertMany([
    { _id: 1, name: 'MongoDB 8 Laptop', price: 1299.99, category: 'Electronics', source: 'rs8' },
    { _id: 2, name: 'Advanced Mouse', price: 49.99, category: 'Electronics', source: 'rs8' },
    { _id: 3, name: 'Mechanical Keyboard', price: 129.99, category: 'Electronics', source: 'rs8' },
    { _id: 4, name: '4K Monitor', price: 399.99, category: 'Electronics', source: 'rs8' }
]);

print('Test data inserted into MongoDB 8 app_prod_rs8 database');
" --quiet

# Create app_v8_test database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('app_test_rs8');

db.createCollection('test_users');
db.createCollection('test_products');
db.test_users.insertMany([
    { _id: 1, name: 'Test User 1', email: 'test1@example.com', source: 'rs8' },
    { _id: 2, name: 'Test User 2', email: 'test2@example.com', source: 'rs8' },
    { _id: 3, name: 'Test User 3', email: 'test3@example.com', source: 'rs8' }
]);

db.test_products.insertMany([
    { _id: 1, name: 'Test Product 1', price: 99.99, source: 'rs8' },
    { _id: 2, name: 'Test Product 2', price: 199.99, source: 'rs8' }
]);

print('Test data inserted into MongoDB 8 app_test_rs8 database');
" --quiet

# Create analytics_v8 database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs1&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_v8');

db.createCollection('events');
db.createCollection('metrics');
db.createCollection('reports');

db.events.insertMany([
    { _id: 1, event_type: 'user_login', timestamp: new Date(), user_id: 1, version: '8.0' },
    { _id: 2, event_type: 'product_view', timestamp: new Date(), user_id: 2, product_id: 1, version: '8.0' },
    { _id: 3, event_type: 'purchase', timestamp: new Date(), user_id: 3, product_id: 2, amount: 199.99, version: '8.0' }
]);

print('Test data inserted into MongoDB 8 analytics_v8 database');
" --quiet

print_success "MongoDB 8 replica set initialization completed successfully!"
print_status "Connection string: mongodb://appuser_rs8:apppass_rs8_123@${MONGO_HOST}:${MONGO_PORT}/app_prod_rs8?replicaSet=rs1&authSource=admin"

#!/bin/bash

# MongoDB 6 Single-Node Replica Set Initialization Script
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
MONGO_PORT="27018"
MONGO_USER="admin"
MONGO_PASS="admin123"
MONGO_AUTH_DB="admin"

print_status "Starting MongoDB 6 replica set initialization..."

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
print_status "Initializing replica set rs0..."
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
rs.initiate({
    _id: 'rs0',
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
    if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs0&authSource=${MONGO_AUTH_DB}" --eval "rs.status().ok" --quiet 2>/dev/null | grep -q "1"; then
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
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs0&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('admin');
db.createUser({
    user: 'appuser_rs6',
    pwd: 'apppass_rs6_123',
    roles: [
        { role: 'readWrite', db: 'app_prod_rs6' },
        { role: 'readWrite', db: 'app_test_rs6' },
        { role: 'readWrite', db: 'analytics_rs6' },
        { role: 'read', db: 'admin' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Application user created successfully"
else
    print_warning "Application user might already exist"
fi

# Create readonly user
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs0&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('admin');
db.createUser({
    user: 'readonly_rs6',
    pwd: 'readonly_rs6_123',
    roles: [
        { role: 'read', db: 'app_prod_rs6' },
        { role: 'read', db: 'app_test_rs6' },
        { role: 'read', db: 'analytics_rs6' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Read-only user created successfully"
else
    print_warning "Read-only user might already exist"
fi

# Create analytics database and user
print_status "Creating analytics database and user..."

# Create analytics database first
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs0&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_rs6');
db.createCollection('events');
db.createCollection('metrics');
db.createCollection('reports');

// Insert some analytics data
db.events.insertMany([
    { _id: 1, event_type: 'page_view', timestamp: new Date(), user_id: 1, page: '/home', source: 'rs6' },
    { _id: 2, event_type: 'click', timestamp: new Date(), user_id: 2, element: 'button', source: 'rs6' },
    { _id: 3, event_type: 'conversion', timestamp: new Date(), user_id: 3, value: 99.99, source: 'rs6' }
]);

db.metrics.insertMany([
    { _id: 1, metric_name: 'page_views', value: 1000, timestamp: new Date(), source: 'rs6' },
    { _id: 2, metric_name: 'conversions', value: 50, timestamp: new Date(), source: 'rs6' },
    { _id: 3, metric_name: 'revenue', value: 4999.50, timestamp: new Date(), source: 'rs6' }
]);

print('Analytics database and collections created');
" --quiet

# Create analytics user in analytics database
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs0&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_rs6');
db.createUser({
    user: 'analytics_rs6',
    pwd: 'analytics_rs6_123',
    roles: [
        { role: 'readWrite', db: 'analytics_rs6' },
        { role: 'read', db: 'app_prod_rs6' },
        { role: 'read', db: 'app_test_rs6' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Analytics user created successfully"
else
    print_warning "Analytics user might already exist"
fi

# Create test databases and collections
print_status "Creating test databases and collections..."

# Create app_prod database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs0&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('app_prod_rs6');

// Create collections
db.createCollection('users');
db.createCollection('products');
db.createCollection('orders');

// Insert test data
db.users.insertMany([
    { _id: 1, name: 'John Doe', email: 'john@example.com', role: 'admin', source: 'rs6' },
    { _id: 2, name: 'Jane Smith', email: 'jane@example.com', role: 'user', source: 'rs6' },
    { _id: 3, name: 'Bob Johnson', email: 'bob@example.com', role: 'user', source: 'rs6' }
]);

db.products.insertMany([
    { _id: 1, name: 'Laptop', price: 999.99, category: 'Electronics', source: 'rs6' },
    { _id: 2, name: 'Mouse', price: 29.99, category: 'Electronics', source: 'rs6' },
    { _id: 3, name: 'Keyboard', price: 79.99, category: 'Electronics', source: 'rs6' }
]);

print('Test data inserted into MongoDB 6 app_prod_rs6 database');
" --quiet

# Create app_test database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?replicaSet=rs0&authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('app_test_rs6');

db.createCollection('test_users');
db.test_users.insertMany([
    { _id: 1, name: 'Test User 1', email: 'test1@example.com', source: 'rs6' },
    { _id: 2, name: 'Test User 2', email: 'test2@example.com', source: 'rs6' }
]);

print('Test data inserted into MongoDB 6 app_test_rs6 database');
" --quiet

print_success "MongoDB 6 replica set initialization completed successfully!"
print_status "Connection string: mongodb://appuser_rs6:apppass_rs6_123@${MONGO_HOST}:${MONGO_PORT}/app_prod_rs6?replicaSet=rs0&authSource=admin"

#!/bin/bash

# MongoDB 6 Standalone Initialization Script
# This script creates necessary users and test data for the standalone MongoDB 6 instance

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
MONGO_PORT="27020"
MONGO_USER="admin"
MONGO_PASS="admin123"
MONGO_AUTH_DB="admin"

print_status "Starting MongoDB 6 standalone initialization..."

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

# Create application users
print_status "Creating application users..."

# Create appuser in admin database
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('admin');
db.createUser({
    user: 'appuser_s6',
    pwd: 'apppass_s6_123',
    roles: [
        { role: 'readWrite', db: 'standalone_v6_db' },
        { role: 'readWrite', db: 'test_v6_db' },
        { role: 'read', db: 'admin' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Application user created successfully"
else
    print_warning "Application user might already exist"
fi

# Create readonly user in admin database
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('admin');
db.createUser({
    user: 'readonly_s6',
    pwd: 'readonly_s6_123',
    roles: [
        { role: 'read', db: 'standalone_v6_db' },
        { role: 'read', db: 'test_v6_db' }
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
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_s6');
db.createCollection('events');
db.createCollection('metrics');
db.createCollection('reports');

// Insert some analytics data
db.events.insertMany([
    { _id: 1, event_type: 'page_view', timestamp: new Date(), user_id: 1, page: '/home' },
    { _id: 2, event_type: 'click', timestamp: new Date(), user_id: 2, element: 'button' },
    { _id: 3, event_type: 'conversion', timestamp: new Date(), user_id: 3, value: 99.99 }
]);

db.metrics.insertMany([
    { _id: 1, metric_name: 'page_views', value: 1000, timestamp: new Date() },
    { _id: 2, metric_name: 'conversions', value: 50, timestamp: new Date() },
    { _id: 3, metric_name: 'revenue', value: 4999.50, timestamp: new Date() }
]);

print('Analytics database and collections created');
" --quiet

# Create analytics user in analytics database
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_s6');
db.createUser({
    user: 'analytics_s6',
    pwd: 'analytics_s6_123',
    roles: [
        { role: 'readWrite', db: 'analytics_s6' },
        { role: 'read', db: 'standalone_v6_db' },
        { role: 'read', db: 'test_v6_db' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Analytics user created successfully"
else
    print_warning "Analytics user might already exist"
fi

# Create test databases and collections
print_status "Creating test databases and collections..."

# Create standalone_v6_db database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('standalone_v6_db');

// Create collections
db.createCollection('documents');
db.createCollection('metadata');
db.createCollection('logs');
db.createCollection('sessions');

// Insert test data
db.documents.insertMany([
    { _id: 1, title: 'MongoDB 6 Document 1', content: 'This is the first document in MongoDB 6', created_at: new Date(), version: '6.0' },
    { _id: 2, title: 'MongoDB 6 Document 2', content: 'This is the second document in MongoDB 6', created_at: new Date(), version: '6.0' },
    { _id: 3, title: 'MongoDB 6 Document 3', content: 'This is the third document in MongoDB 6', created_at: new Date(), version: '6.0' },
    { _id: 4, title: 'MongoDB 6 Document 4', content: 'This is the fourth document in MongoDB 6', created_at: new Date(), version: '6.0' }
]);

db.metadata.insertMany([
    { _id: 1, type: 'config', version: '6.0', last_updated: new Date(), mongodb_version: '6.0' },
    { _id: 2, type: 'settings', version: '6.1', last_updated: new Date(), mongodb_version: '6.0' },
    { _id: 3, type: 'features', version: '6.2', last_updated: new Date(), mongodb_version: '6.0' }
]);

print('Test data inserted into MongoDB 6 standalone_v6_db database');
" --quiet

# Create test_v6_db database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('test_v6_db');

db.createCollection('test_data');
db.createCollection('test_users');
db.test_data.insertMany([
    { _id: 1, name: 'Test Item 1', value: 100, version: '6.0' },
    { _id: 2, name: 'Test Item 2', value: 200, version: '6.0' },
    { _id: 3, name: 'Test Item 3', value: 300, version: '6.0' },
    { _id: 4, name: 'Test Item 4', value: 400, version: '6.0' }
]);

db.test_users.insertMany([
    { _id: 1, name: 'Test User 1', email: 'test1@example.com', version: '6.0' },
    { _id: 2, name: 'Test User 2', email: 'test2@example.com', version: '6.0' },
    { _id: 3, name: 'Test User 3', email: 'test3@example.com', version: '6.0' }
]);

print('Test data inserted into MongoDB 6 test_v6_db database');
" --quiet

print_success "MongoDB 6 standalone initialization completed successfully!"
print_status "Connection strings:"
print_status "  Admin: mongodb://admin:admin123@${MONGO_HOST}:${MONGO_PORT}/?authSource=admin"
print_status "  App: mongodb://appuser:apppass123@${MONGO_HOST}:${MONGO_PORT}/?authSource=admin"
print_status "  Analytics: mongodb://analytics:analytics123@${MONGO_HOST}:${MONGO_PORT}/?authSource=analytics"

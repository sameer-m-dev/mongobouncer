#!/bin/bash

# MongoDB 8 Standalone Initialization Script
# This script creates necessary users and test data for the standalone MongoDB 8 instance

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
MONGO_PORT="27021"
MONGO_USER="admin"
MONGO_PASS="admin123"
MONGO_AUTH_DB="admin"

print_status "Starting MongoDB 8 standalone initialization..."

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
    user: 'appuser_s8',
    pwd: 'apppass_s8_123',
    roles: [
        { role: 'readWrite', db: 'standalone_db' },
        { role: 'readWrite', db: 'test_db' },
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
    user: 'readonly_s8',
    pwd: 'readonly_s8_123',
    roles: [
        { role: 'read', db: 'standalone_db' },
        { role: 'read', db: 'test_db' }
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
db = db.getSiblingDB('analytics_s8');
db.createCollection('events');
db.createCollection('metrics');
db.createCollection('reports');
db.createCollection('dashboards');

// Insert some analytics data
db.events.insertMany([
    { _id: 1, event_type: 'user_login', timestamp: new Date(), user_id: 1, session_id: 'sess_001' },
    { _id: 2, event_type: 'product_view', timestamp: new Date(), user_id: 2, product_id: 1, category: 'electronics' },
    { _id: 3, event_type: 'purchase', timestamp: new Date(), user_id: 3, product_id: 2, amount: 199.99, currency: 'USD' },
    { _id: 4, event_type: 'page_view', timestamp: new Date(), user_id: 4, page: '/dashboard', duration: 45 }
]);

db.metrics.insertMany([
    { _id: 1, metric_name: 'daily_active_users', value: 1500, date: new Date(), version: '8.0' },
    { _id: 2, metric_name: 'conversion_rate', value: 3.2, date: new Date(), version: '8.0' },
    { _id: 3, metric_name: 'revenue', value: 12500.75, date: new Date(), version: '8.0' },
    { _id: 4, metric_name: 'page_load_time', value: 1.2, date: new Date(), version: '8.0' }
]);

db.reports.insertMany([
    { _id: 1, report_name: 'User Activity', generated_at: new Date(), data_points: 1500, version: '8.0' },
    { _id: 2, report_name: 'Sales Summary', generated_at: new Date(), data_points: 250, version: '8.0' }
]);

print('Analytics database and collections created');
" --quiet

# Create analytics user in analytics database
if mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('analytics_s8');
db.createUser({
    user: 'analytics_s8',
    pwd: 'analytics_s8_123',
    roles: [
        { role: 'readWrite', db: 'analytics_s8' },
        { role: 'read', db: 'standalone_db' },
        { role: 'read', db: 'test_db' }
    ]
})
" --quiet 2>/dev/null; then
    print_success "Analytics user created successfully"
else
    print_warning "Analytics user might already exist"
fi

# Create test databases and collections
print_status "Creating test databases and collections..."

# Create standalone_db database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('standalone_db');

// Create collections
db.createCollection('documents');
db.createCollection('metadata');
db.createCollection('logs');

// Insert test data
db.documents.insertMany([
    { _id: 1, title: 'Document 1', content: 'This is the first document', created_at: new Date() },
    { _id: 2, title: 'Document 2', content: 'This is the second document', created_at: new Date() },
    { _id: 3, title: 'Document 3', content: 'This is the third document', created_at: new Date() }
]);

db.metadata.insertMany([
    { _id: 1, type: 'config', version: '1.0', last_updated: new Date() },
    { _id: 2, type: 'settings', version: '1.1', last_updated: new Date() },
    { _id: 3, type: 'features', version: '1.2', last_updated: new Date() }
]);

print('Test data inserted into MongoDB 8 standalone_db database');
" --quiet

# Create test_db database
mongosh "mongodb://${MONGO_USER}:${MONGO_PASS}@${MONGO_HOST}:${MONGO_PORT}/?authSource=${MONGO_AUTH_DB}" --eval "
db = db.getSiblingDB('test_db');

db.createCollection('test_data');
db.createCollection('test_users');
db.test_data.insertMany([
    { _id: 1, name: 'Test Item 1', value: 100 },
    { _id: 2, name: 'Test Item 2', value: 200 },
    { _id: 3, name: 'Test Item 3', value: 300 }
]);

db.test_users.insertMany([
    { _id: 1, name: 'Test User 1', email: 'test1@example.com' },
    { _id: 2, name: 'Test User 2', email: 'test2@example.com' },
    { _id: 3, name: 'Test User 3', email: 'test3@example.com' }
]);

print('Test data inserted into MongoDB 8 test_db database');
" --quiet

print_success "MongoDB 8 standalone initialization completed successfully!"
print_status "Connection strings:"
print_status "  Admin: mongodb://admin:admin123@${MONGO_HOST}:${MONGO_PORT}/?authSource=admin"
print_status "  App: mongodb://appuser:apppass123@${MONGO_HOST}:${MONGO_PORT}/?authSource=admin"
print_status "  Analytics: mongodb://analytics:analytics123@${MONGO_HOST}:${MONGO_PORT}/?authSource=analytics"

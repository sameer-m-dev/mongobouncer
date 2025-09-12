# MongoDB Comprehensive Test Suite

A comprehensive Go-based test suite designed to thoroughly validate MongoBouncer functionality by running 80+ different MongoDB operations across multiple categories.

## 🎯 Purpose

This test suite validates that MongoBouncer correctly proxies and handles all types of MongoDB operations without knowing about MongoBouncer itself. It only requires a MongoDB connection string and tests the database through whatever proxy/system is in front of it.

## 🧪 Test Categories

### 1. CRUD Operations (15 tests)
- Insert operations (single & bulk)
- Find operations (single & multiple)
- Update operations (single & bulk)
- Replace operations
- Delete operations (single & bulk)
- Atomic operations (find and modify)
- Upsert operations
- Bulk write operations
- Document counting

### 2. Complex Queries (12 tests)
- Regular expression queries
- Range queries
- Array queries
- Nested document queries
- Logical operators ($and, $or, $not)
- Text search queries
- Geospatial queries
- Distinct operations
- Sort and limit operations
- Field projection
- Field existence queries
- Type queries

### 3. Aggregation Pipeline (18 tests)
- Match stage
- Group stage
- Sort stage
- Project stage
- Limit and skip stages
- Lookup stage (joins)
- Unwind stage
- Add fields stage
- Replace root stage
- Facet stage
- Bucket stage
- Sample stage
- Count stage
- Output stage
- Complex multi-stage pipelines
- MapReduce alternatives
- Time series aggregation
- Statistical operations

### 4. Index Operations (8 tests)
- Single field indexes
- Compound indexes
- Text search indexes
- Geospatial indexes
- Partial indexes
- TTL (Time To Live) indexes
- Index listing
- Index dropping

### 5. Cursor Operations (6 tests)
- Cursor iteration
- Batch size control
- Skip and limit with cursors
- Cursor timeouts
- Cursor sorting
- Multiple concurrent cursors

### 6. Transaction Tests (6 tests)
- Simple transactions
- Multi-collection transactions
- Transaction rollback
- Read concern transactions
- Write concern transactions
- Nested transaction operations

### 7. Admin Operations (7 tests)
- Collection listing
- Collection statistics
- Database statistics
- Server status
- Collection creation
- Collection dropping
- Collection renaming

### 8. Edge Cases & Error Handling (8 tests)
- Large document handling
- Empty collection queries
- Invalid ObjectID handling
- Concurrent operations
- Deep nested queries
- Special character handling
- Large result sets
- Connection stress testing

## 🚀 Usage

### As Go Test
```bash
# Run with default connection (localhost:27017)
go test -v ./test/ -run TestComprehensiveMongo

# Run with custom connection
MONGODB_CONNECTION_STRING="mongodb://localhost:27016" go test -v ./test/ -run TestComprehensiveMongo

# Skip if running short tests
go test -short ./test/
```

### As Standalone Application
```bash
# Run with default settings
go run test/*.go

# Run with custom connection
go run test/*.go -connection mongodb://localhost:27016 -database my_test_db

# Show help
go run test/*.go -help

# Use environment variables
MONGO_URL="mongodb://localhost:27016" MONGO_DB="test_db" go run test/*.go
```

### Testing MongoBouncer
```bash
# Start MongoBouncer (example)
./mongobouncer -config mongobouncer.toml &

# Run tests against MongoBouncer
go run test/*.go -connection mongodb://localhost:27016 -database bouncer_test

# Or set environment
export MONGO_URL="mongodb://localhost:27016"
export MONGO_DB="bouncer_test"
go run test/*.go
```

## 📊 Output & Reports

### Console Output
Real-time test execution with:
- Progress indicator
- Individual test results (PASS/FAIL)
- Execution time per test
- Final summary statistics

### HTML Report
Comprehensive HTML report (`comprehensive_mongo_test_report.html`) includes:
- **Executive Summary**: Pass/fail counts, success rate, total duration
- **Category Performance**: Breakdown by test category with success rates
- **Failed Tests**: Detailed error information for failed tests
- **Slowest Tests**: Performance analysis of slowest operations
- **Complete Results**: Full details of every test executed
- **Interactive Features**: Click to expand test details

## 🔧 Configuration

### Environment Variables
- `MONGODB_CONNECTION_STRING` or `MONGO_URL`: MongoDB connection string
- `MONGODB_DATABASE` or `MONGO_DB`: Database name for testing

### Command Line Flags
- `-connection`: MongoDB connection string (default: mongodb://localhost:27017)
- `-database`: Database name (default: comprehensive_test_db)
- `-help`: Show detailed help message

## 📋 Sample Test Data

The suite automatically creates and manages test data:
- **Users**: Sample user documents with various fields
- **Products**: Product catalog with categories, prices, ratings
- **Orders**: Order documents with customer and shipping information
- **Reviews**: Product reviews with ratings and comments
- **Locations**: Geospatial location data for testing
- **Events**: Time-series event data for temporal queries

All test data is automatically cleaned up after test completion.

## 🎯 Validating MongoBouncer

This test suite is specifically designed to validate MongoBouncer by:

1. **Connection Multiplexing**: Tests concurrent operations
2. **Query Routing**: Validates complex query handling
3. **Transaction Handling**: Tests transaction pinning and consistency
4. **Cursor Management**: Validates cursor-to-server mapping
5. **Error Handling**: Tests error propagation and handling
6. **Performance**: Measures operation latencies
7. **Edge Cases**: Tests boundary conditions and error scenarios

## 🏁 Expected Results

When running against a properly functioning MongoBouncer:
- **Success Rate**: Should be >95% (some tests may fail due to MongoDB version/config differences)
- **Performance**: Latencies should be reasonable (typically <100ms per operation)
- **No Connection Errors**: Should not see connection timeouts or failures
- **Proper Error Handling**: Error conditions should be properly handled and reported

## 🔍 Troubleshooting

### Common Issues
- **Connection Refused**: Ensure MongoDB/MongoBouncer is running on specified port
- **Authentication Errors**: Check connection string credentials
- **Transaction Failures**: Requires MongoDB replica set for full transaction support
- **Index Failures**: Some operations require specific MongoDB versions

### Debug Mode
Add verbose logging by modifying the test files or using MongoDB's built-in profiling.

## 📈 Performance Metrics

The test suite tracks:
- Individual operation execution time
- Total test suite duration
- Average execution time per category
- Slowest operations identification
- Connection stress test results

## 🔄 Continuous Integration

Perfect for CI/CD pipelines:
```yaml
# Example GitHub Actions
- name: Run MongoDB Comprehensive Tests
  run: |
    export MONGO_URL="mongodb://localhost:27017"
    export MONGO_DB="ci_test_db"
    go run test/*.go
  env:
    MONGODB_CONNECTION_STRING: mongodb://localhost:27017
```

This test suite provides comprehensive validation that MongoBouncer correctly handles the full spectrum of MongoDB operations, ensuring production readiness and reliability.


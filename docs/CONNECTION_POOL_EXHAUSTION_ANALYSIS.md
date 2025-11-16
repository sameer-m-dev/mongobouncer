# Connection Pool Exhaustion Analysis

## Problem Statement

Even with 2000 connections configured (`maxPoolSize: 2000`), the MongoDB driver's connection pool is getting exhausted, resulting in:
- `idle connections: 0`
- `total connections: 2000` (all in use)
- Health checks timing out: `"timed out while checking out a connection from connection pool: context deadline exceeded"`

## Root Causes

### 1. **Two-Level Pooling Architecture Mismatch**

MongoBouncer has a **two-level pooling system** that creates confusion:

#### Level 1: MongoBouncer's PooledConnection (Wrapper)
- Located in `pool/pool.go`
- `PooledConnection` is just a **wrapper** around the MongoDB client
- Does NOT actually manage MongoDB driver connections
- `Checkout()` creates a wrapper, not a real connection
- `Return()` just marks the wrapper as not in use

```go
// pool/pool.go:346-365
func (p *ConnectionPool) Checkout(clientID string) (*PooledConnection, error) {
    // Creates a wrapper, NOT a real MongoDB driver connection
    conn := &PooledConnection{
        ID:          fmt.Sprintf("%s-%d", p.name, time.Now().UnixNano()),
        MongoClient: p.mongoClient, // Always the same client!
        Pool:        p,
        InUse:       true,
    }
    // This increments stats but doesn't checkout a real connection
    p.stats.incrementActiveConnections()
    return conn, nil
}
```

#### Level 2: MongoDB Driver's Connection Pool (Real Connections)
- Located in `mongo/mongo.go`
- **Actual connections** are checked out from MongoDB driver's pool
- Happens in `checkoutConnection()` → `server.Connection()`
- These are the connections that count toward the 2000 limit

```go
// mongo/mongo.go:1237-1274
func (m *Mongo) checkoutConnection(server driver.Server, databaseName string, session *Session) (conn driver.Connection, err error) {
    // If pinned connection exists, reuse it (good)
    if session != nil {
        if pinnedConn, hasPinned := m.sessions.GetPinnedConnection(session); hasPinned {
            return pinnedConn, nil
        }
    }
    
    // THIS is where real connections are checked out from MongoDB driver's pool
    conn, err = server.Connection(m.roundTripCtx)  // ← Real connection checkout
    return conn, nil
}
```

**The Problem**: MongoBouncer's pool stats don't reflect the actual MongoDB driver connection usage. The driver's pool can be exhausted even when MongoBouncer's pool shows available connections.

---

### 2. **Transaction Connection Pinning Without Proper Cleanup**

When transactions start, connections are **pinned** and held until commit/abort:

```go
// mongo/mongo.go:948-960
if transactionDetails.IsStartTransaction {
    // Pin the connection for the entire transaction
    err = m.sessions.StartTransaction(session, server, conn, transactionDetails.TxnNumber)
    // Connection is now pinned and won't be returned until commit/abort
}
```

**Connection Return Logic**:
```go
// mongo/mongo.go:1005-1023
defer func() {
    if isCommitOrAbortCommand {
        // Only close connection after commit/abort
        conn.Close()
        m.sessions.ResetTransactionState(session)
    } else if session == nil || !m.sessions.IsTransactionActive(session) {
        // Only close if no active transaction
        conn.Close()
    }
    // ⚠️ PROBLEM: If transaction is active, connection is NOT closed!
}()
```

**The Problem**: 
- If a transaction starts but **never commits/aborts**, the connection stays checked out forever
- If a client **disconnects** during a transaction, the pinned connection is never released
- If an **error occurs** during a transaction, the connection may not be properly cleaned up
- **Abandoned transactions** hold connections indefinitely

---

### 3. **Session Mode Connection Retention**

In **Session Mode**, connections are assigned to clients and **never returned**:

```go
// pool/pool.go:264-273
case SessionMode:
    // In session mode, client keeps the same connection
    if client.AssignedConn == nil {
        conn, err := pool.Checkout(clientID)
        client.AssignedConn = conn  // Assigned forever
    }
    return client.AssignedConn, nil
```

```go
// pool/pool.go:322-327
case SessionMode:
    // In session mode, only return if this is not the assigned connection
    if conn != client.AssignedConn {
        conn.Pool.Return(conn)  // Only returns non-assigned connections
    }
    // ⚠️ PROBLEM: Assigned connection is NEVER returned!
```

**The Problem**:
- Each client in Session Mode holds a connection for its entire lifetime
- If you have 2000+ clients, you'll exhaust the pool
- Even if clients are idle, their connections remain checked out
- Client disconnections may not properly clean up the underlying MongoDB driver connection

---

### 4. **Pinned Connection Reuse Logic Issue**

Pinned connections are reused, which is good, but there's a subtle issue:

```go
// mongo/session_manager.go:373-387
func (sm *SessionManager) GetPinnedConnection(session *Session) (driver.Connection, bool) {
    // Return pinned connection if transaction is in progress OR committed/aborted
    if (session.TransactionState == TransactionStateInProgress ||
        session.TransactionState == TransactionStateCommitted ||
        session.TransactionState == TransactionStateAborted) && session.PinnedConnection != nil {
        return session.PinnedConnection, true
    }
    return nil, false
}
```

**The Problem**:
- Committed/aborted transactions still return the pinned connection
- The connection is only unpinned in `UnpinTransaction()` which is called in a `defer`
- If the defer doesn't execute (panic, early return), the connection stays pinned
- The connection is only closed after the commit/abort command completes, but if there's an error, it may not close

---

### 5. **No Connection Timeout/Leak Detection**

There's **no mechanism** to detect and clean up:
- Stale pinned connections
- Abandoned transactions
- Long-running operations holding connections
- Clients that disconnected without cleanup

**The Problem**:
- Connections can be held indefinitely
- No automatic cleanup of stuck connections
- No timeout mechanism for transaction connections
- No detection of connection leaks

---

### 6. **Health Check Connection Checkout**

Health checks also need to checkout connections:

```go
// mongo/mongo.go:380-410
func (m *Mongo) CheckHealth() error {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    // This also needs to checkout a connection from the pool!
    err := m.client.Ping(ctx, readpref.Primary())
    // If pool is exhausted, this will timeout
}
```

**The Problem**:
- Health checks compete with regular operations for connections
- If pool is exhausted, health checks fail, making the situation worse
- Health checks don't have priority or reserved connections

---

## Why 2000 Connections Aren't Enough

### Scenario 1: Session Mode with Many Clients
- **1000 clients** in Session Mode = **1000 connections** permanently assigned
- **500 active transactions** = **500 connections** pinned
- **500 regular operations** = **500 connections** in use
- **Total: 2000 connections** (all exhausted)
- **Result**: No connections available for new operations or health checks

### Scenario 2: Abandoned Transactions
- **500 active transactions** that never commit/abort = **500 connections** stuck
- **1000 regular operations** = **1000 connections** in use
- **500 more operations** trying to start = **500 connections** waiting
- **Total: 2000 connections** (all exhausted)
- **Result**: New operations timeout waiting for connections

### Scenario 3: Client Disconnections
- **1000 clients** disconnect during transactions = **1000 connections** leaked
- **500 active operations** = **500 connections** in use
- **500 more operations** = **500 connections** needed
- **Total: 2000 connections** (all exhausted)
- **Result**: Connections are held by disconnected clients

---

## Solutions Implemented

### 1. **Connection Refresh on Exhaustion**
- Automatically detects when pool utilization >= 95%
- Refreshes the MongoDB client (disconnects and reconnects)
- Clears all stuck connections
- **Config**: `connection_refresh_on_exhaustion = true`

### 2. **Connection Age-Based Refresh**
- Refreshes connections after a maximum age
- Prevents stale connections from accumulating
- **Config**: `max_connection_age = "1h"`

### 3. **Periodic Connection Refresh**
- Refreshes connections at regular intervals
- Proactive cleanup of potential leaks
- **Config**: `connection_refresh_interval = "30m"`

---

## Recommendations

### Immediate Actions
1. **Enable connection refresh on exhaustion**:
   ```toml
   [mongobouncer]
   enable_connection_refresh = true
   connection_refresh_on_exhaustion = true
   ```

2. **Monitor pool utilization**:
   - Check Prometheus metrics: `pool_active_connections`, `pool_max_connections`
   - Alert when utilization > 90%

3. **Review pool mode**:
   - If using Session Mode, consider Transaction or Statement Mode
   - Session Mode holds connections for client lifetime

### Long-Term Improvements
1. **Add connection timeout/leak detection**:
   - Timeout for pinned connections (e.g., 5 minutes)
   - Automatic cleanup of abandoned transactions
   - Connection age tracking and cleanup

2. **Improve transaction cleanup**:
   - Detect client disconnections during transactions
   - Automatically abort transactions on client disconnect
   - Cleanup pinned connections on errors

3. **Add connection pool metrics**:
   - Track pinned connections separately
   - Monitor transaction connection usage
   - Alert on connection leaks

4. **Consider connection limits per client**:
   - Limit connections per client in Session Mode
   - Prevent single client from exhausting pool

---

## Monitoring

### Key Metrics to Watch
- `pool_active_connections` / `pool_max_connections` → Utilization ratio
- `pool_checkout_failures_total` → Connection exhaustion events
- `connection_refresh_success` → How often refresh is triggered
- Transaction count → Number of active transactions

### Alerting Thresholds
- **Warning**: Pool utilization > 85%
- **Critical**: Pool utilization > 95%
- **Critical**: Checkout failures > 10/minute
- **Warning**: Connection refresh frequency > 1/hour

---

## Conclusion

The root cause is **not** that 2000 connections are insufficient, but rather that:
1. Connections are being **held indefinitely** by transactions and sessions
2. There's **no automatic cleanup** of stuck/leaked connections
3. **Client disconnections** don't properly release connections
4. **Abandoned transactions** hold connections forever

The connection refresh mechanism addresses this by **forcefully clearing** all connections when the pool is exhausted, but the underlying issues (transaction cleanup, session management, leak detection) should also be addressed for a complete solution.


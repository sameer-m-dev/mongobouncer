# Distributed Cache with Kubernetes Auto-Discovery

MongoBouncer supports distributed caching using [groupcache-go](https://github.com/groupcache/groupcache-go) with automatic peer discovery in Kubernetes via [kubegroup](https://github.com/udhos/kubegroup). This enables horizontal scaling of MongoBouncer instances while maintaining session, transaction, and cursor state across all pods.

## Overview

The distributed cache feature allows multiple MongoBouncer pods to share:
- **Session State**: User sessions created in one pod are accessible from all pods
- **Transaction State**: Transaction pinning and state maintained across pod restarts
- **Cursor State**: Cursor-to-server mappings shared for proper request routing
- **Request Forwarding**: Automatic forwarding of requests to the correct pod holding a session
- **Sticky Sessions**: Kubernetes session affinity to ensure transaction consistency

## Transaction Consistency Solutions

MongoBouncer provides two approaches for handling MongoDB transactions in distributed deployments:

### 1. Sticky Sessions (Primary - Production Ready)
Kubernetes session affinity ensures that all requests from a client are routed to the same pod, providing guaranteed transaction consistency.

### 2. Request Forwarding (Under Active Development)
Request forwarding automatically routes requests to the correct pod holding a session. **⚠️ This feature is under active development and production usage is not recommended without sticky sessions enabled as a fallback.**

## Configuration

### Basic Configuration

```toml
[mongobouncer.shared_cache]
enabled = true                    # Enable distributed cache for Kubernetes scalability
listen_addr = "0.0.0.0"          # Listen address for groupcache peer communication
listen_port = 8080               # Listen port for groupcache peer communication
cache_size_bytes = "64Mi"        # Cache size in Kubernetes-style resource format (e.g., "1Gi", "512Mi", "64Mi")
session_expiry = "30m"           # Session expiration duration
transaction_expiry = "2m"        # Transaction expiration duration  
cursor_expiry = "24h"            # Cursor expiration duration
debug = false                    # Enable debug logging for cache operations
# peer_urls = []                 # Manual peer URLs for local testing (leave empty for Kubernetes auto-discovery)

# Request Forwarding Configuration (automatically enabled when shared_cache is enabled)
# Used to forward requests to the correct pod for session routing
max_connections = 10             # Maximum connections to other pods
connection_timeout = "5s"        # Connection timeout to other pods
keep_alive = true                # Enable TCP keep-alive
keep_alive_interval = "30s"      # Keep-alive interval
```

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `false` | Enable/disable distributed cache |
| `listen_addr` | string | `"0.0.0.0"` | Listen address for groupcache peer communication |
| `listen_port` | int | `8080` | Listen port for groupcache peer communication |
| `cache_size_bytes` | string | `"64Mi"` | Cache size in Kubernetes-style resource format (e.g., "1Gi", "512Mi", "64Mi") |
| `session_expiry` | string | `"30m"` | Session expiration duration |
| `transaction_expiry` | string | `"2m"` | Transaction expiration duration |
| `cursor_expiry` | string | `"24h"` | Cursor expiration duration |
| `debug` | bool | `false` | Enable debug logging for cache operations |
| `peer_urls` | array | `[]` | Manual peer URLs for local testing (leave empty for Kubernetes auto-discovery) |
| `max_connections` | int | `10` | Maximum connections to other pods for request forwarding |
| `connection_timeout` | string | `"5s"` | Connection timeout to other pods |
| `keep_alive` | bool | `true` | Enable TCP keep-alive for pod connections |
| `keep_alive_interval` | string | `"30s"` | Keep-alive interval for pod connections |

## Request Forwarding

**⚠️ IMPORTANT: Request forwarding is under active development. Production usage is not recommended without sticky sessions enabled as a fallback mechanism.**

When distributed cache is enabled, MongoBouncer automatically enables request forwarding for session routing. This feature allows pods to forward MongoDB requests to the correct pod that holds a specific session, ensuring session consistency across multiple pods.

### How Request Forwarding Works

1. **Session Detection**: When a request arrives, MongoBouncer extracts the session ID from transaction details
2. **Local Check**: First checks if the session exists locally
3. **Distributed Lookup**: If not found locally, queries the distributed cache for session location
4. **Request Forwarding**: If session is found on another pod, forwards the request to that pod
5. **Response Handling**: Returns the response from the target pod to the client

### Request Forwarding Flow

```
Client Request → Pod A (Wrong Pod)
                     ↓
                Check Local Session
                     ↓
                Session Not Found
                     ↓
                Query Distributed Cache
                     ↓
                Session Found on Pod B
                     ↓
                Forward Request to Pod B
                     ↓
                Return Response to Client
```

### Configuration for Request Forwarding

Request forwarding is automatically enabled when `shared_cache.enabled = true`. No additional configuration is required, but you can tune the forwarding parameters:

```toml
[mongobouncer.shared_cache]
enabled = true
# ... other cache settings ...

# Request forwarding settings (automatically enabled)
max_connections = 10             # Maximum connections to other pods
connection_timeout = "5s"        # Connection timeout to other pods
keep_alive = true                # Enable TCP keep-alive
keep_alive_interval = "30s"      # Keep-alive interval
```

### Benefits of Request Forwarding

- **Session Consistency**: Ensures requests for a session always reach the correct pod
- **Transparent Operation**: Clients don't need to know about pod routing
- **Automatic Failover**: If a pod fails, sessions can be recreated on other pods
- **Load Distribution**: Allows even distribution of new sessions across pods
- **No Session Affinity Required**: Eliminates the need for Kubernetes session affinity

## Sticky Sessions (Kubernetes Session Affinity)

**✅ PRODUCTION READY: Sticky sessions are the primary and recommended solution for MongoDB transaction consistency in distributed deployments.**

While request forwarding provides automatic session routing, sticky sessions serve as the primary mechanism to ensure transaction consistency. Request forwarding should only be used as an experimental feature with sticky sessions enabled as a fallback.

### Why Sticky Sessions Are Important

MongoDB transactions are pinned to specific servers and connections, which creates a fundamental challenge in distributed proxy deployments:

- **Transaction Pinning**: MongoDB transactions are bound to specific server/connection objects
- **Node Isolation**: Each proxy node maintains its own connection pool to MongoDB
- **Cross-Node Impossibility**: Cannot transfer a transaction from one proxy node to another

### How Sticky Sessions Work

```
Client A ──┐
           ├──► Kubernetes Service (ClientIP affinity)
Client B ──┘
           │
           ▼
    ┌─────────────┐
    │ Proxy Pod 1 │ ◄── All requests from Client A
    └─────────────┘
           │
           ▼
    ┌─────────────┐
    │ MongoDB    │
    │ Cluster    │
    └─────────────┘
```

### Sticky Sessions Configuration

#### Helm Values (`values.yaml`)
```yaml
service:
  sessionAffinity:
    enabled: true
    type: ClientIP
    config:
      clientIP:
        timeoutSeconds: 3600  # 1 hour session affinity
```

#### Generated Kubernetes Service
```yaml
apiVersion: v1
kind: Service
metadata:
  name: mongobouncer-service
spec:
  sessionAffinity: ClientIP
  sessionAffinityConfig:
    clientIP:
      timeoutSeconds: 3600
  selector:
    app: mongobouncer
  ports:
  - port: 27017
    targetPort: 27017
```

### Benefits of Sticky Sessions

#### ✅ What This Solves
- **Transaction Consistency**: All operations for a session go to the same pod
- **Server Pinning Works**: Pinned servers/connections stay valid throughout transaction
- **No Complex Logic**: Kubernetes handles the routing automatically
- **High Performance**: No network overhead for cache synchronization
- **Reliability**: Kubernetes handles pod failover gracefully

#### ✅ Transaction Lifecycle
1. **Client connects** → Routed to specific pod via session affinity
2. **Transaction starts** → Pinned to server/connection on that pod
3. **All operations** → Continue on same pod with same server/connection
4. **Transaction commits/aborts** → Server/connection unpinned on same pod

### Session Affinity Timeout Guidelines

| Use Case | Recommended Timeout | Reason |
|----------|-------------------|--------|
| **Short-lived transactions** | 300-600 seconds | 5-10 minutes |
| **Long-running applications** | 1800-3600 seconds | 30-60 minutes |
| **Batch processing** | 7200+ seconds | 2+ hours |
| **Interactive applications** | 1800 seconds | 30 minutes |

### Comparison: Request Forwarding vs Sticky Sessions

| Aspect | Request Forwarding | Sticky Sessions |
|--------|-------------------|-----------------|
| **Production Ready** | ❌ Under development | ✅ Production ready |
| **Complexity** | Medium (session lookup, forwarding) | Low (K8s handles routing) |
| **Transaction Support** | ⚠️ Experimental | ✅ Works perfectly |
| **Performance** | Small overhead for forwarding | No overhead |
| **Reliability** | Depends on pod connectivity | K8s handles failover |
| **Maintenance** | Custom forwarding logic | Standard K8s feature |
| **Memory Usage** | Shared session state | Each pod independent |
| **Network Traffic** | Low (only forwarded requests) | Low (only client traffic) |
| **Recommended Usage** | ❌ Not for production | ✅ Primary solution |

### When to Use Each Approach

#### Use Sticky Sessions When:
- ✅ **Primary use case**: MongoDB transactions
- ✅ **Production deployments**: All production scenarios
- ✅ **Simplicity preferred**: Minimal operational overhead
- ✅ **Performance critical**: Low latency requirements
- ✅ **Reliability required**: Kubernetes-native failover handling

#### Use Request Forwarding When:
- ⚠️ **Experimental testing**: Development and testing only
- ⚠️ **With sticky sessions**: Only as experimental feature with fallback
- ❌ **NOT for production**: Not recommended for production use
- ❌ **NOT standalone**: Must be used with sticky sessions enabled

### Implementation Recommendations

#### 1. Enable Both Request Forwarding and Sticky Sessions (Recommended)
```bash
# Deploy with both request forwarding and sticky sessions
helm install mongobouncer ./deploy/helm \
  --set app.config.sharedCache.enabled=true \
  --set service.sessionAffinity.enabled=true \
  --set service.sessionAffinity.config.clientIP.timeoutSeconds=3600
```

#### 2. Request Forwarding Only (Advanced)
```bash
# Use only request forwarding (disable session affinity)
helm install mongobouncer ./deploy/helm \
  --set app.config.sharedCache.enabled=true \
  --set service.sessionAffinity.enabled=false
```

#### 3. Sticky Sessions Only (Simple)
```bash
# Use only sticky sessions (disable distributed cache)
helm install mongobouncer ./deploy/helm \
  --set app.config.sharedCache.enabled=false \
  --set service.sessionAffinity.enabled=true
```

### Prerequisites

1. **RBAC Permissions**: Pods need permissions to watch/list pods in the same namespace
2. **Service**: Required for pod-to-pod communication
3. **Deployment**: Standard Kubernetes deployment with multiple replicas
4. **Request Forwarding**: Automatically enabled when distributed cache is enabled

### RBAC Configuration

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: mongobouncer-peer-discovery
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: mongobouncer-peer-discovery
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: mongobouncer-peer-discovery
subjects:
- kind: ServiceAccount
  name: mongobouncer
  namespace: mongobouncer
```

### Deployment Example

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mongobouncer
spec:
  replicas: 3
  selector:
    matchLabels:
      app: mongobouncer
  template:
    metadata:
      labels:
        app: mongobouncer
    spec:
      serviceAccountName: mongobouncer
      containers:
      - name: mongobouncer
        image: mongobouncer:latest
        ports:
        - containerPort: 27017
          name: mongodb
        - containerPort: 8080
          name: groupcache
        - containerPort: 9090
          name: metrics
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        volumeMounts:
        - name: config
          mountPath: /etc/mongobouncer
      volumes:
      - name: config
        configMap:
          name: mongobouncer-config
```

### Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mongobouncer
spec:
  selector:
    app: mongobouncer
  ports:
  - port: 27017
    name: mongodb
  - port: 8080
    name: groupcache
  - port: 9090
    name: metrics
```

## How It Works

### Auto-Discovery Process

1. **Pod Startup**: Each MongoBouncer pod starts with groupcache daemon
2. **Kubernetes API**: Pods watch for other pods with matching `label_selector`
3. **Peer Registration**: Discovered pods are automatically registered as groupcache peers
4. **Consistent Hashing**: groupcache uses consistent hashing to distribute keys across peers
5. **Automatic Failover**: When pods restart, peer discovery automatically updates the cluster

### Cache Operations

- **Store**: Data is stored locally and replicated to peers based on consistent hashing
- **Get**: Local cache checked first, then peers queried if not found
- **Remove**: Keys are removed from all peers (manual cleanup for completed transactions)
- **TTL**: Automatic expiration based on configured durations

### Session Management

```go
// Sessions are automatically shared across pods
session := sessionManager.CreateSession(clientID, lsid)
// Session is now available on all pods in the cluster
```

### Transaction Management

```go
// Transaction state is pinned to specific servers across pods
sessionManager.StartTransaction(session, server)
// Transaction state is maintained even if pod restarts
```

### Cursor Management

```go
// Cursor mappings are shared for proper request routing
cursorCache.Add(cursorID, collection, server, databaseName)
// Cursor routing works correctly across all pods
```

## Benefits

### Horizontal Scalability
- Run multiple MongoBouncer replicas without state conflicts
- Automatic load balancing across pods
- Seamless pod restarts and scaling

### Session Continuity
- Sessions persist across pod restarts
- No session loss during rolling updates
- Consistent user experience

### Transaction Safety
- Transaction state maintained across pod failures
- Proper transaction pinning to backend servers
- ACID compliance preserved

### Performance
- Local cache + distributed cache for optimal performance
- Automatic cache warming from peers
- Reduced MongoDB connection overhead

## Monitoring

### Prometheus Metrics

The distributed cache exposes additional metrics:

```
# Cache hit/miss ratios
mongobouncer_cache_hits_total{type="session|transaction|cursor"}
mongobouncer_cache_misses_total{type="session|transaction|cursor"}

# Cache size and operations
mongobouncer_cache_size_bytes{type="session|transaction|cursor"}
mongobouncer_cache_operations_total{operation="store|get|remove"}

# Request forwarding metrics
mongobouncer_request_forward_total{status="success|failed|local"}
mongobouncer_request_forward_duration_seconds{operation="forward|local"}

# Peer discovery metrics
kubegroup_peers: Number of discovered peer pods
kubegroup_events: Number of peer discovery events
```

### Health Checks

```bash
# Check cache status
curl http://localhost:9090/metrics | grep mongobouncer_cache

# Check request forwarding metrics
curl http://localhost:9090/metrics | grep mongobouncer_request_forward

# Check peer discovery
curl http://localhost:9090/metrics | grep kubegroup
```

## Troubleshooting

### Common Issues

1. **Peer Discovery Failing**
   - Check RBAC permissions for pod listing
   - Verify `label_selector` matches pod labels
   - Ensure headless service is configured

2. **Cache Not Working**
   - Verify `enabled = true` in configuration
   - Check groupcache port is not conflicting
   - Review debug logs for cache operations

3. **Request Forwarding Not Working**
   - Ensure distributed cache is enabled
   - Check that multiple pods are running
   - Verify session affinity is disabled in Kubernetes service
   - Check logs for forwarding activity

4. **Sticky Sessions Not Working**
   - Verify `sessionAffinity: ClientIP` is set in service
   - Check if client is behind NAT/proxy (may share IP)
   - Increase `timeoutSeconds` if sessions are longer than expected
   - Monitor session distribution across pods

5. **Performance Issues**
   - Monitor cache hit/miss ratios
   - Adjust cache size based on usage
   - Check network latency between pods
   - Monitor request forwarding metrics
   - Check for uneven pod load distribution

### Debug Mode

Enable debug logging to troubleshoot cache operations:

```toml
[mongobouncer.shared_cache]
enabled = true
debug = true
```

### Manual Key Removal

For completed transactions, manually remove keys to free cache space:

```go
// Remove completed transaction from all peers
distributedCache.RemoveTransaction(lsid)
```

## Migration from Manual Configuration

If migrating from the previous manual peer configuration:

### Old Configuration
```toml
[mongobouncer.shared_cache]
enabled = true
self_url = "http://mongobouncer-0:8080"
peer_urls = ["http://mongobouncer-1:8080", "http://mongobouncer-2:8080"]
```

### New Configuration
```toml
[mongobouncer.shared_cache]
enabled = true
listen_addr = "0.0.0.0"
listen_port = 8080
cache_size_bytes = "64Mi"
session_expiry = "30m"
transaction_expiry = "2m"
cursor_expiry = "24h"
debug = false
peer_urls = []  # Leave empty for Kubernetes auto-discovery

# Request forwarding automatically enabled
max_connections = 10
connection_timeout = "5s"
keep_alive = true
keep_alive_interval = "30s"
```

The new configuration eliminates the need for manual peer management and provides automatic discovery in Kubernetes environments. Request forwarding is automatically enabled when distributed cache is enabled.

## Production Recommendations

1. **Resource Allocation**: Allocate sufficient memory for cache size
2. **Network Policies**: Ensure pods can communicate on groupcache port
3. **Monitoring**: Set up alerts for cache hit/miss ratios and request forwarding metrics
4. **Backup Strategy**: Consider cache warming strategies for restarts
5. **Security**: Use network policies to restrict peer communication
6. **Deployment**: Use standard Kubernetes Deployment with multiple replicas
7. **RBAC**: Ensure proper permissions for pod discovery
8. **Sticky Sessions**: Enable session affinity as primary mechanism
9. **Request Forwarding**: Use only for experimental testing with sticky sessions fallback
10. **Load Testing**: Test sticky sessions under high load
11. **Session Affinity Timeout**: Configure appropriate timeout based on application needs
12. **Production Safety**: Never use request forwarding alone in production

## Conclusion

MongoBouncer provides comprehensive solutions for MongoDB transaction consistency in distributed deployments:

### ✅ Sticky Sessions (Primary Solution - Production Ready)
- **Transaction consistency** by design
- **High performance** with no overhead
- **Kubernetes-native** reliability
- **Simple configuration** and maintenance
- **Proven in production** environments

### ⚠️ Request Forwarding (Under Active Development)
- **Automatic session routing** across multiple pods
- **Transparent operation** for clients
- **Load distribution** of new sessions
- **Session sharing** across the cluster
- **⚠️ Experimental feature** - not recommended for production without sticky sessions

### 🎯 Recommended Approach
**Use sticky sessions as the primary solution** for production deployments:

1. **Sticky sessions** provide guaranteed transaction consistency
2. **Request forwarding** can be used experimentally with sticky sessions as fallback
3. **Distributed cache** enables session sharing and monitoring (optional)
4. **Kubernetes session affinity** ensures transaction consistency

### Production Recommendations:
- ✅ **Enable sticky sessions** for all production deployments
- ⚠️ **Use request forwarding** only for experimental testing
- ❌ **Do not use request forwarding alone** in production
- ✅ **Monitor both approaches** if using hybrid setup

By using sticky sessions as the primary solution, MongoBouncer ensures that MongoDB transactions work correctly in distributed environments with proven, production-ready technology.

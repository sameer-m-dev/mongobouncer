# Distributed Cache with Kubernetes Auto-Discovery

MongoBouncer now supports distributed caching using [groupcache-go](https://github.com/groupcache/groupcache-go) with automatic peer discovery in Kubernetes via [kubegroup](https://github.com/udhos/kubegroup). This enables horizontal scaling of MongoBouncer instances while maintaining session, transaction, and cursor state across all pods.

## Overview

The distributed cache feature allows multiple MongoBouncer pods to share:
- **Session State**: User sessions created in one pod are accessible from all pods
- **Transaction State**: Transaction pinning and state maintained across pod restarts
- **Cursor State**: Cursor-to-server mappings shared for proper request routing

## Configuration

### Basic Configuration

```toml
[mongobouncer.shared_cache]
enabled = true
groupcache_port = ":8080"
label_selector = "app=mongobouncer"
cache_size_bytes = 134217728  # 128 MB
session_expiry = "30m"
transaction_expiry = "2m"
cursor_expiry = "24h"
debug = false
```

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `false` | Enable/disable distributed cache |
| `groupcache_port` | string | `":8080"` | Port for groupcache peer communication |
| `label_selector` | string | `"app=mongobouncer"` | Kubernetes label selector for peer discovery |
| `cache_size_bytes` | int64 | `67108864` | Total cache size in bytes (64MB default) |
| `session_expiry` | string | `"30m"` | Session expiration duration |
| `transaction_expiry` | string | `"2m"` | Transaction expiration duration |
| `cursor_expiry` | string | `"24h"` | Cursor expiration duration |
| `debug` | bool | `false` | Enable debug logging for cache operations |

## Kubernetes Deployment

### Prerequisites

1. **RBAC Permissions**: Pods need permissions to watch/list pods in the same namespace
2. **Service**: Required for pod-to-pod communication
3. **Deployment**: Standard Kubernetes deployment with multiple replicas

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

# Peer discovery metrics
kubegroup_peers: Number of discovered peer pods
kubegroup_events: Number of peer discovery events
```

### Health Checks

```bash
# Check cache status
curl http://localhost:9090/metrics | grep mongobouncer_cache

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

3. **Performance Issues**
   - Monitor cache hit/miss ratios
   - Adjust cache size based on usage
   - Check network latency between pods

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
groupcache_port = ":8080"
label_selector = "app=mongobouncer"
```

The new configuration eliminates the need for manual peer management and provides automatic discovery in Kubernetes environments.

## Production Recommendations

1. **Resource Allocation**: Allocate sufficient memory for cache size
2. **Network Policies**: Ensure pods can communicate on groupcache port
3. **Monitoring**: Set up alerts for cache hit/miss ratios
4. **Backup Strategy**: Consider cache warming strategies for restarts
5. **Security**: Use network policies to restrict peer communication
6. **Deployment**: Use standard Kubernetes Deployment with multiple replicas
7. **RBAC**: Ensure proper permissions for pod discovery

## Example Deployment

See `examples/mongobouncer.distributed-peer-1.toml` and `examples/mongobouncer.distributed-peer-2.toml` for a complete configuration example suitable for Kubernetes deployment with auto-discovery enabled.
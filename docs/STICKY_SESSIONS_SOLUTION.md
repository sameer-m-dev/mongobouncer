# MongoDB Transaction Consistency with Sticky Sessions

## Problem Statement

MongoDB transactions are pinned to specific servers and connections, which creates a fundamental challenge in distributed proxy deployments:

- **Transaction Pinning**: MongoDB transactions are bound to specific server/connection objects
- **Node Isolation**: Each proxy node maintains its own connection pool to MongoDB
- **Cross-Node Impossibility**: Cannot transfer a transaction from one proxy node to another

## Solution: Kubernetes Session Affinity (Sticky Sessions)

Instead of trying to solve the unsolvable problem of cross-node transaction sharing, we use Kubernetes session affinity to ensure **transaction consistency by design**.

### How It Works

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

### Configuration

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

## Benefits

### ✅ What This Solves
- **Transaction Consistency**: All operations for a session go to the same pod
- **Server Pinning Works**: Pinned servers/connections stay valid throughout transaction
- **No Complex Logic**: Kubernetes handles the routing automatically
- **High Performance**: No network overhead for cache synchronization
- **Reliability**: Kubernetes handles pod failover gracefully

### ✅ Transaction Lifecycle
1. **Client connects** → Routed to specific pod via session affinity
2. **Transaction starts** → Pinned to server/connection on that pod
3. **All operations** → Continue on same pod with same server/connection
4. **Transaction commits/aborts** → Server/connection unpinned on same pod

## Comparison: Distributed Cache vs Sticky Sessions

| Aspect | Distributed Cache | Sticky Sessions |
|--------|------------------|-----------------|
| **Complexity** | High (server resolution, serialization) | Low (K8s handles routing) |
| **Transaction Support** | ❌ Cannot work across nodes | ✅ Works perfectly |
| **Performance** | Network overhead for cache sync | No overhead |
| **Reliability** | Cache consistency issues | K8s handles failover |
| **Maintenance** | Complex custom logic | Standard K8s feature |
| **Memory Usage** | Duplicate data across nodes | Each pod independent |
| **Network Traffic** | High (cache synchronization) | Low (only client traffic) |

## When to Use Each Approach

### Use Sticky Sessions When:
- ✅ **Primary use case**: MongoDB transactions
- ✅ **Standard deployments**: Most production scenarios
- ✅ **Simplicity preferred**: Minimal operational overhead
- ✅ **Performance critical**: Low latency requirements

### Use Distributed Cache When:
- ⚠️ **Monitoring only**: Session discovery across cluster
- ⚠️ **Load balancing hints**: Route new sessions to less loaded pods
- ⚠️ **Cache warming**: Pre-populate session metadata
- ❌ **NOT for transactions**: Cannot solve cross-node transaction problem

## Implementation Recommendations

### 1. Enable Sticky Sessions (Primary Solution)
```bash
# Deploy with session affinity
helm install mongobouncer ./deploy/helm \
  --set service.sessionAffinity.enabled=true \
  --set service.sessionAffinity.config.clientIP.timeoutSeconds=3600
```

### 2. Disable Distributed Cache for Transactions
```bash
# Disable distributed cache entirely
helm install mongobouncer ./deploy/helm \
  --set app.config.sharedCache.enabled=false
```

### 3. Hybrid Approach (Optional)
```bash
# Use sticky sessions + limited distributed cache for monitoring
helm install mongobouncer ./deploy/helm \
  --set service.sessionAffinity.enabled=true \
  --set app.config.sharedCache.enabled=true \
  --set app.config.sharedCache.sessionExpiry=30m \
  --set app.config.sharedCache.transactionExpiry=0s  # Disable transaction caching
```

## Session Affinity Timeout Guidelines

| Use Case | Recommended Timeout | Reason |
|----------|-------------------|--------|
| **Short-lived transactions** | 300-600 seconds | 5-10 minutes |
| **Long-running applications** | 1800-3600 seconds | 30-60 minutes |
| **Batch processing** | 7200+ seconds | 2+ hours |
| **Interactive applications** | 1800 seconds | 30 minutes |

## Monitoring and Observability

### Key Metrics to Track
- **Session Distribution**: How sessions are distributed across pods
- **Pod Load**: Ensure balanced load despite sticky sessions
- **Session Duration**: Average session lifetime
- **Transaction Success Rate**: Monitor transaction completion rates

### Grafana Dashboard Queries
```promql
# Sessions per pod
sum by (pod) (mongobouncer_sessions_active)

# Transaction success rate
rate(mongobouncer_transactions_committed_total[5m]) / 
rate(mongobouncer_transactions_started_total[5m])

# Pod load distribution
mongobouncer_connections_active by (pod)
```

## Troubleshooting

### Common Issues

#### 1. Sessions Not Sticking
**Symptoms**: Client requests hitting different pods
**Solutions**:
- Verify `sessionAffinity: ClientIP` is set
- Check if client is behind NAT/proxy (may share IP)
- Increase `timeoutSeconds` if sessions are longer than expected

#### 2. Uneven Pod Load
**Symptoms**: Some pods overloaded, others idle
**Solutions**:
- Monitor session distribution
- Consider shorter session affinity timeout
- Implement session-aware load balancing

#### 3. Pod Failures During Transactions
**Symptoms**: Transaction errors when pod restarts
**Solutions**:
- Implement proper MongoDB transaction retry logic
- Use MongoDB driver's automatic retry mechanisms
- Consider shorter session affinity for faster failover

## Conclusion

**Sticky sessions are the correct solution for MongoDB transaction consistency in distributed proxy deployments.** They solve the fundamental problem that distributed caching cannot: ensuring that MongoDB transactions remain pinned to the same server/connection throughout their lifecycle.

The distributed cache approach was attempting to solve an unsolvable problem - MongoDB transactions cannot span multiple proxy nodes because they're inherently tied to specific server objects that only exist on one node.

By using Kubernetes session affinity, we get:
- ✅ **Transaction consistency** without complex logic
- ✅ **High performance** without cache synchronization overhead  
- ✅ **Reliability** with Kubernetes-native failover handling
- ✅ **Simplicity** with standard Kubernetes features

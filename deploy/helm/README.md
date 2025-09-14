# MongoBouncer Deployment Guide

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 1.0.0](https://img.shields.io/badge/AppVersion-1.0.0-informational?style=flat-square)

Complete deployment and configuration guide for MongoBouncer - A MongoDB connection pooling proxy that acts as the pgbouncer equivalent for MongoDB.

## Overview

MongoBouncer is a MongoDB connection pooling proxy that serves as a connection multiplexer, reducing connection overhead and providing advanced connection management for high-scale MongoDB deployments.

### Key Features

- **Connection Storm Mitigation**: Reduces MongoDB connections from 30k+ down to ~2k in production environments
- **Connection Multiplexing**: Handles numerous client connections through optimized connection pools
- **Resource Optimization**: Significantly reduces `ismaster` commands and connection overhead
- **Production Scale**: Built for high-scale applications and multi-process deployments
- **Native Prometheus Metrics**: Built-in metrics server with comprehensive MongoDB proxy metrics
- **TLS/SSL Support**: Full support for secure client and server connections

## Prerequisites

- Kubernetes 1.19+
- Helm 3.2.0+
- MongoDB cluster (standalone, replica set, or sharded cluster)

## Directory Structure

```
deploy/
└── helm/                               # Helm chart
    ├── Chart.yaml                      # Chart metadata
    ├── values.yaml                     # Default configuration values
    ├── README.md                       # Readme file
    ├── templates/                      # Kubernetes resource templates
    │   ├── _helpers.tpl               # Template helpers
    │   ├── configmap.yaml             # Configuration management
    │   ├── deployment.yaml            # Main application deployment
    │   ├── service.yaml               # Service exposure
    │   ├── serviceaccount.yaml        # Service account
    │   ├── servicemonitor.yaml        # Prometheus monitoring
    │   ├── poddisruptionbudget.yaml   # High availability
    │   ├── hpa.yaml                   # Auto-scaling
    │   ├── persistentvolumeclaim.yaml # Storage
    │   ├── networkpolicy.yaml         # Network security
    │   ├── ingress.yaml               # External access
    │   ├── NOTES.txt                  # Post-install instructions
    │   └── tests/
    │       └── test-connection.yaml   # Connection tests
    └── charts/                        # Chart dependencies (if any)
```

## Quick Start

### 1. Install Helm

If Helm is not already installed:

```bash
curl https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3 | bash
```

### 2. Deploy MongoBouncer

```bash
# Navigate to the Helm chart
cd deploy/helm/

# Install with default configuration
helm install mongobouncer . --namespace mongobouncer-system --create-namespace

# Or install with custom values
helm install mongobouncer . -f your-values.yaml --namespace mongobouncer-system --create-namespace
```

### 3. Test the Deployment

```bash
# Run Helm tests
helm test mongobouncer --namespace mongobouncer-system

# Manual connection test
kubectl run mongodb-client --rm --tty -i --restart='Never' \
  --image mongo:7 -- \
  mongo --host mongobouncer.mongobouncer-system.svc.cluster.local:27017 \
  --eval "db.runCommand('ping')"
```

### 4. Connect to MongoBouncer

```bash
# Port forward for local access
kubectl port-forward svc/mongobouncer 27017:27017 --namespace mongodb

# Connect using standard MongoDB connection string
mongodb://localhost:27017/your_database
```

## Configuration

### Basic Configuration

Create a `values.yaml` file with your MongoDB configuration:

```yaml
app:
  databases:
    primary:
      connectionString: "mongodb://mongo-cluster:27017/app"
      maxConnections: 50
      poolMode: "session"
    
    analytics:
      connectionString: "mongodb://analytics-cluster:27017/analytics"
      maxConnections: 25
      poolMode: "statement"
      readPreference: "secondary"

# Resource allocation
resources:
  requests:
    cpu: 500m
    memory: 512Mi
  limits:
    cpu: 1000m
    memory: 1Gi

# High availability
replicaCount: 3
podDisruptionBudget:
  enabled: true
  minAvailable: 2
```

### Configuration Parameters

The following table lists the configurable parameters of the MongoBouncer chart and their default values.

#### Global Parameters

| Name | Description | Value |
|------|-------------|-------|
| `global.imageRegistry` | Global Docker image registry | `""` |

#### Image Parameters

| Name | Description | Value |
|------|-------------|-------|
| `image.registry` | MongoBouncer image registry | `ghcr.io` |
| `image.repository` | MongoBouncer image repository | `sameer-m-dev/mongobouncer` |
| `image.tag` | MongoBouncer image tag | `"latest"` |
| `image.pullPolicy` | MongoBouncer image pull policy | `IfNotPresent` |
| `image.pullSecrets` | MongoBouncer image pull secrets | `[]` |

#### Application Parameters

| Name | Description | Value |
|------|-------------|-------|
| `app.config.listenAddr` | Address to bind the proxy server | `"0.0.0.0"` |
| `app.config.listenPort` | Port to bind the proxy server | `27017` |
| `app.config.logLevel` | Logging level | `"info"` |
| `app.config.poolMode` | Default connection pool mode | `"session"` |
| `app.config.minPoolSize` | Minimum connection pool size | `5` |
| `app.config.maxPoolSize` | Maximum connection pool size | `20` |
| `app.config.maxClientConn` | Maximum client connections (0 = unlimited) | `100` |
| `app.databases` | MongoDB database configurations | `{}` |

#### Deployment Parameters

| Name | Description | Value |
|------|-------------|-------|
| `replicaCount` | Number of MongoBouncer replicas | `2` |
| `resources.limits.cpu` | The CPU limits for the MongoBouncer containers | `1000m` |
| `resources.limits.memory` | The memory limits for the MongoBouncer containers | `1Gi` |
| `resources.requests.cpu` | The requested CPU for the MongoBouncer containers | `500m` |
| `resources.requests.memory` | The requested memory for the MongoBouncer containers | `512Mi` |

#### Service Parameters

| Name | Description | Value |
|------|-------------|-------|
| `service.type` | MongoBouncer service type | `ClusterIP` |
| `service.port` | MongoBouncer service port | `27017` |

#### Monitoring Parameters

| Name | Description | Value |
|------|-------------|-------|
| `monitoring.prometheus.enabled` | Enable Prometheus metrics | `true` |
| `monitoring.prometheus.port` | Prometheus metrics port | `9090` |
| `monitoring.serviceMonitor.enabled` | Enable ServiceMonitor for Prometheus Operator | `true` |

See the [values.yaml](helm/values.yaml) file for a complete list of configurable parameters.

## Configuration Examples

### Production Configuration

```yaml
# Production-ready configuration
replicaCount: 3

app:
  config:
    poolMode: "session"
    minPoolSize: 5
    maxPoolSize: 50
    maxClientConn: 500
    
    tls:
      enabled: true
      certFile: "/etc/tls/tls.crt"
      keyFile: "/etc/tls/tls.key"
  
  databases:
    production:
      connectionString: "mongodb+srv://user:pass@prod-cluster.mongodb.net/app"
      maxConnections: 100
      poolMode: "session"
      readPreference: "primary"
      writeConcern: "majority"
    
    analytics:
      connectionString: "mongodb+srv://user:pass@analytics-cluster.mongodb.net/analytics"
      maxConnections: 50
      poolMode: "statement"
      readPreference: "secondaryPreferred"

resources:
  requests:
    cpu: 1000m
    memory: 1Gi
  limits:
    cpu: 2000m
    memory: 2Gi

autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70

podDisruptionBudget:
  enabled: true
  minAvailable: 2

monitoring:
  prometheus:
    enabled: true
  serviceMonitor:
    enabled: true
    additionalLabels:
      release: prometheus
```

### Development Configuration

```yaml
# Development configuration
replicaCount: 1

app:
  config:
    logLevel: "debug"
    verbose: true
  
  databases:
    dev:
      connectionString: "mongodb://mongodb:27017/dev"
      maxConnections: 10
      poolMode: "session"

resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 500m
    memory: 512Mi

monitoring:
  prometheus:
    enabled: true
  serviceMonitor:
    enabled: false
```

### High Availability with TLS

```yaml
# High availability with TLS
replicaCount: 5

app:
  config:
    tls:
      enabled: true
      certFile: "/etc/tls/tls.crt"
      keyFile: "/etc/tls/tls.key"
      caFile: "/etc/tls/ca.crt"
      verifyClient: true

affinity:
  podAntiAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
    - labelSelector:
        matchLabels:
          app.kubernetes.io/name: mongobouncer
      topologyKey: kubernetes.io/hostname

tolerations:
- key: "mongodb"
  operator: "Equal"
  value: "true"
  effect: "NoSchedule"

nodeSelector:
  node-type: "database"

podDisruptionBudget:
  enabled: true
  minAvailable: 3
```

## Usage

### Connecting to MongoBouncer

Once deployed, applications can connect to MongoBouncer using the standard MongoDB connection string:

```bash
# Internal Kubernetes connection
mongodb://mongobouncer.mongobouncer-system.svc.cluster.local:27017/database

# With authentication
mongodb://username:password@mongobouncer.mongobouncer-system.svc.cluster.local:27017/database
```

### Testing the Deployment

```bash
# Run Helm tests
helm test mongobouncer --namespace mongodb

# Manual connection test
kubectl run mongodb-client --rm --tty -i --restart='Never' \
  --namespace mongodb \
  --image mongo:7 -- \
  mongo --host mongobouncer.mongobouncer-system.svc.cluster.local:27017 \
  --eval "db.runCommand('ping')"
```

### Monitoring

```bash
# View Prometheus metrics
kubectl port-forward svc/mongobouncer 9090:9090 --namespace mongodb
curl http://localhost:9090/metrics

# Health check
kubectl port-forward svc/mongobouncer 8080:8080 --namespace mongodb
curl http://localhost:8080/health

# View logs
kubectl logs -l app.kubernetes.io/name=mongobouncer -f --namespace mongodb
```

## Security

### TLS Configuration

Enable TLS for secure connections:

```yaml
app:
  config:
    tls:
      enabled: true
      certFile: "/etc/tls/tls.crt"
      keyFile: "/etc/tls/tls.key"
      caFile: "/etc/tls/ca.crt"
      verifyClient: true
```

Create TLS secret:

```bash
kubectl create secret tls mongobouncer-tls \
  --cert=tls.crt \
  --key=tls.key \
  --ca-cert=ca.crt \
  --namespace mongodb
```

### Security Context

The chart runs with a secure configuration by default:

```yaml
podSecurityContext:
  fsGroup: 1001
  runAsNonRoot: true
  runAsUser: 1001

securityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop:
    - ALL
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  runAsUser: 1001
```

### Network Policies

Enable network policies for additional security:

```yaml
networkPolicy:
  enabled: true
  ingress:
    - from:
      - namespaceSelector:
          matchLabels:
            name: application-namespace
      ports:
      - protocol: TCP
        port: 27017
```

## Upgrading

### To upgrade an existing release

```bash
helm upgrade mongobouncer helm/ -f values.yaml --namespace mongodb
```

### Rolling Updates

The chart supports rolling updates with zero downtime:

```yaml
updateStrategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0

podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

## Troubleshooting

### Common Issues

1. **Connection Refused**
   ```bash
   # Check if pods are running
   kubectl get pods -l app.kubernetes.io/name=mongobouncer --namespace mongodb
   
   # Check service
   kubectl get svc mongobouncer --namespace mongodb
   
   # Check logs
   kubectl logs -l app.kubernetes.io/name=mongobouncer --namespace mongodb
   ```

2. **Configuration Errors**
   ```bash
   # Validate configuration
   kubectl describe configmap mongobouncer-config --namespace mongodb
   
   # Check for validation errors
   kubectl describe deployment mongobouncer --namespace mongodb
   ```

3. **Performance Issues**
   ```bash
   # Check metrics
   kubectl port-forward svc/mongobouncer 9090:9090 --namespace mongodb
   curl http://localhost:9090/metrics | grep mongobouncer
   
   # Scale up if needed
   kubectl scale deployment mongobouncer --replicas=5 --namespace mongodb
   ```

### Debug Mode

Enable debug logging:

```yaml
app:
  config:
    logLevel: "debug"
    verbose: true
```

### Resource Issues

Monitor resource usage:

```bash
# Check resource usage
kubectl top pods -l app.kubernetes.io/name=mongobouncer --namespace mongodb

# Check resource limits
kubectl describe pods -l app.kubernetes.io/name=mongobouncer --namespace mongodb
```

## Production Deployment Best Practices

For production deployments, consider:

1. **High Availability**: Deploy multiple replicas across different nodes
2. **Resource Planning**: Set appropriate CPU and memory limits based on load testing
3. **Monitoring**: Enable Prometheus metrics and set up Grafana dashboards
4. **Alerting**: Configure Prometheus alerts for critical metrics
5. **Security**: Use TLS/SSL, network policies, and proper authentication
6. **Backup Strategy**: Configure persistent storage for logs if needed
7. **Update Strategy**: Use rolling updates with pod disruption budgets
8. **Load Testing**: Test connection pooling under expected load

Example production deployment command:

```bash
helm install mongobouncer helm/ \
  --namespace mongodb \
  --create-namespace \
  -f production-values.yaml \
  --set replicaCount=3 \
  --set resources.limits.cpu=2000m \
  --set resources.limits.memory=2Gi \
  --set monitoring.prometheus.enabled=true \
  --set monitoring.serviceMonitor.enabled=true
```

## Helm Repository Setup

### Add Custom Helm Repository (if available)

```bash
# Add your custom Helm repository
helm repo add mongobouncer https://your-helm-repo.com/charts
helm repo update

# Install from repository
helm install mongobouncer mongobouncer/mongobouncer -f values.yaml
```

### Local Development

```bash
# Clone the repository
git clone https://github.com/sameer-m-dev/mongobouncer.git
cd mongobouncer/deploy/helm

# Validate the chart
helm lint .
helm template mongobouncer . --debug

# Test installation
helm install mongobouncer . --dry-run --debug
```

## Uninstalling

To uninstall/delete the `mongobouncer` deployment:

```bash
helm uninstall mongobouncer --namespace mongodb
```

The command removes all Kubernetes components associated with the chart and deletes the release.

## Support

For deployment support:

- **Documentation**: [GitHub Repository](https://github.com/sameer-m-dev/mongobouncer)
- **Issues**: [GitHub Issues](https://github.com/sameer-m-dev/mongobouncer/issues)
- **Discussions**: [GitHub Discussions](https://github.com/sameer-m-dev/mongobouncer/discussions)
- **Grafana Setup**: See [grafana/README.md](grafana/README.md)

## Contributing

Please read the [contributing guidelines](../CONTRIBUTING.md) before submitting pull requests.

## License

This deployment configuration is licensed under the MIT License - see the [LICENSE](../LICENSE) file for details.

## Changelog

### Version 0.1.0
- Initial release of MongoBouncer Helm chart
- Complete Kubernetes deployment support
- Prometheus metrics integration
- Production-ready configuration options
- TLS/SSL support
- High availability and scaling features
- Comprehensive Grafana monitoring dashboard

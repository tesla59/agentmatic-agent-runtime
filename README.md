# AgentMatic

AgentMatic is an autonomous Kubernetes DevOps runtime and agent harness written in Go. It connects large language models to Kubernetes clusters to diagnose issues, inspect workloads, and assist with cluster operations through a tool-calling reasoning loop.

## Architecture

- Engine: Tool execution loop with live reasoning streaming and step listeners.
- Interceptors: Pipeline for sanitizing tool outputs (secret masking, metadata pruning, and context truncation).
- Sessions: Disk-backed multi-turn session persistence with resume and history tracking.
- Interface: Interactive terminal shell (REPL) and one-shot command-line runner.

## Tools

| Domain | Tool Name | Scope | Status | Description |
| :--- | :--- | :--- | :--- | :--- |
| Workloads | `k8s_list_pods` | Namespaced | Completed | List pods with phase and status |
| Workloads | `k8s_get_pod_logs` | Namespaced | Completed | Container logs with tail limits and previous crash support |
| Workloads | `k8s_list_deployments` | Namespaced | Completed | Deployment status and replica counts |
| Workloads | `k8s_get_deployment` | Namespaced | Completed | Detailed pod template spec, container images, and probes |
| Workloads | `k8s_list_statefulsets` | Namespaced | Planned | Database and stateful clustered workloads |
| Workloads | `k8s_list_daemonsets` | Namespaced | Planned | Node-level agents and daemon distribution |
| Workloads | `k8s_list_jobs` | Namespaced | Planned | Batch jobs and cron execution status |
| Networking | `k8s_list_services` | Namespaced | Planned | Service types, ports, cluster IPs, and selectors |
| Networking | `k8s_get_endpoints` | Namespaced | Planned | Inspect service endpoint resolution and healthy pod mappings |
| Networking | `k8s_list_ingresses` | Namespaced | Planned | External routing rules, hosts, and TLS configuration |
| Networking | `k8s_list_networkpolicies` | Namespaced | Planned | Pod-to-pod network security and traffic isolation |
| Config & Storage | `k8s_get_configmap` | Namespaced | Planned | Inspect configuration files and application settings |
| Config & Storage | `k8s_list_pvcs` | Namespaced | Planned | Persistent volume claims and volume binding status |
| Config & Storage | `k8s_list_storageclasses` | Cluster | Planned | Storage provisioners and parameters |
| Cluster & Health | `k8s_get_events` | Namespaced | Completed | Cluster warnings, backoffs, and probe failures |
| Cluster & Health | `k8s_list_nodes` | Cluster | Completed | Node conditions (Ready, pressure) and allocatable capacity |
| Cluster & Health | `k8s_list_namespaces` | Cluster | Planned | Cluster orientation and active namespace discovery |
| Cluster & Health | `k8s_get_resource_quota` | Namespaced | Planned | Namespace resource limits and quota utilization |
| Metrics | `k8s_top_pods` | Namespaced | Planned | Real-time pod CPU and memory usage (via metrics-server) |
| Metrics | `k8s_top_nodes` | Cluster | Planned | Real-time node resource load and utilization |
| Remediation | `k8s_restart_deployment` | Namespaced | Planned | Trigger rolling restarts for workloads |
| Remediation | `k8s_scale_deployment` | Namespaced | Planned | Scale deployment replica counts |
| Remediation | `k8s_delete_pod` | Namespaced | Planned | Evict or delete a pod to force rescheduling |

## Getting Started

### Prerequisites

- Go 1.23 or newer
- Access to a Kubernetes cluster (`~/.kube/config` or in-cluster service account)
- Gemini API key (`GEMINI_API_KEY`)

### Usage

Run the interactive shell:

```bash
export GEMINI_API_KEY="your-api-key"
go run ./cmd/agentmatic
```

Run a single diagnostic command:

```bash
go run ./cmd/agentmatic "Check for any crashing pods across the cluster"
```

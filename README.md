# AgentMatic

AgentMatic is an autonomous Kubernetes DevOps runtime and agent harness written in Go. It connects large language models to Kubernetes clusters to inspect workloads, diagnose issues, and support team workflows over ConnectRPC and the terminal.

## Architecture

- Engine: Tool-calling reasoning loop with per-request step streaming.
- Interceptors: Sanitizes tool outputs via secret masking, metadata pruning, and context truncation.
- Security & Auth: Two-layer authorization combining Kubernetes TokenReview authentication with pre-tool SubjectAccessReview checks and ServiceAccount ceilings.
- Sessions: Multi-tenant, owner-scoped persistence partitioned by cluster and user UID.
- Server & Client: HTTP/2 ConnectRPC daemon (`agentmatic-server`) for remote CLI and web UIs, plus a Cobra/Viper CLI (`agentmatic`) supporting both remote and local debugging modes.

## Tools

AgentMatic currently ships 8 read-only Kubernetes diagnostic tools across workloads, networking, configuration, events, and nodes, with 15 more planned. See [TOOLS.md](TOOLS.md) for the full catalog.

## Getting Started

### Prerequisites

- Go 1.24 or newer
- Access to a Kubernetes cluster (`~/.kube/config` or in-cluster service account)
- Gemini API key (`GEMINI_API_KEY`)

### 1. Client-Server Mode (ConnectRPC)

Start the server locally (dev mode, no cluster auth):

```bash
export GEMINI_API_KEY="your-api-key"
go run ./cmd/agentmatic-server --auth-mode local --addr :8080
```

Or run with Kubernetes `TokenReview` and `SubjectAccessReview` enforcement enabled (default):

```bash
export GEMINI_API_KEY="your-api-key"
go run ./cmd/agentmatic-server --auth-mode k8s --cluster-id prod-us --addr :8080
```

Connect using the CLI client (one-shot or interactive REPL; no API key required on the client):

```bash
# Interactive shell
go run ./cmd/agentmatic --server http://localhost:8080

# Single diagnostic command
go run ./cmd/agentmatic --server http://localhost:8080 "Check node conditions and events"
```

When connecting to an `--auth-mode k8s` server, pass `--token` (or `AGENTMATIC_TOKEN`) or rely on automatic bearer/exec token resolution from `~/.kube/config`.

### 2. Direct Local Debug Mode

For standalone on-host debugging without running a server:

```bash
export GEMINI_API_KEY="your-api-key"
go run ./cmd/agentmatic --local
go run ./cmd/agentmatic --local "List pods in the default namespace"
```

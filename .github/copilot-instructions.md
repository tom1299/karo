This is **Karo** - a Kubernetes operator that automatically restarts deployments when ConfigMaps or Secrets they depend on are updated. It uses a custom RestartRule CRD to define which resources to watch and which deployments to restart. Please follow these guidelines when contributing:

## Code Standards

## Important
Tests need to have a kind cluster running. Create one with `kind create cluster --name karo-e2e`. If one already
exists, delete the cluster first with `kind delete cluster --name karo-e2e`.

### Required Before Each Commit
- Lint: `make lint`
- Build: `make build`
- Test: `make test`
- Test e2e: `make test-e2e`
- Generate manifests: `make manifests generate`
- Install CRDs: `make install`

## Repository Structure
- `api/v1alpha1/`: Custom Resource Definitions (RestartRule CRD)
- `cmd/main.go`: Operator entry point with CLI flags (e.g., minimum-delay)
- `internal/controller/`: Reconciliation logic for ConfigMaps, Secrets, and RestartRules
- `internal/manager/`: Manager setup and configuration
- `internal/store/`: In-memory store for RestartRule tracking
- `config/`: Kubernetes manifests (CRDs, RBAC)
- `deploy/helm/`: Helm chart for deployment
- `test/e2e/`: End-to-end tests

## Key Guidelines
1. Follow Go best practices and idiomatic patterns
2. Use controller-runtime patterns for Kubernetes controllers
3. Write unit tests for new functionality using table-driven tests when possible
4. Add e2e tests for new features in `test/e2e/`
5. Document controller behavior and edge cases
6. Use the default namespace "default" for tests
7. Ensure RestartRules handle delayed restarts correctly (0-3600 seconds)
8. Controllers should gracefully handle resource deletions and updates

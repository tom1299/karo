# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Karo is a Kubernetes operator that automatically restarts deployments when ConfigMaps or Secrets they depend on are updated. It's built using the Operator SDK framework with controller-runtime.

## Development Commands

### Building and Running
- `make build` - Build the manager binary
- `make run` - Run the controller locally (requires kubebuilder-assets)
- `make docker-build` - Build Docker image
- `make docker-push` - Push Docker image

### Testing
- `make test` - Run unit tests with coverage
- `make test-unit` - Run unit tests only
- `make test-coverage` - Generate HTML coverage report
- `make test-e2e` - Run end-to-end tests (requires Kind cluster)
- `make setup-test-e2e` - Set up Kind cluster for e2e testing
- `make cleanup-test-e2e` - Clean up Kind cluster after e2e testing

#### Debugging E2E Tests
E2E tests run against a Kind cluster named `karo-test-e2e`. For troubleshooting:
- `kind get clusters` - List available Kind clusters
- `kubectl config use-context kind-karo-test-e2e` - Switch to test cluster context
- `kubectl get pods -A` - Check pod status in test cluster
- `kubectl logs -n karo-system deployment/karo-controller-manager` - View controller logs
- Check `test-e2e.log` for controller output during test runs

### Code Quality
- `make lint` - Run golangci-lint linter
- `make lint-fix` - Run golangci-lint with auto-fixes
- `make fmt` - Format Go code
- `make vet` - Run go vet

### Kubernetes Operations
- `make install` - Install CRDs into cluster
- `make uninstall` - Remove CRDs from cluster
- `make deploy` - Deploy controller to cluster
- `make undeploy` - Remove controller from cluster

### Code Generation
- `make generate` - Generate DeepCopy methods
- `make manifests` - Generate CRDs, RBAC, and webhook configs

## Architecture

### Core Components

**API Layer (`api/v1alpha1/`)**
- `RestartRule` CRD definition with change triggers and restart targets
- Supports ConfigMap and Secret monitoring with flexible selectors
- Target resources can be Deployments or StatefulSets

**Controllers (`internal/controller/`)**
- `RestartRuleReconciler` - Manages RestartRule lifecycle and store updates
- `ConfigMapReconciler` - Watches ConfigMap changes and triggers restarts
- `SecretReconciler` - Watches Secret changes and triggers restarts
- `BaseReconciler` - Shared reconciliation logic for resource changes

**Storage (`internal/store/`)**
- `RestartRuleStore` interface for rule persistence
- `MemoryRestartRuleStore` - In-memory implementation with thread-safe operations
- Stores mapping between watched resources and restart targets

### Key Patterns

**Controller Pattern**: Uses controller-runtime framework with separate reconcilers for each resource type. All controllers share a common RestartRuleStore to coordinate restart actions.

**Store-Based Coordination**: Controllers use a shared in-memory store to track which resources should trigger restarts of which targets, enabling efficient lookups during change events.

**Resource Watching**: ConfigMap and Secret controllers watch for changes and query the store to find associated RestartRule targets, then perform rolling restart operations.

### Configuration Files

- `.golangci.yml` - Comprehensive linter configuration with security-focused rules
- `Makefile` - Build automation with kubebuilder patterns
- `config/` - Kubernetes manifests for CRDs and RBAC

## Testing Strategy

The project uses Ginkgo/Gomega for testing:
- Unit tests for store logic and controller functions
- E2E tests run against Kind clusters
- Coverage reports generated in both text and HTML formats
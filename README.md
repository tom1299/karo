# Karo - Kubernetes Restart Operator

[![Tests](https://github.com/tom1299/karo/actions/workflows/test.yml/badge.svg)](https://github.com/tom1299/karo/actions/workflows/test.yml)
[![codecov](https://codecov.io/gh/tom1299/karo/branch/main/graph/badge.svg)](https://codecov.io/gh/tom1299/karo)
[![Go Report Card](https://goreportcard.com/badge/github.com/reuhl/karo)](https://goreportcard.com/report/github.com/reuhl/karo)

Karo is a Kubernetes operator that automatically restarts deployments when ConfigMaps or Secrets they depend on are updated. This ensures your applications always use the latest configuration without manual intervention.

## Features

- 🔄 **Automatic Restart**: Automatically restarts deployments when referenced ConfigMaps or Secrets change
- 📋 **Flexible Rules**: Define custom restart rules using the `RestartRule` CRD
- 🎯 **Selective Targeting**: Target specific deployments based on your requirements
- 🔒 **Secure**: Works with both ConfigMaps and Secrets

## Quick Start

### Prerequisites
- go version v1.24.0+
- docker version 17.03+
- kubectl version v1.11.3+
- Access to a Kubernetes v1.11.3+ cluster

### Installation

**Install the CRDs into the cluster:**
```sh
make install
```

**Deploy the controller:**
```sh
make deploy
```

### Usage

Create a `RestartRule` to automatically restart deployments when ConfigMaps or Secrets change:

```yaml
apiVersion: karo.jeeatwork.com/v1alpha1
kind: RestartRule
metadata:
  name: nginx-restart-rule
  namespace: default
spec:
  changes:
    - kind: ConfigMap
      name: nginx-config
      changeType: ["Update"]
    - kind: Secret
      name: nginx-secret
      changeType: ["Update"]
  targets:
    - kind: Deployment
      name: nginx
```

## Development

### Running Tests

Run all unit tests with coverage:
```sh
make test-coverage
```

View coverage report in browser:
```sh
make test-coverage-view
```

### Building

Build the manager binary:
```sh
make build
```

Build and push Docker image:
```sh
make docker-build docker-push IMG=<registry>/karo:tag
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/karo:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/karo/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v1-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

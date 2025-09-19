/*
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
*/

package store

import (
	"context"

	v1 "k8s.io/api/core/v1"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

type OperationType string

const (
	OperationCreate OperationType = "Create"
	OperationUpdate OperationType = "Update"
	OperationDelete OperationType = "Delete"
)

// RestartRuleStore defines methods for managing RestartRules.
type RestartRuleStore interface {
	Add(ctx context.Context, rule *karov1alpha1.RestartRule)

	Remove(ctx context.Context, namespace, name string)

	GetForSecret(ctx context.Context, secret v1.Secret, operation OperationType) []*karov1alpha1.RestartRule

	GetForConfigMap(ctx context.Context, configMap v1.ConfigMap, operation OperationType) []*karov1alpha1.RestartRule
}

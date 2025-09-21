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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestartRuleSpec defines the desired state of RestartRule
type RestartRuleSpec struct {
	// Changes defines the resources whose changes trigger restarts
	// +required
	// +kubebuilder:validation:MinItems=1
	Changes []ChangeSpec `json:"changes"`

	// Targets defines the resources to restart when changes are detected
	// +required
	// +kubebuilder:validation:MinItems=1
	Targets []TargetSpec `json:"targets"`
}

// ChangeSpec defines a resource whose changes trigger restarts
type ChangeSpec struct {
	// Kind specifies the kind of resource to watch for changes
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	// +required
	Kind string `json:"kind"`

	// Name specifies the name of the resource to watch
	// +optional
	Name string `json:"name,omitempty"`

	// Selector allows watching resources by label selector
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// ChangeType specifies which types of changes should trigger restarts
	// If not specified, only "Update" changes will trigger restarts
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Items:Enum=Create;Update;Delete
	// +optional
	ChangeType []string `json:"changeType,omitempty"`
}

// TargetSpec defines a resource to restart when changes are detected
type TargetSpec struct {
	// Kind specifies the kind of resource to restart
	// +kubebuilder:validation:Enum=Deployment;StatefulSet
	// +required
	Kind string `json:"kind"`

	// Name specifies the name of the resource to restart
	// +optional
	Name string `json:"name,omitempty"`

	// Selector allows targeting resources by label selector
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// Namespace specifies the namespace of the target resources
	// If not specified, the controller will use the namespace of the RestartRule resource
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// RestartRuleStatus defines the observed state of RestartRule.
type RestartRuleStatus struct {
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// RestartRule is the Schema for the restartrules API
type RestartRule struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of RestartRule
	// +required
	Spec RestartRuleSpec `json:"spec"`

	// status defines the observed state of RestartRule
	// +optional
	Status RestartRuleStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// RestartRuleList contains a list of RestartRule
type RestartRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []RestartRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RestartRule{}, &RestartRuleList{})
}

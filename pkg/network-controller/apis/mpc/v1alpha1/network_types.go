// Copyright (c) 2021 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
//
// The original file created by the Operator SDK has been modified to add Carbyne Stack Ephemeral network controller
// logic.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkSpec defines the desired state of Network
type NetworkSpec struct {
	// Port is the port exposed on the Istio gateway.
	// +optional
	Port int32 `json:"port"`
	// TargetPort is the port of the target pod the traffic is routed to.
	// +optional
	TargetPort int32 `json:"targetPort"`
}

// NetworkStatus defines the observed state of Network
type NetworkStatus struct {
	// +optional
	Ready bool `json:"ready"`
}

// Network is the Schema for the networks API
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=networks,scope=Namespaced
type Network struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec NetworkSpec `json:"spec,omitempty"`
	// +optional
	Status NetworkStatus `json:"status,omitempty"`
}

// NetworkList contains a list of Network
// +kubebuilder:object:root=true
type NetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Network `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Network{}, &NetworkList{})
}

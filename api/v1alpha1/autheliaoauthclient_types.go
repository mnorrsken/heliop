/*
Copyright 2026.

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

// AutheliaOAuthClientSpec defines the desired state of AutheliaOAuthClient.
// It maps to an Authelia OpenID Connect client:
// https://www.authelia.com/configuration/identity-providers/openid-connect/clients/
type AutheliaOAuthClientSpec struct {
	// autheliaRef selects the Authelia instance this client is registered with.
	// +required
	AutheliaRef AutheliaReference `json:"autheliaRef"`

	// clientID is the OIDC client_id. Defaults to the resource name.
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// clientName is a human friendly name shown on consent screens.
	// +optional
	ClientName string `json:"clientName,omitempty"`

	// public marks the client as public (no client secret is generated).
	// +optional
	Public bool `json:"public,omitempty"`

	// authorizationPolicy is the Authelia authorization policy, e.g.
	// "one_factor" or "two_factor".
	// +optional
	// +kubebuilder:default="two_factor"
	AuthorizationPolicy string `json:"authorizationPolicy,omitempty"`

	// consentMode controls the consent behaviour, e.g. "implicit".
	// +optional
	ConsentMode string `json:"consentMode,omitempty"`

	// claimsPolicy selects a configured claims policy.
	// +optional
	ClaimsPolicy string `json:"claimsPolicy,omitempty"`

	// lifespan selects a configured custom lifespan.
	// +optional
	Lifespan string `json:"lifespan,omitempty"`

	// redirectURIs is the list of allowed redirect URIs.
	// +optional
	RedirectURIs []string `json:"redirectURIs,omitempty"`

	// scopes is the list of allowed scopes.
	// +optional
	Scopes []string `json:"scopes,omitempty"`

	// grantTypes is the list of allowed grant types.
	// +optional
	GrantTypes []string `json:"grantTypes,omitempty"`

	// responseTypes is the list of allowed response types.
	// +optional
	ResponseTypes []string `json:"responseTypes,omitempty"`

	// tokenEndpointAuthMethod sets token_endpoint_auth_method.
	// +optional
	TokenEndpointAuthMethod string `json:"tokenEndpointAuthMethod,omitempty"`

	// userinfoSignedResponseAlg sets userinfo_signed_response_alg.
	// +optional
	UserinfoSignedResponseAlg string `json:"userinfoSignedResponseAlg,omitempty"`
}

// AutheliaReference references an Authelia resource.
type AutheliaReference struct {
	// name is the name of the Authelia resource.
	// +required
	Name string `json:"name"`

	// namespace is the namespace of the Authelia resource. Defaults to the
	// AutheliaOAuthClient's own namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// AutheliaOAuthClientStatus defines the observed state of AutheliaOAuthClient.
type AutheliaOAuthClientStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the AutheliaOAuthClient resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// secretName is the name of the Secret in this resource's namespace holding
	// the generated client_id and client_secret.
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="ClientID",type="string",JSONPath=".spec.clientID"
// +kubebuilder:printcolumn:name="Authelia",type="string",JSONPath=".spec.autheliaRef.name"
// +kubebuilder:printcolumn:name="Secret",type="string",JSONPath=".status.secretName"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AutheliaOAuthClient is the Schema for the autheliaoauthclients API
type AutheliaOAuthClient struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AutheliaOAuthClient
	// +required
	Spec AutheliaOAuthClientSpec `json:"spec"`

	// status defines the observed state of AutheliaOAuthClient
	// +optional
	Status AutheliaOAuthClientStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AutheliaOAuthClientList contains a list of AutheliaOAuthClient
type AutheliaOAuthClientList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AutheliaOAuthClient `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutheliaOAuthClient{}, &AutheliaOAuthClientList{})
}

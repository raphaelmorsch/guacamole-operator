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

// DesktopPortalSpec deploys an OpenShift Console dynamic plugin for allocating
// DesktopSessions to users sourced from an identity provider (e.g. Keycloak).
type DesktopPortalSpec struct {
	// DisplayName is shown in the OpenShift Console navigation.
	// +kubebuilder:default="Desktop Sessions"
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// PluginImage is the container image that serves the Console dynamic plugin assets.
	// Defaults to the RELATED_IMAGE_DESKTOP_PORTAL_PLUGIN env on the operator.
	// +optional
	PluginImage string `json:"pluginImage,omitempty"`

	// APIImage is the container image for the portal API (Keycloak + DesktopSession).
	// Defaults to the RELATED_IMAGE_DESKTOP_PORTAL_API env on the operator.
	// +optional
	APIImage string `json:"apiImage,omitempty"`

	// DefaultPool is the DesktopPool used when creating sessions from the UI.
	DefaultPool NamespacedObjectReference `json:"defaultPool"`

	// SessionNamespace is where DesktopSession objects are created.
	// Defaults to defaultPool.namespace, then to this resource's namespace.
	// +optional
	SessionNamespace string `json:"sessionNamespace,omitempty"`

	// Keycloak configures the identity provider used to list users.
	Keycloak KeycloakUserDirectorySpec `json:"keycloak"`

	// EnablePlugin, when true, adds this plugin to consoles.operator.openshift.io/cluster.
	// +kubebuilder:default=true
	// +optional
	EnablePlugin *bool `json:"enablePlugin,omitempty"`

	// NavSection is the Console nav section id (e.g. home, workloads).
	// +kubebuilder:default="home"
	// +optional
	NavSection string `json:"navSection,omitempty"`

	// PluginName overrides the OpenShift ConsolePlugin resource name.
	// Must be unique cluster-wide. Defaults to guac-dp-{namespace}-{name}.
	// Set explicitly (e.g. guacamole-desktop-portal) to preserve an existing Console bookmark/path on upgrade.
	// +optional
	PluginName string `json:"pluginName,omitempty"`

	// ConsolePath overrides the in-console route path (must start with /).
	// Must be unique cluster-wide. Defaults to /guacamole-desktops-{namespace}-{name}.
	// +optional
	ConsolePath string `json:"consolePath,omitempty"`

	// Replicas for plugin and API deployments.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// UserPortal exposes a self-service PatternFly UI outside the Console via Route.
	// End users authenticate with Keycloak (same realm as the user directory / Guacamole OpenID).
	// Omit to keep Console-admin-only (backward compatible).
	// +optional
	UserPortal *DesktopPortalUserPortalSpec `json:"userPortal,omitempty"`

	// AdminGroups grants portal admin APIs (batch allocate, pool config) to these OpenShift groups.
	// Users who can create DesktopSessions in the session namespace are also treated as admins.
	// +optional
	AdminGroups []string `json:"adminGroups,omitempty"`
}

// DesktopPortalUserPortalSpec configures the external self-service portal.
type DesktopPortalUserPortalSpec struct {
	// Enabled deploys the user portal Route when true.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Image is the user-portal container image (nginx + PatternFly SPA).
	// Defaults to RELATED_IMAGE_DESKTOP_USER_PORTAL on the operator.
	// +optional
	Image string `json:"image,omitempty"`

	// Hostname is an optional Route host.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// Issuer is the public Keycloak OIDC issuer URL used by the browser login
	// (e.g. https://keycloak.apps.example.com/realms/guacamole).
	// Required when userPortal is enabled. Must match the iss claim of access tokens.
	// Prefer the public Route issuer (same as Guacamole OpenID), not the in-cluster Service URL.
	Issuer string `json:"issuer"`

	// OIDCClientID is a Keycloak public client (standard flow + PKCE) for the user portal.
	// Do not reuse the confidential admin directory client or the Guacamole client.
	// +kubebuilder:default="guacamole-user-portal"
	// +optional
	OIDCClientID string `json:"oidcClientID,omitempty"`
}

// NamespacedObjectReference references a namespaced Kubernetes object.
type NamespacedObjectReference struct {
	// Name of the referent.
	Name string `json:"name"`

	// Namespace of the referent.
	// Defaults to the DesktopPortal namespace when unset.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// KeycloakUserDirectorySpec configures Keycloak Admin API access for user listing.
type KeycloakUserDirectorySpec struct {
	// URL is the Keycloak base URL (e.g. https://keycloak.apps.example.com).
	URL string `json:"url"`

	// Realm is the Keycloak realm that holds end users.
	Realm string `json:"realm"`

	// ClientID is a confidential client with service-account rights to query users.
	ClientID string `json:"clientID"`

	// ClientSecretRef points to the client secret used for client_credentials.
	ClientSecretRef SecretKeyRef `json:"clientSecretRef"`

	// InsecureSkipVerify disables TLS certificate verification against Keycloak.
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// DesktopPortalPhase represents the lifecycle phase of a DesktopPortal.
type DesktopPortalPhase string

const (
	DesktopPortalPhasePending DesktopPortalPhase = "Pending"
	DesktopPortalPhaseReady   DesktopPortalPhase = "Ready"
	DesktopPortalPhaseFailed  DesktopPortalPhase = "Failed"
)

// DesktopPortalStatus defines the observed state of DesktopPortal.
type DesktopPortalStatus struct {
	// Phase is the current lifecycle phase.
	Phase DesktopPortalPhase `json:"phase,omitempty"`

	// PluginName is the ConsolePlugin resource name.
	// +optional
	PluginName string `json:"pluginName,omitempty"`

	// PluginService is the Service hosting plugin assets.
	// +optional
	PluginService string `json:"pluginService,omitempty"`

	// APIService is the Service backing the portal API proxy.
	// +optional
	APIService string `json:"apiService,omitempty"`

	// ConsolePath is the in-console route path.
	// +optional
	ConsolePath string `json:"consolePath,omitempty"`

	// UserPortalURL is the external self-service portal URL when userPortal is enabled.
	// +optional
	UserPortalURL string `json:"userPortalURL,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=dportal
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Plugin",type=string,JSONPath=`.status.pluginName`
// +kubebuilder:printcolumn:name="UserPortal",type=string,JSONPath=`.status.userPortalURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DesktopPortal deploys an OpenShift Console dynamic plugin for DesktopSession allocation.
type DesktopPortal struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesktopPortalSpec   `json:"spec,omitempty"`
	Status DesktopPortalStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesktopPortalList contains a list of DesktopPortal.
type DesktopPortalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DesktopPortal `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DesktopPortal{}, &DesktopPortalList{})
}

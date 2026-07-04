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

// GuacamoleConnectionSpec defines a Guacamole connection stored in the instance database.
type GuacamoleConnectionSpec struct {
	// GuacamoleRef links this connection to a Guacamole stack instance.
	GuacamoleRef GuacamoleInstanceRef `json:"guacamoleRef"`

	// DisplayName is the connection name shown in the Guacamole UI.
	// Defaults to the resource name when unset.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Protocol is the guacd protocol for this connection.
	// +kubebuilder:validation:Enum=rdp;vnc;ssh;telnet;kubernetes
	Protocol string `json:"protocol"`

	// ParentGroup places the connection inside a connection group by name.
	// Leave empty to create the connection at the root level.
	// +optional
	ParentGroup string `json:"parentGroup,omitempty"`

	// MaxConnections limits concurrent sessions for this connection.
	// +optional
	MaxConnections *int32 `json:"maxConnections,omitempty"`

	// MaxConnectionsPerUser limits concurrent sessions per user.
	// +optional
	MaxConnectionsPerUser *int32 `json:"maxConnectionsPerUser,omitempty"`

	// Proxy optionally overrides the guacd proxy for this connection.
	// +optional
	Proxy *ConnectionProxySpec `json:"proxy,omitempty"`

	// RDP holds protocol parameters when protocol is "rdp".
	// +optional
	RDP *RDPConnectionSpec `json:"rdp,omitempty"`

	// VNC holds protocol parameters when protocol is "vnc".
	// +optional
	VNC *VNCConnectionSpec `json:"vnc,omitempty"`

	// SSH holds protocol parameters when protocol is "ssh".
	// +optional
	SSH *SSHConnectionSpec `json:"ssh,omitempty"`

	// AdditionalParameters adds arbitrary Guacamole connection parameters.
	// +optional
	AdditionalParameters map[string]string `json:"additionalParameters,omitempty"`

	// Permissions grants users or groups access to this connection.
	// +optional
	Permissions []ConnectionPermissionSpec `json:"permissions,omitempty"`
}

// GuacamoleInstanceRef references a Guacamole custom resource.
type GuacamoleInstanceRef struct {
	// Name of the Guacamole resource.
	Name string `json:"name"`

	// Namespace of the Guacamole resource.
	// Defaults to the GuacamoleConnection namespace when unset.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ConnectionProxySpec overrides guacd proxy settings for a connection.
type ConnectionProxySpec struct {
	// Hostname of the guacd instance to use.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// Port of the guacd instance to use.
	// +optional
	Port *int32 `json:"port,omitempty"`

	// Encryption method used by guacd.
	// +kubebuilder:validation:Enum=NONE;SSL
	// +optional
	Encryption string `json:"encryption,omitempty"`
}

// SecretKeyRef references a key in a Kubernetes Secret.
type SecretKeyRef struct {
	// Name of the secret.
	Name string `json:"name"`

	// Key within the secret.
	// +kubebuilder:default="password"
	Key string `json:"key,omitempty"`
}

// RDPConnectionSpec maps to guacamole_connection_parameter rows for RDP.
// See https://guacamole.apache.org/doc/gug/configuring-guacamole.html#rdp
type RDPConnectionSpec struct {
	// Hostname or IP address of the remote desktop server.
	Hostname string `json:"hostname"`

	// Port of the RDP server.
	// +kubebuilder:default=3389
	// +optional
	Port *int32 `json:"port,omitempty"`

	// Username for RDP authentication.
	// +optional
	Username string `json:"username,omitempty"`

	// Password for RDP authentication.
	// Prefer passwordSecretRef in production.
	// +optional
	Password string `json:"password,omitempty"`

	// PasswordSecretRef references a secret containing the RDP password.
	// +optional
	PasswordSecretRef *SecretKeyRef `json:"passwordSecretRef,omitempty"`

	// Domain for RDP authentication.
	// +optional
	Domain string `json:"domain,omitempty"`

	// Security mode for the RDP session.
	// +kubebuilder:default="nla"
	// +kubebuilder:validation:Enum=any;nla;nla-ext;tls;vmconnect;rdp
	// +optional
	Security string `json:"security,omitempty"`

	// IgnoreCert accepts self-signed or invalid RDP certificates.
	// +optional
	IgnoreCert *bool `json:"ignoreCert,omitempty"`

	// Timeout in seconds while connecting to the RDP server.
	// +optional
	Timeout *int32 `json:"timeout,omitempty"`

	// Width of the remote display in pixels.
	// +optional
	Width *int32 `json:"width,omitempty"`

	// Height of the remote display in pixels.
	// +optional
	Height *int32 `json:"height,omitempty"`

	// DPI of the remote display.
	// +optional
	Dpi *int32 `json:"dpi,omitempty"`

	// ColorDepth of the remote display.
	// +optional
	ColorDepth *int32 `json:"colorDepth,omitempty"`

	// ResizeMethod controls display resizing behavior.
	// +kubebuilder:validation:Enum=reconnect;display-update;off
	// +optional
	ResizeMethod string `json:"resizeMethod,omitempty"`

	// ServerLayout selects the keyboard layout sent to the server.
	// +optional
	ServerLayout string `json:"serverLayout,omitempty"`

	// Console connects to the administrative console session.
	// +optional
	Console *bool `json:"console,omitempty"`

	// InitialProgram starts a specific program instead of the desktop shell.
	// +optional
	InitialProgram string `json:"initialProgram,omitempty"`

	// ClientName sent to the RDP server.
	// +optional
	ClientName string `json:"clientName,omitempty"`

	// Timezone for the remote desktop session.
	// +optional
	Timezone string `json:"timezone,omitempty"`

	// EnableDrive enables drive redirection.
	// +optional
	EnableDrive *bool `json:"enableDrive,omitempty"`

	// DriveName is the name of the redirected drive.
	// +optional
	DriveName string `json:"driveName,omitempty"`

	// DrivePath is the path exposed through drive redirection.
	// +optional
	DrivePath string `json:"drivePath,omitempty"`

	// CreateDrivePath creates the drive path automatically.
	// +optional
	CreateDrivePath *bool `json:"createDrivePath,omitempty"`

	// EnablePrinting enables printer redirection.
	// +optional
	EnablePrinting *bool `json:"enablePrinting,omitempty"`

	// PrinterName is the redirected printer name.
	// +optional
	PrinterName string `json:"printerName,omitempty"`

	// DisableAudio disables audio output.
	// +optional
	DisableAudio *bool `json:"disableAudio,omitempty"`

	// EnableAudioInput enables microphone redirection.
	// +optional
	EnableAudioInput *bool `json:"enableAudioInput,omitempty"`

	// GatewayHostname is the hostname of the RDP gateway.
	// +optional
	GatewayHostname string `json:"gatewayHostname,omitempty"`

	// GatewayPort is the port of the RDP gateway.
	// +optional
	GatewayPort *int32 `json:"gatewayPort,omitempty"`

	// GatewayUsername is the username for the RDP gateway.
	// +optional
	GatewayUsername string `json:"gatewayUsername,omitempty"`

	// GatewayPassword is the password for the RDP gateway.
	// +optional
	GatewayPassword string `json:"gatewayPassword,omitempty"`

	// GatewayPasswordSecretRef references the gateway password secret.
	// +optional
	GatewayPasswordSecretRef *SecretKeyRef `json:"gatewayPasswordSecretRef,omitempty"`

	// GatewayDomain is the domain for the RDP gateway.
	// +optional
	GatewayDomain string `json:"gatewayDomain,omitempty"`

	// RemoteApp launches a RemoteApp instead of the full desktop.
	// +optional
	RemoteApp string `json:"remoteApp,omitempty"`

	// RemoteAppDir is the working directory for the RemoteApp.
	// +optional
	RemoteAppDir string `json:"remoteAppDir,omitempty"`

	// RemoteAppArgs are arguments passed to the RemoteApp.
	// +optional
	RemoteAppArgs string `json:"remoteAppArgs,omitempty"`

	// EnableSFTP enables the SFTP side channel.
	// +optional
	EnableSFTP *bool `json:"enableSftp,omitempty"`

	// EnableWallpaper enables the remote wallpaper.
	// +optional
	EnableWallpaper *bool `json:"enableWallpaper,omitempty"`

	// EnableTheming enables window theming on the remote desktop.
	// +optional
	EnableTheming *bool `json:"enableTheming,omitempty"`

	// EnableFontSmoothing enables font smoothing.
	// +optional
	EnableFontSmoothing *bool `json:"enableFontSmoothing,omitempty"`

	// DisableBitmapCaching disables bitmap caching.
	// +optional
	DisableBitmapCaching *bool `json:"disableBitmapCaching,omitempty"`

	// DisableOffscreenCaching disables offscreen caching.
	// +optional
	DisableOffscreenCaching *bool `json:"disableOffscreenCaching,omitempty"`

	// DisableGlyphCaching disables glyph caching.
	// +optional
	DisableGlyphCaching *bool `json:"disableGlyphCaching,omitempty"`
}

// VNCConnectionSpec maps to guacamole_connection_parameter rows for VNC.
type VNCConnectionSpec struct {
	// Hostname or IP address of the VNC server.
	Hostname string `json:"hostname"`

	// Port of the VNC server.
	// +kubebuilder:default=5900
	// +optional
	Port *int32 `json:"port,omitempty"`

	// Password for VNC authentication.
	// +optional
	Password string `json:"password,omitempty"`

	// PasswordSecretRef references a secret containing the VNC password.
	// +optional
	PasswordSecretRef *SecretKeyRef `json:"passwordSecretRef,omitempty"`

	// ReadOnly opens the session in read-only mode.
	// +optional
	ReadOnly *bool `json:"readOnly,omitempty"`

	// SwapRedBlue swaps red and blue color channels.
	// +optional
	SwapRedBlue *bool `json:"swapRedBlue,omitempty"`

	// Cursor selects the local cursor rendering mode.
	// +kubebuilder:validation:Enum=remote;local
	// +optional
	Cursor string `json:"cursor,omitempty"`

	// ColorDepth of the remote display.
	// +optional
	ColorDepth *int32 `json:"colorDepth,omitempty"`

	// Width of the remote display in pixels.
	// +optional
	Width *int32 `json:"width,omitempty"`

	// Height of the remote display in pixels.
	// +optional
	Height *int32 `json:"height,omitempty"`

	// Dpi of the remote display.
	// +optional
	Dpi *int32 `json:"dpi,omitempty"`
}

// SSHConnectionSpec maps to guacamole_connection_parameter rows for SSH.
type SSHConnectionSpec struct {
	// Hostname or IP address of the SSH server.
	Hostname string `json:"hostname"`

	// Port of the SSH server.
	// +kubebuilder:default=22
	// +optional
	Port *int32 `json:"port,omitempty"`

	// Username for SSH authentication.
	// +optional
	Username string `json:"username,omitempty"`

	// Password for SSH authentication.
	// +optional
	Password string `json:"password,omitempty"`

	// PasswordSecretRef references a secret containing the SSH password.
	// +optional
	PasswordSecretRef *SecretKeyRef `json:"passwordSecretRef,omitempty"`

	// PrivateKey for key-based authentication.
	// +optional
	PrivateKey string `json:"privateKey,omitempty"`

	// PrivateKeySecretRef references a secret containing the SSH private key.
	// +optional
	PrivateKeySecretRef *SecretKeyRef `json:"privateKeySecretRef,omitempty"`

	// Passphrase for the private key.
	// +optional
	Passphrase string `json:"passphrase,omitempty"`

	// PassphraseSecretRef references a secret containing the key passphrase.
	// +optional
	PassphraseSecretRef *SecretKeyRef `json:"passphraseSecretRef,omitempty"`

	// Command executed after the SSH session starts.
	// +optional
	Command string `json:"command,omitempty"`

	// ColorScheme for the terminal emulator.
	// +kubebuilder:validation:Enum=gray-black;green-black;white-black
	// +optional
	ColorScheme string `json:"colorScheme,omitempty"`

	// FontName used by the terminal emulator.
	// +optional
	FontName string `json:"fontName,omitempty"`

	// FontSize used by the terminal emulator.
	// +optional
	FontSize *int32 `json:"fontSize,omitempty"`
}

// ConnectionPermissionSpec maps to guacamole_connection_permission rows.
type ConnectionPermissionSpec struct {
	// EntityName is the Guacamole user or group name.
	EntityName string `json:"entityName"`

	// EntityType is USER or USER_GROUP.
	// +kubebuilder:default="USER"
	// +kubebuilder:validation:Enum=USER;USER_GROUP
	// +optional
	EntityType string `json:"entityType,omitempty"`

	// Permission granted on the connection.
	// +kubebuilder:default="READ"
	// +kubebuilder:validation:Enum=READ;UPDATE;DELETE;ADMINISTER
	// +optional
	Permission string `json:"permission,omitempty"`
}

// GuacamoleConnectionPhase represents the lifecycle phase of a connection.
type GuacamoleConnectionPhase string

const (
	GuacamoleConnectionPhasePending GuacamoleConnectionPhase = "Pending"
	GuacamoleConnectionPhaseReady   GuacamoleConnectionPhase = "Ready"
	GuacamoleConnectionPhaseFailed  GuacamoleConnectionPhase = "Failed"
)

// GuacamoleConnectionStatus defines the observed state of GuacamoleConnection.
type GuacamoleConnectionStatus struct {
	// Phase is the current lifecycle phase.
	Phase GuacamoleConnectionPhase `json:"phase,omitempty"`

	// ConnectionID is the guacamole_connection.connection_id value.
	ConnectionID int64 `json:"connectionID,omitempty"`

	// Conditions represent the latest observations of the connection state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Instance",type=string,JSONPath=`.spec.guacamoleRef.name`
// +kubebuilder:printcolumn:name="Protocol",type=string,JSONPath=`.spec.protocol`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="ConnectionID",type=integer,JSONPath=`.status.connectionID`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GuacamoleConnection is the Schema for the guacamoleconnections API.
type GuacamoleConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GuacamoleConnectionSpec   `json:"spec,omitempty"`
	Status GuacamoleConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GuacamoleConnectionList contains a list of GuacamoleConnection.
type GuacamoleConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GuacamoleConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GuacamoleConnection{}, &GuacamoleConnectionList{})
}

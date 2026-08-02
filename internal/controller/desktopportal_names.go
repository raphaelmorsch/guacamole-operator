/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

    10|Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const (
	// legacyPortalPluginName is the pre-multi-portal ConsolePlugin name.
	// Kept for migration cleanup when a portal previously owned this singleton.
	legacyPortalPluginName  = "guacamole-desktop-portal"
	legacyPortalConsolePath = "/guacamole-desktops"
)

type portalNames struct {
	Plugin                       string
	ConsolePath                  string
	PluginService                string
	PluginDeployment             string
	APIService                   string
	APIDeployment                string
	APIServiceAccount            string
	SessionRole                  string
	SessionRoleBinding           string
	GuacamoleRole                string
	GuacamoleRoleBinding         string
	UserPortalDeployment         string
	UserPortalService            string
	UserPortalRoute              string
	UserPortalOAuthSA            string
	UserPortalCookieSecret       string
	AuthReviewClusterRole        string
	AuthReviewClusterRoleBinding string
}

func portalResourceNames(portal *guacamolev1alpha1.DesktopPortal) portalNames {
	base := fmt.Sprintf("%s-portal", portal.Name)
	plugin := strings.TrimSpace(portal.Spec.PluginName)
	if plugin == "" {
		plugin = uniqueDNS1123("guac-dp", portal.Namespace, portal.Name)
	} else {
		plugin = sanitizeDNS1123(plugin, 63)
	}
	path := strings.TrimSpace(portal.Spec.ConsolePath)
	if path == "" {
		path = "/" + uniqueDNS1123("guacamole-desktops", portal.Namespace, portal.Name)
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// Cluster-scoped RBAC must include namespace to avoid collisions across NS.
	authReview := uniqueDNS1123(portal.Namespace, portal.Name, "portal-authreview")
	return portalNames{
		Plugin:                       plugin,
		ConsolePath:                  path,
		PluginService:                base + "-plugin",
		PluginDeployment:             base + "-plugin",
		APIService:                   base + "-api",
		APIDeployment:                base + "-api",
		APIServiceAccount:            base + "-api",
		SessionRole:                  base + "-sessions",
		SessionRoleBinding:           base + "-sessions",
		GuacamoleRole:                base + "-guacamole",
		GuacamoleRoleBinding:         base + "-guacamole",
		UserPortalDeployment:         base + "-user",
		UserPortalService:            base + "-user",
		UserPortalRoute:              base + "-user",
		UserPortalOAuthSA:            base + "-user-oauth",
		UserPortalCookieSecret:       base + "-user-proxy",
		AuthReviewClusterRole:        authReview,
		AuthReviewClusterRoleBinding: authReview,
	}
}

func portalOwnerLabels(portal *guacamolev1alpha1.DesktopPortal) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by":          "guacamole-operator",
		"desktop.guacamole.io/portal":           portal.Name,
		"desktop.guacamole.io/portal-namespace": portal.Namespace,
	}
}

// uniqueDNS1123 builds a DNS-1123 label from parts, hashing the suffix when needed to stay ≤63 chars.
func uniqueDNS1123(parts ...string) string {
	joined := strings.Join(parts, "-")
	full := sanitizeDNS1123(joined, 253)
	if len(full) <= 63 {
		return full
	}
	sum := sha1.Sum([]byte(joined))
	suffix := hex.EncodeToString(sum[:4]) // 8 hex chars
	baseMax := 63 - 1 - len(suffix)
	base := sanitizeDNS1123(joined, baseMax)
	return sanitizeDNS1123(base+"-"+suffix, 63)
}

func sanitizeDNS1123(in string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 63
	}
	out := strings.ToLower(strings.TrimSpace(in))
	var b strings.Builder
	for _, r := range out {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		s = "portal"
	}
	if len(s) > maxLen {
		s = strings.Trim(s[:maxLen], "-")
	}
	if s == "" {
		s = "portal"
	}
	return s
}

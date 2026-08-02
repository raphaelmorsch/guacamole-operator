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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

func unstructuredFrom(obj map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: obj}
}

func TestPortalResourceNamesUniqueAcrossPortals(t *testing.T) {
	a := &guacamolev1alpha1.DesktopPortal{
		ObjectMeta: metav1.ObjectMeta{Name: "desktop-portal", Namespace: "team-a"},
	}
	b := &guacamolev1alpha1.DesktopPortal{
		ObjectMeta: metav1.ObjectMeta{Name: "desktop-portal", Namespace: "team-b"},
	}
	na := portalResourceNames(a)
	nb := portalResourceNames(b)

	if na.Plugin == nb.Plugin {
		t.Fatalf("expected distinct ConsolePlugin names, both %q", na.Plugin)
	}
	if na.ConsolePath == nb.ConsolePath {
		t.Fatalf("expected distinct console paths, both %q", na.ConsolePath)
	}
	if na.AuthReviewClusterRole == nb.AuthReviewClusterRole {
		t.Fatalf("expected distinct ClusterRole names, both %q", na.AuthReviewClusterRole)
	}
	if na.Plugin == legacyPortalPluginName {
		t.Fatalf("default plugin name should not be the legacy singleton %q", legacyPortalPluginName)
	}
	if !strings.HasPrefix(na.ConsolePath, "/") {
		t.Fatalf("console path must start with /, got %q", na.ConsolePath)
	}
	if len(na.Plugin) > 63 || len(na.AuthReviewClusterRole) > 63 {
		t.Fatalf("DNS names exceed 63 chars: plugin=%q auth=%q", na.Plugin, na.AuthReviewClusterRole)
	}
}

func TestPortalResourceNamesExplicitOverrides(t *testing.T) {
	portal := &guacamolev1alpha1.DesktopPortal{
		ObjectMeta: metav1.ObjectMeta{Name: "desktop-portal", Namespace: "guacamole-desktops"},
		Spec: guacamolev1alpha1.DesktopPortalSpec{
			PluginName:  legacyPortalPluginName,
			ConsolePath: legacyPortalConsolePath,
		},
	}
	names := portalResourceNames(portal)
	if names.Plugin != legacyPortalPluginName {
		t.Fatalf("plugin=%q want %q", names.Plugin, legacyPortalPluginName)
	}
	if names.ConsolePath != legacyPortalConsolePath {
		t.Fatalf("path=%q want %q", names.ConsolePath, legacyPortalConsolePath)
	}
}

func TestUniqueDNS1123TruncatesWithHash(t *testing.T) {
	longNS := strings.Repeat("n", 40)
	longName := strings.Repeat("p", 40)
	got := uniqueDNS1123("guac-dp", longNS, longName)
	if len(got) > 63 {
		t.Fatalf("len=%d value=%q", len(got), got)
	}
	if got == "" {
		t.Fatal("empty dns name")
	}
	other := uniqueDNS1123("guac-dp", longNS, longName+"x")
	if got == other {
		t.Fatalf("expected hash to differentiate long names: %q", got)
	}
}

func TestConsolePluginOwnedBy(t *testing.T) {
	portal := &guacamolev1alpha1.DesktopPortal{
		ObjectMeta: metav1.ObjectMeta{Name: "desktop-portal", Namespace: "ns-a"},
	}
	names := portalResourceNames(portal)

	labeled := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"desktop.guacamole.io/portal":           "desktop-portal",
				"desktop.guacamole.io/portal-namespace": "ns-a",
			},
		},
	}
	cp := unstructuredFrom(labeled)
	if !consolePluginOwnedBy(cp, portal, names) {
		t.Fatal("expected ownership via labels")
	}

	byService := map[string]interface{}{
		"spec": map[string]interface{}{
			"backend": map[string]interface{}{
				"service": map[string]interface{}{
					"name":      names.PluginService,
					"namespace": "ns-a",
				},
			},
		},
	}
	cp2 := unstructuredFrom(byService)
	if !consolePluginOwnedBy(cp2, portal, names) {
		t.Fatal("expected ownership via backend service")
	}

	other := map[string]interface{}{
		"spec": map[string]interface{}{
			"backend": map[string]interface{}{
				"service": map[string]interface{}{
					"name":      "other-portal-plugin",
					"namespace": "ns-b",
				},
			},
		},
	}
	cp3 := unstructuredFrom(other)
	if consolePluginOwnedBy(cp3, portal, names) {
		t.Fatal("did not expect ownership of unrelated plugin")
	}
}

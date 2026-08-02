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

package main

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestMapUxPhase(t *testing.T) {
	cases := []struct {
		phase, conn, desktop, want string
	}{
		{"Pending", "", "", "Provisioning"},
		{"Queued", "", "", "Provisioning"},
		{"Ready", "None", "vm-1", "Ready"},
		{"Ready", "Connected", "vm-1", "InUse"},
		{"InUse", "Connected", "vm-1", "InUse"},
		{"Disconnected", "Disconnected", "vm-1", "Disconnected"},
		{"Released", "", "", "Released"},
		{"Failed", "", "", "Failed"},
	}
	for _, tc := range cases {
		got := mapUxPhase(tc.phase, tc.conn, tc.desktop)
		if got != tc.want {
			t.Fatalf("mapUxPhase(%q,%q,%q)=%q want %q", tc.phase, tc.conn, tc.desktop, got, tc.want)
		}
	}
}

func TestGuacamoleConnectURL(t *testing.T) {
	url := guacamoleConnectURL("https://guac.example.com/guacamole/", 42)
	if !strings.HasPrefix(url, "https://guac.example.com/guacamole/#/client/") {
		t.Fatalf("unexpected url prefix: %s", url)
	}
	hash := strings.TrimPrefix(url, "https://guac.example.com/guacamole/#/client/")
	raw, err := base64.RawStdEncoding.DecodeString(hash)
	if err != nil {
		padded := hash
		for len(padded)%4 != 0 {
			padded += "="
		}
		raw, err = base64.StdEncoding.DecodeString(padded)
	}
	if err != nil {
		t.Fatalf("decode hash: %v", err)
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 || parts[0] != "42" || parts[1] != "c" || parts[2] != "mysql" {
		t.Fatalf("payload parts=%v", parts)
	}
	if guacamoleConnectURL("", 1) != "" || guacamoleConnectURL("https://x", 0) != "" {
		t.Fatalf("expected empty connect URL for incomplete inputs")
	}
}

func TestSessionPoolRef(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"namespace": "sessions"},
		"spec": map[string]interface{}{
			"poolRef": map[string]interface{}{"name": "pool-b"},
		},
	}}
	ns, name := sessionPoolRef(obj, "fallback-ns", "pool-a")
	if ns != "sessions" || name != "pool-b" {
		t.Fatalf("got %s/%s want sessions/pool-b", ns, name)
	}

	empty := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"namespace": "sessions"},
		"spec":     map[string]interface{}{},
	}}
	ns, name = sessionPoolRef(empty, "fallback-ns", "pool-a")
	if ns != "sessions" || name != "pool-a" {
		t.Fatalf("got %s/%s want sessions/pool-a", ns, name)
	}
}

func TestResolvePoolName(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/pool?poolName=pool-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolvePoolName(req, "pool-a"); got != "pool-b" {
		t.Fatalf("got %q want pool-b", got)
	}
	req2, _ := http.NewRequest(http.MethodGet, "/pool", nil)
	if got := resolvePoolName(req2, "pool-a"); got != "pool-a" {
		t.Fatalf("got %q want pool-a", got)
	}
}

func TestFilterSessionsByPool(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "s1"},
			"spec":     map[string]interface{}{"poolRef": map[string]interface{}{"name": "pool-a"}},
		}},
		{Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "s2"},
			"spec":     map[string]interface{}{"poolRef": map[string]interface{}{"name": "pool-b"}},
		}},
		{Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "s3"},
			"spec":     map[string]interface{}{"poolRef": map[string]interface{}{"name": "pool-a"}},
		}},
	}
	got := filterSessionsByPool(items, "pool-a")
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if sessionPoolRefName(&got[0]) != "pool-a" || sessionPoolRefName(&got[1]) != "pool-a" {
		t.Fatalf("unexpected pool refs")
	}
	if len(filterSessionsByPool(items, "")) != 3 {
		t.Fatal("empty poolName should keep all")
	}
}

func TestPortalSessionNameAndSelector(t *testing.T) {
	portal := desktopPortalRef{Name: "portal-a", Namespace: "team-a"}
	name := portalSessionName(portal, "alice.smith", "windows-desktop")
	if !strings.Contains(name, "windows-desktop") {
		t.Fatalf("unexpected session name %q", name)
	}
	if len(name) > 63 {
		t.Fatalf("session name too long: %q", name)
	}
	sel := portalSessionLabelSelector(portal)
	if !strings.Contains(sel, "desktop.guacamole.io/portal=portal-a") {
		t.Fatalf("selector missing portal: %q", sel)
	}
	if !strings.Contains(sel, "desktop.guacamole.io/portal-namespace=team-a") {
		t.Fatalf("selector missing portal-namespace: %q", sel)
	}

	other := desktopPortalRef{Name: "portal-b", Namespace: "team-a"}
	if portalSessionName(portal, "alice", "pool-a") == portalSessionName(other, "alice", "pool-a") {
		t.Fatal("expected distinct session names per portal")
	}
	if portalSessionName(portal, "alice", "pool-a") == portalSessionName(portal, "alice", "pool-b") {
		t.Fatal("expected distinct session names per pool")
	}
}

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

package main

import (
	"encoding/base64"
	"strings"
	"testing"
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
		// Also accept padded std encoding trimmed in builder — try Std with pad.
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

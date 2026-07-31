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
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type configResponse struct {
	DisplayName      string `json:"displayName"`
	SessionNamespace string `json:"sessionNamespace"`
	PoolName         string `json:"poolName"`
	PoolNamespace    string `json:"poolNamespace"`
	PluginName       string `json:"pluginName"`
}

type keycloakUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Enabled   bool   `json:"enabled"`
}

type createSessionRequest struct {
	Subject     string `json:"subject"`
	PoolName    string `json:"poolName,omitempty"`
	SessionName string `json:"sessionName,omitempty"`
}

type tokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func main() {
	addr := envOr("LISTEN_ADDR", ":8080")
	cfg := configResponse{
		DisplayName:      envOr("DISPLAY_NAME", "Desktop Sessions"),
		SessionNamespace: mustEnv("SESSION_NAMESPACE"),
		PoolName:         mustEnv("POOL_NAME"),
		PoolNamespace:    envOr("POOL_NAMESPACE", mustEnv("SESSION_NAMESPACE")),
		PluginName:       envOr("PLUGIN_NAME", "guacamole-desktop-portal"),
	}

	kcURL := strings.TrimRight(mustEnv("KEYCLOAK_URL"), "/")
	kcRealm := mustEnv("KEYCLOAK_REALM")
	kcClientID := mustEnv("KEYCLOAK_CLIENT_ID")
	kcSecret := mustEnv("KEYCLOAK_CLIENT_SECRET")
	kcInsecure := strings.EqualFold(os.Getenv("KEYCLOAK_INSECURE_SKIP_VERIFY"), "true")

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("kubernetes config: %v", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		log.Fatalf("dynamic client: %v", err)
	}
	sessionsGVR := schema.GroupVersionResource{
		Group:    "guacamole.guacamole.io",
		Version:  "v1alpha1",
		Resource: "desktopsessions",
	}

	httpClient := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: kcInsecure}, //nolint:gosec // optional for lab IdPs
		},
	}
	tokens := &tokenCache{}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, cfg)
	})
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		search := r.URL.Query().Get("search")
		users, err := listKeycloakUsers(r.Context(), httpClient, tokens, kcURL, kcRealm, kcClientID, kcSecret, search)
		if err != nil {
			log.Printf("list users: %v", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, users)
	})
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := dyn.Resource(sessionsGVR).Namespace(cfg.SessionNamespace).List(r.Context(), metav1.ListOptions{})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, list.Items)
		case http.MethodPost:
			var req createSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON body", http.StatusBadRequest)
				return
			}
			req.Subject = strings.TrimSpace(req.Subject)
			if req.Subject == "" {
				http.Error(w, "subject is required", http.StatusBadRequest)
				return
			}
			poolName := req.PoolName
			if poolName == "" {
				poolName = cfg.PoolName
			}
			name := req.SessionName
			if name == "" {
				name = sanitizeName(fmt.Sprintf("desktop-session-%s", req.Subject))
			}
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "guacamole.guacamole.io/v1alpha1",
					"kind":       "DesktopSession",
					"metadata": map[string]interface{}{
						"name":      name,
						"namespace": cfg.SessionNamespace,
						"labels": map[string]interface{}{
							"desktop.guacamole.io/managed-by": "desktop-portal",
							"desktop.guacamole.io/requester":  sanitizeLabel(req.Subject),
						},
					},
					"spec": map[string]interface{}{
						"poolRef": map[string]interface{}{
							"name": poolName,
						},
						"requester": map[string]interface{}{
							"subject": req.Subject,
						},
					},
				},
			}
			created, err := dyn.Resource(sessionsGVR).Namespace(cfg.SessionNamespace).Create(r.Context(), obj, metav1.CreateOptions{})
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeJSON(w, created.Object)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/sessions/")
		name = strings.Trim(name, "/")
		if name == "" {
			http.Error(w, "session name required", http.StatusBadRequest)
			return
		}
		err := dyn.Resource(sessionsGVR).Namespace(cfg.SessionNamespace).Delete(r.Context(), name, metav1.DeleteOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Ensure typed import used (namespaced get for readiness of pool optional)
	_ = types.NamespacedName{}

	log.Printf("desktop portal API listening on %s (namespace=%s pool=%s)", addr, cfg.SessionNamespace, cfg.PoolName)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func listKeycloakUsers(
	ctx context.Context,
	httpClient *http.Client,
	tokens *tokenCache,
	baseURL, realm, clientID, clientSecret, search string,
) ([]keycloakUser, error) {
	token, err := getKeycloakToken(ctx, httpClient, tokens, baseURL, realm, clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users", baseURL, url.PathEscape(realm))
	q := url.Values{}
	q.Set("max", "50")
	if search != "" {
		q.Set("search", search)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("keycloak users: %s: %s", resp.Status, string(body))
	}
	var users []keycloakUser
	if err := json.Unmarshal(body, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func getKeycloakToken(
	ctx context.Context,
	httpClient *http.Client,
	tokens *tokenCache,
	baseURL, realm, clientID, clientSecret string,
) (string, error) {
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	if tokens.token != "" && time.Now().Before(tokens.expiresAt.Add(-30*time.Second)) {
		return tokens.token, nil
	}
	endpoint := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", baseURL, url.PathEscape(realm))
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("keycloak token: %s: %s", resp.Status, string(body))
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.AccessToken == "" {
		return "", fmt.Errorf("keycloak token response missing access_token")
	}
	tokens.token = parsed.AccessToken
	exp := parsed.ExpiresIn
	if exp <= 0 {
		exp = 60
	}
	tokens.expiresAt = time.Now().Add(time.Duration(exp) * time.Second)
	return tokens.token, nil
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env %s is empty", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func sanitizeName(in string) string {
	out := strings.ToLower(in)
	var b strings.Builder
	for _, r := range out {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "desktop-session"
	}
	if len(s) > 63 {
		s = s[:63]
		s = strings.Trim(s, "-")
	}
	return s
}

func sanitizeLabel(in string) string {
	s := sanitizeName(in)
	if len(s) > 63 {
		return s[:63]
	}
	return s
}

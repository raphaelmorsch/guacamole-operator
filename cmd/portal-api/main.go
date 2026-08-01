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

type createBatchSessionRequest struct {
	Subjects []string `json:"subjects"`
	PoolName string   `json:"poolName,omitempty"`
}

type deleteBatchSessionRequest struct {
	Names []string `json:"names"`
}

type batchSessionResult struct {
	Subject string                 `json:"subject"`
	Name    string                 `json:"name,omitempty"`
	Status  string                 `json:"status"` // created | exists | deleted | error
	Error   string                 `json:"error,omitempty"`
	Object  map[string]interface{} `json:"object,omitempty"`
}

type tokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

type poolPowerManagementView struct {
	Enabled     bool  `json:"enabled"`
	IdleSeconds int64 `json:"idleSeconds"`
}

type poolDesktopView struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type poolStatusResponse struct {
	Name              string                  `json:"name"`
	Namespace         string                  `json:"namespace"`
	Phase             string                  `json:"phase,omitempty"`
	Desired           int64                   `json:"desired"`
	Available         int64                   `json:"available"`
	Allocated         int64                   `json:"allocated"`
	Stopped           int64                   `json:"stopped"`
	Provisioning      int64                   `json:"provisioning"`
	Failed            int64                   `json:"failed"`
	Replicas          int32                   `json:"replicas"`
	MinReady          int32                   `json:"minReady"`
	RecyclePolicy     string                  `json:"recyclePolicy"`
	CreateConnections bool                    `json:"createConnections"`
	PowerManagement   poolPowerManagementView `json:"powerManagement"`
	Desktops          []poolDesktopView       `json:"desktops,omitempty"`
}

// poolConfigUpdate is the subset of DesktopPoolSpec editable from the portal.
type poolConfigUpdate struct {
	Replicas          *int32 `json:"replicas,omitempty"`
	MinReady          *int32 `json:"minReady,omitempty"`
	RecyclePolicy     string `json:"recyclePolicy,omitempty"`
	CreateConnections *bool  `json:"createConnections,omitempty"`
	PowerManagement   *struct {
		Enabled     *bool  `json:"enabled,omitempty"`
		IdleSeconds *int64 `json:"idleSeconds,omitempty"`
	} `json:"powerManagement,omitempty"`
}

type guacamoleOpenIDView struct {
	Configured        bool   `json:"configured"`
	Enabled           bool   `json:"enabled"`
	Issuer            string `json:"issuer,omitempty"`
	ClientID          string `json:"clientID,omitempty"`
	UsernameClaimType string `json:"usernameClaimType,omitempty"`
	Scope             string `json:"scope,omitempty"`
	ExtensionPriority string `json:"extensionPriority,omitempty"`
	RedirectURI       string `json:"redirectURI,omitempty"`
}

type guacamoleStatusResponse struct {
	Name          string              `json:"name"`
	Namespace     string              `json:"namespace"`
	Phase         string              `json:"phase,omitempty"`
	RouteURL      string              `json:"routeURL,omitempty"`
	Replicas      int32               `json:"replicas"`
	GuacdReplicas int32               `json:"guacdReplicas"`
	LogLevel      string              `json:"logLevel"`
	RouteEnabled  bool                `json:"routeEnabled"`
	OpenID        guacamoleOpenIDView `json:"openID"`
}

type guacamoleConfigUpdate struct {
	Replicas      *int32 `json:"replicas,omitempty"`
	GuacdReplicas *int32 `json:"guacdReplicas,omitempty"`
	LogLevel      string `json:"logLevel,omitempty"`
	RouteEnabled  *bool  `json:"routeEnabled,omitempty"`
	OpenID        *struct {
		Enabled           *bool  `json:"enabled,omitempty"`
		UsernameClaimType string `json:"usernameClaimType,omitempty"`
		Scope             string `json:"scope,omitempty"`
		ExtensionPriority string `json:"extensionPriority,omitempty"`
	} `json:"openID,omitempty"`
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
	poolsGVR := schema.GroupVersionResource{
		Group:    "guacamole.guacamole.io",
		Version:  "v1alpha1",
		Resource: "desktoppools",
	}
	guacamolesGVR := schema.GroupVersionResource{
		Group:    "guacamole.guacamole.io",
		Version:  "v1alpha1",
		Resource: "guacamoles",
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
	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			view, err := getPoolStatus(r.Context(), dyn, poolsGVR, cfg.PoolNamespace, cfg.PoolName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, view)
		case http.MethodPut, http.MethodPatch:
			var req poolConfigUpdate
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON body", http.StatusBadRequest)
				return
			}
			if err := updatePoolConfig(r.Context(), dyn, poolsGVR, cfg.PoolNamespace, cfg.PoolName, req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			view, err := getPoolStatus(r.Context(), dyn, poolsGVR, cfg.PoolNamespace, cfg.PoolName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, view)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/guacamole", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			view, err := getGuacamoleStatus(r.Context(), dyn, poolsGVR, guacamolesGVR, cfg.PoolNamespace, cfg.PoolName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, view)
		case http.MethodPut, http.MethodPatch:
			var req guacamoleConfigUpdate
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON body", http.StatusBadRequest)
				return
			}
			if err := updateGuacamoleConfig(r.Context(), dyn, poolsGVR, guacamolesGVR, cfg.PoolNamespace, cfg.PoolName, req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			view, err := getGuacamoleStatus(r.Context(), dyn, poolsGVR, guacamolesGVR, cfg.PoolNamespace, cfg.PoolName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, view)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/pool/wake", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := setPoolPowerRequest(r.Context(), dyn, poolsGVR, cfg.PoolNamespace, cfg.PoolName, "wake"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "accepted", "action": "wake"})
	})
	mux.HandleFunc("/pool/suspend", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := setPoolPowerRequest(r.Context(), dyn, poolsGVR, cfg.PoolNamespace, cfg.PoolName, "suspend"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "accepted", "action": "suspend"})
	})
	mux.HandleFunc("/sessions/batch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req createBatchSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		subjects := uniqueSubjects(req.Subjects)
		if len(subjects) == 0 {
			http.Error(w, "subjects is required", http.StatusBadRequest)
			return
		}
		poolName := req.PoolName
		if poolName == "" {
			poolName = cfg.PoolName
		}
		results := make([]batchSessionResult, 0, len(subjects))
		for _, subject := range subjects {
			created, name, err := createDesktopSession(r.Context(), dyn, sessionsGVR, cfg.SessionNamespace, poolName, subject, "")
			if err != nil {
				status := "error"
				if strings.Contains(strings.ToLower(err.Error()), "already exists") {
					status = "exists"
				}
				results = append(results, batchSessionResult{
					Subject: subject,
					Name:    name,
					Status:  status,
					Error:   err.Error(),
				})
				continue
			}
			results = append(results, batchSessionResult{
				Subject: subject,
				Name:    name,
				Status:  "created",
				Object:  created.Object,
			})
		}
		writeJSON(w, map[string]interface{}{
			"results": results,
			"created": countBatchStatus(results, "created"),
			"exists":  countBatchStatus(results, "exists"),
			"errors":  countBatchStatus(results, "error"),
		})
	})
	mux.HandleFunc("/sessions/batch-delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req deleteBatchSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		names := uniqueSubjects(req.Names)
		if len(names) == 0 {
			http.Error(w, "names is required", http.StatusBadRequest)
			return
		}
		results := make([]batchSessionResult, 0, len(names))
		for _, name := range names {
			err := dyn.Resource(sessionsGVR).Namespace(cfg.SessionNamespace).Delete(r.Context(), name, metav1.DeleteOptions{})
			if err != nil {
				results = append(results, batchSessionResult{
					Name:   name,
					Status: "error",
					Error:  err.Error(),
				})
				continue
			}
			results = append(results, batchSessionResult{
				Name:   name,
				Status: "deleted",
			})
		}
		writeJSON(w, map[string]interface{}{
			"results": results,
			"deleted": countBatchStatus(results, "deleted"),
			"errors":  countBatchStatus(results, "error"),
		})
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
			created, _, err := createDesktopSession(r.Context(), dyn, sessionsGVR, cfg.SessionNamespace, poolName, req.Subject, req.SessionName)
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

	tlsAddr := os.Getenv("TLS_LISTEN_ADDR")
	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	handler := withCORS(mux)

	errCh := make(chan error, 2)
	go func() {
		errCh <- http.ListenAndServe(addr, handler)
	}()
	if tlsAddr != "" && certFile != "" && keyFile != "" {
		go func() {
			// Wait briefly for OpenShift serving-cert secret to be mounted.
			deadline := time.Now().Add(2 * time.Minute)
			for {
				if _, err := os.Stat(certFile); err == nil {
					if _, err := os.Stat(keyFile); err == nil {
						break
					}
				}
				if time.Now().After(deadline) {
					errCh <- fmt.Errorf("timed out waiting for TLS certs %s / %s", certFile, keyFile)
					return
				}
				time.Sleep(2 * time.Second)
			}
			log.Printf("desktop portal API TLS listening on %s", tlsAddr)
			errCh <- http.ListenAndServeTLS(tlsAddr, certFile, keyFile, handler)
		}()
	}
	log.Fatal(<-errCh)
}

func getPoolStatus(
	ctx context.Context,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name string,
) (*poolStatusResponse, error) {
	obj, err := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")

	idleSeconds := int64(900)
	enabled := false
	if pm, ok := spec["powerManagement"].(map[string]interface{}); ok {
		enabled = true
		if v, ok := pm["enabled"].(bool); ok {
			enabled = v
		}
		if v, ok := asInt64(pm["idleSeconds"]); ok {
			idleSeconds = v
		}
	}
	replicas := int32(1)
	if v, ok := asInt64(spec["replicas"]); ok {
		replicas = int32(v)
	} else if v, ok := asInt64(status["desired"]); ok {
		replicas = int32(v)
	}
	minReady := int32(0)
	if v, ok := asInt64(spec["minReady"]); ok {
		minReady = int32(v)
	}
	recycle := asString(spec["recyclePolicy"])
	if recycle == "" {
		recycle = "Delete"
	}
	createConns := true
	if v, ok := spec["createConnections"].(bool); ok {
		createConns = v
	}

	resp := &poolStatusResponse{
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		Phase:             asString(status["phase"]),
		Desired:           asInt64Default(status["desired"], 0),
		Available:         asInt64Default(status["available"], 0),
		Allocated:         asInt64Default(status["allocated"], 0),
		Stopped:           asInt64Default(status["stopped"], 0),
		Provisioning:      asInt64Default(status["provisioning"], 0),
		Failed:            asInt64Default(status["failed"], 0),
		Replicas:          replicas,
		MinReady:          minReady,
		RecyclePolicy:     recycle,
		CreateConnections: createConns,
		PowerManagement: poolPowerManagementView{
			Enabled:     enabled,
			IdleSeconds: idleSeconds,
		},
	}
	if desktops, ok := status["desktops"].([]interface{}); ok {
		for _, d := range desktops {
			m, ok := d.(map[string]interface{})
			if !ok {
				continue
			}
			resp.Desktops = append(resp.Desktops, poolDesktopView{
				Name:    asString(m["name"]),
				State:   asString(m["state"]),
				Message: asString(m["message"]),
			})
		}
	}
	return resp, nil
}

func updatePoolConfig(
	ctx context.Context,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name string,
	req poolConfigUpdate,
) error {
	if req.Replicas == nil && req.MinReady == nil && req.RecyclePolicy == "" &&
		req.CreateConnections == nil && req.PowerManagement == nil {
		return fmt.Errorf("no configuration fields provided")
	}
	if req.Replicas != nil && *req.Replicas < 0 {
		return fmt.Errorf("replicas must be >= 0")
	}
	if req.MinReady != nil && *req.MinReady < 0 {
		return fmt.Errorf("minReady must be >= 0")
	}
	if req.RecyclePolicy != "" && req.RecyclePolicy != "Delete" && req.RecyclePolicy != "Retain" {
		return fmt.Errorf("recyclePolicy must be Delete or Retain")
	}
	if req.PowerManagement != nil && req.PowerManagement.IdleSeconds != nil && *req.PowerManagement.IdleSeconds < 0 {
		return fmt.Errorf("powerManagement.idleSeconds must be >= 0")
	}

	specPatch := map[string]interface{}{}
	if req.Replicas != nil {
		specPatch["replicas"] = *req.Replicas
	}
	if req.MinReady != nil {
		specPatch["minReady"] = *req.MinReady
	}
	if req.RecyclePolicy != "" {
		specPatch["recyclePolicy"] = req.RecyclePolicy
	}
	if req.CreateConnections != nil {
		specPatch["createConnections"] = *req.CreateConnections
	}
	if req.PowerManagement != nil {
		pm := map[string]interface{}{}
		if req.PowerManagement.Enabled != nil {
			pm["enabled"] = *req.PowerManagement.Enabled
		}
		if req.PowerManagement.IdleSeconds != nil {
			pm["idleSeconds"] = *req.PowerManagement.IdleSeconds
		}
		if len(pm) == 0 {
			// Explicit empty object still enables the powerManagement block with CRD defaults.
			pm["enabled"] = true
		}
		specPatch["powerManagement"] = pm
	}
	body, err := json.Marshal(map[string]interface{}{"spec": specPatch})
	if err != nil {
		return err
	}
	_, err = dyn.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, body, metav1.PatchOptions{},
	)
	return err
}

func resolveGuacamoleRef(
	ctx context.Context,
	dyn dynamic.Interface,
	poolsGVR schema.GroupVersionResource,
	poolNamespace, poolName string,
) (namespace, name string, err error) {
	pool, err := dyn.Resource(poolsGVR).Namespace(poolNamespace).Get(ctx, poolName, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	name, _, _ = unstructured.NestedString(pool.Object, "spec", "guacamole", "instanceRef", "name")
	if name == "" {
		return "", "", fmt.Errorf("pool %s/%s has no spec.guacamole.instanceRef.name", poolNamespace, poolName)
	}
	namespace, _, _ = unstructured.NestedString(pool.Object, "spec", "guacamole", "instanceRef", "namespace")
	if namespace == "" {
		namespace = pool.GetNamespace()
	}
	return namespace, name, nil
}

func getGuacamoleStatus(
	ctx context.Context,
	dyn dynamic.Interface,
	poolsGVR, guacamolesGVR schema.GroupVersionResource,
	poolNamespace, poolName string,
) (*guacamoleStatusResponse, error) {
	ns, name, err := resolveGuacamoleRef(ctx, dyn, poolsGVR, poolNamespace, poolName)
	if err != nil {
		return nil, err
	}
	obj, err := dyn.Resource(guacamolesGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")

	replicas := int32(1)
	if v, ok := asInt64(spec["replicas"]); ok {
		replicas = int32(v)
	}
	guacdReplicas := int32(1)
	if v, ok := asInt64(spec["guacdReplicas"]); ok {
		guacdReplicas = int32(v)
	}
	logLevel := asString(spec["logLevel"])
	if logLevel == "" {
		logLevel = "info"
	}
	routeEnabled := true
	if route, ok := spec["route"].(map[string]interface{}); ok {
		if v, ok := route["enabled"].(bool); ok {
			routeEnabled = v
		}
	}

	openid := guacamoleOpenIDView{}
	if oidc, ok := spec["openID"].(map[string]interface{}); ok {
		openid.Configured = true
		openid.Enabled = true
		if v, ok := oidc["enabled"].(bool); ok {
			openid.Enabled = v
		}
		openid.Issuer = asString(oidc["issuer"])
		openid.ClientID = asString(oidc["clientID"])
		openid.UsernameClaimType = asString(oidc["usernameClaimType"])
		if openid.UsernameClaimType == "" {
			openid.UsernameClaimType = "preferred_username"
		}
		openid.Scope = asString(oidc["scope"])
		if openid.Scope == "" {
			openid.Scope = "openid email profile"
		}
		openid.ExtensionPriority = asString(oidc["extensionPriority"])
		if openid.ExtensionPriority == "" {
			openid.ExtensionPriority = "*,openid"
		}
		openid.RedirectURI = asString(oidc["redirectURI"])
	}

	return &guacamoleStatusResponse{
		Name:          obj.GetName(),
		Namespace:     obj.GetNamespace(),
		Phase:         asString(status["phase"]),
		RouteURL:      asString(status["routeURL"]),
		Replicas:      replicas,
		GuacdReplicas: guacdReplicas,
		LogLevel:      logLevel,
		RouteEnabled:  routeEnabled,
		OpenID:        openid,
	}, nil
}

func updateGuacamoleConfig(
	ctx context.Context,
	dyn dynamic.Interface,
	poolsGVR, guacamolesGVR schema.GroupVersionResource,
	poolNamespace, poolName string,
	req guacamoleConfigUpdate,
) error {
	if req.Replicas == nil && req.GuacdReplicas == nil && req.LogLevel == "" &&
		req.RouteEnabled == nil && req.OpenID == nil {
		return fmt.Errorf("no configuration fields provided")
	}
	if req.Replicas != nil && *req.Replicas < 1 {
		return fmt.Errorf("replicas must be >= 1")
	}
	if req.GuacdReplicas != nil && *req.GuacdReplicas < 1 {
		return fmt.Errorf("guacdReplicas must be >= 1")
	}
	if req.LogLevel != "" {
		switch req.LogLevel {
		case "debug", "info", "warn", "error":
		default:
			return fmt.Errorf("logLevel must be one of debug, info, warn, error")
		}
	}
	if req.OpenID != nil && req.OpenID.ExtensionPriority != "" {
		switch req.OpenID.ExtensionPriority {
		case "*,openid", "openid", "openid,*":
		default:
			// Allow custom priorities; Guacamole accepts free-form lists.
		}
	}

	ns, name, err := resolveGuacamoleRef(ctx, dyn, poolsGVR, poolNamespace, poolName)
	if err != nil {
		return err
	}

	specPatch := map[string]interface{}{}
	if req.Replicas != nil {
		specPatch["replicas"] = *req.Replicas
	}
	if req.GuacdReplicas != nil {
		specPatch["guacdReplicas"] = *req.GuacdReplicas
	}
	if req.LogLevel != "" {
		specPatch["logLevel"] = req.LogLevel
	}
	if req.RouteEnabled != nil {
		specPatch["route"] = map[string]interface{}{"enabled": *req.RouteEnabled}
	}
	if req.OpenID != nil {
		// Merge onto existing openID so we do not wipe issuer/clientID.
		obj, err := dyn.Resource(guacamolesGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		existing, _, _ := unstructured.NestedMap(obj.Object, "spec", "openID")
		if existing == nil {
			return fmt.Errorf("openID is not configured on Guacamole %s/%s; set it on the CR first", ns, name)
		}
		if req.OpenID.Enabled != nil {
			existing["enabled"] = *req.OpenID.Enabled
		}
		if req.OpenID.UsernameClaimType != "" {
			existing["usernameClaimType"] = req.OpenID.UsernameClaimType
		}
		if req.OpenID.Scope != "" {
			existing["scope"] = req.OpenID.Scope
		}
		if req.OpenID.ExtensionPriority != "" {
			existing["extensionPriority"] = req.OpenID.ExtensionPriority
		}
		specPatch["openID"] = existing
	}

	body, err := json.Marshal(map[string]interface{}{"spec": specPatch})
	if err != nil {
		return err
	}
	_, err = dyn.Resource(guacamolesGVR).Namespace(ns).Patch(
		ctx, name, types.MergePatchType, body, metav1.PatchOptions{},
	)
	return err
}

func setPoolPowerRequest(
	ctx context.Context,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name, action string,
) error {
	patch := []byte(fmt.Sprintf(
		`{"metadata":{"annotations":{"desktop.guacamole.io/power-request":%q}}}`,
		action,
	))
	_, err := dyn.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	return err
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func asInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func asInt64Default(v interface{}, def int64) int64 {
	if n, ok := asInt64(v); ok {
		return n
	}
	return def
}

func createDesktopSession(
	ctx context.Context,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, poolName, subject, sessionName string,
) (*unstructured.Unstructured, string, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, "", fmt.Errorf("subject is required")
	}
	name := strings.TrimSpace(sessionName)
	if name == "" {
		name = sanitizeName(fmt.Sprintf("desktop-session-%s", subject))
	}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "guacamole.guacamole.io/v1alpha1",
			"kind":       "DesktopSession",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"desktop.guacamole.io/managed-by": "desktop-portal",
					"desktop.guacamole.io/requester":  sanitizeLabel(subject),
				},
			},
			"spec": map[string]interface{}{
				"poolRef": map[string]interface{}{
					"name": poolName,
				},
				"requester": map[string]interface{}{
					"subject": subject,
				},
			},
		},
	}
	created, err := dyn.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, name, err
	}
	return created, name, nil
}

func uniqueSubjects(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func countBatchStatus(results []batchSessionResult, status string) int {
	n := 0
	for _, r := range results {
		if r.Status == status {
			n++
		}
	}
	return n
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
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

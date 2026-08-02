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
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type contextKey string

const identityContextKey contextKey = "portalIdentity"

// portalIdentity is the authenticated user for a request (Keycloak or OpenShift).
type portalIdentity struct {
	Username string
	UID      string
	Groups   []string
	// Source is "keycloak" or "openshift".
	Source string
}

func identityFromContext(ctx context.Context) *portalIdentity {
	v, _ := ctx.Value(identityContextKey).(*portalIdentity)
	return v
}

func withIdentity(ctx context.Context, id *portalIdentity) context.Context {
	return context.WithValue(ctx, identityContextKey, id)
}

type authenticator struct {
	client      kubernetes.Interface
	httpClient  *http.Client
	adminGroups map[string]struct{}
	sessionNS   string
	kcURL       string
	kcRealm     string
	oidcIssuer  string
	oidcClient  string

	jwksMu      sync.Mutex
	jwksKeys    map[string]*rsa.PublicKey
	jwksFetched time.Time
}

func newAuthenticator(
	client kubernetes.Interface,
	httpClient *http.Client,
	sessionNS string,
	adminGroups []string,
	kcURL, kcRealm, oidcIssuer, oidcClientID string,
) *authenticator {
	m := make(map[string]struct{}, len(adminGroups))
	for _, g := range adminGroups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		m[g] = struct{}{}
	}
	// Preserve Authorization across Keycloak hostname redirects (internal → public).
	if httpClient != nil && httpClient.CheckRedirect == nil {
		base := httpClient
		httpClient = &http.Client{
			Timeout:   base.Timeout,
			Transport: base.Transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				if len(via) > 0 {
					if auth := via[0].Header.Get("Authorization"); auth != "" {
						req.Header.Set("Authorization", auth)
					}
				}
				return nil
			},
		}
	}
	return &authenticator{
		client:      client,
		httpClient:  httpClient,
		adminGroups: m,
		sessionNS:   sessionNS,
		kcURL:       strings.TrimRight(kcURL, "/"),
		kcRealm:     kcRealm,
		oidcIssuer:  strings.TrimRight(oidcIssuer, "/"),
		oidcClient:  oidcClientID,
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(h, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(h, prefix))
		}
	}
	if t := strings.TrimSpace(r.Header.Get("X-Forwarded-Access-Token")); t != "" {
		return t
	}
	return ""
}

var errUnauthenticated = &httpError{code: http.StatusUnauthorized, msg: "unauthorized"}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func looksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

func (a *authenticator) reviewOpenShiftToken(ctx context.Context, token string) (*portalIdentity, error) {
	tr, err := a.client.AuthenticationV1().TokenReviews().Create(ctx, &authv1.TokenReview{
		Spec: authv1.TokenReviewSpec{Token: token},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	if !tr.Status.Authenticated || tr.Status.User.Username == "" {
		return nil, errUnauthenticated
	}
	return &portalIdentity{
		Username: tr.Status.User.Username,
		UID:      tr.Status.User.UID,
		Groups:   tr.Status.User.Groups,
		Source:   "openshift",
	}, nil
}

type keycloakClaims struct {
	Issuer            string   `json:"iss"`
	Subject           string   `json:"sub"`
	Audience          any      `json:"aud"`
	ExpiresAt         int64    `json:"exp"`
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	Azp               string   `json:"azp"`
	Groups            []string `json:"groups"`
	RealmAccess       *struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (a *authenticator) jwksURLs() []string {
	var urls []string
	if a.oidcIssuer != "" {
		urls = append(urls, a.oidcIssuer+"/protocol/openid-connect/certs")
	}
	if a.kcURL != "" && a.kcRealm != "" {
		urls = append(urls, fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", a.kcURL, url.PathEscape(a.kcRealm)))
	}
	return urls
}

func parseJWKSKeys(body []byte) (map[string]*rsa.PublicKey, error) {
	var doc jwksResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.N == "" || k.E == "" {
			continue
		}
		// Skip encryption keys (Keycloak publishes RSA-OAEP use=enc alongside RS256 use=sig).
		if k.Use == "enc" {
			continue
		}
		if k.Alg != "" && !strings.HasPrefix(k.Alg, "RS") {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		kid := k.Kid
		if kid == "" {
			kid = "_"
		}
		keys[kid] = pub
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("jwks: no RSA signing keys")
	}
	return keys, nil
}

func (a *authenticator) fetchJWKS(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	a.jwksMu.Lock()
	defer a.jwksMu.Unlock()
	if a.jwksKeys != nil && time.Since(a.jwksFetched) < 10*time.Minute {
		return a.jwksKeys, nil
	}
	var lastErr error
	for _, jwksURL := range a.jwksURLs() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("keycloak jwks fetch %s: %v", jwksURL, err)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("jwks: %s", resp.Status)
			log.Printf("keycloak jwks fetch %s: %s", jwksURL, resp.Status)
			continue
		}
		keys, err := parseJWKSKeys(body)
		if err != nil {
			lastErr = err
			continue
		}
		a.jwksKeys = keys
		a.jwksFetched = time.Now()
		return keys, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("jwks: no endpoints")
	}
	return nil, lastErr
}

func rsaPublicKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	var eInt int
	for _, b := range eb {
		eInt = eInt<<8 + int(b)
	}
	if eInt == 0 {
		return nil, fmt.Errorf("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: eInt}, nil
}

func decodeJWTPart(part string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(part)
}

func (a *authenticator) reviewKeycloakJWT(ctx context.Context, token string) (*portalIdentity, error) {
	if a.kcURL == "" || a.kcRealm == "" || a.httpClient == nil {
		return nil, errUnauthenticated
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errUnauthenticated
	}
	headerJSON, err := decodeJWTPart(parts[0])
	if err != nil {
		return nil, errUnauthenticated
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, errUnauthenticated
	}
	keys, err := a.fetchJWKS(ctx)
	if err != nil {
		log.Printf("keycloak jwks: %v", err)
		return nil, errUnauthenticated
	}
	var pub *rsa.PublicKey
	if header.Kid != "" {
		pub = keys[header.Kid]
	}
	if pub == nil {
		for _, k := range keys {
			pub = k
			break
		}
	}
	if pub == nil {
		return nil, errUnauthenticated
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	sig, err := decodeJWTPart(parts[2])
	if err != nil {
		return nil, errUnauthenticated
	}
	var h hash.Hash
	var hashType crypto.Hash
	switch header.Alg {
	case "RS256":
		h = sha256.New()
		hashType = crypto.SHA256
	case "RS384":
		h = sha512.New384()
		hashType = crypto.SHA384
	case "RS512":
		h = sha512.New()
		hashType = crypto.SHA512
	default:
		log.Printf("keycloak jwt unsupported alg %q", header.Alg)
		return nil, errUnauthenticated
	}
	_, _ = h.Write(signingInput)
	if err := rsa.VerifyPKCS1v15(pub, hashType, h.Sum(nil), sig); err != nil {
		log.Printf("keycloak jwt signature: %v", err)
		return nil, errUnauthenticated
	}
	payload, err := decodeJWTPart(parts[1])
	if err != nil {
		return nil, errUnauthenticated
	}
	var claims keycloakClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errUnauthenticated
	}
	if claims.ExpiresAt > 0 && time.Now().Unix() >= claims.ExpiresAt {
		log.Printf("keycloak jwt expired")
		return nil, errUnauthenticated
	}
	if a.oidcIssuer != "" && claims.Issuer != "" && claims.Issuer != a.oidcIssuer {
		if !strings.HasSuffix(claims.Issuer, "/realms/"+a.kcRealm) {
			log.Printf("keycloak jwt iss mismatch: got %q want %q", claims.Issuer, a.oidcIssuer)
			return nil, errUnauthenticated
		}
	}
	username := strings.TrimSpace(claims.PreferredUsername)
	if username == "" {
		username = strings.TrimSpace(claims.Email)
	}
	if username == "" {
		username = strings.TrimSpace(claims.Name)
	}
	if username == "" {
		username = strings.TrimSpace(claims.Subject)
	}
	if username == "" {
		return nil, errUnauthenticated
	}
	groups := append([]string{}, claims.Groups...)
	if claims.RealmAccess != nil {
		groups = append(groups, claims.RealmAccess.Roles...)
	}
	return &portalIdentity{
		Username: username,
		UID:      claims.Subject,
		Groups:   groups,
		Source:   "keycloak",
	}, nil
}

type keycloakUserInfo struct {
	Sub               string   `json:"sub"`
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
	RealmAccess       *struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

func (a *authenticator) reviewKeycloakUserInfo(ctx context.Context, token string) (*portalIdentity, error) {
	if a.kcURL == "" || a.kcRealm == "" || a.httpClient == nil {
		return nil, errUnauthenticated
	}
	// Prefer the public issuer: in-cluster Keycloak hostname often rejects tokens
	// whose iss is the Route URL (userinfo → 401).
	var endpoints []string
	if a.oidcIssuer != "" {
		endpoints = append(endpoints, a.oidcIssuer+"/protocol/openid-connect/userinfo")
	}
	endpoints = append(endpoints,
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/userinfo", a.kcURL, url.PathEscape(a.kcRealm)),
	)
	var lastStatus int
	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := a.httpClient.Do(req)
		if err != nil {
			log.Printf("keycloak userinfo %s: %v", endpoint, err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		if resp.StatusCode != http.StatusOK {
			log.Printf("keycloak userinfo %s: status=%d", endpoint, resp.StatusCode)
			continue
		}
		var info keycloakUserInfo
		if err := json.Unmarshal(body, &info); err != nil {
			continue
		}
		username := strings.TrimSpace(info.PreferredUsername)
		if username == "" {
			username = strings.TrimSpace(info.Email)
		}
		if username == "" {
			username = strings.TrimSpace(info.Name)
		}
		if username == "" {
			username = strings.TrimSpace(info.Sub)
		}
		if username == "" {
			continue
		}
		groups := append([]string{}, info.Groups...)
		if info.RealmAccess != nil {
			groups = append(groups, info.RealmAccess.Roles...)
		}
		return &portalIdentity{
			Username: username,
			UID:      info.Sub,
			Groups:   groups,
			Source:   "keycloak",
		}, nil
	}
	if lastStatus != 0 {
		log.Printf("keycloak userinfo failed lastStatus=%d", lastStatus)
	}
	return nil, errUnauthenticated
}

// authenticate accepts Keycloak access tokens (user portal) or OpenShift tokens (Console admin).
func (a *authenticator) authenticate(ctx context.Context, token string) (*portalIdentity, error) {
	if looksLikeJWT(token) {
		if id, err := a.reviewKeycloakJWT(ctx, token); err == nil {
			return id, nil
		}
		if id, err := a.reviewKeycloakUserInfo(ctx, token); err == nil {
			return id, nil
		}
		return a.reviewOpenShiftToken(ctx, token)
	}
	return a.reviewOpenShiftToken(ctx, token)
}

func (a *authenticator) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/oidc-config" && r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		token := bearerToken(r)
		if token == "" {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		id, err := a.authenticate(r.Context(), token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
	})
}

func (a *authenticator) isAdmin(ctx context.Context, id *portalIdentity) (bool, error) {
	if id == nil {
		return false, nil
	}
	for _, g := range id.Groups {
		if _, ok := a.adminGroups[g]; ok {
			return true, nil
		}
		if g == "system:cluster-admins" || g == "system:masters" {
			return true, nil
		}
	}
	if id.Source != "openshift" {
		return false, nil
	}
	sar, err := a.client.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User:   id.Username,
			Groups: id.Groups,
			UID:    id.UID,
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: a.sessionNS,
				Verb:      "create",
				Group:     "guacamole.guacamole.io",
				Resource:  "desktopsessions",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, err
	}
	return sar.Status.Allowed, nil
}

func (a *authenticator) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := identityFromContext(r.Context())
		ok, err := a.isAdmin(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func subjectFromIdentity(id *portalIdentity) string {
	if id == nil {
		return ""
	}
	return strings.TrimSpace(id.Username)
}

func parseAdminGroupsEnv(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

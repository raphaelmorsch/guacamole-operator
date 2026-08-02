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
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestBearerToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/me", nil)
	r.Header.Set("Authorization", "Bearer tok-a")
	if got := bearerToken(r); got != "tok-a" {
		t.Fatalf("bearer=%q", got)
	}
}

func signRS256(t *testing.T, priv *rsa.PrivateKey, header, payload map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	signingInput := enc(hb) + "." + enc(pb)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + enc(sig)
}

func TestKeycloakJWTAuth(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())

	issuer := "https://keycloak.example.com/realms/guacamole"
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/guacamole/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{
				// Encryption key first — must be ignored for JWT signature verify.
				{"kid": "enc1", "kty": "RSA", "alg": "RSA-OAEP", "use": "enc", "n": n, "e": e},
				{"kid": "k1", "kty": "RSA", "alg": "RS256", "use": "sig", "n": n, "e": e},
			},
		})
	})
	mux.HandleFunc("/realms/guacamole/protocol/openid-connect/userinfo", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not need userinfo", http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := signRS256(t, priv, map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"}, map[string]any{
		"iss":                issuer,
		"sub":                "u1",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"preferred_username": "alice",
		"azp":                "guacamole-user-portal",
	})

	client := fake.NewSimpleClientset()
	authn := newAuthenticator(client, srv.Client(), "ns", nil, srv.URL, "guacamole", issuer, "guacamole-user-portal")
	id, err := authn.authenticate(context.Background(), token)
	if err != nil || id.Username != "alice" || id.Source != "keycloak" {
		t.Fatalf("jwt auth: id=%+v err=%v", id, err)
	}
}

func TestOpenShiftFallback(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "tokenreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		tr := action.(clienttesting.CreateAction).GetObject().(*authv1.TokenReview)
		out := tr.DeepCopy()
		if tr.Spec.Token == "ocp-good" {
			out.Status.Authenticated = true
			out.Status.User = authv1.UserInfo{Username: "kubeadmin", Groups: []string{"system:cluster-admins"}}
		}
		return true, out, nil
	})
	client.PrependReactor("create", "subjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		sar := action.(clienttesting.CreateAction).GetObject().(*authzv1.SubjectAccessReview)
		out := sar.DeepCopy()
		out.Status.Allowed = true
		return true, out, nil
	})
	authn := newAuthenticator(client, http.DefaultClient, "ns", nil, "http://unused", "guacamole", "", "")
	id, err := authn.authenticate(context.Background(), "ocp-good")
	if err != nil || id.Username != "kubeadmin" {
		t.Fatalf("ocp auth: %+v %v", id, err)
	}
	ok, err := authn.isAdmin(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("admin: %v %v", ok, err)
	}
}

func TestSubjectFromIdentity(t *testing.T) {
	if got := subjectFromIdentity(&portalIdentity{Username: "  alice  "}); got != "alice" {
		t.Fatalf("got %q", got)
	}
}

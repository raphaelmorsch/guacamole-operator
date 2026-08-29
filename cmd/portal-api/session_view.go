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
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const guacamoleClientDataSource = "mysql"

// sessionView is an enriched DesktopSession for portal UIs.
type sessionView struct {
	Object            map[string]interface{} `json:"object"`
	Name              string                 `json:"name"`
	Namespace         string                 `json:"namespace"`
	Subject           string                 `json:"subject,omitempty"`
	PoolName          string                 `json:"poolName,omitempty"`
	Phase             string                 `json:"phase,omitempty"`
	UxPhase           string                 `json:"uxPhase,omitempty"`
	ConnectionState   string                 `json:"connectionState,omitempty"`
	DesktopName       string                 `json:"desktopName,omitempty"`
	ConnectionName    string                 `json:"connectionName,omitempty"`
	QueuePosition     int64                  `json:"queuePosition,omitempty"`
	QueueLength       int64                  `json:"queueLength,omitempty"`
	Message           string                 `json:"message,omitempty"`
	ReleasedReason    string                 `json:"releasedReason,omitempty"`
	GuacamoleRouteURL string                 `json:"guacamoleRouteURL,omitempty"`
	ConnectionID      int64                  `json:"connectionID,omitempty"`
	ConnectURL        string                 `json:"connectURL,omitempty"`
}

func mapUxPhase(phase, connectionState, desktopName string) string {
	switch phase {
	case "Failed":
		return "Failed"
	case "Released":
		return "Released"
	case "InUse":
		return "InUse"
	case "Disconnected":
		return "Disconnected"
	case "Ready":
		if connectionState == "Connected" {
			return "InUse"
		}
		return "Ready"
	case "Pending", "Queued":
		if desktopName == "" {
			return "Provisioning"
		}
		return "Provisioning"
	default:
		if phase == "" {
			return "Provisioning"
		}
		return phase
	}
}

func guacamoleConnectURL(routeURL string, connectionID int64) string {
	if routeURL == "" || connectionID <= 0 {
		return ""
	}
	base := strings.TrimRight(routeURL, "/")
	payload := fmt.Sprintf("%d\x00c\x00%s", connectionID, guacamoleClientDataSource)
	// Guacamole client hash uses standard base64 without padding.
	encoded := strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(payload)), "=")
	return base + "/#/client/" + encoded
}

func sessionRequesterSubject(obj *unstructured.Unstructured) string {
	s, _, _ := unstructured.NestedString(obj.Object, "spec", "requester", "subject")
	return s
}

func buildSessionView(
	obj *unstructured.Unstructured,
	routeURL string,
	connectionID int64,
) sessionView {
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	phase := asString(status["phase"])
	connState := asString(status["connectionState"])
	desktop := asString(status["desktopName"])
	connName := asString(status["connectionName"])
	if connName == "" {
		connName = obj.GetName()
	}
	msg := asString(status["message"])
	poolName, _, _ := unstructured.NestedString(obj.Object, "spec", "poolRef", "name")
	view := sessionView{
		Object:            obj.Object,
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		Subject:           sessionRequesterSubject(obj),
		PoolName:          poolName,
		Phase:             phase,
		UxPhase:           mapUxPhase(phase, connState, desktop),
		ConnectionState:   connState,
		DesktopName:       desktop,
		ConnectionName:    connName,
		QueuePosition:     asInt64Default(status["queuePosition"], 0),
		QueueLength:       asInt64Default(status["queueLength"], 0),
		Message:           msg,
		ReleasedReason:    asString(status["releasedReason"]),
		GuacamoleRouteURL: routeURL,
		ConnectionID:      connectionID,
		ConnectURL:        guacamoleConnectURL(routeURL, connectionID),
	}
	return view
}

func getConnectionID(
	ctx context.Context,
	dyn dynamic.Interface,
	connectionsGVR schema.GroupVersionResource,
	namespace, name string,
) (int64, error) {
	obj, err := dyn.Resource(connectionsGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	v, found, err := unstructured.NestedInt64(obj.Object, "status", "connectionID")
	if err != nil || !found {
		// JSON numbers sometimes decode as float via NestedField.
		if raw, ok, _ := unstructured.NestedFieldNoCopy(obj.Object, "status", "connectionID"); ok {
			switch t := raw.(type) {
			case int64:
				return t, nil
			case float64:
				return int64(t), nil
			case int:
				return int64(t), nil
			case string:
				n, e := strconv.ParseInt(t, 10, 64)
				return n, e
			}
		}
		return 0, nil
	}
	return v, nil
}

func sessionPoolRef(obj *unstructured.Unstructured, fallbackNS, fallbackName string) (namespace, name string) {
	name, _, _ = unstructured.NestedString(obj.Object, "spec", "poolRef", "name")
	if name == "" {
		name = fallbackName
	}
	// DesktopSession.poolRef is namespaced-local; resolve Guacamole via pool in the session namespace
	// (or the portal default pool namespace when the session has no poolRef yet).
	namespace = obj.GetNamespace()
	if namespace == "" {
		namespace = fallbackNS
	}
	return namespace, name
}

func enrichSession(
	ctx context.Context,
	dyn dynamic.Interface,
	connectionsGVR, poolsGVR, guacamolesGVR schema.GroupVersionResource,
	fallbackPoolNamespace, fallbackPoolName string,
	obj *unstructured.Unstructured,
) sessionView {
	poolNS, poolName := sessionPoolRef(obj, fallbackPoolNamespace, fallbackPoolName)
	routeURL := ""
	if poolName != "" {
		if view, err := getGuacamoleStatus(ctx, dyn, poolsGVR, guacamolesGVR, poolNS, poolName); err == nil && view != nil {
			routeURL = view.RouteURL
		}
	}
	// GuacamoleConnection CR name matches DesktopSession name. status.connectionName
	// is the Guacamole display name in MySQL, not the Kubernetes resource name.
	var connID int64
	if id, err := getConnectionID(ctx, dyn, connectionsGVR, obj.GetNamespace(), obj.GetName()); err == nil {
		connID = id
	}
	return buildSessionView(obj, routeURL, connID)
}

package controller

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

var routeGVK = schema.GroupVersionKind{
	Group:   "route.openshift.io",
	Version: "v1",
	Kind:    "Route",
}

func desiredRoute(g *guacamolev1alpha1.Guacamole) *unstructured.Unstructured {
	tlsTermination := valueOrDefault(g.Spec.Route.TLSTermination, "edge")
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	route.SetName(routeName(g.Name))
	route.SetNamespace(g.Namespace)
	route.SetLabels(labelsFor(g, "guacamole"))

	spec := map[string]interface{}{
		"to": map[string]interface{}{
			"kind":   "Service",
			"name":   guacServiceName(g.Name),
			"weight": int64(100),
		},
		"path": routePath(&g.Spec),
		"port": map[string]interface{}{
			"targetPort": "http",
		},
		"tls": map[string]interface{}{
			"termination":                   tlsTermination,
			"insecureEdgeTerminationPolicy": "Redirect",
		},
	}
	if g.Spec.Route.Hostname != "" {
		spec["host"] = g.Spec.Route.Hostname
	}
	if err := unstructured.SetNestedField(route.Object, spec, "spec"); err != nil {
		panic(err)
	}
	return route
}

func routeHost(route *unstructured.Unstructured) string {
	host, found, err := unstructured.NestedString(route.Object, "spec", "host")
	if err != nil || !found {
		return ""
	}
	return host
}

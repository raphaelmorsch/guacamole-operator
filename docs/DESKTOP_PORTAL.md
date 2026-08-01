# DesktopPortal — OpenShift Console Dynamic Plugin

The `DesktopPortal` CR deploys an OpenShift Console dynamic plugin and a small API that:

1. Lists users from a Keycloak realm (Admin API + `client_credentials`)
2. Creates `DesktopSession` objects for one or more selected users against a `DesktopPool` (batch create via `POST /sessions/batch`)
3. Deletes existing sessions in batch (`POST /sessions/batch-delete`)

## Flow

```text
DesktopPortal CR
   ├── Deployment/Service (plugin nginx + ConsolePlugin)
   ├── Deployment/Service (portal-api)
   ├── SA + Role/RoleBinding (create DesktopSessions)
   └── optionally enables plugin on consoles.operator.openshift.io/cluster
```

Console nav item: **Home → Desktop Sessions** (`/guacamole-desktops`).

## Sample

See `config/samples/guacamole_v1alpha1_desktopportal.yaml`.

### Keycloak client

Create a confidential client with a service account that can query users, for example:

- Client authentication: ON
- Service accounts roles: `realm-management` → `view-users` / `query-users`

Store the client secret in a Kubernetes Secret referenced by `spec.keycloak.clientSecretRef`.

Set `spec.keycloak.url` to the **in-cluster Service** (for example
`http://keycloak-service.guacamole-desktops.svc:8080`), not the public Route.
Pods calling the Route hostname often hit hairpin/NAT timeouts and the Console
shows **Bad Gateway** on user search.

### Images

Build and push:

```bash
# portal API
podman build -f Dockerfile.portal-api -t $REGISTRY/guacamole-operator/guacamole-desktop-portal-api:0.0.21 .

# console plugin
podman build -f console-plugin/Dockerfile -t $REGISTRY/guacamole-operator/guacamole-desktop-portal-plugin:0.0.21 console-plugin/
```

Set `spec.pluginImage` / `spec.apiImage`, or inject `RELATED_IMAGE_DESKTOP_PORTAL_PLUGIN` and `RELATED_IMAGE_DESKTOP_PORTAL_API` on the operator Deployment.

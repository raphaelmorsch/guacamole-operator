# DesktopPortal — Admin Console plugin + self-service user portal

The `DesktopPortal` CR deploys:

1. An **OpenShift Console** dynamic plugin (admin) for batch allocate/release and pool/Guacamole config
2. A **portal-api** that talks to Keycloak (user directory) and manages `DesktopSession`s
3. Optionally a **self-service PatternFly UI** outside the Console (`spec.userPortal`) behind an OpenShift **Route**, with **Keycloak OIDC** login

## Auth

| Surface | Identity | Endpoints |
|---|---|---|
| Self-service user portal | **Keycloak** access token (PKCE); portal-api checks token via Keycloak `userinfo` | `GET /me`, `GET/POST /sessions/mine`, `GET/DELETE /sessions/mine/{name}` |
| Admin Console plugin | **OpenShift** Bearer (`UserToken`) via TokenReview + SAR `create desktopsessions` (or `spec.adminGroups`) | `/users`, batch sessions, pool/Guacamole writes, wake/suspend |

`requester.subject` is the Keycloak `preferred_username` (same claim Guacamole OpenID uses).

portal-api tries Keycloak `userinfo` first, then OpenShift TokenReview, so both surfaces share one API.

## Self-service user portal

Enable:

```yaml
spec:
  userPortal:
    enabled: true
    # Public issuer (Route), NOT the in-cluster Service URL:
    issuer: https://keycloak.apps.example.com/realms/guacamole
    oidcClientID: guacamole-user-portal   # Keycloak public client (PKCE)
    # image: .../guacamole-desktop-user-portal:0.0.29
    # hostname: desktops.apps.example.com
```

Status: `status.userPortalURL` (external HTTPS URL).

Users open the Route → Keycloak login → **Pedir desktop / Conectar / Reconectar / Liberar**. Session enrichment includes `uxPhase` and Guacamole `connectURL` deep-link.

### Keycloak public client (user portal)

Create a **public** client in the same realm as end users (separate from the confidential admin directory client and from the Guacamole client):

- Client ID: `guacamole-user-portal` (or `spec.userPortal.oidcClientID`)
- Client authentication: **OFF** (public)
- Standard flow: ON
- Direct access grants: OFF
- Valid redirect URIs: `https://<userPortalURL>/*`
- Web origins: `+` or the portal origin
- PKCE: S256 (enforced by the SPA)

### Keycloak confidential client (admin directory)

Create a confidential client with a service account that can query users:

- Client authentication: ON
- Service accounts roles: `realm-management` → `view-users` / `query-users`

Store the client secret in a Secret referenced by `spec.keycloak.clientSecretRef`.

Set `spec.keycloak.url` to the **in-cluster Service** (for example
`http://keycloak-service.guacamole-desktops.svc:8080`), not the public Route.
Set `spec.userPortal.issuer` to the **public** issuer (same host Guacamole OpenID uses).

## Admin Console plugin

Nav: **Home → Desktop Sessions** (`/guacamole-desktops`).

Capabilities:

1. List Keycloak users and batch-create `DesktopSession`s
2. Batch-delete sessions
3. Configure DesktopPool (replicas, minReady, recycle, power, session lifecycle)
4. Configure Guacamole (replicas, OpenID toggles when already configured)
5. Per-session **Connect** when `connectURL` is available

Editable pool fields: `replicas`, `minReady`, `recyclePolicy`, `createConnections`, `powerManagement.*`, `sessionLifecycle.*`.

When creating sessions, the portal copies pool `sessionLifecycle` defaults into the DesktopSession.

## Flow

```text
DesktopPortal CR
   ├── Deployment/Service (Console plugin nginx + ConsolePlugin)
   ├── Deployment/Service (portal-api + TokenReview/SAR RBAC + Keycloak userinfo)
   ├── optional: user-portal nginx SPA + Route (status.userPortalURL)
   └── optionally enables plugin on consoles.operator.openshift.io/cluster
```

## Sample

See `config/samples/guacamole_v1alpha1_desktopportal.yaml`.

### Images

```bash
export VERSION=0.0.29
podman build -f Dockerfile.portal-api -t $REGISTRY/guacamole-operator/guacamole-desktop-portal-api:${VERSION} .
podman build -f console-plugin/Dockerfile -t $REGISTRY/guacamole-operator/guacamole-desktop-portal-plugin:${VERSION} console-plugin/
podman build -f user-portal/Dockerfile -t $REGISTRY/guacamole-operator/guacamole-desktop-user-portal:${VERSION} user-portal/
```

Set `spec.pluginImage` / `spec.apiImage` / `spec.userPortal.image`, or inject
`RELATED_IMAGE_DESKTOP_PORTAL_PLUGIN`, `RELATED_IMAGE_DESKTOP_PORTAL_API`, and
`RELATED_IMAGE_DESKTOP_USER_PORTAL` on the operator Deployment.

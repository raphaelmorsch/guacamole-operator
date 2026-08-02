# DesktopPortal — Admin Console plugin + self-service user portal

The `DesktopPortal` CR deploys:

1. An **OpenShift Console** dynamic plugin (admin) for batch allocate/release and pool/Guacamole config
2. A **portal-api** that talks to Keycloak (user directory) and manages `DesktopSession`s
3. Optionally a **self-service PatternFly UI** outside the Console (`spec.userPortal`) behind an OpenShift **Route**, with **Keycloak OIDC** login

---

## Auth model

| Surface | Identity | Endpoints |
|---|---|---|
| Self-service **user portal** | Keycloak access token (OIDC + PKCE). portal-api validates the JWT (JWKS) / userinfo | `GET /me`, `GET/POST /sessions/mine`, `GET/DELETE /sessions/mine/{name}` |
| Admin **Console plugin** | OpenShift Bearer (`UserToken`) via TokenReview + SAR `create desktopsessions` (or `spec.adminGroups`) | `/users`, batch sessions, pool/Guacamole writes, wake/suspend |

`requester.subject` is the Keycloak `preferred_username` (same claim Guacamole OpenID uses).

> **Important:** the browser must use the **public** Keycloak issuer (`spec.userPortal.issuer`).  
> The admin directory client keeps using the **in-cluster** Keycloak URL (`spec.keycloak.url`).

---

## Configure the User Portal on DesktopPortal

Omit `spec.userPortal` to keep Console-admin-only (backward compatible).  
When the block is present and `enabled` is true (default), the operator deploys:

- Deployment + Service (nginx SPA)
- OpenShift Route (edge TLS)
- Status field `status.userPortalURL`

### Prerequisites

1. A working `DesktopPool` referenced by `spec.defaultPool`
2. Keycloak realm with end users (same realm Guacamole OpenID uses)
3. **Confidential** Keycloak client for the admin directory (`spec.keycloak`)
4. **Public** Keycloak client for the user portal (`spec.userPortal.oidcClientID`)
5. Images available to the cluster (`RELATED_IMAGE_*` on the operator, or explicit `spec.*.image`)

### `spec.userPortal` fields

| Field | Required | Default | Description |
|---|---|---|---|
| `enabled` | no | `true` | Deploy the external Route/UI when `userPortal` is set |
| `issuer` | **yes** | — | Public OIDC issuer, e.g. `https://keycloak.apps.example.com/realms/guacamole`. Must match the token `iss` claim. **Do not** use the in-cluster Service URL |
| `oidcClientID` | no | `guacamole-user-portal` | Keycloak **public** client (standard flow + PKCE) |
| `image` | no | `RELATED_IMAGE_DESKTOP_USER_PORTAL` | User-portal container image |
| `hostname` | no | OpenShift-generated | Optional custom Route host |

### Minimal example

```yaml
apiVersion: guacamole.guacamole.io/v1alpha1
kind: DesktopPortal
metadata:
  name: desktop-portal
  namespace: guacamole-desktops
spec:
  displayName: Desktop Sessions
  defaultPool:
    name: windows-desktop
    namespace: guacamole-desktops
  sessionNamespace: guacamole-desktops
  enablePlugin: true

  # --- User Portal (self-service) ---
  userPortal:
    enabled: true
    issuer: https://keycloak-guacamole-desktops.apps.example.com/realms/guacamole
    oidcClientID: guacamole-user-portal

  # --- Admin directory (Console plugin user list) ---
  keycloak:
    url: http://keycloak-service.guacamole-desktops.svc:8080
    realm: guacamole
    clientID: guacamole-desktop-portal
    clientSecretRef:
      name: keycloak-portal-client
      key: client-secret
```

### Full example (custom host + images)

```yaml
apiVersion: guacamole.guacamole.io/v1alpha1
kind: DesktopPortal
metadata:
  name: desktop-portal
  namespace: guacamole-desktops
spec:
  displayName: Desktop Sessions
  defaultPool:
    name: windows-desktop
    namespace: guacamole-desktops
  sessionNamespace: guacamole-desktops
  enablePlugin: true
  navSection: home
  replicas: 1
  # adminGroups: ["desktop-portal-admins"]

  pluginImage: image-registry.openshift-image-registry.svc:5000/guacamole-operator/guacamole-desktop-portal-plugin:0.0.30
  apiImage: image-registry.openshift-image-registry.svc:5000/guacamole-operator/guacamole-desktop-portal-api:0.0.30

  userPortal:
    enabled: true
    issuer: https://keycloak-guacamole-desktops.apps.example.com/realms/guacamole
    oidcClientID: guacamole-user-portal
    hostname: desktops.apps.example.com
    image: image-registry.openshift-image-registry.svc:5000/guacamole-operator/guacamole-desktop-user-portal:0.0.30

  keycloak:
    url: http://keycloak-service.guacamole-desktops.svc:8080
    realm: guacamole
    clientID: guacamole-desktop-portal
    clientSecretRef:
      name: keycloak-portal-client
      key: client-secret
    insecureSkipVerify: false
---
apiVersion: v1
kind: Secret
metadata:
  name: keycloak-portal-client
  namespace: guacamole-desktops
type: Opaque
stringData:
  client-secret: change-me
```

### Apply / patch on a live cluster

```bash
# Create from sample
oc apply -f config/samples/guacamole_v1alpha1_desktopportal.yaml

# Or enable/update only the user portal on an existing CR
oc patch desktopportal desktop-portal -n guacamole-desktops --type=merge -p '{
  "spec": {
    "userPortal": {
      "enabled": true,
      "issuer": "https://keycloak-guacamole-desktops.apps.example.com/realms/guacamole",
      "oidcClientID": "guacamole-user-portal"
    }
  }
}'
```

### Verify

```bash
oc get desktopportal desktop-portal -n guacamole-desktops \
  -o jsonpath='phase={.status.phase}{"\n"}userPortalURL={.status.userPortalURL}{"\n"}'

oc get deploy,svc,route -n guacamole-desktops | grep portal-user

# SPA OIDC bootstrap (served by nginx)
curl -sk "$(oc get desktopportal desktop-portal -n guacamole-desktops -o jsonpath='{.status.userPortalURL}')/config.json"
```

Expected `config.json`:

```json
{
  "url": "https://keycloak-guacamole-desktops.apps.example.com",
  "realm": "guacamole",
  "clientId": "guacamole-user-portal",
  "issuer": "https://keycloak-guacamole-desktops.apps.example.com/realms/guacamole"
}
```

Open `status.userPortalURL` in a browser → Keycloak login → **Pedir desktop / Conectar / Reconectar / Liberar**.

To disable without deleting the whole CR:

```yaml
spec:
  userPortal:
    enabled: false
    issuer: https://keycloak-guacamole-desktops.apps.example.com/realms/guacamole
```

---

## Keycloak setup for the User Portal

You need **two** clients in the same realm (do not reuse one client for both):

| Client | Type | Used by |
|---|---|---|
| `guacamole-desktop-portal` (example) | Confidential + service account | Console admin `/users` (`spec.keycloak`) |
| `guacamole-user-portal` (example) | **Public** + PKCE | Browser login (`spec.userPortal`) |

### 1) Public client (user portal)

In Keycloak Admin → realm → **Clients** → Create:

| Setting | Value |
|---|---|
| Client ID | `guacamole-user-portal` (or your `oidcClientID`) |
| Client authentication | **Off** (public) |
| Authentication flow | Standard flow **ON** |
| Direct access grants | **OFF** |
| Valid redirect URIs | `https://<userPortalURL>/*` |
| Valid post logout redirect URIs | `https://<userPortalURL>/*` |
| Web origins | `+` (or the exact portal origin) |
| PKCE | S256 (required by the SPA) |

After the Route exists, set redirect URI from status:

```bash
URL=$(oc get desktopportal desktop-portal -n guacamole-desktops -o jsonpath='{.status.userPortalURL}')
echo "${URL}/*"
```

Align `issuer` with Guacamole OpenID when present:

```bash
oc get guacamole -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name} {.spec.openID.issuer}{"\n"}{end}'
```

Use that same public issuer in `spec.userPortal.issuer`.

### 2) Confidential client (admin directory)

- Client authentication: **ON**
- Service account: **ON**
- Service account roles: `realm-management` → `view-users` / `query-users`
- Secret in cluster → `spec.keycloak.clientSecretRef`
- `spec.keycloak.url` = **in-cluster** Service, e.g. `http://keycloak-service.guacamole-desktops.svc:8080`

---

## Admin Console plugin

Nav: **Home → Desktop Sessions** (`/guacamole-desktops`).

Capabilities:

1. List Keycloak users and batch-create `DesktopSession`s
2. Batch-delete sessions
3. Configure DesktopPool (replicas, minReady, recycle, power, session lifecycle)
4. Configure Guacamole (replicas, OpenID toggles when already configured)
5. Per-session **Connect** when `connectURL` is available

When creating sessions, the portal copies pool `sessionLifecycle` defaults into the DesktopSession.

---

## Architecture

```text
Browser ──► Route (edge TLS) ──► user-portal (nginx + PatternFly SPA)
                                      │
                                      │  /api/* + Bearer (Keycloak access token)
                                      ▼
                                 portal-api
                                      ├── Keycloak JWKS / userinfo (end-user auth)
                                      ├── Keycloak Admin API (user directory, admin only)
                                      └── DesktopSession / DesktopPool / Guacamole CRs

OpenShift Console ──► Console plugin ──► portal-api (OpenShift UserToken)
```

---

## Multiple DesktopPortals

You can run several `DesktopPortal` CRs in the same cluster (typically one per tenant namespace).

Each portal gets a unique:

| Identity | Default | Override |
|---|---|---|
| ConsolePlugin name | `guac-dp-{namespace}-{name}` | `spec.pluginName` |
| Console path / nav | `/guacamole-desktops-{namespace}-{name}` | `spec.consolePath` |
| ClusterRole (TokenReview/SAR) | `{namespace}-{name}-portal-authreview` | (derived) |
| DesktopSession names | `ds-{portalNS}-{portalName}-{subject}` | `sessionName` on create |
| Session labels | `desktop.guacamole.io/portal` + `portal-namespace` | (set by portal-api) |

### Recommendations

1. Prefer **one portal per namespace**, with its own `defaultPool` and `sessionNamespace`.
2. Use a **distinct Keycloak public client** (and redirect URI) per `userPortal` Route.
3. To keep an existing Console bookmark after upgrade, pin the old identity:

```yaml
spec:
  pluginName: guacamole-desktop-portal
  consolePath: /guacamole-desktops
```

Only one portal in the cluster may use those values.

4. Sessions created before multi-portal support lack portal labels and will not appear in the filtered portal UI; recreate them from the portal if needed.

See also [`config/samples/guacamole_v1alpha1_desktopportal_second.yaml`](../config/samples/guacamole_v1alpha1_desktopportal_second.yaml).

---

## Sample file

See [`config/samples/guacamole_v1alpha1_desktopportal.yaml`](../config/samples/guacamole_v1alpha1_desktopportal.yaml).

### Build images

```bash
export VERSION=0.0.30
export REGISTRY=image-registry.openshift-image-registry.svc:5000/guacamole-operator

podman build -f Dockerfile.portal-api -t $REGISTRY/guacamole-desktop-portal-api:${VERSION} .
podman build -f console-plugin/Dockerfile -t $REGISTRY/guacamole-desktop-portal-plugin:${VERSION} console-plugin/
podman build -f user-portal/Dockerfile -t $REGISTRY/guacamole-desktop-user-portal:${VERSION} user-portal/
```

Set `spec.pluginImage` / `spec.apiImage` / `spec.userPortal.image`, or inject on the operator Deployment:

- `RELATED_IMAGE_DESKTOP_PORTAL_PLUGIN`
- `RELATED_IMAGE_DESKTOP_PORTAL_API`
- `RELATED_IMAGE_DESKTOP_USER_PORTAL`

### Troubleshooting auth (user portal)

| Symptom | Likely cause |
|---|---|
| `Unauthorized` after Keycloak login | `issuer` is the in-cluster URL, or does not match token `iss` |
| Redirect URI error in Keycloak | Public client missing `https://<userPortalURL>/*` |
| `config.json` missing / wrong client | User-portal pod env not reconciled; check Deployment env `OIDC_*` |
| Admin `/users` Bad Gateway | `spec.keycloak.url` should be the in-cluster Service, not the public Route |

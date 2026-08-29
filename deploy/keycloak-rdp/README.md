# Keycloak (RHBK) — namespace `rdp`

Red Hat Build of Keycloak instalado para autenticação do fluxo DesktopPortal + Guacamole OpenID.

## URLs

| Recurso | URL |
|---|---|
| **Keycloak (público)** | https://keycloak-ingress-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io |
| **Issuer OIDC** | https://keycloak-ingress-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io/realms/guacamole |
| **Admin Console** | https://keycloak-ingress-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io/admin |
| **Guacamole** | https://gateway-guacamole-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io/guacamole |
| **Keycloak in-cluster** | http://keycloak-service.rdp.svc:8080 |

## Credenciais admin Keycloak (realm master)

```bash
oc get secret keycloak-initial-admin -n rdp -o jsonpath='{.data.username}' | base64 -d; echo
oc get secret keycloak-initial-admin -n rdp -o jsonpath='{.data.password}' | base64 -d; echo
```

## Realm `guacamole`

### Usuários de teste

| Username | Senha | Papel |
|---|---|---|
| joao | RedHat123! | Usuário desktop |
| maria | RedHat123! | Usuário desktop |
| admin | RedHat123! | Usuário desktop |
| **guacadmin** | RedHat123! | **Admin Guacamole** (mapeia ao usuário MySQL existente via `preferred_username`) |

### Clients OIDC

| Client ID | Tipo | Uso |
|---|---|---|
| `guacamole` | Confidential | SSO do Guacamole (`gateway` OpenID habilitado) |
| `guacamole-desktop-portal` | Confidential + service account | portal-api admin (`/users`, batch sessions) |
| `guacamole-user-portal` | Public + PKCE | User Portal self-service |

### Secrets no namespace `rdp`

| Secret | Chave | Client |
|---|---|---|
| `gateway-openid-client` | `password` | guacamole |
| `keycloak-portal-client` | `client-secret` | guacamole-desktop-portal |

## DesktopPortal — valores para o CR

```yaml
spec:
  defaultPool:
    name: <seu-pool>
    namespace: rdp
  sessionNamespace: rdp
  userPortal:
    enabled: true
    issuer: https://keycloak-ingress-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io/realms/guacamole
    oidcClientID: guacamole-user-portal
  keycloak:
    url: http://keycloak-service.rdp.svc:8080
    realm: guacamole
    clientID: guacamole-desktop-portal
    clientSecretRef:
      name: keycloak-portal-client
      key: client-secret
```

Após criar o DesktopPortal, adicione no client `guacamole-user-portal` o redirect URI:

`https://<status.userPortalURL>/*`

(o pattern `https://*-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io/*` já cobre rotas no namespace rdp)

## Instalação (ordem)

```bash
oc apply -f deploy/keycloak-rdp/00-operator.yaml
oc apply -f deploy/keycloak-rdp/01-postgres.yaml
# aguardar CSV rhbk + postgres ready
oc apply -f deploy/keycloak-rdp/02-keycloak.yaml
# aguardar Keycloak Ready
oc apply -f deploy/keycloak-rdp/04-secrets.yaml
oc apply -f deploy/keycloak-rdp/03-realm-import.yaml
# aguardar KeycloakRealmImport Done=true
# atribuir roles view-users/query-users ao service account (script local ou Admin Console)
```

## Keycloak login theme (Energisa)

Realm `guacamole` uses custom theme `energisa`:

- **Título:** Energisa Desktop Gateway
- **Logo:** montado via ConfigMap `keycloak-energisa-theme` + init container no Keycloak CR
- **Reaplicar:** `./deploy/keycloak-rdp/apply-energisa-theme.sh`

Ao abrir o Guacamole (redirect SSO), a tela de login exibida é a do Keycloak com branding Energisa.


Já habilitado no CR `gateway`:

- issuer: `https://keycloak-ingress-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io/realms/guacamole`
- clientID: `guacamole`
- secret: `gateway-openid-client` / key `password`

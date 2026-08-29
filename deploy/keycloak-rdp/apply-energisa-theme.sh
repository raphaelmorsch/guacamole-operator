#!/usr/bin/env bash
# Build theme ConfigMap with binary logo and apply Keycloak theme mount + realm settings.
set -euo pipefail
NS=rdp
ROOT="$(cd "$(dirname "$0")" && pwd)"
LOGO="$ROOT/theme/energisa/login/resources/img/logo.png"

oc create configmap keycloak-energisa-theme -n "$NS" \
  --from-file=theme.properties="$ROOT/theme/energisa/login/theme.properties" \
  --from-file=energisa.css="$ROOT/theme/energisa/login/resources/css/energisa.css" \
  --from-file=logo.png="$LOGO" \
  --dry-run=client -o yaml | oc apply -f -

oc patch keycloak keycloak -n "$NS" --type=merge -p '{
  "spec": {
    "unsupported": {
      "podTemplate": {
        "spec": {
          "initContainers": [
            {
              "name": "init-energisa-theme",
              "image": "registry.access.redhat.com/ubi9/ubi-minimal:9.5",
              "command": [
                "sh", "-c",
                "set -e; mkdir -p /mnt/themes/energisa/login/resources/css /mnt/themes/energisa/login/resources/img; cp /theme-in/theme.properties /mnt/themes/energisa/login/; cp /theme-in/energisa.css /mnt/themes/energisa/login/resources/css/; cp /theme-in/logo.png /mnt/themes/energisa/login/resources/img/"
              ],
              "volumeMounts": [
                {"name": "theme-in", "mountPath": "/theme-in"},
                {"name": "energisa-themes", "mountPath": "/mnt/themes"}
              ],
              "securityContext": {"runAsUser": 1001000000, "runAsNonRoot": true, "allowPrivilegeEscalation": false}
            }
          ],
          "containers": [
            {
              "name": "keycloak",
              "volumeMounts": [
                {
                  "name": "energisa-themes",
                  "mountPath": "/opt/keycloak/themes/energisa",
                  "subPath": "energisa"
                }
              ]
            }
          ],
          "volumes": [
            {"name": "theme-in", "configMap": {"name": "keycloak-energisa-theme"}},
            {"name": "energisa-themes", "emptyDir": {}}
          ]
        }
      }
    }
  }
}'

echo "Waiting for Keycloak rollout..."
oc rollout status statefulset/keycloak -n "$NS" --timeout=300s

KC_HOST="https://keycloak-ingress-rdp.apps.cluster-k7vlv.dyn.redhatworkshops.io"
ADMIN_USER=$(oc get secret keycloak-initial-admin -n "$NS" -o jsonpath='{.data.username}' | base64 -d)
ADMIN_PASS=$(oc get secret keycloak-initial-admin -n "$NS" -o jsonpath='{.data.password}' | base64 -d)
TOKEN=$(curl -sk -X POST "$KC_HOST/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli" -d "username=$ADMIN_USER" -d "password=$ADMIN_PASS" -d "grant_type=password" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')

curl -sk -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"displayName":"Energisa Desktop Gateway","loginTheme":"energisa"}' \
  "$KC_HOST/admin/realms/guacamole" -w "\nrealm update HTTP:%{http_code}\n"

echo "Done. Open Guacamole to see branded Keycloak login."

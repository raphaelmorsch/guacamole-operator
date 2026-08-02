#!/bin/sh
set -eu
UPSTREAM="${PORTAL_API_UPSTREAM:-desktop-portal-portal-api:8443}"
CONF=/tmp/nginx.conf
sed "s|server desktop-portal-portal-api:8443;|server ${UPSTREAM};|" /etc/nginx/nginx.conf > "$CONF"

# Runtime OIDC bootstrap for the SPA (Keycloak public client + PKCE).
if [ -n "${OIDC_KEYCLOAK_URL:-}" ] && [ -n "${OIDC_REALM:-}" ] && [ -n "${OIDC_CLIENT_ID:-}" ]; then
  cat > /usr/share/nginx/html/config.json <<EOF
{
  "url": "${OIDC_KEYCLOAK_URL}",
  "realm": "${OIDC_REALM}",
  "clientId": "${OIDC_CLIENT_ID}",
  "issuer": "${OIDC_ISSUER:-${OIDC_KEYCLOAK_URL}/realms/${OIDC_REALM}}"
}
EOF
fi

exec nginx -c "$CONF" -g "daemon off;"

#!/bin/sh
set -eu

ROOT="${PLUGIN_ROOT:-/usr/share/nginx/html}"
PLUGIN_NAME="${PLUGIN_NAME:-guacamole-desktop-portal}"
CONSOLE_PATH="${CONSOLE_PATH:-/guacamole-desktops}"
NAV_SECTION="${NAV_SECTION:-home}"
DISPLAY_NAME="${DISPLAY_NAME:-Desktop Sessions}"
DEFAULT_PLUGIN_NAME="guacamole-desktop-portal"
DEFAULT_CONSOLE_PATH="/guacamole-desktops"
DEFAULT_NAV_ID="guacamole-desktops"
DEFAULT_DISPLAY_NAME="Desktop Sessions"
DEFAULT_NAV_SECTION="home"

NAV_ID="${CONSOLE_PATH#/}"
if [ -z "$NAV_ID" ]; then
  NAV_ID="$PLUGIN_NAME"
fi

# Rewrite plugin identity so one image can back many DesktopPortal CRs.
if [ -f "$ROOT/plugin-manifest.json" ]; then
  sed -i \
    -e "s|\"name\": \"${DEFAULT_PLUGIN_NAME}\"|\"name\": \"${PLUGIN_NAME}\"|g" \
    -e "s|\"displayName\": \"Guacamole Desktop Portal\"|\"displayName\": \"${DISPLAY_NAME}\"|g" \
    "$ROOT/plugin-manifest.json"
fi

if [ -f "$ROOT/console-extensions.json" ]; then
  sed -i \
    -e "s|${DEFAULT_CONSOLE_PATH}|${CONSOLE_PATH}|g" \
    -e "s|\"id\": \"${DEFAULT_NAV_ID}\"|\"id\": \"${NAV_ID}\"|g" \
    -e "s|\"name\": \"${DEFAULT_DISPLAY_NAME}\"|\"name\": \"${DISPLAY_NAME}\"|g" \
    -e "s|\"section\": \"${DEFAULT_NAV_SECTION}\"|\"section\": \"${NAV_SECTION}\"|g" \
    "$ROOT/console-extensions.json"
fi

# Replace baked-in defaults inside webpack bundles (runtime plugin name / path).
find "$ROOT" -type f \( -name '*.js' -o -name '*.json' \) -print0 |
  xargs -0 sed -i \
    -e "s|${DEFAULT_PLUGIN_NAME}|${PLUGIN_NAME}|g" \
    -e "s|${DEFAULT_CONSOLE_PATH}|${CONSOLE_PATH}|g"

exec nginx -g "daemon off;"

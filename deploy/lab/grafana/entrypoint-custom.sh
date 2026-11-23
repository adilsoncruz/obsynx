#!/bin/sh
set -e

# Target Grafana public directory
PUBLIC_DIR="/usr/share/grafana/public"

# Copy logo and favicon
if [ -f /etc/grafana/branding/obsynx-logo.svg ]; then
  cp /etc/grafana/branding/obsynx-logo.svg "$PUBLIC_DIR/img/obsynx-logo.svg"
  cp /etc/grafana/branding/obsynx-logo.svg "$PUBLIC_DIR/img/grafana_icon.svg"
  cp /etc/grafana/branding/obsynx-logo.svg "$PUBLIC_DIR/img/fav32.png"
  cp /etc/grafana/branding/obsynx-logo.svg "$PUBLIC_DIR/img/grafana_com_auth_icon.svg"
fi

# Inject custom theme into index.html if not already present
INDEX_FILE="$PUBLIC_DIR/views/index.html"
if [ -f "$INDEX_FILE" ] && ! grep -q "obsynx-theme.css" "$INDEX_FILE"; then
  # Copy CSS to public/css
  mkdir -p "$PUBLIC_DIR/css"
  cp /etc/grafana/branding/obsynx-theme.css "$PUBLIC_DIR/css/obsynx-theme.css"

  # Replace page title and inject stylesheet link
  sed -i 's/<title>\[\[\.AppTitle\]\]<\/title>/<title>Obsynx — Zero-Code Observability<\/title>/g' "$INDEX_FILE" 2>/dev/null || true
  sed -i 's/<\/head>/<link rel="stylesheet" href="\/public\/css\/obsynx-theme.css"><\/head>/g' "$INDEX_FILE" 2>/dev/null || true
fi

# Execute original entrypoint
exec /run.sh "$@"

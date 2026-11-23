# OBSynx Agent Injector Container Image
# Multi-stage lightweight image (~5MB) for Kubernetes InitContainers & Docker Compose
FROM alpine:3.20

LABEL org.opencontainers.image.title="OBSynx Agent Injector"
LABEL org.opencontainers.image.description="Zero-code runtime agent injector for OBSynx"
LABEL org.opencontainers.image.source="https://github.com/adilsoncruz/obsynx"

RUN apk add --no-cache ca-certificates

WORKDIR /opt/obsynx

# Bundle all runtime capability adapters
COPY adapters /opt/obsynx/adapters
COPY deploy/injector/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

VOLUME ["/obsynx-agents"]

ENTRYPOINT ["/entrypoint.sh"]

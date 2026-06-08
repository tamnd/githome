# This Dockerfile is consumed by GoReleaser: it copies the already cross-compiled
# binaries out of the build context rather than compiling, so the image build is
# fast and uses the same static binaries every other artifact ships. The base is
# Alpine (not distroless) because the server shells out to a real git binary for
# the smart-HTTP transport and history reads.
#
# GoReleaser builds one multi-platform image with buildx and stages each
# platform's binaries under a $TARGETPLATFORM directory (e.g. linux/amd64/) in
# the build context, so the COPY lines select the right binaries through the
# automatic TARGETPLATFORM build arg.
FROM alpine:3.21

ARG TARGETPLATFORM

RUN apk add --no-cache ca-certificates git tzdata \
 && adduser -D -H -u 10001 githome \
 && mkdir -p /var/lib/githome \
 && chown githome:githome /var/lib/githome

COPY $TARGETPLATFORM/githome /usr/bin/githome
COPY $TARGETPLATFORM/githome-migrate /usr/bin/githome-migrate
COPY $TARGETPLATFORM/githome-conform /usr/bin/githome-conform

USER githome
WORKDIR /var/lib/githome

# The server reads its configuration from the environment; see INSTALL.md. The
# data directory is a sensible mount point for the SQLite database and repos.
ENV GITHOME_DATA_DIR=/var/lib/githome
VOLUME ["/var/lib/githome"]
EXPOSE 3000

ENTRYPOINT ["/usr/bin/githome"]

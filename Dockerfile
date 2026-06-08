# This Dockerfile is consumed by GoReleaser: it copies the already cross-compiled
# binaries out of the build context rather than compiling, so the image build is
# fast and uses the same static binaries every other artifact ships. The base is
# Alpine (not distroless) because the server shells out to a real git binary for
# the smart-HTTP transport and history reads.
FROM alpine:3.21

RUN apk add --no-cache ca-certificates git tzdata \
 && adduser -D -H -u 10001 githome \
 && mkdir -p /var/lib/githome \
 && chown githome:githome /var/lib/githome

COPY githome /usr/bin/githome
COPY githome-migrate /usr/bin/githome-migrate
COPY githome-conform /usr/bin/githome-conform

USER githome
WORKDIR /var/lib/githome

# The server reads its configuration from the environment; see INSTALL.md. The
# data directory is a sensible mount point for the SQLite database and repos.
ENV GITHOME_DATA_DIR=/var/lib/githome
VOLUME ["/var/lib/githome"]
EXPOSE 3000

ENTRYPOINT ["/usr/bin/githome"]

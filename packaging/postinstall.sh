#!/bin/sh
# Provision the service account and data directory after the package installs.
# It runs on Debian/RPM and Alpine, so it probes for whichever user tools the
# distribution ships and never fails the install if one is missing.
set -e

if ! getent group githome >/dev/null 2>&1; then
	groupadd --system githome >/dev/null 2>&1 ||
		addgroup --system githome >/dev/null 2>&1 || true
fi

if ! getent passwd githome >/dev/null 2>&1; then
	useradd --system --gid githome --home-dir /var/lib/githome \
		--shell /usr/sbin/nologin githome >/dev/null 2>&1 ||
		adduser --system --ingroup githome --home /var/lib/githome \
			--shell /sbin/nologin --no-create-home githome >/dev/null 2>&1 || true
fi

mkdir -p /var/lib/githome
chown githome:githome /var/lib/githome 2>/dev/null || true
chmod 750 /var/lib/githome 2>/dev/null || true

if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	echo "githome installed. Edit /etc/githome/githome.env, then run: systemctl enable --now githome"
fi

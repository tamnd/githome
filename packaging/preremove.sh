#!/bin/sh
# Stop and disable the service before the package is removed. The data directory
# and the service account are left in place so an upgrade or reinstall keeps the
# existing repositories and database.
set -e

if command -v systemctl >/dev/null 2>&1; then
	systemctl disable --now githome >/dev/null 2>&1 || true
fi

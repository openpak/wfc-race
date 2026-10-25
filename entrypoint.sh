#!/bin/sh
# Renders config.xml from WFC_* environment variables, then runs the server.
set -e
: "${WFC_DB_USER:?}" "${WFC_DB_PASSWORD:?}" "${WFC_DB_ADDRESS:?}" "${WFC_API_SECRET:?}"
: "${WFC_DB_NAME:=wwfc}" "${WFC_NAS_PORT:=20120}" "${WFC_LOG_LEVEL:=4}"
sed -e "s|@DB_USER@|$WFC_DB_USER|; s|@DB_PASSWORD@|$WFC_DB_PASSWORD|; s|@DB_ADDRESS@|$WFC_DB_ADDRESS|; s|@DB_NAME@|$WFC_DB_NAME|" \
    -e "s|@NAS_PORT@|$WFC_NAS_PORT|; s|@API_SECRET@|$WFC_API_SECRET|; s|@LOG_LEVEL@|$WFC_LOG_LEVEL|" \
    -e "s|@CORE_ADDRESS@|$WFC_CORE_ADDRESS|; s|@CORE_KEY@|$WFC_CORE_KEY|" \
    config.template.xml > config.xml
exec wwfc "$@"

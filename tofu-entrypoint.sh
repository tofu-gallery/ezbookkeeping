#!/bin/sh
# Tofu (2026-10-25): this file is Tofu's addition; it is not part of upstream ezBookkeeping.
#
# Maps the variables Tofu sets on every app onto ezBookkeeping's own EBK_* settings, then starts upstream's own
# /docker-entrypoint.sh with the same arguments:
#   PORT          -> EBK_SERVER_HTTP_PORT. Tofu sends traffic to PORT, so PORT wins over EBK_SERVER_HTTP_PORT; without
#                    PORT the configuration file's 8080 stays.
#   DATABASE_URL  -> EBK_DATABASE_TYPE=postgres plus EBK_DATABASE_HOST (host:port), EBK_DATABASE_NAME,
#                    EBK_DATABASE_USER, EBK_DATABASE_PASSWD and EBK_DATABASE_SSL_MODE. An EBK_DATABASE_* value that is
#                    already set is kept as it is.
#   EBK_SECURITY_SECRET_KEY, when it is not set, is derived from DATABASE_URL (TOFU.md, "The secret key").
set -eu

# Decodes the %XX escapes of one URL component: DATABASE_URL percent-encodes its user, password and database.
url_decode() {
	printf '%b' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/%\([0-9A-Fa-f][0-9A-Fa-f]\)/\\x\1/g')"
}

if [ -n "${PORT:-}" ]; then
	EBK_SERVER_HTTP_PORT=$PORT
	export EBK_SERVER_HTTP_PORT
fi

if [ -n "${DATABASE_URL:-}" ]; then
	case $DATABASE_URL in
	postgres://* | postgresql://*)
		# postgresql://user:password@host:port/database?sslmode=require
		rest=${DATABASE_URL#*://}

		query=
		case $rest in
		*\?*)
			query=${rest#*\?}
			rest=${rest%%\?*}
			;;
		esac

		database=
		case $rest in
		*/*)
			database=${rest#*/}
			rest=${rest%%/*}
			;;
		esac

		userinfo=
		case $rest in
		*@*)
			userinfo=${rest%@*}
			rest=${rest##*@}
			;;
		esac
		user=${userinfo%%:*}
		password=
		case $userinfo in
		*:*) password=${userinfo#*:} ;;
		esac

		# ezBookkeeping reads host and port as one "host:port" value; an IPv6 address keeps its brackets.
		hostport=$rest
		case $hostport in
		\[*\]:* | \[*\]) ;;
		*:*) ;;
		*) hostport=$hostport:5432 ;;
		esac
		case $hostport in
		\[*\]) hostport=$hostport:5432 ;;
		esac

		sslmode=
		query="&$query"
		case $query in
		*'&sslmode='*)
			sslmode=${query#*'&sslmode='}
			sslmode=${sslmode%%'&'*}
			;;
		esac

		: "${EBK_DATABASE_TYPE:=postgres}"
		: "${EBK_DATABASE_HOST:=$hostport}"
		: "${EBK_DATABASE_USER:=$(url_decode "$user")}"
		: "${EBK_DATABASE_PASSWD:=$(url_decode "$password")}"
		export EBK_DATABASE_TYPE EBK_DATABASE_HOST EBK_DATABASE_USER EBK_DATABASE_PASSWD
		if [ -n "$database" ]; then
			: "${EBK_DATABASE_NAME:=$(url_decode "$database")}"
			export EBK_DATABASE_NAME
		fi
		# A URL without sslmode gets libpq's own default, prefer, rather than the configuration file's "disable".
		: "${EBK_DATABASE_SSL_MODE:=${sslmode:-prefer}}"
		export EBK_DATABASE_SSL_MODE
		;;
	*)
		echo "tofu-entrypoint: DATABASE_URL is not a postgres:// URL, so the EBK_DATABASE_* settings are left as they are" >&2
		;;
	esac
fi

# ezBookkeeping encrypts each two-factor secret with secret_key, and without one it falls back to the fixed word
# "ezbookkeeping" from its source code. Tofu does not supply one; derive it from the database URL so a copy starts with
# a key of its own and nothing to set by hand. An owner-set EBK_SECURITY_SECRET_KEY always wins.
if [ -z "${EBK_SECURITY_SECRET_KEY:-}" ] && [ -z "${EBKCFP_SECURITY_SECRET_KEY:-}" ] && [ -n "${DATABASE_URL:-}" ]; then
	EBK_SECURITY_SECRET_KEY=$(printf '%s' "ezbookkeeping-secret-key:${DATABASE_URL}" | sha256sum | cut -d' ' -f1)
	if [ ${#EBK_SECURITY_SECRET_KEY} -ne 64 ]; then
		echo "tofu-entrypoint: could not derive secret_key from DATABASE_URL; set EBK_SECURITY_SECRET_KEY" >&2
		exit 1
	fi
	export EBK_SECURITY_SECRET_KEY
fi

exec /docker-entrypoint.sh "$@"

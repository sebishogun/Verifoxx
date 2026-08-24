#!/bin/sh
set -eu

script_directory=$(CDPATH= cd -P "$(dirname "$0")" && pwd)
dispatcher=$script_directory/devx-dispatch
dispatcher_marker='# devx-repository-dispatch-v1'
prefix=${DEVX_PREFIX:-${HOME:?HOME is required}/.local}
dry_run=0
uninstall=0

usage() {
	printf '%s\n' 'Usage: ./cli/install.sh [--prefix DIR] [--dry-run] [--uninstall]'
}

while [ "$#" -gt 0 ]; do
	case $1 in
		--prefix)
			if [ "$#" -lt 2 ] || [ -z "$2" ]; then
				printf '%s\n' 'install.sh: --prefix requires a directory' >&2
				exit 2
			fi
			prefix=$2
			shift 2
			;;
		--dry-run)
			dry_run=1
			shift
			;;
		--uninstall)
			uninstall=1
			shift
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			printf 'install.sh: unknown argument: %s\n' "$1" >&2
			usage >&2
			exit 2
			;;
	esac
done

case $prefix in
	/*) ;;
	*)
		printf 'install.sh: prefix must be absolute: %s\n' "$prefix" >&2
		exit 2
		;;
esac

bin_directory=$prefix/bin
destination=$bin_directory/devx
printf 'prefix: %s\n' "$prefix"

is_owned_dispatcher() {
	[ -f "$destination" ] && [ ! -L "$destination" ] || return 1
	installed_marker=
	{
		IFS= read -r _installed_shebang || :
		IFS= read -r installed_marker || :
	} <"$destination"
	[ "$installed_marker" = "$dispatcher_marker" ]
}

is_legacy_repository_link() {
	[ -L "$destination" ] || return 1
	installed_target=$(readlink "$destination")
	case $installed_target in
	*/cli/devx) return 0 ;;
	*) return 1 ;;
	esac
}

refuse_unowned_destination() {
	if [ -L "$destination" ]; then
		printf 'install.sh: refusing unowned symlink: %s -> %s\n' "$destination" "$(readlink "$destination")" >&2
	else
		printf 'install.sh: refusing unowned path: %s\n' "$destination" >&2
	fi
	exit 1
}

if [ "$uninstall" -eq 1 ]; then
	if is_owned_dispatcher || is_legacy_repository_link; then
		if [ "$dry_run" -eq 1 ]; then
			printf '[dry-run] rm %s\n' "$destination"
		else
			rm "$destination"
			printf 'removed: %s\n' "$destination"
		fi
	elif [ -e "$destination" ] || [ -L "$destination" ]; then
		refuse_unowned_destination
	else
		printf 'not installed: %s\n' "$destination"
	fi
	exit 0
fi

if [ ! -x "$dispatcher" ]; then
	printf 'install.sh: dispatcher is not executable: %s\n' "$dispatcher" >&2
	exit 1
fi

destination_exists=0
if [ -e "$destination" ] || [ -L "$destination" ]; then
	destination_exists=1
	if ! is_owned_dispatcher && ! is_legacy_repository_link; then
		refuse_unowned_destination
	fi
fi

if [ "$dry_run" -eq 1 ]; then
	if [ ! -d "$bin_directory" ]; then
		printf '[dry-run] mkdir -p %s\n' "$bin_directory"
	fi
	printf '[dry-run] install -m 0755 %s %s\n' "$dispatcher" "$destination"
else
	mkdir -p "$bin_directory"
	temporary=$(mktemp "$destination.tmp.XXXXXX")
	cleanup_temporary() {
		if [ -n "${temporary:-}" ]; then
			rm -f "$temporary"
		fi
	}
	trap cleanup_temporary EXIT
	trap 'exit 1' HUP INT TERM
	install -m 0755 "$dispatcher" "$temporary"
	if [ -L "$destination" ]; then
		rm "$destination"
	fi
	mv -f "$temporary" "$destination"
	temporary=
	if [ "$destination_exists" -eq 1 ]; then
		printf 'updated: %s\n' "$destination"
	else
		printf 'installed: %s\n' "$destination"
	fi
fi

case :${PATH:-}: in
	*:$bin_directory:*) printf 'PATH: %s is present\n' "$bin_directory" ;;
	*) printf 'PATH: %s is not present\n' "$bin_directory" ;;
esac
printf '%s\n' 'Shell startup files were not modified.'

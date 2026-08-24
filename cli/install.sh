#!/bin/sh
set -eu

script_directory=$(CDPATH= cd -P "$(dirname "$0")" && pwd)
wrapper=$script_directory/devx
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

if [ "$uninstall" -eq 1 ]; then
	if [ -L "$destination" ]; then
		installed_target=$(readlink "$destination")
		if [ "$installed_target" != "$wrapper" ]; then
			printf 'install.sh: refusing to remove unowned symlink: %s -> %s\n' "$destination" "$installed_target" >&2
			exit 1
		fi
		if [ "$dry_run" -eq 1 ]; then
			printf '[dry-run] rm %s\n' "$destination"
		else
			rm "$destination"
			printf 'removed: %s\n' "$destination"
		fi
	elif [ -e "$destination" ]; then
		printf 'install.sh: refusing to remove unowned path: %s\n' "$destination" >&2
		exit 1
	else
		printf 'not installed: %s\n' "$destination"
	fi
	exit 0
fi

if [ ! -x "$wrapper" ]; then
	printf 'install.sh: wrapper is not executable: %s\n' "$wrapper" >&2
	exit 1
fi

if [ "$dry_run" -eq 1 ]; then
	if [ -L "$destination" ]; then
		installed_target=$(readlink "$destination")
		if [ "$installed_target" != "$wrapper" ]; then
			printf 'install.sh: refusing to replace unowned symlink: %s -> %s\n' "$destination" "$installed_target" >&2
			exit 1
		fi
		printf '[dry-run] already installed: %s -> %s\n' "$destination" "$wrapper"
	elif [ -e "$destination" ]; then
		printf 'install.sh: refusing to replace unowned path: %s\n' "$destination" >&2
		exit 1
	else
		printf '[dry-run] mkdir -p %s\n' "$bin_directory"
		printf '[dry-run] ln -s %s %s\n' "$wrapper" "$destination"
	fi
else
	mkdir -p "$bin_directory"
	if [ -L "$destination" ]; then
		installed_target=$(readlink "$destination")
		if [ "$installed_target" != "$wrapper" ]; then
			printf 'install.sh: refusing to replace unowned symlink: %s -> %s\n' "$destination" "$installed_target" >&2
			exit 1
		fi
		printf 'already installed: %s -> %s\n' "$destination" "$wrapper"
	elif [ -e "$destination" ]; then
		printf 'install.sh: refusing to replace unowned path: %s\n' "$destination" >&2
		exit 1
	else
		ln -s "$wrapper" "$destination"
		printf 'installed: %s -> %s\n' "$destination" "$wrapper"
	fi
fi

case :${PATH:-}: in
	*:$bin_directory:*) printf 'PATH: %s is present\n' "$bin_directory" ;;
	*) printf 'PATH: %s is not present\n' "$bin_directory" ;;
esac
printf '%s\n' 'Shell startup files were not modified.'

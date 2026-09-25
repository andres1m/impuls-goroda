#!/bin/sh
set -eu

schema_dir=/etc/temporal/schema/postgresql/v12

for db in temporal temporal_visibility; do
    case "$db" in
        temporal) dir="$schema_dir/temporal/versioned" ;;
        temporal_visibility) dir="$schema_dir/visibility/versioned" ;;
    esac
    # setup-schema only creates the version table; update-schema applies pending versions, so reruns are no-ops.
    temporal-sql-tool --plugin postgres12 --ep postgres -p 5432 -u temporal --db "$db" \
        setup-schema -v 0.0
    temporal-sql-tool --plugin postgres12 --ep postgres -p 5432 -u temporal --db "$db" \
        update-schema -d "$dir"
done

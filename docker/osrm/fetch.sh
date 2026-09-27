#!/bin/sh
# Downloads the OpenStreetMap extracts, cuts the configured cities out of them and
# stages one file for the graph build under /data/<version>/.
set -eu

regions=/etc/osrm/regions.conf
recipe=/etc/osrm/build.sh
data=/data
downloads=$data/downloads
work=$data/work
mirror=${GEOFABRIK_URL:-https://download.geofabrik.de}
agent="impuls-goroda-routing (+https://github.com/andres1m/impuls-goroda)"

entries() {
    grep -v '^[[:space:]]*#' "$regions" | awk 'NF == 3 {print $1, $2, $3}'
}

mkdir -p "$downloads"
rm -rf "$work"
mkdir -p "$work"

stamps=
for extract in $(entries | awk '{print $2}' | sort -u); do
    file=$downloads/$(echo "$extract" | tr / _).osm.pbf
    rm -f "$file.part"
    # With -z the mirror sends the file only if it changed since the cached copy; -R keeps
    # the mirror's modification time on the copy so that comparison stays exact.
    if [ -f "$file" ]; then
        curl -fsSLR -A "$agent" -z "$file" -o "$file.part" "$mirror/$extract-latest.osm.pbf"
    else
        curl -fsSLR -A "$agent" -o "$file.part" "$mirror/$extract-latest.osm.pbf"
    fi
    if [ -s "$file.part" ]; then
        mv "$file.part" "$file"
        echo "downloaded $extract"
    else
        rm -f "$file.part"
        echo "$extract is unchanged"
    fi
    stamp=$(osmium fileinfo -g header.option.osmosis_replication_timestamp "$file")
    if [ -z "$stamp" ]; then
        echo "$extract has no replication timestamp" >&2
        exit 1
    fi
    stamps="$stamps$stamp
"
done
# The graph is only as fresh as its oldest extract, and a changed city list or build recipe
# is a new graph even when the map data is the same.
stamp=$(printf '%s' "$stamps" | sort | head -n 1 | tr -d ':-')
version=$stamp-$( { entries; cat "$recipe"; } | sha256sum | cut -c1-8)
if [ -z "$stamp" ]; then
    echo "no regions configured in $regions" >&2
    exit 1
fi

entries | while read -r city extract box; do
    osmium extract --overwrite -b "$box" -o "$work/$city.osm.pbf" "$downloads/$(echo "$extract" | tr / _).osm.pbf"
done
osmium merge --overwrite -o "$work/regions.osm.pbf" "$work"/*.osm.pbf

if [ -f "$data/$version/.complete" ]; then
    echo "routing data $version is already built"
else
    rm -rf "${data:?}/$version"
    mkdir -p "$data/$version"
    mv "$work/regions.osm.pbf" "$data/$version/regions.osm.pbf"
    cp "$regions" "$data/$version/regions.conf"
    echo "staged routing data $version"
fi
rm -rf "$work"
echo "$version" > "$data/next"

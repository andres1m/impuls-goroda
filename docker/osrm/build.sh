#!/bin/sh
# Builds the graphs of the staged version and switches /data/current to it. The algorithm
# of each profile must match the one its osrm-routed service is started with.
set -eu

data=/data
keep=2

version=$(cat "$data/next")
dir=$data/$version

if [ -f "$dir/.complete" ]; then
    echo "routing graphs $version are already built"
else
    for profile in foot car; do
        rm -rf "${dir:?}/$profile"
        mkdir "$dir/$profile"
        cp "$dir/regions.osm.pbf" "$dir/$profile/regions.osm.pbf"
        # Every router response carries the version, so the optimizer can cite its data date.
        osrm-extract -p "/opt/$profile.lua" --data_version "$version" "$dir/$profile/regions.osm.pbf"
        rm "$dir/$profile/regions.osm.pbf"
    done
    # Contraction hierarchies answer distance tables fastest on the dense walking network.
    osrm-contract "$dir/foot/regions.osrm"
    # Multi-level Dijkstra builds quickly and lets segment speeds change without a full rebuild.
    osrm-partition "$dir/car/regions.osrm"
    osrm-customize "$dir/car/regions.osrm"
    rm "$dir/regions.osm.pbf"
    touch "$dir/.complete"
    echo "built routing graphs $version"
fi

ln -sfn "$version" "$data/current.next"
mv -T "$data/current.next" "$data/current"
echo "current routing graphs: $version"

# Only built versions count towards the ones kept, so a failed build never pushes out a good one.
find "$data" -mindepth 1 -maxdepth 1 -type d -name '[0-9]*T*Z*' | sed 's|.*/||' | sort -r | while read -r old; do
    [ "$old" = "$version" ] && continue
    if [ -f "$data/$old/.complete" ]; then
        keep=$((keep - 1))
        [ "$keep" -gt 0 ] && continue
    fi
    rm -rf "${data:?}/$old"
    echo "removed routing graphs $old"
done

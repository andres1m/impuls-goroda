#!/bin/sh
set -eu

nodes="redis-1 redis-2 redis-3"

for node in $nodes; do
    until redis-cli -h "$node" ping | grep -q PONG; do sleep 1; done
done

if ! redis-cli -h redis-1 cluster info | tr -d '\r' | grep -qx 'cluster_known_nodes:1'; then
    until redis-cli -h redis-1 cluster info | grep -q 'cluster_state:ok'; do sleep 1; done
    echo "redis cluster already formed"
    exit 0
fi

redis-cli --cluster create redis-1:6379 redis-2:6379 redis-3:6379 --cluster-replicas 0 --cluster-yes

until redis-cli -h redis-1 cluster info | grep -q 'cluster_state:ok'; do sleep 1; done
echo "redis cluster formed"

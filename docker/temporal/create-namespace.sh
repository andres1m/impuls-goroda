#!/bin/sh
set -eu

until temporal operator cluster health --address temporal:7233 | grep -q SERVING; do sleep 1; done

if temporal operator namespace describe --address temporal:7233 --namespace "$TEMPORAL_NAMESPACE" >/dev/null 2>&1; then
    echo "namespace $TEMPORAL_NAMESPACE exists"
    exit 0
fi

temporal operator namespace create --address temporal:7233 --namespace "$TEMPORAL_NAMESPACE" --retention 72h

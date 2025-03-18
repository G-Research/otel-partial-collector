#!/bin/bash

main() {
    for target in "exporter/otelpartialexporter" "receiver/otelpartialreceiver" "internal/postgres"; do
        cd $target
        golangci-lint run --config ../../.golangci.yaml
        cd -
    done
}

main

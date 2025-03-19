#!/bin/bash

gotest() {
    local target=$1
    cd $target
    go test ./...
    exit_code=$?
    cd -
    return $exit_code
}

main() {
    local failed=()

    for target in "exporter/otelpartialexporter" "receiver/otelpartialreceiver" "internal/postgres"; do
        gotest "${target}" || failed+=("${target}")
    done

    if [[ "${#failed[@]}" -ne 0 ]]; then
        echo "------------------------"
        echo "Error: Some tests failed"
        for test in "${failed[@]}"; do
            echo "  - ${test}"
        done
        echo "------------------------"
        exit 1
    fi
}

main

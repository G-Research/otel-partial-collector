#!/bin/bash

TAG_REGEX="^v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(\\-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?(\\+[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$"

main() {
    local tag=$1
    echo $tag
    if [[ ! "${tag}" =~ $TAG_REGEX ]]; then
        echo "Tag validation failed: tag '${tag}'"
        exit 1
    fi

    for prefix in "" "exporter/otelpartialexporter/" "internal/postgres/" "receiver/otelpartialreceiver/"; do
        git tag "${prefix}${tag}"
        git push origin "${prefix}${tag}"
    done
}

main "$@"

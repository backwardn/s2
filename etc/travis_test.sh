#!/bin/bash

set -e

make ./conformance/s3-tests

pushd examples/sql
    make run 2> stderr.txt &

    until (curl --connect-timeout 1 -s -D - http://localhost:8080 -o /dev/null | head -n1 | grep 403); do
        echo -n '.'
        sleep 1
    done

    make test
popd

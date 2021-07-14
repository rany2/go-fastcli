#!/bin/sh
find -name '*.go' -type f -print0 | xargs --verbose --null gofmt -l -w -s

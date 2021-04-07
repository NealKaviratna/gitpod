#!/bin/bash

GOFLAGS="-mod=readonly"

leeway exec --filter-type go -v -- go mod download

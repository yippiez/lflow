#!/bin/sh

# Set default DBPath to /data if not specified
export DBPath=${DBPath:-/data/lflow.db}

exec "$@"

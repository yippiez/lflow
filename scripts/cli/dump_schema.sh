#!/usr/bin/env bash
# dump_schema.sh dumps the current system's lflow schema
set -eux

sqlite3 ~/.lflow/lflow.db .schema

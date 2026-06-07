![Lflow](assets/logo.png)
=========================

![Build Status](https://github.com/lflow/lflow/actions/workflows/ci.yml/badge.svg)

Lflow is a simple command line notebook. Single binary, no dependencies.

Your notes are stored in **one SQLite file** - portable, searchable, and completely under your control. Optional sync between devices via a self-hosted server with REST API access.

```sh
# Add a note (or omit -c to launch your editor)
lflow add linux -c "Check disk usage with df -h"

# View notes in a book
lflow view linux

# Full-text search
lflow find "disk usage"

# Sync notes
lflow sync
```

## Installation

```bash
# Quick install script
curl -s https://raw.githubusercontent.com/lflow/lflow/master/install.sh | sh

# macOS with Homebrew
brew install lflow
```

Or [download binary](https://github.com/lflow/lflow/releases).

## Server (Optional)

Server is a binary with SQLite embedded. No database setup is required.

If using docker, create a compose.yml:

```yaml
services:
  lflow:
    image: lflow/lflow:latest
    container_name: lflow
    ports:
      - 3001:3001
    volumes:
      - ./lflow_data:/data
    restart: unless-stopped
```

Then run:

```bash
docker-compose up -d
```

Or see the [guide](https://github.com/lflow/lflow) for binary installation.

## Documentation

See the [Lflow docs](https://github.com/lflow/lflow).

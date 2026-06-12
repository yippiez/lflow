# Self-Hosting Lflow Server

The lflow server is what `lflow sync` talks to. It stores your outline nodes and
speaks the USN-based sync protocol; the CLI works fully offline without it, so the
server is only needed if you want to sync nodes across devices.

Please see the [doc](https://github.com/lflow/lflow) for more.

## Docker Installation

1. Install [Docker](https://docs.docker.com/install/).
2. Install Docker [Compose plugin](https://docs.docker.com/compose/install/linux/).
3. Create a `compose.yml` file with the following content:

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

4. Run the following to download the image and start the container

```
docker compose up -d
```

Visit http://localhost:3001 in your browser to see Lflow running.

## Manual Installation

Download from [releases](https://github.com/lflow/lflow/releases), extract, and run:

```bash
tar -xzf lflow-server-$version-$os.tar.gz
mv ./lflow-server /usr/local/bin
lflow-server start --baseUrl=https://your.server
```

You're up and running. Database: `~/.local/share/lflow/server.db` (customize with `--dbPath`). Run `lflow-server start --help` for options.

Set `apiEndpoint: https://your.server/api` in `~/.config/lflow/lflowrc` to connect your CLI to the server.

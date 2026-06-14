# Self-Hosting Lflow Server

The lflow server is what `lflow server sync` talks to. It stores your outline nodes and
speaks the USN-based sync protocol; the CLI works fully offline without it, so the
server is only needed if you want to sync nodes across devices.

Please see the [doc](https://github.com/lflow/lflow) for more.

## Manual Installation

Download from [releases](https://github.com/lflow/lflow/releases), extract, and run:

```bash
tar -xzf lflow-server-$version-$os.tar.gz
mv ./lflow-server /usr/local/bin
lflow-server start --baseUrl=https://your.server
```

You're up and running. Database: `~/.local/share/lflow/server.db` (customize with `--dbPath`). Run `lflow-server start --help` for options.

Set `apiEndpoint: https://your.server/api` in `~/.config/lflow/lflowrc` to connect your CLI to the server.

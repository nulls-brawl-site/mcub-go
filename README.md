# MCUB Go

A Go port of [MCUB-fork](https://github.com/hairpin01/MCUB-fork), a Telegram userbot.

## Requirements

- Go 1.19+
- GCC (required by `mattn/go-sqlite3` CGo driver)

## Quick start

```bash
# 1. Copy and fill in the config
cp config.example.json config.json
$EDITOR config.json   # set api_id, api_hash, phone

# 2. Build
go build ./cmd/mcub

# 3. Run
./mcub
```

## CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.json` | Path to config file |
| `--core` | `standard` | Kernel type: `standard` or `zen` |
| `--no-web` | `false` | Disable web panel |
| `--port` | `8080` | Web panel port |
| `--host` | `127.0.0.1` | Web panel host |
| `--set-default-core` | | Save selected core as default |
| `--clear-default-core` | | Clear default core from config |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--version` | | Print version and exit |

## Project layout

```
cmd/mcub/main.go            Entry point, CLI flags
internal/config/            Config struct, load/save JSON
internal/database/          SQLite KV store (mattn/go-sqlite3)
internal/kernel/
  kernel.go                 Kernel struct, registries, state
  lifecycle.go              Init, Run, Shutdown, Restart
  handlers.go               Event handler wiring, middleware
  commands.go               Command dispatch + alias resolution
internal/loader/
  registry.go               Module + command registry
  loader.go                 Built-in and plugin module loading
internal/logger/            Levelled structured logger
modules/                    Built-in system modules (Go source)
modules_loaded/             User-added modules (Go plugins)
```

## Module interface

```go
type Module interface {
    Name() string
    OnLoad(kernel interface{}) error
    OnUnload(kernel interface{}) error
    Commands() []Command
}
```

Implement this interface and call `kernel.Loader.LoadBuiltin(m)` during startup,
or build a `*.so` plugin and place it in `modules_loaded/`.

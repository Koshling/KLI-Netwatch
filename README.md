# KLI-Netwatch
KLI-Netwatch is a free-software TUI program for cool display of your sites (or sth else) visitors from logs (nginx usually) on a spinning ASCII globe. Heavily vibe-coded, but this program is too useless in its functionality to harm your PC (prolly).

## Status

**pre-v1** is an experimental source snapshot, not a stable release. Linux terminal UI; Nginx access logs are the primary input. Use at your own risk.

## Building from source

Requires Go 1.26.10 or a compatible newer toolchain.

```sh
go test ./...
go vet ./...
CGO_ENABLED=0 go build -o netwatch ./cmd/netwatch
```

## First launch

Run `./netwatch` in a Linux terminal. If a configuration is missing, setup asks for your access log and optional GeoIP data. Press Enter to accept a suggested log path or type your own. NETWATCH chooses where to save the config automatically. NETWATCH looks for readable access logs in the usual Nginx and Apache locations and samples the log for a supported format. If it cannot identify a single format, setup asks you to choose and suggests `combined`. The process needs read access to your access log.

The user configuration defaults to `$XDG_CONFIG_HOME/netwatch/netwatch.json` (normally `~/.config/netwatch/netwatch.json`). NETWATCH also checks the legacy `configs/netwatch.json` in the current directory and system configuration directories. To run setup separately or use a particular file:

```sh
./netwatch setup
./netwatch setup -config /path/to/netwatch.json
./netwatch -config /path/to/netwatch.json
```

Use `./netwatch --auto-config` to create a configuration from a readable standard access log and any detected local GeoIP files, then exit. It leaves existing configurations intact and performs no downloads.

See the [access log format reference](docs/access-logs.md) for custom Nginx templates, Caddy, Traefik, Apache, Envoy and JSON field mappings.

## Modes and controls

The regular launch opens Live. Press **M** (`m` or `M`) in Live, Replay or Interval to open **MENU**. Use the arrow keys to select a mode and Enter to open it. Themes **1–5** carry over between screens, and **Q** exits NETWATCH.

Replay and Interval use the configured access log and the last 15 minutes by default. Press **S** on either card to set a different duration (for example `01:30`, `01,30`, `0130` or `1h30m`), a pair of RFC3339 timestamps separated by `/`, or a different log file. The form shows the configured log path until you type an override. In the settings, use Enter or the down arrow to move to **Start** and Enter to launch. Esc goes back.

**GeoIP Convert** asks for a CSV path, an output path and the IP family. An existing folder, or a new folder path ending in `/`, receives `ipv4.nwgeo` or `ipv6.nwgeo`. Convert returns to the menu after success.

Replay and interval can also be launched directly:

```sh
./netwatch -mode replay -last 15m
./netwatch -interval 1h
./netwatch -mode replay -from 2026-11-22T10:00:00+03:00 -to 2026-11-22T11:00:00+03:00
```

These flags use historical access logs. The `geoip convert` subcommand works independently of monitor configuration.

## GeoIP

GeoIP is optional. Setup checks for `ipv4.nwgeo`, `ipv6.nwgeo`, `ipv4.csv` and `ipv6.csv` beside the config, in `$XDG_DATA_HOME/netwatch` (normally `~/.local/share/netwatch`), and in the local `data` directory. Choose `path` to supply your own compatible files.

Setup can download DB-IP Lite as a gzip-compressed CSV and convert both address families to NWGEO automatically. It keeps the two NWGEO files and their attribution and removes the conversion CSV files. **Countries** is the smallest download. **Regions** downloads the same archive as **Cities** and combines adjacent ranges with the same region to reduce the installed size. Region points are approximate. When no local database is found, Enter selects download and opens the detail menu. `n` skips the download for now. `never` skips future prompts. To re-enable prompting, set `"geoip_prompt": true` in the config while both database paths are empty.

IP Geolocation by [DB-IP](https://db-ip.com), licensed separately under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). NETWATCH releases contain the program, and setup downloads the data separately. Setup saves the source, license and description of the conversion alongside the converted files. Country map points use public-domain [Natural Earth](https://www.naturalearthdata.com/about/terms-of-use/) labels.

You can also convert supported local CSV data to NWGEO with the same executable:

```sh
./netwatch geoip convert -input path/to/IPV4.csv -output data/ipv4.nwgeo -family 4
```

Configure `geoip_ipv4_db` and/or `geoip_ipv6_db` in your local configuration to enable geographic lookups. Database files, logs and machine-specific config are intentionally excluded from this repository.

## License

GNU General Public License v3.0; see [LICENSE](LICENSE).

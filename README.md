# KLI-Netwatch
KLI-Netwatch is a free-software TUI program for cool display of your sites (or sth else) visitors from logs (nginx usually) on a spinning ASCII globe. Heavily vibe-coded, but this program is too useless in its functionality to harm your PC (prolly).

## Status

**pre-v1** is an experimental source snapshot, not a stable release. Linux terminal UI; Nginx access logs are the primary input. Use at your own risk.

## Building from source

Requires Go 1.26.10 or a compatible newer toolchain.

```sh
go test ./...
go vet ./...
go build -o netwatch ./cmd/netwatch
```

## First launch

Run `./netwatch` in a Linux terminal. If a configuration is missing, setup asks for your access log and optional GeoIP data. Press Enter to accept a suggested log path or type your own. NETWATCH chooses where to save the config automatically. NETWATCH looks for readable access logs in the usual Nginx and Apache locations and samples the log for a supported format. If it cannot identify a single format, setup asks you to choose and suggests `combined`. The process needs read access to your access log.

The user configuration defaults to `$XDG_CONFIG_HOME/netwatch/netwatch.json` (normally `~/.config/netwatch/netwatch.json`). NETWATCH also checks the legacy `configs/netwatch.json` in the current directory and system configuration directories. To run setup separately or use a particular file:

```sh
./netwatch setup
./netwatch setup -config /path/to/netwatch.json
./netwatch -config /path/to/netwatch.json
```

Replay and interval modes:

```sh
./netwatch -mode replay -last 15m
./netwatch -interval 1h
./netwatch -mode replay -from 2026-11-22T10:00:00+03:00 -to 2026-11-22T11:00:00+03:00
```

These flags use historical access logs. The `geoip convert` subcommand works independently of monitor configuration.

## GeoIP

GeoIP is optional. Setup checks for `ipv4.nwgeo`, `ipv6.nwgeo`, `ipv4.csv` and `ipv6.csv` beside the config, in `$XDG_DATA_HOME/netwatch` (normally `~/.local/share/netwatch`), and in the local `data` directory. Choose `path` to supply your own compatible files.

Setup can download DB-IP Lite and convert both address families to NWGEO automatically. **Countries** is the smallest download. **Regions** downloads the same archive as **Cities** and combines adjacent ranges with the same region to reduce the installed size. Region points are approximate. When no local database is found, Enter selects download and opens the detail menu. `n` skips the download for now. `never` skips future prompts. To re-enable prompting, set `"geoip_prompt": true` in the config while both database paths are empty.

IP Geolocation by [DB-IP](https://db-ip.com), licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). Setup saves the source and attribution alongside the converted files. Country map points use public-domain [Natural Earth](https://www.naturalearthdata.com/about/terms-of-use/) labels.

You can also convert supported local CSV data to NWGEO with the same executable:

```sh
./netwatch geoip convert -input path/to/IPV4.csv -output data/ipv4.nwgeo -family 4
```

Configure `geoip_ipv4_db` and/or `geoip_ipv6_db` in your local configuration to enable geographic lookups. Database files, logs and machine-specific config are intentionally excluded from this repository.

## License

GNU General Public License v3.0; see [LICENSE](LICENSE).

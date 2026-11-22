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

## Configuration

```sh
mkdir -p configs
cp configs/netwatch.example.json configs/netwatch.json
```

Edit `configs/netwatch.json` to set the access log path and log format (`combined`, `common`, `main`, or `json`). Your actual configuration is ignored by Git; do not commit private log paths or secrets. Start the TUI in a compatible terminal:

```sh
./netwatch -config configs/netwatch.json
```

Replay and interval modes:

```sh
./netwatch -mode replay -last 15m
./netwatch -interval 1h
./netwatch -mode replay -from 2026-11-22T10:00:00+03:00 -to 2026-11-22T11:00:00+03:00
```

The log file must be readable by your user. These flags use historical access logs; GeoIP is optional. The `geoip convert` subcommand does not require a monitor configuration file.

## GeoIP

The IP2Location LITE GeoIP database is **not included** and must be obtained separately according to its provider's terms. The `netwatch` binary also includes GeoIP conversion; no separate converter executable is required. Convert supported CSV data to NETWATCH's NWGEO binary format:

```sh
./netwatch geoip convert -input path/to/IPV4.csv -output data/ipv4.nwgeo -family 4
```

Configure `geoip_ipv4_db` and/or `geoip_ipv6_db` in your local configuration to enable geographic lookups. Database files, logs and machine-specific config are intentionally excluded from this repository.

## License

GNU General Public License v3.0; see [LICENSE](LICENSE).

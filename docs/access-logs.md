# Access log formats

Set `access_log` to a readable file and choose `access_format` in `netwatch.json`. First-run setup detects the built-in formats from a sample. Live, Replay and Interval use the configured parser, including when Replay or Interval opens a different file.

| Server | NETWATCH setting |
| --- | --- |
| [Nginx](https://nginx.org/en/docs/http/ngx_http_log_module.html) | `combined`, `common`, legacy `main` (combined with an optional forwarded-for column), `nginx` for a custom template, or `json` for named fields |
| [Caddy 2](https://caddyserver.com/docs/caddyfile/directives/log) | `json` for the default file encoder, or `caddy-console` for the tab-separated console encoder. Wall-clock timestamps without an offset are interpreted as UTC. |
| [Traefik](https://doc.traefik.io/traefik/observe/logs-and-access-logs/) | `traefik` for extended `common` or `genericCLF`. Setup can identify `genericCLF` as `combined`. Use `json` for Traefik's JSON and its downstream status and byte count. |
| [Apache HTTP Server](https://httpd.apache.org/docs/2.4/logs.html) | `common`, `combined` or `apache-vhost` for combined with the virtual host prefix, including Debian's `vhost_combined` |
| [Envoy](https://www.envoyproxy.io/docs/envoy/latest/configuration/observability/access_log/usage) | `envoy` for the default text format, or `json` for `json_format` and `typed_json_format` |
| Other servers | `json` with conventional field names or explicit `access_fields` mappings |

Use one record per line in a regular file. NETWATCH follows the current file across rotation. The readers use the configured `max_line_size` (64 KiB by default). Compressed files, direct syslog and gRPC inputs, and server runtime/error logs are outside the monitor's access-event pipeline. The Apache formats require a numeric client IP. Use `%a` when hostname lookup is enabled.

## Custom Nginx format

Paste a complete `log_format` directive or its concatenated template into `nginx_log_format`. A directive can include multiple fragments and an `escape=default`, `escape=json` or `escape=none` option. For a bare template, set `nginx_escape` if the server uses a mode other than `default`. JSON can also be parsed through its field names or mappings.

```json
{
  "access_log": "/var/log/nginx/site-access.log",
  "access_format": "nginx",
  "nginx_log_format": "log_format site 'at=$time_iso8601 ip=${remote_addr} $status ' '\"$request\" $body_bytes_sent \"$http_user_agent\"';"
}
```

The parser uses `$time_iso8601`, `$msec` or `$time_local` for time, and `$remote_addr`, `$realip_remote_addr` or `$proxy_protocol_addr` for the client address. It also reads `$request` or individual request variables, response status and size, referer, agent and forwarded-for. Other variables can appear anywhere in the template. To use a variable defined in a `map`, set the corresponding field to its variable name, for example `"access_fields": {"client_ip": "logged_client"}`.

Time and IP must be present. Use distinct separators or quotes when values can contain spaces or punctuation. The parser recognizes `$uri$is_args$args` as one URL. Other adjacent variables need a separator. Embedded newlines and templates that emit indistinguishable records cannot be decoded reliably. NETWATCH does not inspect Nginx configuration files.

## JSON from other programs

Use one JSON object per line. NETWATCH recognizes conventional keys such as `timestamp`, `client_ip`, `method`, `path`, `status` and `response_size`. JSON needs a timestamp, numeric IP (a port is allowed) and HTTP status. Use `0` when no response was sent. Timestamps can use RFC 3339, the common-log layout or a Unix epoch in seconds, milliseconds, microseconds or nanoseconds. Numeric epoch units are inferred from the number of digits at contemporary dates.

For different names, map fields with [JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901), including nested objects and array indices.

```json
{
  "access_log": "/var/log/example/events.jsonl",
  "access_format": "json",
  "access_fields": {
    "time": "/event/time",
    "client_ip": "/request/connection/ip",
    "method": "/request/verb",
    "url": "/request/target",
    "status": "/response/code",
    "response_size": "/response/bytes"
  }
}
```

The available mapping keys are `time`, `client_ip`, `request` (a request line), `method`, `url`, `protocol`, `status`, `response_size`, `referer`, `user_agent` and `x_forwarded_for`. An explicit mapping takes precedence for that field.

Envoy's default text contains only the forwarded-for header. NETWATCH uses its first address when present. Without one, the request still counts and has no client location. In structured output, use `%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%` for the client address selected by Envoy or `%DOWNSTREAM_DIRECT_REMOTE_ADDRESS_WITHOUT_PORT%` for the direct socket peer. Output it as `client_ip` or map the chosen key in `access_fields`.

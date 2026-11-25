# NETWATCH v1.0 verification

Compared PR #9 before the final pass (`2f24888`) with the implementation in `a1b4ab1`. Both binaries ran on the same Debian 13 Linux/amd64 environment with an AMD EPYC 9V74 and Go 1.26.10. The load driver used a 120×30 PTY, `GOMAXPROCS=2`, the same synthetic combined-format logs and the same 23.25 MiB NWGEO v1 database with 262,144 IPv4 ranges.

## CLI load

The 1,000-IP scenarios process 20,000 requests. The 600,000-IP scenarios process 600,000 requests. Sustained Live processes 115,000 requests at roughly 1,000 per second over two minutes. Each run checks the displayed request count before exiting.

GNU `time -v` measures peak process RSS and total user plus system CPU seconds. The heap column is the largest live Go heap after a recorded GC, rounded down by `gctrace`. A displayed zero denotes less than 1 MiB. The sampled peak column is the largest size immediately before or during a GC. Linux filesystem cache is outside the process RSS figures. CLI results are one run per scenario.

| Scenario | Peak RSS MiB | CPU s | Live heap MiB | Sampled peak heap MiB | GC cycles |
|---|---:|---:|---:|---:|---:|
| Live without GeoIP, 1,000 IPs | 12.8 → 12.4 | 0.10 → 0.11 | 0 → 0 | 3 → 3 | 4 → 4 |
| Live with GeoIP, 1,000 IPs | 13.0 → 13.4 | 0.16 → 0.19 | 1 → 1 | 3 → 3 | 5 → 5 |
| Replay with GeoIP, 1,000 IPs | 13.5 → 13.1 | 0.28 → 0.22 | 1 → 1 | 4 → 4 | 8 → 8 |
| Interval with GeoIP, 1,000 IPs | 12.9 → 12.4 | 0.08 → 0.08 | 1 → 0 | 3 → 3 | 4 → 3 |
| Live without GeoIP, 600,000 IPs | 38.2 → 30.1 | 3.60 → 3.19 | 14 → 10 | 27 → 20 | 43 → 53 |
| Live with GeoIP, 600,000 IPs | 81.3 → 47.5 | 6.87 → 5.37 | 35 → 19 | 70 → 37 | 25 → 35 |
| Replay with GeoIP, 600,000 IPs | 80.9 → 46.6 | 7.58 → 7.33 | 35 → 18 | 69 → 36 | 27 → 35 |
| Interval with GeoIP, 600,000 IPs | 72.9 → 46.6 | 3.89 → 4.18 | 31 → 19 | 61 → 37 | 22 → 26 |
| Live with GeoIP over 120.21 s, 1,150 IPs | 13.8 → 13.6 | 2.02 → 1.98 | 1 → 1 | 4 → 4 | 87 → 73 |

## Go benchmarks

Medians from three `-benchmem` runs per revision. The profiled frame includes a server marker and a long Unicode URL.

| Benchmark | ns/op | B/op | Allocations/op |
|---|---:|---:|---:|
| OverviewEmpty | 248.7 → 193.7 | 1,152 → 1,152 | 1 → 1 |
| FeedStream | 37.31 → 47.1 | 10 → 16 | 2 → 1 |
| GlobeFrame | 485317 → 381647 | 149,010 → 96,813 | 797 → 343 |
| DiskBlockLookup | 1296 → 1418 | 21 → 24 | 4 → 1 |

In the separately profiled frame benchmark, the time per frame was 529,791 → 396,552 ns/op. CPU profiles kept globe projection as the largest cumulative cost. In the sampled allocation profiles, the share attributed to `fitANSI` moved from 23.85% to 12.34%, and the share attributed to `applyGlobeOverlay` moved from 10.08% to 0.59%. Those percentages describe each profile's allocation mix. The table's B/op gives the per-frame comparison.

Feed retention and disk lookup perform fewer allocations, with a higher measured time and bytes per microbenchmark operation. The CLI load results include the effects of deduplicated client state and the bounded FIFO GeoIP cache.

The visual regression compares the full ANSI output of 224 frames with the baseline across seven themes, four terminal sizes and eight globe angles. The suite also covers arcs, theme colors, supported log formats, Traffic Filter, NWGEO and legacy config round-tripping. A PTY check exercised ordered and rejected unordered input in Replay and Interval, then verified the counters after a backward seek in Replay.

## Reproduce

The load driver uses Python 3's standard library and GNU time as test tools. A Fedora machine can install them with `sudo dnf install python3 time`. Run this from the repository's final PR checkout with Go 1.26.10 on `PATH`.

```bash
set -euo pipefail
repo=$(pwd)
bench_dir=$(mktemp -d)
git worktree add --detach "$bench_dir/before-src" 2f24888
cp internal/dashboard/memory_bench_test.go "$bench_dir/before-src/internal/dashboard/"
export GOMAXPROCS=2

for revision in before after; do
  src="$repo"
  if [[ "$revision" == before ]]; then
    src="$bench_dir/before-src"
  fi
  out="$bench_dir/$revision"
  mkdir -p "$out"
  (
    cd "$src"
    go build -buildvcs=false -o "$out/netwatch" ./cmd/netwatch
    go test -buildvcs=false -run='^$' -bench=. -benchmem -count=3 ./... > "$out/bench.txt"
    go test -buildvcs=false ./internal/dashboard -o "$out/dashboard.test" \
      -run='^$' -bench='^BenchmarkGlobeFrame$' -benchtime=3s \
      -cpuprofile="$out/cpu.pprof" -memprofile="$out/mem.pprof" > "$out/profile.txt"
    go tool pprof -top "$out/dashboard.test" "$out/cpu.pprof" > "$out/cpu.txt"
    go tool pprof -top -alloc_space "$out/dashboard.test" "$out/mem.pprof" > "$out/mem.txt"
  )
  python3 "$repo/scripts/profile-netwatch.py" \
    "$out/netwatch" "$bench_dir/fixtures" "$out" > "$out/cli.jsonl"
done
printf '%s\n' "$bench_dir"
```

The driver checks synthetic traffic and the local TLS test checks certificate validation. The environment's HTTPS tunnel returned HTTP 403 for the production DB-IP archive URL. A production DB-IP download and extended operation against real Fedora logs remain unverified.

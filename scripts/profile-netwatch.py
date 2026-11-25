#!/usr/bin/env python3
"""Run NETWATCH in a Linux PTY against repeatable synthetic logs."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import termios
import time
from itertools import islice

STAMP = "01/Jan/2026:00:00:01 +0000"
ANSI = re.compile(rb"\x1b\[[0-?]*[ -/]*[@-~]")
GC = re.compile(r"^gc (\d+) @.*?: .*? (\d+)->(\d+)->(\d+) MB", re.M)

def fixtures(binary, root):
    for unique, count in [(1000, 20000), (600000, 600000), (1150, 115000)]:
        path = root / f"{unique}.log"
        if path.exists():
            continue
        with path.open("w") as log:
            for index in range(count):
                address = index % unique
                ip = f"11.{address >> 16}.{address >> 8 & 255}.{address & 255}"
                log.write(f'{ip} - - [{STAMP}] "GET /index.html HTTP/1.1" '
                          '200 512 "-" "ExampleBrowser/1.0"\n')
    nwgeo = root / "synthetic.nwgeo"
    if not nwgeo.exists():
        csv = root / "synthetic.csv"
        with csv.open("w") as log:
            for index in range(262144):
                start = (11 << 24) + 256 * index
                log.write(f"{start},{start + 255},US,United States,CA,City,37,-122\n")
        subprocess.run([binary, "geoip", "convert", "-input", str(csv),
                        "-output", str(nwgeo), "-family", "4"], check=True, stdout=subprocess.DEVNULL)
    return nwgeo

def measure(binary, timer, root, out, nwgeo, mode, geo, unique, count, long=False):
    name = f"{mode}-{'geo' if geo else 'plain'}-{unique}"
    source = root / f"{unique}.log"
    log = out / "live.log" if mode == "live" else source
    if mode == "live":
        log.write_bytes(b"")
    config = out / "netwatch.json"
    config.write_text(json.dumps({"access_log": str(log), "poll_interval": "100ms",
                                  "geoip_ipv4_db": str(nwgeo) if geo else ""}))
    timing = out / f"{name}.time"
    trace = out / f"{name}.gc"
    command = [timer, "-v", "-o", str(timing), binary, "-config", str(config), "-mode", mode]
    if mode != "live":
        command += ["-from", "2026-01-01T00:00:00Z", "-to", "2026-01-01T00:00:02Z"]
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
    env = dict(os.environ, GODEBUG="gctrace=1", GOMAXPROCS="2", LC_ALL="C")
    with trace.open("wb") as stderr, source.open("rb") as rows:
        process = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=stderr, env=env,
                                   start_new_session=True)
        os.close(slave)
        limit = time.monotonic() + (150 if long else 90)
        next_batch = None
        sent = 0
        data = b""
        summary = b""
        saw_ready = False
        wanted = (rb"REQ " if mode == "live" else rb"Requests ") + str(count).encode() + rb" +[Cc][Ll][Ii][Ee][Nn][Tt][Ss] \d+\+?"
        try:
            while process.poll() is None and time.monotonic() < limit:
                now = time.monotonic()
                if mode == "live" and next_batch is not None and now >= next_batch and sent < count:
                    size = min(count - sent, 1000 if long else count)
                    with log.open("ab") as output:
                        output.writelines(islice(rows, size))
                    sent += size
                    next_batch += 1
                if select.select([master], [], [], 0.05)[0]:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError:
                        break
                    data = (data + ANSI.sub(b"", chunk))[-65536:]
                if mode == "live" and not saw_ready and b"REQ 0   CLIENTS" in data:
                    saw_ready = True
                    next_batch = now + (1 if long else 0.15)
                if match := re.search(wanted, data):
                    summary = match[0]
                # Keep the sustained workload running for the full two minutes.
                if summary and (not long or now >= next_batch + 4):
                    os.write(master, b"q")
                    break
            if not summary or process.wait(timeout=5) != 0:
                raise RuntimeError(f"{name}: expected count {count}, got {summary!r}")
        finally:
            if process.poll() is None:
                os.killpg(process.pid, 9)
                process.wait()
            os.close(master)
    values = dict(re.findall(r"^\s*([^\n:]+): ([\d.]+)$", timing.read_text(), re.M))
    gc = [tuple(map(int, row)) for row in GC.findall(trace.read_text())]
    print(json.dumps({"scenario": name, "requests": count, "summary": summary.decode(),
                      "cpu_user_s": float(values["User time (seconds)"]),
                      "cpu_sys_s": float(values["System time (seconds)"]),
                      "rss_kib": int(values["Maximum resident set size (kbytes)"]),
                      "gc_cycles": gc[-1][0] if gc else 0,
                      "heap_peak_mib": max((max(row[1:3]) for row in gc), default=None),
                      "heap_live_mib": max((row[3] for row in gc), default=None)}), flush=True)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("fixtures", type=Path)
    parser.add_argument("results", type=Path)
    parser.add_argument("--time", default="/usr/bin/time", help="path to GNU time")
    parser.add_argument("--only", choices=["short", "long"])
    args = parser.parse_args()
    binary, root, out = (path.resolve() for path in (args.binary, args.fixtures, args.results))
    root.mkdir(parents=True, exist_ok=True)
    out.mkdir(parents=True, exist_ok=True)
    nwgeo = fixtures(binary, root)
    if args.only != "long":
        for unique, count in [(1000, 20000), (600000, 600000)]:
            for mode, geo in [("live", False), ("live", True), ("replay", True), ("interval", True)]:
                measure(binary, args.time, root, out, nwgeo, mode, geo, unique, count)
    if args.only != "short":
        measure(binary, args.time, root, out, nwgeo, "live", True, 1150, 115000, long=True)

if __name__ == "__main__":
    main()

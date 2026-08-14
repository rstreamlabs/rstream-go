#!/usr/bin/env python3

import argparse
import json
import os
import selectors
import subprocess
import sys
import tempfile
import time


class MCPReader:
    def __init__(self, stream):
        self._stream = stream
        self._buffer = bytearray()
        self._selector = selectors.DefaultSelector()
        self._selector.register(stream, selectors.EVENT_READ)

    def close(self):
        self._selector.close()

    def read(self, timeout):
        deadline = time.monotonic() + timeout
        while True:
            message = self._extract()
            if message is not None:
                return json.loads(message)
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("timed out waiting for an MCP response")
            if not self._selector.select(remaining):
                raise TimeoutError("timed out waiting for MCP output")
            chunk = os.read(self._stream.fileno(), 65536)
            if not chunk:
                raise EOFError("MCP stdout closed before a complete response")
            self._buffer.extend(chunk)

    def _extract(self):
        if not self._buffer:
            return None
        if self._buffer.startswith(b"{"):
            delimiter = self._buffer.find(b"\n")
            if delimiter < 0:
                return None
            payload = bytes(self._buffer[:delimiter])
            del self._buffer[: delimiter + 1]
            return payload
        header_end = self._buffer.find(b"\r\n\r\n")
        if header_end < 0:
            return None
        headers = bytes(self._buffer[:header_end]).decode("ascii")
        lengths = [
            int(line.split(":", 1)[1].strip())
            for line in headers.split("\r\n")
            if line.lower().startswith("content-length:")
        ]
        if len(lengths) != 1 or lengths[0] < 0:
            raise RuntimeError(f"invalid MCP response headers: {headers!r}")
        payload_start = header_end + 4
        payload_end = payload_start + lengths[0]
        if len(self._buffer) < payload_end:
            return None
        payload = bytes(self._buffer[payload_start:payload_end])
        del self._buffer[:payload_end]
        return payload


def write_message(stream, message, framing):
    payload = json.dumps(message, separators=(",", ":")).encode()
    if framing == "line":
        stream.write(payload + b"\n")
    else:
        stream.write(f"Content-Length: {len(payload)}\r\n\r\n".encode() + payload)
    stream.flush()


def assert_success(response, request_id):
    if response.get("id") != request_id:
        raise RuntimeError(f"unexpected response id: {response!r}")
    if "error" in response:
        raise RuntimeError(f"MCP request {request_id} failed: {response['error']!r}")
    result = response.get("result")
    if isinstance(result, dict) and result.get("isError"):
        raise RuntimeError(f"MCP tool request {request_id} failed: {result!r}")
    return result


def assert_exec_result(result, marker):
    if not isinstance(result, dict):
        raise RuntimeError(f"MCP WebTTY exec returned an invalid result: {result!r}")
    structured = result.get("structuredContent")
    if not isinstance(structured, dict):
        raise RuntimeError(f"MCP WebTTY exec omitted structured content: {result!r}")
    if structured.get("exit_code") != 0 or structured.get("stdout") != marker:
        raise RuntimeError(f"MCP WebTTY exec returned an invalid command result: {structured!r}")


def run(binary, context, framing, rounds, url):
    environment = os.environ.copy()
    environment["RSTREAM_CONTEXT"] = context
    with tempfile.TemporaryFile() as stderr:
        process = subprocess.Popen(
            [binary, "mcp", "serve"],
            env=environment,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=stderr,
        )
        reader = MCPReader(process.stdout)
        try:
            initialize = {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {
                    "protocolVersion": "2025-11-25",
                    "capabilities": {},
                    "clientInfo": {"name": "rstream-e2e", "version": "1"},
                },
            }
            write_message(process.stdin, initialize, framing)
            assert_success(reader.read(15), 1)
            write_message(
                process.stdin,
                {"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}},
                framing,
            )
            next_id = 2
            for _ in range(rounds):
                expected = set()
                for _ in range(8):
                    request_id = next_id
                    next_id += 1
                    expected.add(request_id)
                    write_message(
                        process.stdin,
                        {
                            "jsonrpc": "2.0",
                            "id": request_id,
                            "method": "tools/call",
                            "params": {"name": "rstream_webtty_list", "arguments": {}},
                        },
                        framing,
                    )
                while expected:
                    response = reader.read(30)
                    request_id = response.get("id")
                    if request_id not in expected:
                        raise RuntimeError(f"duplicate or unknown MCP response: {response!r}")
                    assert_success(response, request_id)
                    expected.remove(request_id)
                if url:
                    expected_exec = {}
                    for index in range(8):
                        request_id = next_id
                        next_id += 1
                        marker = f"rstream-mcp-{framing}-{request_id}-{index}"
                        expected_exec[request_id] = marker
                        write_message(
                            process.stdin,
                            {
                                "jsonrpc": "2.0",
                                "id": request_id,
                                "method": "tools/call",
                                "params": {
                                    "name": "rstream_webtty_exec",
                                    "arguments": {
                                        "url": url,
                                        "command": [
                                            "/bin/sh",
                                            "-c",
                                            'printf "%s" "$1"',
                                            "rstream-mcp",
                                            marker,
                                        ],
                                    },
                                },
                            },
                            framing,
                        )
                    while expected_exec:
                        response = reader.read(60)
                        request_id = response.get("id")
                        if request_id not in expected_exec:
                            raise RuntimeError(f"duplicate or unknown MCP exec response: {response!r}")
                        result = assert_success(response, request_id)
                        assert_exec_result(result, expected_exec.pop(request_id))
            process.stdin.close()
            return_code = process.wait(timeout=15)
            if return_code != 0:
                raise RuntimeError(f"MCP server returned {return_code} after EOF")
        except Exception:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
            stderr.seek(0)
            diagnostics = stderr.read().decode(errors="replace")
            if diagnostics:
                print(diagnostics, file=sys.stderr)
            raise
        finally:
            reader.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--rounds", type=int, default=10)
    parser.add_argument("--url", help="optional WebTTY URL for concurrent command execution")
    args = parser.parse_args()
    if args.rounds < 1:
        parser.error("--rounds must be positive")
    for framing in ("line", "content-length"):
        run(args.binary, args.context, framing, args.rounds, args.url)
    inventory_count = args.rounds * 8 * 2
    exec_count = inventory_count if args.url else 0
    print(
        f"PASS: {inventory_count} concurrent inventory calls and {exec_count} "
        "concurrent WebTTY commands passed across both MCP framings"
    )


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)

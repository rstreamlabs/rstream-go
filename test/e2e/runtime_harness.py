#!/usr/bin/env python3
# See LICENSE file in the project root for license information.

import argparse
import http.client
import http.server
import os
import socket
import socketserver
import ssl
import sys
import time
from urllib.parse import urlparse


PAYLOAD = b"ping-stream\n"


class ThreadingTCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


class EchoTCPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        while True:
            data = self.request.recv(65536)
            if not data:
                return
            self.request.sendall(data)


class ThreadingTLSServer(ThreadingTCPServer):
    def __init__(self, server_address, handler, certfile, keyfile):
        super().__init__(server_address, handler)
        self.context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        self.context.load_cert_chain(certfile=certfile, keyfile=keyfile)

    def get_request(self):
        sock, addr = super().get_request()
        return self.context.wrap_socket(sock, server_side=True), addr


class HTTPHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/ping":
            body = b"pong\n"
            self.send_response(200)
            self.send_header("content-type", "text/plain")
            self.send_header("content-length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path == "/directory":
            self.send_response(301)
            self.send_header("location", "/directory/")
            self.send_header("content-length", "0")
            self.end_headers()
            return
        if self.path == "/directory/":
            body = b"directory\n"
            self.send_response(200)
            self.send_header("content-type", "text/plain")
            self.send_header("content-length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, *_args):
        return


class ThreadingHTTPServer(http.server.ThreadingHTTPServer):
    allow_reuse_address = True
    daemon_threads = True


def serve_tcp(args):
    server = ThreadingTCPServer((args.host, args.port), EchoTCPHandler)
    print(f"READY {server.server_address[0]}:{server.server_address[1]}", flush=True)
    server.serve_forever()


def serve_tls(args):
    server = ThreadingTLSServer((args.host, args.port), EchoTCPHandler, args.cert, args.key)
    print(f"READY {server.server_address[0]}:{server.server_address[1]}", flush=True)
    server.serve_forever()


def serve_http(args):
    server = ThreadingHTTPServer((args.host, args.port), HTTPHandler)
    print(f"READY {server.server_address[0]}:{server.server_address[1]}", flush=True)
    server.serve_forever()


def serve_udp(args):
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind((args.host, args.port))
    host, port = sock.getsockname()
    print(f"READY {host}:{port}", flush=True)
    while True:
        data, peer = sock.recvfrom(65536)
        sock.sendto(data, peer)


def parse_host_port(raw, default_port):
    value = raw.strip()
    if "://" in value:
        parsed = urlparse(value)
        host = parsed.hostname
        port = parsed.port or default_port
        if not host:
            raise ValueError(f"missing host in {raw!r}")
        return host, int(port)
    value = value.split()[0]
    if value.startswith("["):
        end = value.find("]")
        if end == -1:
            raise ValueError(f"invalid IPv6 address {raw!r}")
        host = value[1:end]
        port = default_port
        if len(value) > end + 1 and value[end + 1] == ":":
            port = value[end + 2 :]
        return host, int(port)
    if value.count(":") == 1:
        host, port = value.rsplit(":", 1)
        return host, int(port)
    return value, int(default_port)


def client_tls_context(args):
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    if bool(args.cert) != bool(args.key):
        raise ValueError("--cert and --key must be provided together")
    if args.cert:
        context.load_cert_chain(certfile=args.cert, keyfile=args.key)
    return context


def check_tls_echo(args):
    host, port = parse_host_port(args.addr, args.default_port)
    context = client_tls_context(args)
    if args.alpn:
        context.set_alpn_protocols([args.alpn])
    with socket.create_connection((host, port), timeout=args.timeout) as raw:
        with context.wrap_socket(raw, server_hostname=host) as conn:
            if args.alpn and conn.selected_alpn_protocol() != args.alpn:
                raise RuntimeError(
                    f"unexpected ALPN {conn.selected_alpn_protocol()!r}, want {args.alpn!r}"
                )
            conn.sendall(PAYLOAD)
            got = conn.recv(len(PAYLOAD))
            if got != PAYLOAD:
                raise RuntimeError(f"echo mismatch: got {got!r}, want {PAYLOAD!r}")


def check_https_ping(args):
    host, port = parse_host_port(args.addr, args.default_port)
    context = client_tls_context(args)
    conn = http.client.HTTPSConnection(host, port, timeout=args.timeout, context=context)
    try:
        conn.request("GET", "/ping")
        resp = conn.getresponse()
        body = resp.read()
        if resp.status != 200:
            raise RuntimeError(f"unexpected status {resp.status}: {body!r}")
        if body != b"pong\n":
            raise RuntimeError(f"body mismatch: got {body!r}, want b'pong\\n'")
    finally:
        conn.close()


def h2_frame(frame_type, flags, stream_id, payload=b""):
    length = len(payload)
    return (
        length.to_bytes(3, "big")
        + bytes([frame_type, flags])
        + (stream_id & 0x7FFFFFFF).to_bytes(4, "big")
        + payload
    )


def h2_read_exact(conn, size):
    out = bytearray()
    while len(out) < size:
        chunk = conn.recv(size - len(out))
        if not chunk:
            raise RuntimeError("HTTP/2 connection closed")
        out.extend(chunk)
    return bytes(out)


def h2_read_frame(conn):
    header = h2_read_exact(conn, 9)
    length = int.from_bytes(header[:3], "big")
    frame_type = header[3]
    flags = header[4]
    stream_id = int.from_bytes(header[5:9], "big") & 0x7FFFFFFF
    return frame_type, flags, stream_id, h2_read_exact(conn, length)


def h2_drain_server_settings(conn):
    deadline = time.time() + 10
    while time.time() < deadline:
        frame_type, _flags, _stream_id, _payload = h2_read_frame(conn)
        if frame_type == 4:
            conn.sendall(h2_frame(4, 1, 0))
            return
    raise RuntimeError("server HTTP/2 settings were not received")


def h2_encode_literal(index, value):
    raw = value.encode()
    if len(raw) > 127:
        raise ValueError("literal value is too long for the runtime probe")
    return bytes([index, len(raw)]) + raw


def h2_request_headers(authority, path):
    return b"".join(
        [
            b"\x82",
            b"\x87",
            h2_encode_literal(4, path),
            h2_encode_literal(1, authority),
        ],
    )


def h2_send_get(conn, stream_id, authority, path):
    conn.sendall(h2_frame(1, 5, stream_id, h2_request_headers(authority, path)))


def h2_read_response_body(conn, stream_id):
    body = bytearray()
    while True:
        frame_type, flags, got_stream_id, payload = h2_read_frame(conn)
        if got_stream_id != stream_id:
            continue
        if frame_type == 0:
            body.extend(payload)
        if frame_type == 3:
            raise RuntimeError(f"stream {stream_id} was reset")
        if flags & 1:
            return bytes(body)


def parsed_https_target(raw):
    parsed = urlparse(raw)
    if parsed.scheme != "https" or not parsed.hostname:
        raise ValueError(f"expected https URL, got {raw!r}")
    port = parsed.port or 443
    authority = parsed.netloc
    path = parsed.path or "/"
    if parsed.query:
        path += "?" + parsed.query
    return parsed.hostname, port, authority, path


def wait_for_file(path, timeout):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if os.path.exists(path):
            return
        time.sleep(0.1)
    raise RuntimeError(f"timed out waiting for {path}")


def check_h2_reused_connection_routes(args):
    first_host, first_port, first_authority, first_path = parsed_https_target(args.first)
    _second_host, _second_port, second_authority, second_path = parsed_https_target(args.second)
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    context.set_alpn_protocols(["h2"])
    with socket.create_connection((first_host, first_port), timeout=args.timeout) as raw:
        with context.wrap_socket(raw, server_hostname=first_host) as conn:
            conn.settimeout(args.timeout)
            if conn.selected_alpn_protocol() != "h2":
                raise RuntimeError(f"unexpected ALPN {conn.selected_alpn_protocol()!r}")
            conn.sendall(b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" + h2_frame(4, 0, 0))
            h2_drain_server_settings(conn)
            h2_send_get(conn, 1, first_authority, first_path)
            first_body = h2_read_response_body(conn, 1)
            if first_body != b"pong\n":
                raise RuntimeError(f"first response mismatch: {first_body!r}")
            print("READY h2", flush=True)
            wait_for_file(args.trigger, args.timeout)
            h2_send_get(conn, 3, second_authority, second_path)
            second_body = h2_read_response_body(conn, 3)
            if second_body != b"pong\n":
                raise RuntimeError(f"second response mismatch: {second_body!r}")


def check_h2_reused_connection_requires_mtls_handshake(args):
    first_host, first_port, first_authority, first_path = parsed_https_target(args.first)
    _second_host, _second_port, second_authority, second_path = parsed_https_target(args.second)
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    context.set_alpn_protocols(["h2"])
    with socket.create_connection((first_host, first_port), timeout=args.timeout) as raw:
        with context.wrap_socket(raw, server_hostname=first_host) as conn:
            conn.settimeout(args.timeout)
            if conn.selected_alpn_protocol() != "h2":
                raise RuntimeError(f"unexpected ALPN {conn.selected_alpn_protocol()!r}")
            conn.sendall(b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" + h2_frame(4, 0, 0))
            h2_drain_server_settings(conn)
            h2_send_get(conn, 1, first_authority, first_path)
            first_body = h2_read_response_body(conn, 1)
            if first_body != b"pong\n":
                raise RuntimeError(f"first response mismatch: {first_body!r}")
            h2_send_get(conn, 3, second_authority, second_path)
            second_body = h2_read_response_body(conn, 3)
            if b"Misdirected Request" not in second_body:
                raise RuntimeError(f"expected mTLS handshake rejection, got {second_body!r}")


def check_h2_subpath_response(args):
    host, port, authority, path = parsed_https_target(args.url)
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    context.set_alpn_protocols(["h2"])
    with socket.create_connection((host, port), timeout=args.timeout) as raw:
        with context.wrap_socket(raw, server_hostname=host) as conn:
            conn.settimeout(args.timeout)
            if conn.selected_alpn_protocol() != "h2":
                raise RuntimeError(f"unexpected ALPN {conn.selected_alpn_protocol()!r}")
            conn.sendall(b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" + h2_frame(4, 0, 0))
            h2_drain_server_settings(conn)
            h2_send_get(conn, 1, authority, path)
            body = h2_read_response_body(conn, 1)
            if body != b"directory\n":
                raise RuntimeError(f"subpath response mismatch: {body!r}")


def print_hostport(args):
    host, port = parse_host_port(args.addr, args.default_port)
    print(f"{host}:{port}")


def main():
    parser = argparse.ArgumentParser()
    subcommands = parser.add_subparsers(dest="command", required=True)

    serve = subcommands.add_parser("serve")
    serve_subcommands = serve.add_subparsers(dest="mode", required=True)
    for mode, handler in (("tcp", serve_tcp), ("http", serve_http), ("udp", serve_udp)):
        sub = serve_subcommands.add_parser(mode)
        sub.add_argument("--host", default="127.0.0.1")
        sub.add_argument("--port", type=int, default=0)
        sub.set_defaults(func=handler)
    tls = serve_subcommands.add_parser("tls")
    tls.add_argument("--host", default="127.0.0.1")
    tls.add_argument("--port", type=int, default=0)
    tls.add_argument("--cert", required=True)
    tls.add_argument("--key", required=True)
    tls.set_defaults(func=serve_tls)

    check = subcommands.add_parser("check")
    check_subcommands = check.add_subparsers(dest="mode", required=True)
    tls_echo = check_subcommands.add_parser("tls-echo")
    tls_echo.add_argument("--addr", required=True)
    tls_echo.add_argument("--alpn", default="")
    tls_echo.add_argument("--cert", default="")
    tls_echo.add_argument("--key", default="")
    tls_echo.add_argument("--default-port", default="443")
    tls_echo.add_argument("--timeout", type=float, default=15.0)
    tls_echo.set_defaults(func=check_tls_echo)
    https_ping = check_subcommands.add_parser("https-ping")
    https_ping.add_argument("--addr", required=True)
    https_ping.add_argument("--cert", default="")
    https_ping.add_argument("--key", default="")
    https_ping.add_argument("--default-port", default="443")
    https_ping.add_argument("--timeout", type=float, default=15.0)
    https_ping.set_defaults(func=check_https_ping)
    h2_reuse = check_subcommands.add_parser("h2-reuse-routes")
    h2_reuse.add_argument("--first", required=True)
    h2_reuse.add_argument("--second", required=True)
    h2_reuse.add_argument("--trigger", required=True)
    h2_reuse.add_argument("--timeout", type=float, default=15.0)
    h2_reuse.set_defaults(func=check_h2_reused_connection_routes)
    h2_mtls = check_subcommands.add_parser("h2-reuse-requires-mtls-handshake")
    h2_mtls.add_argument("--first", required=True)
    h2_mtls.add_argument("--second", required=True)
    h2_mtls.add_argument("--timeout", type=float, default=15.0)
    h2_mtls.set_defaults(func=check_h2_reused_connection_requires_mtls_handshake)
    h2_subpath = check_subcommands.add_parser("h2-subpath-response")
    h2_subpath.add_argument("--url", required=True)
    h2_subpath.add_argument("--timeout", type=float, default=15.0)
    h2_subpath.set_defaults(func=check_h2_subpath_response)

    hostport = subcommands.add_parser("hostport")
    hostport.add_argument("--addr", required=True)
    hostport.add_argument("--default-port", default="443")
    hostport.set_defaults(func=print_hostport)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"ERROR {exc}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env python3
# See LICENSE file in the project root for license information.

import argparse
import http.client
import http.server
import socket
import socketserver
import ssl
import sys
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
        if self.path != "/ping":
            self.send_response(404)
            self.end_headers()
            return
        body = b"pong\n"
        self.send_response(200)
        self.send_header("content-type", "text/plain")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

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


def check_tls_echo(args):
    host, port = parse_host_port(args.addr, args.default_port)
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
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
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
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
    tls_echo.add_argument("--default-port", default="443")
    tls_echo.add_argument("--timeout", type=float, default=15.0)
    tls_echo.set_defaults(func=check_tls_echo)
    https_ping = check_subcommands.add_parser("https-ping")
    https_ping.add_argument("--addr", required=True)
    https_ping.add_argument("--default-port", default="443")
    https_ping.add_argument("--timeout", type=float, default=15.0)
    https_ping.set_defaults(func=check_https_ping)

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

"""
HTTP server for simulation control (spawn, list robots).
Rendering is now done client-side via Three.js.

Endpoints:
  POST /spawn   - Spawn a new robot (returns JSON with robot_id)
  GET  /robots  - List active robots
  GET  /health  - Health check
"""

import json
import logging
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from typing import Callable

log = logging.getLogger(__name__)

_spawn_callback: Callable[[], dict] | None = None
_list_robots_callback: Callable[[], list[str]] | None = None


def set_spawn_callback(cb: Callable[[], dict]):
    global _spawn_callback
    _spawn_callback = cb


def set_list_robots_callback(cb: Callable[[], list[str]]):
    global _list_robots_callback
    _list_robots_callback = cb


class Handler(BaseHTTPRequestHandler):

    def do_GET(self):
        if self.path == "/robots":
            self._serve_robots()
        elif self.path == "/health":
            self._ok("ok")
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        if self.path == "/spawn":
            self._handle_spawn()
        else:
            self.send_response(404)
            self.end_headers()

    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.end_headers()

    def _handle_spawn(self):
        if not _spawn_callback:
            self._json(500, {"error": "spawn not configured"})
            return
        try:
            result = _spawn_callback()
            self._json(200, result)
        except Exception as e:
            self._json(500, {"error": str(e)})

    def _serve_robots(self):
        robots = _list_robots_callback() if _list_robots_callback else []
        self._json(200, {"robots": robots})

    def _ok(self, msg: str):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(msg.encode())

    def _json(self, code: int, data: dict):
        body = json.dumps(data).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass


def start_server(port: int = 8085):
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    import threading
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    log.info("Control server on :%d (/spawn, /robots, /health)", port)
    return server

import importlib.util
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def load_smoke_module():
    path = Path(__file__).with_name("live_stream_smoke.py")
    spec = importlib.util.spec_from_file_location("live_stream_smoke", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SmokeHandler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        return

    def do_GET(self):
        if self.path == "/allinone.m3u":
            body = (
                "#EXTM3U\n"
                "#EXTINF:-1 group-title=\"虎牙\",测试\n"
                "http://127.0.0.1:19090/stream/huya/12345\n"
            ).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.startswith("/stream/huya/12345"):
            self.send_response(200)
            self.send_header("Content-Type", "video/x-flv")
            self.end_headers()
            payload = b"FLV" + b"\x00" * (32 * 1024 - 3)
            try:
                for _ in range(100):
                    self.wfile.write(payload)
                    self.wfile.flush()
                    time.sleep(0.001)
            except (BrokenPipeError, ConnectionResetError):
                pass
            return
        self.send_error(404)


class LiveStreamSmokeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.smoke = load_smoke_module()
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), SmokeHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join(timeout=2)

    def test_extracts_room_from_playlist(self):
        rooms = self.smoke.fetch_room_ids(f"{self.base}/allinone.m3u", 1, 5)
        self.assertEqual(rooms, ["12345"])

    def test_measures_real_flv_bytes(self):
        result = self.smoke.probe_room(
            self.base,
            "12345",
            duration=0.02,
            min_bytes=1024,
            max_gap=1,
        )
        self.assertTrue(result["passed"], result)
        self.assertEqual(result["flv_magic"], "FLV")
        self.assertGreaterEqual(result["bytes"], 1024)
        self.assertGreater(result["average_mbps"], 0)


if __name__ == "__main__":
    unittest.main()

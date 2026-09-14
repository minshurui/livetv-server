import http.client
import http.server
import threading
import unittest
from types import SimpleNamespace
from unittest import mock

from test_python_helpers import load_module


class LegacyRefreshTests(unittest.TestCase):
    def test_temporary_redirect_and_offline_are_not_cached(self):
        # 这里只测 HTTP 响应语义，不联网请求真实解析器。
        with mock.patch.dict("sys.modules", {"requests": SimpleNamespace()}):
            module = load_module("legacy_refresh_aio", "docker/allinone.py")
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), module.Handler)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            for platform in ("huya", "douyu"):
                for url in ("https://cdn.example/old.flv", None, "https://cdn.example/new.flv"):
                    with mock.patch.object(module, "resolve_" + platform, return_value=url):
                        client = http.client.HTTPConnection(*server.server_address, timeout=2)
                        client.request("GET", "/" + platform + "/123")
                        response = client.getresponse()
                        self.assertEqual(response.status, 302 if url else 404)
                        self.assertEqual(response.getheader("Location"), url)
                        self.assertIn("no-store", response.getheader("Cache-Control"))
                        response.read()
                        client.close()
        finally:
            server.shutdown()
            server.server_close()
            worker.join(timeout=2)

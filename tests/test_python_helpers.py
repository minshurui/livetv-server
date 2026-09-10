import importlib.util
import pathlib
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock


ROOT = pathlib.Path(__file__).resolve().parents[1]


def load_module(name, relative_path):
    spec = importlib.util.spec_from_file_location(name, ROOT / relative_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


class PythonHelperTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.stream_proxy = load_module("stream_proxy", "docker/stream-proxy.py")
        cls.sync_channels = load_module("sync_channels", "docker/sync_channels.py")
        cls.filter_iptv = load_module("filter_iptv", "docker/filter_iptv.py")

    def test_hls_filename_allowlist(self):
        for name in ("index.m3u8", "seg_00001.ts", "seg_9.ts"):
            self.assertTrue(self.stream_proxy.valid_hls_filename(name))
        for name in ("ffmpeg.err", "../index.m3u8", "seg_.ts", "seg_one.ts"):
            self.assertFalse(self.stream_proxy.valid_hls_filename(name))

    def test_resolver_does_not_turn_http_error_into_stream_url(self):
        failed = SimpleNamespace(returncode=22, stdout="", stderr="404")
        with mock.patch.object(self.stream_proxy.subprocess, "run", return_value=failed):
            self.assertIsNone(self.stream_proxy.resolve_with_curl("/huya/offline"))

    def test_resolver_reads_redirect_location(self):
        redirected = SimpleNamespace(
            returncode=0,
            stdout="HTTP/1.1 301 Moved Permanently\r\nLocation: https://cdn.example/live.flv\r\n",
            stderr="",
        )
        with mock.patch.object(self.stream_proxy.subprocess, "run", return_value=redirected):
            self.assertEqual(
                self.stream_proxy.resolve_with_curl("/huya/1"),
                "https://cdn.example/live.flv",
            )

    def test_extinf_escapes_attribute_quotes(self):
        value = self.sync_channels.build_extinf('game"name', "display", True)
        self.assertIn('group-title="game&quot;name"', value)

    def test_filter_iptv_rejects_obvious_vod_and_blocklist(self):
        source = """#EXTM3U url-tvg="epg.xml"
#EXTINF:-1 group-title="直播",CCTV-1
http://live.example/cctv1/index.m3u8
#EXTINF:120,录播片段
https://media.example/clip.m3u8
#EXTINF:-1,电影文件
https://media.example/movie.mp4?token=example
#EXTINF:-1,自定义黑名单
https://blocked.example/live.m3u8
"""
        result, count, rejected = self.filter_iptv.filter_m3u(
            source, ["blocked.example"], reject_vod=True
        )
        self.assertEqual(count, 1)
        self.assertEqual(result.count("#EXTM3U"), 1)
        self.assertTrue(result.startswith('#EXTM3U url-tvg="epg.xml"'))
        self.assertIn("CCTV-1", result)
        self.assertNotIn("录播片段", result)
        self.assertEqual(rejected["positive_duration"], 1)
        self.assertEqual(rejected["vod_file"], 1)
        self.assertEqual(rejected["blocklist"], 1)

    def test_filter_iptv_keeps_per_entry_options(self):
        source = """#EXTM3U
#EXTINF:-1,UDP直播
#EXTVLCOPT:http-user-agent=example
udp://@239.0.0.1:1234
"""
        result, count, rejected = self.filter_iptv.filter_m3u(source, [])
        self.assertEqual(count, 1)
        self.assertFalse(rejected)
        self.assertIn("#EXTVLCOPT:http-user-agent=example", result)
        self.assertIn("udp://@239.0.0.1:1234", result)

    def test_filter_cli_preserves_last_good_when_result_too_small(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = pathlib.Path(tmp, "source.m3u")
            target = pathlib.Path(tmp, "published.m3u")
            source.write_text("#EXTM3U\n#EXTINF:-1,Only\nhttps://live.example/one.m3u8\n")
            target.write_text("last-good\n")
            argv = [
                "filter_iptv.py", "--input", str(source), "--output", str(target),
                "--min-channels", "2",
            ]
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(self.filter_iptv.main(), 3)
            self.assertEqual(target.read_text(), "last-good\n")


if __name__ == "__main__":
    unittest.main()

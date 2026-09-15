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
    def test_image_defaults_match_refresh_contract(self):
        dockerfile = (ROOT / "Dockerfile").read_text()
        for setting in ("HUYA_CACHE_TTL=30", "DOUYU_CACHE_TTL=30",
                        "STREAM_READ_IDLE_SECONDS=10", "SYNC_CHANNELS_MINUTES=15",
                        "IPTV_CHANNEL_SCOPE=all", "IPTV_VERIFY_STREAMS=1",
                        "IPTV_VERIFY_FALLBACK=1"):
            self.assertIn(setting + " \\", dockerfile)
        self.assertNotIn("CACHE_TTL=300", dockerfile)
        env_example = (ROOT / "docker/.env.example").read_text()
        self.assertIn("HUYA_CACHE_TTL=30", env_example)
        self.assertIn("DOUYU_CACHE_TTL=30", env_example)
        self.assertIn("IPTV_VERIFY_FALLBACK=1", env_example)

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
            stdout="HTTP/1.1 302 Found\r\nLocation: https://cdn.example/live.flv\r\n",
            stderr="",
        )
        with mock.patch.object(self.stream_proxy.subprocess, "run", return_value=redirected):
            self.assertEqual(
                self.stream_proxy.resolve_with_curl("/huya/1"),
                "https://cdn.example/live.flv",
            )

    def test_sync_lock_skips_overlapping_writer(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = str(pathlib.Path(tmp) / "channels.json")
            action = mock.Mock(return_value=0)
            with open(path + ".lock", "a") as lock:
                self.sync_channels.fcntl.flock(lock, self.sync_channels.fcntl.LOCK_EX)
                self.assertEqual(self.sync_channels.guarded_sync(path, action), 0)
                action.assert_not_called()
            self.assertEqual(self.sync_channels.guarded_sync(path, action), 0)
            action.assert_called_once()

    def test_sync_lock_released_after_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = str(pathlib.Path(tmp) / "channels.json")
            with self.assertRaises(RuntimeError):
                self.sync_channels.guarded_sync(path, mock.Mock(side_effect=RuntimeError("network failure")))
            self.assertEqual(self.sync_channels.guarded_sync(path, lambda: 7), 7)

    def test_extinf_escapes_attribute_quotes(self):
        value = self.sync_channels.build_extinf('game"name', "display", True)
        self.assertIn('group-title="game&quot;name"', value)

    def test_extinf_uses_channel_avatar(self):
        value = self.sync_channels.build_extinf(
            "一起看", "主播", True, "https://img.example/avatar.jpg?a=1&b=2"
        )
        self.assertIn('tvg-logo="https://img.example/avatar.jpg?a=1&b=2"', value)

    def test_huya_sync_supplements_full_together_category(self):
        def fake_get(url, timeout=20):
            query = self.sync_channels.urllib.parse.parse_qs(
                self.sync_channels.urllib.parse.urlparse(url).query
            )
            page = int(query["page"][0])
            game_id = query.get("gameId", [""])[0]
            if game_id == "2135":
                rows = [
                    {"profileRoom": f"together-{page}", "nick": f"一起看{page}",
                     "gameFullName": "一起看", "avatar180": f"https://img/{page}.jpg"},
                    {"profileRoom": "duplicate", "nick": "重复", "gameFullName": "一起看"},
                ]
                total_pages = 2
            else:
                rows = [{"profileRoom": "duplicate", "nick": "热门", "gameFullName": "星秀"}]
                total_pages = 10
            return self.sync_channels.json.dumps({
                "data": {"datas": rows, "totalPage": total_pages}
            })

        with mock.patch.object(self.sync_channels, "http_get", side_effect=fake_get):
            entries = self.sync_channels.fetch_huya(
                pages=1, extra_game_ids=["2135"], extra_pages=0, workers=2
            )
        self.assertEqual([entry[0] for entry in entries], [
            "duplicate", "together-1", "together-2"
        ])
        self.assertEqual({entry[1] for entry in entries}, {"一起看"})
        self.assertEqual(entries[1][3], "https://img/1.jpg")

    def test_huya_compact_groups_reduce_player_clutter(self):
        cases = {
            "王者荣耀": "手游",
            "英雄联盟": "网游电竞",
            "主机游戏": "单机",
            "星秀": "娱乐",
            "户外": "户外",
            "斯诺克": "体育",
            "未知新游戏": "其他",
            "原创": "一起看",
        }
        for game, expected in cases.items():
            with self.subTest(game=game):
                self.assertEqual(self.sync_channels.compact_live_group(game), expected)

    def test_huya_detail_group_mode_keeps_original_category(self):
        payload = self.sync_channels.json.dumps({
            "data": {"datas": [{"profileRoom": "1", "gameFullName": "王者荣耀"}], "totalPage": 1}
        })
        with mock.patch.object(self.sync_channels, "http_get", return_value=payload):
            entries = self.sync_channels.fetch_huya(
                pages=1, extra_game_ids=[], workers=1, group_mode="detail"
            )
        self.assertEqual(entries[0][1], "王者荣耀")

    def test_douyu_compact_group_and_avatar(self):
        payload = self.sync_channels.json.dumps({
            "code": 0,
            "data": {"rl": [{
                "rid": 7, "nn": "主播", "c2name": "主机其他游戏",
                "av": "avatar_v3/example"
            }]}
        })
        with mock.patch.object(self.sync_channels, "http_get", return_value=payload):
            entries = self.sync_channels.fetch_douyu(pages=1)
        self.assertEqual(entries[0][1], "单机")
        self.assertEqual(
            entries[0][3],
            "https://apic.douyucdn.cn/upload/avatar_v3/example_middle.jpg"
        )

    def test_huya_sync_rejects_severely_incomplete_pages(self):
        first = self.sync_channels.json.dumps({
            "data": {"datas": [{"profileRoom": "one"}], "totalPage": 5}
        })

        def mostly_broken(url, timeout=20):
            page = int(self.sync_channels.urllib.parse.parse_qs(
                self.sync_channels.urllib.parse.urlparse(url).query
            )["page"][0])
            if page == 1:
                return first
            raise OSError("temporary failure")

        with mock.patch.object(self.sync_channels, "http_get", side_effect=mostly_broken), \
             mock.patch.object(self.sync_channels.time, "sleep"):
            with self.assertRaisesRegex(RuntimeError, "保留旧目录"):
                self.sync_channels.fetch_huya(
                    pages=5, extra_game_ids=[], workers=2
                )

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
#EXTINF:-1 tvg-logo="https://img.example/iptv.png",UDP直播
#EXTVLCOPT:http-user-agent=example
udp://@239.0.0.1:1234
"""
        result, count, rejected = self.filter_iptv.filter_m3u(source, [])
        self.assertEqual(count, 1)
        self.assertFalse(rejected)
        self.assertIn('tvg-logo="https://img.example/iptv.png"', result)
        self.assertIn("#EXTVLCOPT:http-user-agent=example", result)
        self.assertIn("udp://@239.0.0.1:1234", result)

    def test_filter_core_scope_rejects_non_core_channels_and_repairs_groups(self):
        source = """#EXTM3U
#EXTINF:-1 group-title="错误分组",CCTV-1综合
https://live.example/cctv1.m3u8
#EXTINF:-1 group-title="地方频道",湖南卫视高清
https://live.example/hunan.m3u8
#EXTINF:-1 group-title="央视卫视",广东珠江
https://live.example/zhujiang.m3u8
#EXTINF:-1 group-title="央视卫视",湖南公共
https://live.example/hunan-public.m3u8
"""
        result, count, rejected = self.filter_iptv.filter_m3u(
            source, [], channel_scope="cctv_satellite"
        )
        self.assertEqual(count, 2)
        self.assertEqual(rejected["outside_scope"], 2)
        self.assertIn('group-title="IPTV·央视",CCTV-1综合', result)
        self.assertIn('group-title="IPTV·卫视",湖南卫视高清', result)
        self.assertNotIn("广东珠江", result)
        self.assertNotIn("湖南公共", result)
        self.assertIsNone(self.filter_iptv.classify_core_channel("CCTV-18测试"))
        self.assertIsNone(self.filter_iptv.classify_core_channel("CETV-5测试"))

    def test_filter_default_scope_keeps_configured_local_channels(self):
        source = """#EXTM3U
#EXTINF:-1 group-title="☘️广东频道",广东珠江
https://live.example/zhujiang.m3u8
"""
        result, count, rejected = self.filter_iptv.filter_m3u(source, [])
        self.assertEqual(count, 1)
        self.assertFalse(rejected)
        self.assertIn("广东珠江", result)
        self.assertIn('group-title="☘️广东频道"', result)

    def test_filter_publish_probe_removes_unplayable_and_limits_fallbacks(self):
        source = """#EXTM3U
#EXTINF:-1,CCTV-1
https://dead.example/cctv1.m3u8
#EXTINF:-1,CCTV-1
https://good.example/cctv1-a.m3u8
#EXTINF:-1,CCTV-1
https://good.example/cctv1-b.m3u8
#EXTINF:-1,CCTV-1
https://good.example/cctv1-c.m3u8
"""

        def fake_probe(entry, timeout):
            self.assertEqual(timeout, 7)
            self.assertNotIn("cctv1-c", entry.url)
            return "good.example" in entry.url

        result, count, rejected = self.filter_iptv.filter_m3u(
            source, [], channel_scope="cctv_satellite", verify_streams=True,
            verify_timeout=7, verify_workers=2, urls_per_channel=2, probe=fake_probe,
        )
        self.assertEqual(count, 2)
        self.assertEqual(rejected["unplayable"], 1)
        self.assertEqual(rejected["url_limit"], 1)
        self.assertNotIn("dead.example", result)
        self.assertIn("cctv1-a.m3u8", result)
        self.assertIn("cctv1-b.m3u8", result)
        self.assertNotIn("cctv1-c.m3u8", result)
        self.assertEqual(self.filter_iptv.unique_channel_count(result), 1)

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

    def test_filter_cli_falls_back_only_when_every_probe_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = pathlib.Path(tmp, "source.m3u")
            target = pathlib.Path(tmp, "published.m3u")
            source.write_text("""#EXTM3U
#EXTINF:-1 tvg-logo="https://img.example/a.png",频道一
https://fresh.example/one.m3u8
#EXTINF:-1 tvg-logo="https://img.example/b.png",频道二
https://fresh.example/two.m3u8
""")
            argv = [
                "filter_iptv.py", "--input", str(source), "--output", str(target),
                "--min-channels", "2", "--verify-streams",
                "--fallback-on-probe-outage",
            ]
            with mock.patch.object(
                self.filter_iptv, "verify_channel_candidates",
                return_value=([], 2, 0),
            ), mock.patch.object(sys, "argv", argv):
                self.assertEqual(self.filter_iptv.main(), 0)
            published = target.read_text()
            self.assertIn("fresh.example/one.m3u8", published)
            self.assertIn('tvg-logo="https://img.example/a.png"', published)

    def test_filter_cli_does_not_fallback_after_partial_probe_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = pathlib.Path(tmp, "source.m3u")
            target = pathlib.Path(tmp, "published.m3u")
            source.write_text("""#EXTM3U
#EXTINF:-1,频道一
https://fresh.example/one.m3u8
#EXTINF:-1,频道二
https://fresh.example/two.m3u8
""")
            target.write_text("last-good\n")
            argv = [
                "filter_iptv.py", "--input", str(source), "--output", str(target),
                "--min-channels", "2", "--verify-streams",
                "--fallback-on-probe-outage",
            ]
            _, entries = self.filter_iptv.parse_entries(source.read_text())
            with mock.patch.object(
                self.filter_iptv, "verify_channel_candidates",
                return_value=([entries[0]], 1, 0),
            ), mock.patch.object(sys, "argv", argv):
                self.assertEqual(self.filter_iptv.main(), 3)
            self.assertEqual(target.read_text(), "last-good\n")


if __name__ == "__main__":
    unittest.main()

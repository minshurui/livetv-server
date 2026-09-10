import importlib.util
import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


def load_module(name, relative_path):
    spec = importlib.util.spec_from_file_location(name, ROOT / relative_path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class PythonHelperTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.stream_proxy = load_module("stream_proxy", "docker/stream-proxy.py")
        cls.sync_channels = load_module("sync_channels", "docker/sync_channels.py")

    def test_hls_filename_allowlist(self):
        for name in ("index.m3u8", "seg_00001.ts", "seg_9.ts"):
            self.assertTrue(self.stream_proxy.valid_hls_filename(name))
        for name in ("ffmpeg.err", "../index.m3u8", "seg_.ts", "seg_one.ts"):
            self.assertFalse(self.stream_proxy.valid_hls_filename(name))

    def test_extinf_escapes_attribute_quotes(self):
        value = self.sync_channels.build_extinf('game"name', "display", True)
        self.assertIn('group-title="game&quot;name"', value)


if __name__ == "__main__":
    unittest.main()

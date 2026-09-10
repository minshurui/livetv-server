#!/usr/bin/env python3
# -*- coding: utf-8 -*-
import argparse, json, os, sys, tempfile, time, urllib.request
try:
    from xml.sax.saxutils import escape as _xml_escape
except Exception:
    def _xml_escape(s): return s

UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/120.0 Safari/537.36")

def http_get(url, timeout=20):
    req = urllib.request.Request(url, headers={
        "User-Agent": UA, "Referer": "https://www.huya.com/",
        "Accept": "application/json, text/javascript, */*; q=0.01"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        data = r.read()
    for enc in ("utf-8", "gbk", "gb18030"):
        try: return data.decode(enc)
        except UnicodeDecodeError: continue
    return data.decode("utf-8", errors="replace")

def fetch_huya(pages=15, page_size=120):
    entries, seen = [], set()
    for page in range(1, pages+1):
        url = ("https://www.huya.com/cache.php?m=LiveList&do=getLiveListByPage"
               f"&tagAll=0&page={page}&pageSize={page_size}")
        try: d = json.loads(http_get(url, 25))
        except Exception as e:
            print(f"  [huya] 第{page}页失败: {e}"); break
        datas = (d.get("data") or {}).get("datas") or []
        if not datas: print(f"  [huya] 第{page}页无数据, 停止翻页"); break
        got = 0
        for it in datas:
            rid = it.get("profileRoom") or it.get("roomId")
            nick = (it.get("nick") or "").strip(); room = (it.get("roomName") or "").strip()
            game = (it.get("gameFullName") or "").strip() or "直播"
            if not rid or rid in seen: continue
            seen.add(rid); display = nick or room or str(rid)
            entries.append((str(rid), game, display)); got += 1
        print(f"  [huya] 第{page}/{pages}页: {len(datas)}条, 新增{got}条")
        if got == 0: break
        time.sleep(0.3)
    return entries

def fetch_douyu(pages=20):
    entries, seen = [], set()
    for page in range(1, pages+1):
        url = f"https://www.douyu.com/gapi/rkc/directory/0_0/{page}"
        try: d = json.loads(http_get(url, 25))
        except Exception as e:
            print(f"  [douyu] 第{page}页失败: {e}"); break
        if d.get("code") != 0: print(f"  [douyu] 第{page}页 code={d.get('code')}, 停止"); break
        rl = ((d.get("data") or {}).get("rl")) or []
        if not rl: print(f"  [douyu] 第{page}页无数据, 停止"); break
        got = 0
        for it in rl:
            rid = it.get("rid"); nn = (it.get("nn") or "").strip(); rn = (it.get("rn") or "").strip()
            c2 = (it.get("c2name") or "").strip()
            # 全部分类目录常见 c2name="热门游戏"(过多, 展开成具体子分类若可用)
            if not rid or rid in seen: continue
            seen.add(rid); display = nn or rn or str(rid)
            game = c2
            entries.append((str(rid), game, display)); got += 1
        print(f"  [douyu] 第{page}/{pages}页: {len(rl)}条, 新增{got}条")
        if got == 0: break
        time.sleep(0.3)
    return entries

def build_extinf(game, display, is_huya):
    logo = ("https://huyaimg.msstatic.com/avatar/logo.jpg" if is_huya
            else "https://apic.douyucdn.cn/upload/avatar_v3/logo.jpg")
    game = game or "直播"
    attrs = {'"': '&quot;'}
    return ('#EXTINF:-1 tvg-logo="%s" group-title="%s", %s'
            % (logo, _xml_escape(game, attrs), _xml_escape(display)))

def main():
    ap = argparse.ArgumentParser(description="自研虎牙/斗鱼白名单获取器")
    ap.add_argument("--out", default=None)
    ap.add_argument("--huya-pages", type=int, default=int(os.environ.get("SYNC_HUYA_PAGES", "15")))
    ap.add_argument("--douyu-pages", type=int, default=int(os.environ.get("SYNC_DOUYU_PAGES", "20")))
    # 500 会让冷门时段或平台接口缩减时首次启动永远没有频道；只用合理下限
    # 防止错误响应覆盖 last-good 文件，实际可用性继续由拉流健康检查负责。
    ap.add_argument("--min-huya", type=int, default=int(os.environ.get("SYNC_MIN_HUYA", "20")))
    ap.add_argument("--min-douyu", type=int, default=int(os.environ.get("SYNC_MIN_DOUYU", "20")))
    args = ap.parse_args()
    if args.out: out_path = args.out
    else:
        data_root = os.environ.get("DATA") or os.path.expanduser("~")
        out_path = os.path.join(data_root, "lnmp", "allinone", "channels.json")
    print(f"=== 开始同步直播白名单 -> {out_path} ===")
    huya = fetch_huya(pages=args.huya_pages); print(f"[huya] 共抓取 {len(huya)} 房间")
    douyu = fetch_douyu(pages=args.douyu_pages); print(f"[douyu] 共抓取 {len(douyu)} 房间")
    if len(huya) < args.min_huya: print(f"[警告] 虎牙仅{len(huya)}<{args.min_huya}, 保留旧文件"); return 1
    if len(douyu) < args.min_douyu: print(f"[警告] 斗鱼仅{len(douyu)}<{args.min_douyu}, 保留旧文件"); return 1
    result = {
        "huya": [[rid, build_extinf(g, d, True)] for rid, g, d in huya],
        "douyu": [[rid, build_extinf(g, d, False)] for rid, g, d in douyu],
    }
    out_dir = os.path.dirname(out_path) or "."
    os.makedirs(out_dir, exist_ok=True)
    fd, tmpf = tempfile.mkstemp(dir=out_dir, prefix=".channels-", suffix=".tmp")
    with os.fdopen(fd, "w", encoding="utf-8") as f: json.dump(result, f, ensure_ascii=False)
    os.replace(tmpf, out_path)
    print(f"=== 写入完成: 虎牙{len(huya)} + 斗鱼{len(douyu)} = {len(huya)+len(douyu)} 房间 ===")
    return 0

if __name__ == "__main__":
    sys.exit(main())

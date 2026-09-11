#!/usr/bin/env python3
# -*- coding: utf-8 -*-
import argparse, concurrent.futures, json, os, sys, tempfile, time, urllib.parse, urllib.request
try:
    from xml.sax.saxutils import escape as _xml_escape
except Exception:
    def _xml_escape(s): return s

UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/120.0 Safari/537.36")

COMPACT_GROUPS = (
    ("体育", ("体育", "斯诺克", "台球", "足球", "篮球", "网球", "棋牌")),
    ("户外", ("户外", "旅行", "钓鱼", "美食", "科技")),
    ("手游", ("王者荣耀", "和平精英", "金铲铲", "手游", "第五人格", "蛋仔", "火影忍者",
             "洛克王国", "皇室战争", "实况足球", "鹅鸭杀")),
    ("网游电竞", ("英雄联盟", "无畏契约", "CS2", "三角洲", "穿越火线", "云顶之弈",
                 "炉石", "DOTA", "QQ飞车", "魔兽", "坦克世界")),
    ("单机", ("主机", "我的世界", "RUST", "永劫无间", "逃离塔科夫", "天天吃鸡",
             "互动点播", "单机")),
    ("娱乐", ("星秀", "交友", "二次元", "音乐", "虚拟偶像", "上分", "娱乐")),
)

def compact_live_group(game, forced_group=""):
    if forced_group:
        return forced_group
    game = (game or "").strip()
    if game in ("一起看", "原创"):
        return "一起看"
    for group, keywords in COMPACT_GROUPS:
        if any(keyword.casefold() in game.casefold() for keyword in keywords):
            return group
    return "其他"

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

def _fetch_huya_page(page, page_size, game_id=None, retries=2):
    query = {"m": "LiveList", "do": "getLiveListByPage", "tagAll": 0,
             "page": page, "pageSize": page_size}
    if game_id:
        query["gameId"] = game_id
    url = "https://www.huya.com/cache.php?" + urllib.parse.urlencode(query)
    last_error = None
    for attempt in range(retries + 1):
        try:
            payload = json.loads(http_get(url, 25))
            data = payload.get("data") or {}
            return data.get("datas") or [], int(data.get("totalPage") or 0)
        except Exception as exc:
            last_error = exc
            if attempt < retries:
                time.sleep(0.5 * (attempt + 1))
    raise RuntimeError(f"虎牙第 {page} 页请求失败: {last_error}")

def _fetch_huya_directory(page_limit, page_size, workers, game_id=None,
                          max_pages=100, min_page_ratio=0.8):
    label = f"分类 {game_id}" if game_id else "热门总榜"
    first, total_pages = _fetch_huya_page(1, page_size, game_id)
    if not first:
        raise RuntimeError(f"虎牙{label}第 1 页无数据")
    requested = page_limit if page_limit > 0 else (total_pages or 1)
    target = min(requested, total_pages or requested, max_pages)
    target = max(1, target)
    print(f"  [huya] {label}: 接口共 {total_pages or '?'} 页，本次抓取 {target} 页")

    results, failures = {1: first}, []
    if target > 1:
        with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, min(workers, target - 1))) as pool:
            pending = {
                pool.submit(_fetch_huya_page, page, page_size, game_id): page
                for page in range(2, target + 1)
            }
            for future in concurrent.futures.as_completed(pending):
                page = pending[future]
                try:
                    datas, _ = future.result()
                    results[page] = datas
                    print(f"  [huya] {label} 第{page}/{target}页: {len(datas)}条")
                except Exception as exc:
                    failures.append(page)
                    print(f"  [huya] {label} 第{page}/{target}页失败: {exc}")

    coverage = len(results) / target
    if coverage < min_page_ratio:
        raise RuntimeError(
            f"虎牙{label}只成功 {len(results)}/{target} 页，低于 {min_page_ratio:.0%}，保留旧目录"
        )
    if failures:
        print(f"  [huya] {label} 缺失页: {','.join(map(str, sorted(failures)))}")
    return [item for page in sorted(results) for item in results[page]]

def fetch_huya(pages=15, page_size=120, extra_game_ids=("2135",),
               extra_pages=0, workers=6, group_mode="compact"):
    # 热门总榜只包含平台前若干页，冷门分类会严重缺项；额外按 gameId 抓取并去重。
    # 2135 是虎牙“一起看”，extra_pages=0 表示按接口报告页数抓完整分类。
    raw = [(item, "") for item in _fetch_huya_directory(pages, page_size, workers)]
    for game_id in extra_game_ids:
        game_id = str(game_id).strip()
        if game_id:
            forced_group = "一起看" if game_id == "2135" else ""
            raw.extend((item, forced_group) for item in _fetch_huya_directory(
                extra_pages, page_size, workers, game_id=game_id
            ))

    entries, seen = [], set()
    forced_groups = {}
    for it, forced_group in raw:
        rid = str(it.get("profileRoom") or it.get("roomId") or "").strip()
        if rid and forced_group:
            forced_groups[rid] = forced_group
    for it, forced_group in raw:
        rid = it.get("profileRoom") or it.get("roomId")
        nick = (it.get("nick") or "").strip(); room = (it.get("roomName") or "").strip()
        game = (it.get("gameFullName") or "").strip() or "直播"
        rid = str(rid or "").strip()
        if not rid or rid in seen: continue
        seen.add(rid); display = nick or room or rid
        group = game if group_mode == "detail" else compact_live_group(game, forced_groups.get(rid, forced_group))
        logo = (it.get("avatar180") or it.get("screenshot") or "").strip()
        entries.append((rid, group, display, logo))
    return entries

def fetch_douyu(pages=20, group_mode="compact"):
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
            group = game if group_mode == "detail" else compact_live_group(game)
            avatar = (it.get("av") or "").strip()
            if avatar and not avatar.startswith(("http://", "https://")):
                avatar = "https://apic.douyucdn.cn/upload/" + avatar.strip("/") + "_middle.jpg"
            logo = avatar or (it.get("rs16") or "").strip()
            entries.append((str(rid), group, display, logo)); got += 1
        print(f"  [douyu] 第{page}/{pages}页: {len(rl)}条, 新增{got}条")
        if got == 0: break
        time.sleep(0.3)
    return entries

def build_extinf(game, display, is_huya, logo=""):
    if not logo:
        logo = ("https://huyaimg.msstatic.com/avatar/logo.jpg" if is_huya
                else "https://apic.douyucdn.cn/upload/avatar_v3/logo.jpg")
    game = game or "直播"
    attrs = {'"': '&quot;'}
    # M3U 不是 XML，头像查询串里的 & 必须原样保留，否则部分客户端会请求到错误 URL。
    logo = logo.replace('"', '%22').replace('\r', '').replace('\n', '')
    return ('#EXTINF:-1 tvg-logo="%s" group-title="%s", %s'
            % (logo, _xml_escape(game, attrs), _xml_escape(display)))

def main():
    ap = argparse.ArgumentParser(description="自研虎牙/斗鱼白名单获取器")
    ap.add_argument("--out", default=None)
    ap.add_argument("--huya-pages", type=int, default=int(os.environ.get("SYNC_HUYA_PAGES", "15")))
    ap.add_argument("--huya-extra-game-ids", default=os.environ.get("SYNC_HUYA_EXTRA_GAME_IDS", "2135"),
                    help="逗号分隔的虎牙分类 gameId；默认补全一起看(2135)，留空关闭")
    ap.add_argument("--huya-extra-pages", type=int, default=int(os.environ.get("SYNC_HUYA_EXTRA_PAGES", "0")),
                    help="额外分类抓取页数；0 表示按接口报告的总页数抓取")
    ap.add_argument("--huya-workers", type=int, default=int(os.environ.get("SYNC_HUYA_WORKERS", "6")))
    ap.add_argument("--huya-group-mode", choices=("compact", "detail"),
                    default=os.environ.get("HUYA_GROUP_MODE", "compact"),
                    help="compact 合并为 8 个大类；detail 保留虎牙原始游戏分类")
    ap.add_argument("--douyu-pages", type=int, default=int(os.environ.get("SYNC_DOUYU_PAGES", "20")))
    ap.add_argument("--douyu-group-mode", choices=("compact", "detail"),
                    default=os.environ.get("DOUYU_GROUP_MODE", "compact"),
                    help="compact 合并为大类；detail 保留斗鱼原始游戏分类")
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
    extra_game_ids = [value.strip() for value in args.huya_extra_game_ids.split(",") if value.strip()]
    huya = fetch_huya(pages=args.huya_pages, extra_game_ids=extra_game_ids,
                      extra_pages=args.huya_extra_pages, workers=args.huya_workers,
                      group_mode=args.huya_group_mode)
    print(f"[huya] 共抓取 {len(huya)} 房间")
    douyu = fetch_douyu(pages=args.douyu_pages, group_mode=args.douyu_group_mode)
    print(f"[douyu] 共抓取 {len(douyu)} 房间")
    if len(huya) < args.min_huya: print(f"[警告] 虎牙仅{len(huya)}<{args.min_huya}, 保留旧文件"); return 1
    if len(douyu) < args.min_douyu: print(f"[警告] 斗鱼仅{len(douyu)}<{args.min_douyu}, 保留旧文件"); return 1
    result = {
        "huya": [[rid, build_extinf(g, d, True, logo)] for rid, g, d, logo in huya],
        "douyu": [[rid, build_extinf(g, d, False, logo)] for rid, g, d, logo in douyu],
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

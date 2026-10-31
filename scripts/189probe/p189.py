#!/usr/bin/env python3
"""天翼云盘（189）登录态只读探测脚本 —— 无第三方依赖（urllib + hashlib + openssl）。

设计要点：
- 凭据只从环境变量 CT189_LOGIN_COOKIE 读取，绝不打印、绝不落盘（输出一律占位符）。
- 只读：只调用 listFiles / getFileDownloadUrl / 探测分片签名；不建、不删、不改任何既有文件。
- 所有对外输出经过 redact()，Cookie / sessionKey / sessionSecret / 直链一律脱敏。
"""
import base64
import hashlib
import hmac
import json
import os
import re
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

COOKIE = os.environ.get("CT189_LOGIN_COOKIE", "").strip()
APP_ID = "8025431004"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
PROXY = os.environ.get("PROBE_PROXY", "").strip()

SECRETS: list[str] = []          # 注册到这里的字符串，输出前一律替换


def redact(text: str) -> str:
    out = text
    if COOKIE:
        out = out.replace(COOKIE, "<COOKIE_REDACTED>")
        for part in COOKIE.split(";"):
            part = part.strip()
            if len(part) > 12:
                out = out.replace(part, "<COOKIE_PART_REDACTED>")
                out = out.replace(part.split("=", 1)[-1], "<COOKIE_VAL_REDACTED>")
    for s in SECRETS:
        if s and len(s) > 6:
            out = out.replace(s, "<REDACTED>")
    # 兜底：任何 >32 的十六进制/长 token 串
    out = re.sub(r"[0-9a-fA-F]{32,}", "<HEX_REDACTED>", out)
    out = re.sub(r"(SessionKey=)[^&\s]+", r"\1<REDACTED>", out)
    out = re.sub(r"(sessionKey=)[^&\s]+", r"\1<REDACTED>", out)
    return out


def p(*args):
    print(redact(" ".join(str(a) for a in args)))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def opener(follow=True):
    handlers = []
    if not follow:
        handlers.append(NoRedirect())
    ctx = ssl.create_default_context()
    handlers.append(urllib.request.HTTPSHandler(context=ctx))
    if PROXY:
        handlers.append(urllib.request.ProxyHandler({"http": PROXY, "https": PROXY}))
    return urllib.request.build_opener(*handlers)


def http(method, url, headers=None, body=None, follow=True, timeout=25):
    req = urllib.request.Request(url, data=body, method=method)
    h = {"User-Agent": UA}
    h.update(headers or {})
    for k, v in h.items():
        req.add_header(k, v)
    try:
        resp = opener(follow).open(req, timeout=timeout)
        return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read()
    except Exception as e:  # noqa: BLE001
        return None, {}, str(e).encode()


# ---------------------------------------------------------------- 会话换取
def step1_web_session():
    """用 cookie 打 cloud.189.cn 的 cookie 会话接口，确认登录态是否有效。"""
    p("\n=== [1] cookie → 网站会话有效性 ===")
    st, hdr, body = http("GET", "https://cloud.189.cn/api/portal/getUserInfo.action",
                         {"Accept": "application/json",
                          "Cookie": COOKIE,
                          "Referer": "https://cloud.189.cn/web/main"})
    p(f"HTTP {st}")
    txt = body.decode("utf-8", "replace")
    p("body:", txt[:400])
    ok = st == 200 and '"success":true' in txt
    # 从响应里捞 sessionKey / sessionSecret（若网站侧直接下发）
    mp = {}
    for key in ("sessionKey", "sessionSecret", "familySessionKey", "familySessionSecret",
                "accessToken", "loginName"):
        m = re.search(rf'"{key}"\s*:\s*"([^"]*)"', txt)
        if m:
            mp[key] = m.group(1)
            if key in ("sessionKey", "sessionSecret", "familySessionKey", "familySessionSecret"):
                SECRETS.append(m.group(1))
    p("捞到字段:", {k: ("<present,len=%d>" % len(v)) for k, v in mp.items()})
    return mp, ok


def step1b_access_token():
    """尝试用 cookie 换 accessToken，再换 api.cloud.189.cn 的 session。"""
    p("\n=== [1b] cookie → accessToken → getSessionForPC ===")
    cands = [
        "https://cloud.189.cn/api/open/oauth/getAccessToken.action",
        "https://cloud.189.cn/api/portal/getAccessToken.action",
    ]
    tok = None
    for u in cands:
        st, _, body = http("GET", u, {"Cookie": COOKIE, "Accept": "application/json",
                                     "Referer": "https://cloud.189.cn/web/main"})
        txt = body.decode("utf-8", "replace")
        p(f"{u} -> HTTP {st} {txt[:200]}")
        m = re.search(r'"(?:accessToken|token)"\s*:\s*"([^"]+)"', txt)
        if m:
            tok = m.group(1)
            SECRETS.append(tok)
            break
    if not tok:
        p("未取到 accessToken（该路径不可用）")
        return None
    p("accessToken 已取到 len=", len(tok))
    u = (f"https://api.cloud.189.cn/getSessionForPC.action?appId={APP_ID}"
         f"&accessToken={tok}&clientType=TELEPC&version=6.2"
         f"&channelId=web_cloud.189.cn")
    st, _, body = http("GET", u, {"Accept": "application/json"})
    txt = body.decode("utf-8", "replace")
    p(f"getSessionForPC -> HTTP {st} {txt[:400]}")
    mp = {}
    for key in ("sessionKey", "sessionSecret", "familySessionKey", "familySessionSecret"):
        m = re.search(rf'"{key}"\s*:\s*"([^"]*)"', txt)
        if m:
            mp[key] = m.group(1)
            SECRETS.append(m.group(1))
    p("session 字段:", {k: f"<len={len(v)}>" for k, v in mp.items()})
    return mp or None


# ---------------------------------------------------------------- 签名
def aes_ecb_encrypt_hex(key_hex_src: str, plaintext: str) -> str:
    """AES-ECB(PKCS7) 加密，返回 hex。密钥取 sessionSecret[:16]。"""
    key = key_hex_src[:16].encode()
    data = plaintext.encode()
    pad = 16 - (len(data) % 16)
    data += bytes([pad]) * pad
    r = subprocess.run(["openssl", "enc", "-aes-128-ecb", "-K", key.hex(),
                        "-nosalt", "-nopad"], input=data, capture_output=True)
    if r.returncode != 0:
        raise RuntimeError("openssl aes failed: " + r.stderr.decode()[:200])
    return r.stdout.hex()


def hmac_sha1_hex(secret_hex_src: str, msg: str) -> str:
    """HMAC-SHA1，secret 用 sessionSecret 的字节表示（hex 字符串本身），大写输出。"""
    dig = hmac.new(secret_hex_src.encode(), msg.encode(), hashlib.sha1).hexdigest()
    return dig.upper()


def rfc1123_now() -> str:
    return datetime.now(timezone.utc).strftime("%a, %d %b %Y %H:%M:%S GMT")


def signed_request(session_key, session_secret, path, query_params=None):
    """按 AList drivers/189pc 的签名规则构造请求。"""
    date = rfc1123_now()
    parts = [f"SessionKey={session_key}", "Operate=GET", f"RequestURI={path}", f"Date={date}"]
    params_hex = None
    if query_params:
        params_hex = aes_ecb_encrypt_hex(session_secret, query_params)
        parts.append(f"params={params_hex}")
    sign_src = "&".join(parts)
    signature = hmac_sha1_hex(session_secret, sign_src)
    url = (f"https://api.cloud.189.cn{path}?clientType=TELEPC&version=6.2"
           f"&channelId=web_cloud.189.cn&rand={int(time.time()*1000)}")
    if params_hex:
        url += f"&params={params_hex}"
    headers = {"Accept": "application/json", "Date": date, "SessionKey": session_key,
               "Signature": signature,
               "X-Request-ID": hashlib.md5(date.encode()).hexdigest()}
    return url, headers


def api_get(sk, ss, path, params_qs=None):
    url, headers = signed_request(sk, ss, path, params_qs)
    st, hdr, body = http("GET", url, headers)
    return st, hdr, body.decode("utf-8", "replace")


def step0_ip_binding():
    """天翼 cookie 与签发时的客户端 IP 绑定；IP 不符时一切都无法继续。
    返回 (ok, cookies_ip, cur_ip)。"""
    p("\n=== [0] 前置：cookie 的 IP 绑定校验 ===")
    # 实测存在瞬时 cookieUserSession=null（服务端会话缓存抖动），重试后再下结论，
    # 避免把抖动误判成「凭据无效」。
    txt, st = "", None
    for attempt in range(1, 4):
        st, _, body = http("GET", "https://cloud.189.cn/api/portal/getUserInfo.action",
                           {"Accept": "application/json", "Cookie": COOKIE,
                            "Referer": "https://cloud.189.cn/web/main"})
        txt = body.decode("utf-8", "replace")
        p(f"  尝试 {attempt}: HTTP {st} {txt[:200]}")
        if "cookieUserSession is null" not in txt:
            break
        time.sleep(2 * attempt)
    p(f"最终响应: HTTP {st} {txt[:300]}")
    m = re.search(r"check ip error - curIp=([^,]+), cookiesIp=(\S+?)[\"}]", txt)
    if m:
        cur, cip = m.group(1), m.group(2)
        p(f"BLOCKED: cookie 绑定 IP={cip}，本机出口 IP={cur}")
        return False, cip, cur
    if "cookieUserSession is null" in txt:
        p("BLOCKED: 会话在服务端查不到（凭据已被登出/轮换，或从未生效）")
        return False, None, None
    ok = st == 200 and '"success":true' in txt
    p(f"IP 绑定校验通过={ok}")
    return ok, None, None


def main():
    if not COOKIE:
        p("FATAL: CT189_LOGIN_COOKIE 为空，无法探测。")
        return 2
    p("cookie 已载入：len=%d（remainder redacted）" % len(COOKIE))

    ip_ok, cip, cur = step0_ip_binding()
    if not ip_ok:
        p("\n结论：cookie 与签发 IP 绑定，本机出口 IP 不符 —— 后续探测全部无法进行。")
        if cip:
            p(f"  需要：在与 {cip} 相同出口 IP 的机器上运行本脚本，或在该机器重新登录后取新 cookie。")
        return 6

    mp, web_ok = step1_web_session()
    sess = step1b_access_token()
    if not sess and mp.get("sessionKey") and mp.get("sessionSecret"):
        sess = {"sessionKey": mp["sessionKey"], "sessionSecret": mp["sessionSecret"]}
    if not sess:
        p("\n未能取得 sessionKey/sessionSecret —— 后续探测跳过。")
        return 3

    sk, ss = sess["sessionKey"], sess["sessionSecret"]
    p(f"\nsessionKey len={len(sk)}  sessionSecret len={len(ss)}")

    p("\n=== [2] 列目录（验证签名逻辑是否正确）===")
    st, _, txt = api_get(sk, ss, "/listFiles.action", '{"folderId":"-11","pageNum":1,"pageSize":5}')
    p(f"HTTP {st} {txt[:400]}")

    p("\n=== [3] 取下载地址 ===")
    # 只认同 type=="file" 的条目；类型可能是 "file"/"folder"（数字编码亦兼容）
    fid = None
    for fm in re.finditer(r'\{[^{}]*\}', txt):
        seg = fm.group(0)
        if not re.search(r'"type"\s*:\s*"?file"?', seg):
            continue
        im = re.search(r'"id"\s*:\s*"?([\w-]+)"?', seg)
        if im:
            fid = im.group(1)
            break
    if not fid:
        m = re.search(r'"id"\s*:\s*"?([\w-]+)"?', txt)
        fid = m.group(1) if m else None
        p("警告：未在响应中识别到 type=file，回退到首个 id")
    if not fid:
        p("目录内无可用文件 id，跳过下载探测（根目录可能只有文件夹）")
        return 4
    p(f"fileId 已取到 len={len(fid)}")
    st, _, txt = api_get(sk, ss, "/getFileDownloadUrl.action",
                         json.dumps({"fileId": fid, "dt": 3, "flag": 1, "mediaType": -1},
                                    separators=(",", ":")))
    p(f"HTTP {st}")
    mu = re.search(r'"fileDownloadUrl"\s*:\s*"([^"]+)"', txt)
    p("响应字段:", txt[:300])
    if not mu:
        p("未返回 fileDownloadUrl")
        return 5
    dl = mu.group(1).replace("&amp;", "&")
    if dl.startswith("http://"):
        dl = "https://" + dl[len("http://"):]
    SECRETS.append(dl)

    p("\n=== [4] 探 302 拿真实 CDN 地址 ===")
    st, hdr, _ = http("GET", dl, {"Accept": "*/*"}, follow=False)
    loc = hdr.get("Location") or hdr.get("location")
    p(f"第一跳 HTTP {st}  Location={'<present>' if loc else '<none>'}")
    if loc:
        SECRETS.append(loc)
        p("真实地址 host:", re.sub(r"^https?://([^/]+).*", r"\1", loc))

    p("\n=== [5] 裸取（无 Cookie / 无 Referer）—— 决定严格直连 ===")
    for label, hdrs in [("无任何头(仅UA)", {}),
                       ("仅 UA", {"User-Agent": UA})]:
        st, hdr, body = http("GET", loc or dl, hdrs, follow=True)
        cl = hdr.get("Content-Length") or hdr.get("content-length")
        p(f"  {label}: HTTP {st} len={len(body) if body else 0} Content-Length={cl} "
          f"Content-Type={hdr.get('Content-Type')}")
    # Range 支持
    st, hdr, body = http("GET", loc or dl, {"Range": "bytes=0-1023"})
    p(f"  Range bytes=0-1023: HTTP {st} got={len(body) if body else 0} "
      f"Content-Range={hdr.get('Content-Range')}")

    p("\n=== [6] TTL 观测：按真实间隔重取真实地址 ===")
    p("  说明：TTL_SCHEDULE 可用环境变量覆盖，默认 0/60/300/900 秒累积计时")
    sched = os.environ.get("TTL_SCHEDULE", "0,60,300,900")
    marks = sorted({int(x) for x in sched.split(",") if x.strip().isdigit()})
    t0 = time.time()
    for at in marks:
        delta = at - (time.time() - t0)
        if delta > 0:
            time.sleep(delta)
        st, hdr, body = http("GET", loc or dl, {})
        p(f"  t+{int(time.time()-t0)}s (目标 +{at}s) -> HTTP {st} "
          f"len={len(body) if body else 0} Content-Type={hdr.get('Content-Type')}")
        if st in (403, 404, 410):
            p("  -> 该时刻地址已失效，TTL 夹逼区间上限确立")
            break
    return 0


if __name__ == "__main__":
    sys.exit(main())

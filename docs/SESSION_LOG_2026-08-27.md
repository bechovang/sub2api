# Nhật ký phiên làm việc — 2026-08-27

> Bản ghi phiên vận hành/điều chỉnh `sub2api`. Đọc kèm
> [`ANTIGRAVITY_SETUP_VI.md`](ANTIGRAVITY_SETUP_VI.md) (runbook OAuth/gateway) và
> [`SESSION_LOG_2026-08-24.md`](SESSION_LOG_2026-08-24.md) (phiên trước, cùng lỗi §2.4).

## Mục tiêu phiên

1. Kiểm tra toàn bộ account Antigravity, xác định account nào còn hoạt động.
2. Chẩn đoán nguyên nhân lỗi `429 RESOURCE_EXHAUSTED` xuất hiện lại trên gateway.
3. Ghi nhận root cause + cách phát hiện sớm để không lặp lại.

---

## 1. Trạng thái hạ tầng khi bắt đầu

- Server chạy tại `http://127.0.0.1:8080`, process `server` (PID 9100),
  **start 08:42:24 cùng ngày** — restart để thêm account mới, **không có** env
  `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily`.
- Log: `backend/data/logs/sub2api.log`.
- Admin API key: header `x-api-key` (xem `backend/data/local-dev.env`).
- Sáng 09:11–09:13 đã thêm **3 account Antigravity mới** (id 13, 14, 15) qua luồng
  OAuth `auth-url` → `exchange-code` (một account exchange lỗi session, làm lại OK).

---

## 2. Kết quả kiểm tra 6 account Antigravity

Test bằng `POST /api/v1/admin/accounts/:id/test` (model mặc định `claude-sonnet-4-5`):

| ID | Account | OAuth refresh | Plan (Google trả) | Test | Kết luận |
|----|---------|---------------|-------------------|------|----------|
| 4 | hanngoziratech | ✅ | Pro | ❌ 429 | Acc sống, bị chặn ở endpoint prod |
| 5 | phuchcm2006@gmail.com | ✅ | Pro | ❌ 429 | Acc sống — **đã verify trực tiếp 200 qua daily** |
| 6 | bechovang@gmail.com | ✅ | Pro | ❌ 429 | Acc sống, bị chặn ở endpoint prod |
| 14 | phong007m@gmail.com | ✅ | Pro | ❌ 429 | Acc sống, chưa từng chạy request nào |
| 13 | fbphuchcm2006@gmail.com | ✅ | Pro | ❌ 403 VALIDATION_REQUIRED | Cần verify tay qua `validation_url` (§4.1 runbook) |
| 15 | thanhthuymaplearn@gmail.com | ✅ | Pro | ❌ 403 VALIDATION_REQUIRED | Như trên |

- Token OAuth **refresh bình thường trên cả 6 account** (Google chấp nhận grant,
  `_token_version` cập nhật liên tục mỗi giờ) → account **không chết**, không bị thu hồi.
- Tổng usage toàn bộ 6 acc từ trước tới nay: **~15 request / ~5K token**
  (`GET /accounts/:id/stats`; acc 14 = 0 request) → **không thể là hết quota thật**.

---

## 3. Chuỗi chẩn đoán (evidence)

### 3.1 Triệu chứng
- 11:19–11:31: gateway `/v1/chat/completions` group 4 (Antigravity) từ user test 16 →
  **174 dòng lỗi** `[antigravity-Forward] status=429`, failover quay vòng 4 account
  (14 → 6 → 4 → 5) đều 429, body lỗi luôn là:
  ```json
  { "error": { "code": 429, "message": "Resource has been exhausted (e.g. check quota).",
    "status": "RESOURCE_EXHAUSTED" } }
  ```
- Body 429 **trống thông tin**: không có `RetryInfo`, không có `quotaId`/`details`
  → các dòng `reset_in=59s` trong log là **mặc định của sub2api**
  (`parseAntigravitySmartRetryInfo` gán default khi upstream không trả retryDelay),
  không phải Google chỉ định.
- **Request Antigravity đầu tiên trong ngày** (11:19:47, sau 2,5 ngày không traffic)
  đã 429 ngay lần thử đầu → loại giả thuyết "client đánh hết quota trong phiên".

### 3.2 Loại trừ sub2api — gọi thẳng Google, bỏ qua relay
Lấy `refresh_token` account 5 (file `backend/data/antigravity-tokens.json`), tự refresh
access token bằng client credential công khai của Antigravity CLI
(`internal/pkg/antigravity/oauth.go`), rồi gọi `POST /v1internal:streamGenerateContent?alt=sse`
với body tối giản đúng format `wrapV1InternalRequest`:

| Endpoint | Kết quả |
|----------|---------|
| `https://cloudcode-pa.googleapis.com` (prod) | **429 RESOURCE_EXHAUSTED** |
| `https://daily-cloudcode-pa.sandbox.googleapis.com` (daily) | **200 OK** — model trả lời `"OK"`, có `usageMetadata`, `modelVersion: gemini-2.5-flash` |

→ **sub2api vô trespass** (request shape, relay, billing đều không phải nguyên nhân).
Google đang từ chối endpoint **prod** với các account consumer Antigravity; endpoint
**daily** vẫn nhận và trả kết quả thật.

### 3.3 Root cause
`resolveAntigravityForwardBaseURL()` (`antigravity_gateway_retry.go:61`) mặc định trả
**prod**; chỉ trả daily khi env `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily` được set
**tại lúc khởi động** (đọc bằng `os.Getenv`, không đọc từ `config.yaml`).

- Vòng retry chỉ dùng **đúng 1 URL** đã resolve (`availableURLs := []string{baseURL}`) —
  **không tự fallback** prod → daily khi dính 429.
- Server hiện tại khởi động 08:42 **không có env** → mọi forward + mọi admin "Test
  connection" đều đi prod → 429 toàn bộ.

Đây là **tái diễn đúng lỗi phiên 2026-08-24 §2.4** — không phải regression code, mà là
**mất biến môi trường khi restart backend** (chính là cảnh báo đã ghi trong runbook §8).

### 3.4 Lưu ý về nút "Test connection" (kiểm chứng code)
Với account platform `antigravity`, admin test đi qua
`AccountTestService.TestConnection` → `antigravityGatewayService.TestConnection` →
**cùng `antigravityRetryLoop`** → **cùng respect env daily**. Tức là sau khi bật env,
nút Test sẽ xanh lại như thường. (Nhánh `buildCodeAssistRequest` hardcode prod chỉ áp
dụng cho platform gemini-cli, không phải antigravity.)

---

## 4. Cách sửa

Restart backend **kèm env**. Từ thư mục `backend`:

**PowerShell (Windows):**
```powershell
$env:DATA_DIR = "$PWD\data"
$env:GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL = "daily"
Get-Content data\local-dev.env | ForEach-Object {
  if ($_ -match '^\s*([^=]+?)\s*=\s*(.*)$') { Set-Item ("env:"+$Matches[1].Trim()) $Matches[2].Trim() }
}
.\server.exe
```

**bash (đã dùng phiên 08-24):**
```bash
cd backend
set -a; source data/local-dev.env; set +a
DATA_DIR=$PWD/data GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily ./server
```

Sau restart:
1. Test lại từng account: `POST /api/v1/admin/accounts/:id/test`
   `{\"model_id\":\"gemini-2.5-flash\"}` → phải nhận `test_complete` + `success:true`.
2. Test end-to-end qua gateway group 4 bằng user key (user có balance + api_key group 4).
3. Các state `model_rate_limited` cũ trong `extra.model_rate_limits` tự hết (cửa sổ 59s).

**Đề nghị lâu dài (chưa làm phiên này):** đưa `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL`
vào script/service khởi động chính thức (systemd/NSSM/`.env` của process manager) để
không bao giờ phụ thuộc việc "nhớ gõ env tay"; cân nhắc patch `resolveAntigravityForwardBaseURL`
+ retry loop để **tự fallback prod → daily khi gặp 429 trống RetryInfo** (hiện chỉ 1 URL).

---

## 5. Việc còn treo

| Việc | Trạng thái |
|------|-----------|
| Restart server kèm env `daily` | ⏳ Chờ thực hiện (phiên này mới chẩn đoán) |
| Verify account 13, 15 qua `validation_url` (runbook §4.1) | ⏳ Cần mở browser đăng nhập đúng 2 email đó |
| Test end-to-end qua gateway group 4 sau restart | ⏳ |
| Patch auto-fallback prod→daily trên 429 trống | 💡 Đề xuất, chưa triển khai |

---

## 6. Bảng đối chiếu account Antigravity (cập nhật 2026-08-27)

| ID | Platform | Type | Ghi chú |
|----|----------|------|---------|
| 4 | antigravity | oauth | `hanngoziratech` Pro sub (cclog OAuth), project `swift-analogy-8ds98` |
| 5 | antigravity | oauth | `phuchcm2006@gmail.com` Pro, project `effective-ember-28chg` |
| 6 | antigravity | oauth | `bechovang@gmail.com` Pro, project `heroic-pact-blcf1` |
| 13 | antigravity | oauth | `fbphuchcm2006@gmail.com` — **403 chờ verify**, project `aicode-consumers` |
| 14 | antigravity | oauth | `phong007m@gmail.com` Pro (mới 08-27), project `cellular-drummer-t90z1` |
| 15 | antigravity | oauth | `thanhthuymaplearn@gmail.com` — **403 chờ verify**, project `aicode-consumers` |

> Nhận xét: 2 account chờ verify (13, 15) cùng map project default `aicode-consumers`
> (chưa onboard project riêng) — có thể liên quan việc Google bắt verify. Sau khi verify
> nên kiểm tra lại `project_id` có được onboard sang project riêng không (runbook §5).

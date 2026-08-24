# Nhật ký phiên làm việc — 2026-08-24

> Bản ghi phiên vận hành/điều chỉnh `sub2api`. Đọc kèm
> [`ANTIGRAVITY_SETUP_VI.md`](ANTIGRAVITY_SETUP_VI.md) (runbook OAuth/gateway) và
> [`BACKEND_VI.md`](BACKEND_VI.md).

## Mục tiêu phiên
1. Chạy dự án, xác minh trạng thái hiện tại.
2. Bỏ OpenRouter, tạo script load-test nhiều user.
3. Sửa lỗi relay Antigravity trả lỗi upstream.
4. Thêm **2 tài khoản Antigravity** mới để có rotation trong group.
5. Kiểm thử luồng queue / xoay vòng.

---

## 1. Trạng thái hạ tầng khi vận hành
- Backend binary: `backend/bin/server`, chạy từ `backend/` với:
  `DATA_DIR=$PWD/data`, `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily`.
- Server: `http://127.0.0.1:8080`, mode `production`, listener PID 25060
  (đã restart 13:36:57+07 để nạp biến môi trường `daily`).
- Log server: `/tmp/sub2api_server.log` (dùng `grep -iE "sticky|queue|rotat"` để soi).
- Admin API key: `admin-ef28def122597db0b89b545658de34a9926fd02098aa99a2fa85e15690ad6ec2`
  (header `x-api-key`), base `http://127.0.0.1:8080/api/v1/admin`.
- DB: psql `postgres/123456 @ sub2api` (`localhost:5432`).

---

## 2. Việc đã làm

### 2.1 Chạy dự án & xác minh routes
- Đã đối chiếu view: chỉ **Admin / Common / Gateway** được wire; **Auth / User bị "strip"**
  (không gọi `RegisterAuthRoutes`/`RegisterUserRoutes`) — không có panel/payment/login.
- Payment bị xoá ở tầng router (file còn nhưng không có route); chỉ còn **key-auth gateway
  + admin API**.

### 2.2 Bỏ OpenRouter
- `DELETE /api/v1/admin/accounts/1` → soft-delete "OpenRouter Main" (openai)
  (`deleted_at` = 2026-08-24 13:20:09+07).
- ⚠️ Hệ quả: group **"Token Le"** (platform openai, group 2) **không còn upstream** nào
  hoạt động. Chưa có account thay thế.
- Thay 2 placeholder cosmetic `openrouter/gpt-5` → `claude-sonnet-4` trong
  `frontend/src/views/admin/GroupsView.vue`.

### 2.3 Script load-test (không phụ thuộc thư viện)
- Tạo `sub2api/loadtest_100_users.mjs` (Node, không dep).
- Lệnh: `setup | run | all | cleanup | smoke` + option
  `--users --group --concurrency --req-per-user --kind anthropic|openai --model
  --path --max-tokens --prompt --timeout --balance --prefix`.
- Key tạo nằm ở `.loadtest/keys_<prefix>.json`; xoá user tạm bằng `cleanup --prefix`.
- Đã smoke-test: cơ chế ok.

### 2.4 Sửa relay Antigravity trả 429/503 (root cause)
- Triệu chứng: gateway báo "Upstream rate limit exceeded" → `503 no available accounts`.
- Nguyên nhân (runbook §2/§8): **thiếu**
  `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily` → server đang forward **PROD**
  (`cloudcode-pa.googleapis.com`) → account consumer bị `429 RESOURCE_EXHAUSTED`.
- Cách sửa: kill server cũ, chạy lại với env `daily`. → Test `success:true`.

### 2.5 Thêm 2 tài khoản Antigravity (rotation)
Dùng luồng OAuth consent (authorization code + PKCE):
1. `POST /api/v1/admin/antigravity/oauth/auth-url` → lấy `auth_url`+`session_id`+`state`.
2. User mở link, chọn Google account, Allow, dán callback `?code=...`.
3. `POST /api/v1/admin/antigravity/oauth/exchange-code`
   `{session_id, state, code}` → trả `access_token/refresh_token/project_id`.
4. `POST /api/v1/admin/accounts` với `credentials` từ response.

⚠️ **Bẫy quan trọng (dặn lại từ runbook §3):** code là **single-use**; mất session do restart
thì không đổi lại được. Lần đầu phiên này exchange thành công nhưng **chỉ in 20 ký tự,
không lưu full response** → mất token, phải consent lại. **Phải lưu nguyên response vào file
ngay** trước khi xử lý, sau đó xoá file token.

Account mới (cả 2 `status=active`, gắn group Antigravity id=4):

| ID | Name | Email | Project | Concurrency |
|----|------|-------|---------|-------------|
| 5 | phuchcm2006@gmail.com (Antigravity) | phuchcm2006@gmail.com | effective-ember-28chg | 5 |
| 6 | bechovang@gmail.com (Antigravity) | bechovang@gmail.com | heroic-pact-blcf1 | 5 |

File token tạm (`data/_ag_acc*`) đã **xoá** sau khi tạo account (đúng runbook §9).

### 2.6 Kiểm thử thực tế & billing
- User test: `agtest@test.local` (key sk-...36), balance 10.
- Model **chạy OK** qua Antigravity:
  - `gemini-3-flash` ✅ (348→21 token)
  - `gemini-2.5-flash` ✅ (348→2 token)
  - `claude-haiku-4-5` ✅ (404→6 token)
- Model **không chạy** (account consumer Antigravity không hỗ trợ, không phải lỗi):
  - `gemini-2.5-pro` (timeout 60s), `claude-sonnet-4-5` (upstream_error).
- Billing ghi đúng: `usage_logs` có `billing_mode=token`, đủ input/output/total_cost;
  balance trừ đúng (vd 10 → 9.9989).

---

## 3. Kiểm thử 10 user đồng thời — cơ chế queue & rotation

Lệnh:
```
node loadtest_100_users.mjs all --users 10 --group 4 --concurrency 10 \
  --kind anthropic --model gemini-3-flash --req-per-user 3 --max-tokens 16 --prefix lt10
```
Kết quả: **10/10 success (100%)**, throughput 5.4 req/s, p50 ≈ 1.6s.

### Giải mã log (mỗi request 3 mốc)
1. `sticky.selecting_account` — `group_id=4`, `session_key` riêng, `sticky_bound_account_id=0`
   (không session dính), `failed_account_count=0`.
2. `sticky.scheduler_entry` — scheduler tính điểm, `load_batch=true`, `has_concurrency_svc=true`,
   `excluded_count=0`.
3. `sticky.account_selected` — `selected_account_id` + **`slot_acquired=true`** +
   **`has_wait_plan=false`** (không phải chờ queue).

### Phân bổ thực tế (rotation)
| Account | concurrency | Nhận request |
|---|---|---|
| 4 hanngoziratech | 3 | 3 |
| 6 bechovang | 5 | 5 |
| 5 phuchcm | 5 | 2 |

### Kết luận về queue
- Tổng slot khả dụng của group 4 = **3 + 5 + 5 = 13**.
- 10 user < 13 → **không queue**; request vào thẳng, chỉ chờ model inference (~1.4–1.6s).
- **Queue chỉ xuất hiện khi demand > 13** (>13 concurrent) → log đổi thành
  `has_wait_plan=true` / có trường `queued`. Mặc định `UserMessageQueue.mode=""` (tắt queue).
  Scheduler + failover (`MaxAccountSwitches`) vẫn hoạt động.
- Duty `[DeferredService] BatchUpdateLastUsed flushed 3 accounts` cập nhật `last_used` nền.

---

## 4. Trạng thái hiện tại (cuối phiên)
- Server chạy (env `daily`).
- Group Antigravity (4): **3 account active** (id 4, 5, 6) cho rotation/failover.
- Script load-test có sẵn; dữ liệu user test `agtest` + một đợt `lt10` chưa cleanup (xoá mềm
  khi cần: `node loadtest_100_users.mjs cleanup --prefix lt10`).

## 5. Việc còn lại (gợi ý)
1. 100-user test thật → hiện bottleneck là **tổng slot (13)**; muốn mượt cần thêm account
   tăng slot, hoặc bật `UserMessageQueue` + tăng `concurrency` account.
2. Group "Token Le" (openai) đang không có upstream — cân nhắc bổ sung hoặc gỡ group.
3. Cân nhắc xoá user test `agtest`/`lt10`.

---

## 6. Phiên: Thêm gói free OpenRouter (nemotron-3) vào group "Token Le"

> Đọc kèm [`COMPOSITE_GROUPS.md`](COMPOSITE_GROUPS.md) (routing/gateway) nếu mở rộng sau này.

### Mục tiêu
- Tận dụng key OpenRouter (tài khoản **không nạp tiền, chỉ dùng model `:free`**) để tạo một
  "gói" bán **lãi 100%** (upstream cost = $0) qua group `openai` có sẵn **"Token Le" (id=2)**.
- Cả 3 model xác nhận tồn tại trên OpenRouter, chi phí $0, context ~1M, **hỗ trợ tools + thinking**:
  - `stealth/ox-alpha`
  - `nvidia/nemotron-3-ultra-550b-a55b:free`
  - `nvidia/nemotron-3.5-lightning:free`

### Điểm kỹ thuật (từ code)
1. sub2api không có platform `openrouter` → dùng lại platform `openai` + account **type `apikey`**
   với credential `base_url=https://openrouter.ai/api/v1`. `GetOpenAIBaseURL` đọc `base_url` trước fallback.
2. ⚠️ **Bẫy:** `GatewayService.GetAccessToken` **chỉ xử lý** OAuth / SetupToken / **APIKey** / Bedrock /
   ServiceAccount — **KHÔNG xử lý type `upstream`**. Nên OpenRouter phải dùng `type=apikey`
   (credential `api_key` + `base_url`), chứ không phải `upstream`.
3. **Đơn vị giá:** backend lưu `input_price`/`output_price` theo **USD/token**; frontend hiển thị
   **$/1M token** (`MToK=1e6`, hàm `mTokToPerToken` trong `frontend/.../channel/types.ts`).
   ⇒ Bán $0.2/1M = lưu **`0.0000002`**/token.
4. **Bán có lãi theo model** = tạo **channel** (`billing_model_source=channel_mapped`,
   `restrict_models=true`) với `model_pricing` cho 3 model → thay vì để chain
   Group→Channel→LiteLLM→Fallback trả $0.

### Đã tạo (instance local 127.0.0.1:8080, admin API key trong `backend/data/local-dev.env`)
| Loại | ID | Mô tả |
|------|----|-------|
| Account | 9 | `OpenRouter Free (Token Le)` — platform `openai`, type `apikey`, `base_url=openrouter`, gắn group 2 |
| Channel | 1 | `OpenRouter Free Pricing` — `channel_mapped`, `restrict_models`, group [2], giá input+output **$0.2/1M** cho 3 model |
| API key test | sk-a640… | user `khach1` (id 2), gắn group 2 |

Payload mẫu (account và channel) lưu trong ghi chú phiên / có thể tái dùng khi replay lên production.

### Kiểm thử end-to-end (dùng key sub2api, không phải key OpenRouter)
- `POST /v1/chat/completions` model `nvidia/nemotron-3.5-lightning:free` → `200`, "OK", kèm
  `reasoning_tokens`.
- `nvidia/nemotron-3-ultra-550b-a55b:free` → `200` "ping".
- `stealth/ox-alpha` → `200` "Ping!" (có `cached_tokens`).
- **Billing trừ đúng:** balance `khach1` 0.99989055 → 0.99986755 (115 tok × $0.2/1M = 0.000023).
  Upstream $0 ⇒ toàn bộ là lãi.
- **Restrict hoạt động:** model ngoài gói (vd `gpt-4o`) → `503 Service temporarily unavailable`
  (không có account phục vụ).

### Cách user dùng
Gọi OpenAI-compatible endpoint của gateway với key **sub2api**:
```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer <key-user-s2a>" \
  -d '{"model":"nvidia/nemotron-3.5-lightning:free","messages":[{"role":"user","content":"hi"}]}'
```
Client phù hợp: **Codex / Cline / OpenCode / curl** (OpenAI-format, không convert Anthropic).
Chưa dùng thẳng trong Claude Code (cần convert Anthropic→OpenAI nếu muốn).

### Lưu ý & rủi ro
- Đây là **free-tier OpenRouter**: rate-limit theo key/IP, và OpenRouter có thể **gỡ model free** bất cứ lúc nào → phù hợp gói free/re-lẻ, không bền cho scale lớn.
- Key OpenRouter nằm **server-side** trong DB (credential `api_key` của account 9); response admin
  che `api_key`. **Không commit key gốc vào git**. File temp chứa secret đã xoá.
- Đây là cấu hình **DB trên instance local** → muốn lên production phải **replay** các bước
  (tạo account/type apikey + channel + api key) trên backend chính thức.
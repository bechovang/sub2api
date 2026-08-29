# Thiết lập & vận hành Antigravity OAuth (bán token)

Tài liệu này ghi lại **runbook vận hành** đã được kiểm chứng thực tế để đưa tài khoản
Google **Antigravity** (nền tảng của Google, kế thừa Gemini CLI / Gemini Code Assist)
vào sub2api, phục vụ bán/re-sell token cho người dùng cuối qua gateway.

> Mục đích: hướng dẫn từ lúc OAuth → tạo account → gắn group → kiểm thử completion →
> xác minh billing, cùng các bẫy (pitfall) đã đụng phải và cách xử lý.

---

## 1. Bối cảnh / vì sao đi hướng Antigravity

- Google công bố (khoảng 05/2026): **Gemini CLI & Gemini Code Assist chuyển sang
  Antigravity**; các request consumer (cá nhân + Google AI Pro/Ultra) không còn được phục
  vụ trên nhánh cũ sau **18/06/2026**.
- Do đó các account `gemini` kiểu cũ (`cloudcode-pa`/`generativelanguage`) trả
  `403 SUBSCRIPTION_REQUIRED` hoặc `429 RESOURCE_EXHAUSTED` → **không bán được**.
- Điểm đầu tư mới của Google là **Antigravity**; sub2api đã hỗ trợ sẵn:
  `platform=antigravity`, OAuth client nhúng sẵn trong binary, gateway + pool/failover.

---

## 2. Kiến trúc upstream & 2 endpoint quan trọng

Nguồn: `backend/internal/pkg/antigravity/oauth.go`, `client.go`,
`backend/internal/service/antigravity_gateway_retry.go`.

| Endpoint | URL |
|---|---|
| **Prod** | `https://cloudcode-pa.googleapis.com` |
| **Daily (sandbox)** | `https://daily-cloudcode-pa.sandbox.googleapis.com` |

Điểm mấu chốt đã kiểm chứng:

- **Prod** yêu cầu subscription **agent-mode trả phí** (Google AI Pro/Ultra thực sự kích
  hoạt cho product Antigravity) hoặc license enterprise. Với account consumer:
  - chưa xác minh → `403 VALIDATION_REQUIRED` ("Verify your account to continue")
  - đã xác minh nhưng không đủ quyền → `429 RESOURCE_EXHAUSTED` mọi model.
- **Daily** (`daily-cloudcode-pa.sandbox.googleapis.com`) là nhánh nội bộ/một-ngày, là nơi
  account consumer **free-tier / chưa active AI Pro** vẫn tạo được completion (`200`).

> ⚠️ **Vì sao gateway mặc định dùng prod?** Trong
> `resolveAntigravityForwardBaseURL()` (`antigravity_gateway_retry.go`), nếu không chỉ định
> thì trả `BaseURLs[0]` = **prod**. Chỉ khi env
> `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily` (hoặc `sandbox`) mới trả `BaseURLs[1]` =
> daily. Có cảnh báo lịch sử rằng daily từng khiến token sản xuất bị `401` — đó là với
> token doanh nghiệp; với account consumer miễn phí thì **daily là con đường chạy được**.

---

## 3. Bước 1 — Vòng OAuth (authorization code + PKCE)

Các endpoint admin (header `x-api-key: <ADMIN_KEY>`, base `http://127.0.0.1:8080`):

```
POST /api/v1/admin/antigravity/oauth/auth-url          # body {} → auth_url/session_id/state
POST /api/v1/admin/antigravity/oauth/exchange-code     # body {session_id,state,code,proxy_id}
```

Luồng thực hiện:

1. Gọi `auth-url` → nhận `auth_url`, `session_id`, `state`.
2. Mở `auth_url` trong trình duyệt, **đăng nhập đúng account Google muốn bán**:
   - nếu trình duyệt đang có session account khác → bấm **Switch account**.
   - nếu account cần kích hoạt → đã có tài liệu trong mục §4.
3. Sau consent, trình duyệt redirect về `http://localhost:8085/callback?state=...&code=4/0...`
   (không có listener thật — mã nằm trên thanh địa chỉ, đó là **bình thường**).
4. Copy nguyên callback URL **hoặc** phần `code=4/0...`, gọi `exchange-code` kèm đúng
   `session_id` + `state` → trả `access_token`, `refresh_token`, `project_id`, `email`.

> ⚠️ **Bẫy PKCE:** `code` **chỉ dùng một lần (single-use)**, session bị xoá ngay sau
> exchange. Nếu làm mất token/response thì phải consent lại từ đầu (`auth-url` mới →
> mở lại → dán code mới). **Luôn lưu toàn bộ response exchange vào file trước khi xử lý.**

### Scope được yêu cầu
```
https://www.googleapis.com/auth/cloud-platform
https://www.googleapis.com/auth/userinfo.email
https://www.googleapis.com/auth/userinfo.profile
https://www.googleapis.com/auth/cclog
https://www.googleapis.com/auth/experimentsandconfigs
```
(phiên bản Antigravity **không** yêu cầu scope `aicode` như Gemini cũ.)

### Client/Redirect
- `ClientID`: `1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com`
  (nhúng sẵn trong binary).
- `ClientSecret`: nhúng sẵn; có thể ghi đè env `ANTIGRAVITY_OAUTH_CLIENT_SECRET`.
- `RedirectURI`: `http://localhost:8085/callback`.

---

## 4. Bước 2 — Bẫy quyền (đã kiểm chứng thực tế)

Đây là **nguyên nhân số 1** khiến account Antigravity không bán được dù OAuth đã thành công.

Dùng hàm `POST /v1internal:loadCodeAssist` với `access_token` (cùng kiểu 3 headers:
`Authorization: Bearer`, `Content-Type: application/json`,
`User-Agent: antigravity/1.23.2`) để đọc trạng thái:

```json
{ "currentTier": { "id": "free-tier" }, "paidTier": { "id": "g1-pro-tier" }, ... }
```

- `currentTier: free-tier` → generation agent-mode **không có quota** trên prod → `429`.
- `currentTier: standard-tier` + `paidTier: g1-pro-tier` → có quyền cao hơn, nhưng vẫn cần
  xác minh account mới thực sự tạo được.
- `paidTier` chỉ nghĩa là account **đủ điều kiện** AI Pro, **không** đồng nghĩa quyền đang
  hoạt động — xem dòng upgrade do Google trả.

### 4.1 Xác minh account (khi gặp `403 VALIDATION_REQUIRED`)
Khi generation trả `403 ... "Verify your account to continue"` kèm `validation_url`
(thuộc `error.details[].metadata.validation_url`) thì tài khoản cần xác minh lần đầu:

1. Mở `validation_url` (luồng `developers.google.com/gemini-code-assist/auth/auth_success_gemini`)
   **đăng nhập đúng account đang dùng** → bấm tiếp tục.
2. Trang xác nhận: "Gemini Code Assist ... Gemini CLI ... **Antigravity** ... now authorized".
3. Retest generation → nếu account thực sự có quyền thì ra `200`.

### 4.2 Bảng chẩn đoán lỗi generation (`streamGenerateContent?alt=sse`)

| Tình huống | Prod `cloudcode-pa` | Daily `.sandbox` | Kết luận |
|---|---|---|---|
| Free/không đủ quyền | `429 RESOURCE_EXHAUSTED` | `400 INVALID_ARGUMENT` * | Chưa đủ quyền |
| Chưa xác minh account | `403 VALIDATION_REQUIRED` | — | Cần §4.1 |
| Body thiếu `systemInstruction` identity patch | thường `429`/`400` | `400` | **Phải thêm identity patch** |
| Có quyền + đúng format | `200` | `200` | ✅ Hoạt động |

\* Trên daily, body **bắt buộc** kèm `systemInstruction` (identity patch) — như `buildGeminiTestRequest()`
trong `antigravity_gateway_service.go`. Nhận kết quả `400` trên daily nghĩa là body thiếu, không
phải hết quyền.

---

## 5. Bước 3 — Tạo / cập nhật account trên sub2api

### Cấu trúc credentials (đọc bởi `antigravity_token_provider.go`)
```json
{
  "access_token":   "<ya29...>",
  "refresh_token":  "<1//...>",
  "project_id":     "<project-onboard>",
  "token_type":     "Bearer",
  "expires_at":     "<unix>",
  "scope":          "<danh sách scope đã consent>"
}
```

### API
- Tạo: `POST /api/v1/admin/accounts` với `{name, platform:"antigravity", type:"oauth",
  credentials:{...}, group_ids:[...], concurrency}`.
- Cập nhật (đổi token sang account khác cho account đang có): `PUT /api/v1/admin/accounts/:id`
  với `{name, type:"oauth", credentials:{...}}`.
- `extra.privacy_mode` được auto-set thành `privacy_set` khi onboard.

### Onboard project
Trong `antigravity_oauth_service.go`, `tryOnboardProjectID` gọi `onboardUser` với
`resolveDefaultTierID` (tier `isDefault` đọc từ `allowedTiers`). Nếu `project_id` rỗng hãy chờ
`loadCodeAssist` trả `cloudaicompanionProject` hoặc gọi `onboardUser` với `tierId=standard-tier`.

---

## 6. Bước 4 — Gắn group & cấu hình dispatch

- Liên kết account → group: bảng `account_groups` hoặc admin `group_ids`.
- Với group muốn bán qua `/v1/messages` (Anthropic-style) cần bật:
  `allow_messages_dispatch = true`, `model_routing_enabled = true` (hoặc đặt
  `default_mapped_model`).
- `rate_multiplier` phải `> 0` khi tạo group (nếu thiếu sẽ trả `500`).

---

## 7. Bước 5 — Kiểm thử & xác minh billing

### Test account qua gateway
```
POST /api/v1/admin/accounts/:id/test   # body {"model_id":"gemini-2.5-flash"}
```
Thành công khi nhận sự kiện `test_complete` + `success:true`.

> ⚠️ **Quan trọng:** test này chạy qua gateway thật; nếu gateway đang forward **prod** mà account
> không đủ quyền thì sẽ ra `429` dù raw-call **daily** là `200`. Khi đã xác nhận daily chạy được,
> phải bật env (mục §8) rồi mới test lại.
>
> ⚠️ **Model tồn tại khác nhau giữa prod/daily (phát hiện 2026-08-29):** một số model
> chạy trên **prod** nhưng không có trên **daily** → trả `404 NOT_FOUND` ("Requested entity
> was not found"). Đã gặp: `claude-sonnet-4-5`, `gemini-3.6-flash` (404 trên daily) trong
> khi `gemini-2.5-flash` → `200`. **Không lấy kết quả `404` trên một model để kết luận acc
> chết**; thử `gemini-2.5-flash` trước. Đồng thời kiểm tra model mà group/account đang bán
> có tồn tại trên daily không — nếu không, map sang model khả dụng (`gemini-3.6-flash →
> gemini-2.5-flash`) nếu không muốn client fail 404.**

### Gọi thực tế qua gateway `/v1/messages`
Phải có: user **có balance**, api_key thuộc **group Antigravity** (group_id = id group), model
nằm trong nhóm hỗ trợ (vd `gemini-2.5-flash`, `gemini-3-flash`).

```
POST /v1/messages
Authorization: Bearer <user_key>
{ "model":"gemini-2.5-flash", "max_tokens":50, "messages":[...] }
```

### Xác minh billing
- `users.balance` của user giảm đúng chi phí.
- Bảng `usage_logs` được ghi bản ghi mới:
  `account_id` = id account Antigravity, `group_id` = id group,
  `model`, `input_tokens`/`output_tokens`, `total_cost` = `actual_cost`, `billing_type`,
  `billing_mode=token`, `upstream_endpoint=/v1/messages`.
- Bảng `billing_usage_entries` chỉ ghi dòng khi có `billing_type` (vd subscription); request
  token-charged thường thấy ở `usage_logs` + trừ `users.balance`.

---

## 8. ⚠️ Biến môi trường bắt buộc khi vận hành

```
GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily
```

- Đặt **tại lúc khởi động backend** (env, không phải config.yaml — được đọc bằng `os.Getenv`).
- **Mất biến này sau khi restart backend** → gateway quay về **prod** → account consumer quay lại
  `429` "Resource has been exhausted". **Phải ghi vào script/`.env` khởi động để auto-matic.**
- Chỉ ảnh hưởng nhánh forward của Antigravity; các platform khác (openai/gemini/...) không đổi.
- Vòng retry chỉ dùng **đúng 1 URL** đã resolve (`availableURLs := []string{baseURL}` trong
  `antigravity_gateway_retry.go`) — **không tự fallback** prod → daily khi gặp 429. Admin
  "Test connection" (platform `antigravity`) đi qua cùng retry loop nên **cũng respect env này**.
- **Dấu hiệu nhận biết regression này** (đã tái diễn 2026-08-24 §2.4 và 2026-08-27, xem
  [`SESSION_LOG_2026-08-27.md`](SESSION_LOG_2026-08-27.md)):
  - **Tất cả** account Antigravity cùng lúc `429 RESOURCE_EXHAUSTED` **trống thông tin**
    (không `RetryInfo`/`quotaId`), kể cả request đầu tiên trong ngày;
  - Token OAuth vẫn refresh bình thường (Google không thu hồi gì);
  - Log forward ghi `base_url=https://cloudcode-pa.googleapis.com` (prod).
  → Kiểm tra env của process trước khi nghi account/quota.

> ⚠️ **Watchdog/process-manager bên ngoài (phát hiện 2026-08-29, tái diễn lần 3):** có
> thể tồn tại một phiên agent/terminal khác tự restart `server.exe` khi nó tắt — và
> restart đó **không kèm env daily** (chỉ set `DATA_DIR`). Triệu chứng: kill server để
> restart kèm env, vài phút sau port 8080 lại bị chiếm bởi PID khác, log lại ghi prod.
> **Trước khi restart: check `netstat -ano | findstr :8080` + process tree** xem ai đang
> spawn server, cắt nhánh watchdog đó (giữ nguyên các process khác của nó), rồi mới tự
> start kèm env. Chi tiết: [`SESSION_LOG_2026-08-29.md`](SESSION_LOG_2026-08-29.md) §2.2.
>
> ⚠️ **Bẫy BOM Windows:** file `.env` tạo trên Windows thường có BOM (`EF BB BF`) ở dòng
> đầu; `source` trực tiếp sẽ lỗi `$'\357\273\277KEY=...': command not found` và **biến
> đầu tiên không được set**. Lọc trước khi source:
> `source <(sed -e '1s/^\xEF\xBB\xBF//' -e 's/\r$//' local-dev.env)`.

---

## 9. Vòng đời tài khoản & vệ sinh

- Account `gemini` consumer cũ (không generation) nên **xoá** (không giữ): `DELETE
  /api/v1/admin/accounts/:id`.
- Sau khi xoá account, group tương ứng có thể rỗng → xoá group nếu không còn account.
- Không commit bất kỳ token/secret vào git. Token OAuth/refresh lưu ở DB trong
  `accounts.credentials` (jsonb), không nằm trong tệp cấu hình.
- Khi xoá thử nghiệm, nhớ dọn: balance user thử, api_key test, nhóm test.

---

## 10. Checklist triển khai nhanh (cho account mới)

1. `auth-url` → mở link → consent đúng account → dán callback `code`.
2. `exchange-code` (lưu nguyên response).
3. `loadCodeAssist`: đọc `currentTier`/`paidTier`; nếu nghi vấn → `streamGenerateContent`
   trên **daily** để xác nhận `200`.
4. Gặp `403 VALIDATION_REQUIRED` → xác minh account qua `validation_url`.
5. Tạo/update account (`platform=antigravity`, `type=oauth`, credentials đủ token+project).
6. Gắn group + bật `allow_messages_dispatch`/`model_routing_enabled`.
7. Bật `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily` + restart backend.
8. Test account qua gateway → `/v1/messages` → đối chiếu `usage_logs` + balance.
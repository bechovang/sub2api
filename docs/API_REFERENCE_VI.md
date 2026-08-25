# Sub2API — API Reference (bản thực tế đang chạy)

> Tài liệu API cho instance `127.0.0.1:8080` (reselling gateway).
> Gồm 2 khu vực: **Public Gateway** (khách dùng) và **Admin API** (bạn vận hành).
> Header chung & body đều `application/json`; xem thống kê trạng thái hiện tại ở `SESSION_LOG_2026-08-24.md` §9.

---

## A. Public Gateway — client / khách hàng

Client compatible **OpenAI / Anthropic** trỏ tới base URL của server. Khách chỉ cần 1 API key và biết group/model họ được mua.

- **Base URL (openai-style / anthropic-style)**: `http://<host>:8080`
- **Auth header**:
  ```
  Authorization: Bearer sk-<API_KEY>
  ```
- Khách muốn biết quyền -> `GET /v1/models`.

### A1. Hook vào model được phép (list models)
```
GET /v1/models
Authorization: Bearer sk-...
```
Trả danh sách model trong group/catalog của key đó.

### A2. Chat completions (chuẩn OpenAI)
```
POST /v1/chat/completions
Authorization: Bearer sk-...
Content-Type: application/json
```
Body:
```json
{
  "model": "qwen3.7-plus",
  "messages": [{"role": "user", "content": "Hello"}],
  "max_tokens": 512,
  "stream": false
}
```
Response (non-stream):
```json
{
  "id": "chatcmpl-...",
  "object": "chat.completion",
  "model": "qwen3.7-plus",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "...", "reasoning_content": "..."},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 20, "completion_tokens": 30, "total_tokens": 50}
}
```
- `stream: true` → SSE `data: {...}` + `data: [DONE]`.
- Model ngoài catalog bị chặn → **`503`**.

### A3. Anthropic Messages (dùng Platform `anthropic` / Claude Code)
```
POST /v1/messages
Authorization: Bearer sk-...
```
Body chuẩn Anthropic:
```json
{
  "model": "glm-5.3",
  "max_tokens": 4096,
  "messages": [{"role": "user", "content": "Hi"}],
  "stream": false
}
```
Response dạng Anthropic `{content:[{type:"text",text:"..."}], usage:{input_tokens,output_tokens}}`.
(Claude Code gọi thêm `GET /v1/messages/count_tokens` để đếm token.)

### A4. Tra cứu billing / quota
```
GET /sub2api/billing
Authorization: Bearer sk-...
```
Trả `quota_total`, `quota_used`, còn dư của user theo key.

---

## B. Admin API — bạn vận hành

Tất cả dưới `http://<host>:8080/api/v1/admin/...`.

**Auth header**: `x-api-key: <ADMIN_API_KEY>` (lấy từ `backend/data/local-dev.env` → `ADMIN_API_KEY`).
> Admin KHÔNG dùng `Authorization: Bearer`. Không dùng chung với `x-api-key` public.

Error trả về: HTTP `400` (bad request), `500` (fail), `503` (bị chặn/không có nguồn). Payload create trả `{ "data": { ... } }`.

### B1. Groups (kệ hàng)
| Method | Path | Ghi chú |
|---|---|---|
| GET | `/groups` | list mọi group |
| POST | `/groups` | tạo group |
| GET | `/groups/:id` | chi tiết 1 group |
| PUT | `/groups/:id` | update group |
| DELETE | `/groups/:id` | xoá group |
| GET | `/groups/:id/api-keys` | key đang trong group |

Body tạo group:
```json
{
  "name": "Qwen Token Plan",
  "platform": "openai",
  "rate_multiplier": 1.0
}
```
⚠️ `rate_multiplier` **bắt buộc > 0** (để 0 → `500 must be > 0`).
`platform` hợp lệ: `anthropic openai gemini antigravity grok kimi zhipu deepseek composite`.

### B2. Accounts (nguồn cung/upstream)
| Method | Path | Ghi chú |
|---|---|---|
| GET | `/accounts` | list upstream |
| POST | `/accounts` | tạo account |
| GET | `/accounts/:id` | chi tiết |
| PUT | `/accounts/:id` | cập nhật credentials/chính sách |
| DELETE | `/accounts/:id` | xoá |
| POST | `/accounts/:id/test` | test kết nối |
| GET | `/accounts/:id/models` | lấy model upstream expose |

Body tạo account apikey:
```json
{
  "name": "Qwen Token Plan",
  "platform": "openai",
  "type": "apikey",
  "credentials": {
    "api_key": "<UPSTREAM_KEY>",
    "base_url": "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"
  },
  "group_ids": [5]
}
```
- `type`: `apikey` (key+base_url) hoặc `oauth` (Antigravity/Gemini/Claude... qua browser flow).
- `base_url` override cho upstream không chuẩn (Qwen token-plan, GLM Z.ai,
  cc_bridge, OpenRouter...). Với bridge chỉ dùng **placeholder key** — key thật để trong env bridge.
- Account đặt `allow_messages_dispatch: true` nếu muốn cho bán (nhất là OAuth).

### B3. Channels (bảng giá + giới hạn model)
| Method | Path | Ghi chú |
|---|---|---|
| GET | `/channels` | list các bảng giá |
| POST | `/channels` | tạo channel |
| GET | `/channels/:id` | chi tiết (gồm `model_pricing`) |
| PUT | `/channels/:id` | cập nhật bảng giá |
| DELETE | `/channels/:id` | xoá |
| GET | `/channels/model-pricing` | giá mặc định mẫu |

Body tạo/cập nhật channel:
```json
{
  "name": "Qwen Token Plan Pricing",
  "billing_model_source": "channel_mapped",
  "restrict_models": true,
  "group_ids": [5],
  "model_pricing": [
    {
      "platform": "openai",
      "models": ["qwen3.7-plus"],
      "billing_mode": "token",
      "input_price": 0.0000005,
      "output_price": 0.000002
    }
  ]
}
```
- **Giá = USD/token** (không phải /1M): `input_price` cho $/M ÷ 1e6
  (vd `$0.5/M` → `0.0000005`).
- `restrict_models: true` + `channel_mapped` ⇒ chỉ bán ĐÚNG model trong `model_pricing`
  ở group đó, giá theo chính bảng này. Model ngoài → `503`.

> Khách chọn account/channel tự động qua scheduler của group; bạn **không cần** gán
> model vào account — chỉ cần group+channel đúng, account thuộc group đó.

### B4. User & API keys (khách)
| Method | Path | Ghi chú |
|---|---|---|
| GET | `/users` | list user |
| GET | `/users/:id/api-keys` | key của user |
| POST | `/users/:id/api-keys` | cấp key cho user |
| PUT | `/users/:id/api-keys/:keyId` | sửa key (đổi group/quota/trạng thái) |
| DELETE | `/users/:id/api-keys/:keyId` | thu hồi key |
| GET | `/api-keys` (admin) | quản lý chung; `PUT /api-keys/:id` đổi group |
| PUT | `/api-keys/:id` | chuyển group cho key đó |
| GET | `/admin` … | các group admin con khác (dashboard, payments, redeems, etc.) |

Body cấp key khách:
```json
{
  "name": "khach1 test",
  "group_id": 5,
  "quota": 0.5
}
```
→ `{ "data": { "id": 19, "key": "sk-...", "quota": 0.5, "quota_used": 0 } }`.
`quota` = mức USD tối đa (dựa theo giá token do bạn đặt ở channel).

### B5. Billing / usage
- Khách tra quyền: `GET /sub2api/billing` (public).
- Admin xem chi tiết từng key/log: nhánh `/api-keys`, `/users/:id/api-keys`, và `/dashboard` / `/usage`.
- Mỗi lần gọi gateway đều ghi 1 dòng `UsageLog` theo giá `input_price`/`output_price` × token → trừ `quota_used`.

---

## C. Mã lỗi thường gặp (public)

| HTTP | Ý nghĩa |
|---|---|
| 200 | OK (hoặc SSE stream) |
| 401 | `Authorization` thiếu/sai key |
| 402/403 | hết hạn / key bị vô hiệu |
| 429 | vượt quota/RPM |
| 500 | upstream fail (hết sub, key hỏng) — gateway có thể failover sang account khác |
| 503 | model/group không được phép (restrict) hoặc không có nguồn khả dụng |

---

## D. Ghi chú vận hành riêng instance của bạn

- **Admin key**: `backend/data/local-dev.env` (`ADMIN_API_KEY`).
- **Gateway** chạy `backend/server.exe`, đọc `backend/data/config.yaml`; env quan trọng:
  `DATA_DIR=backend/data`, `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily`.
- **Bridge Command Code** chạy riêng `cc_bridge/cc-bridge.exe` (port `:8788`),
  key thật `COMMANDCODE_API_KEY` trong env bridge; sub2api account 12 trỏ
  `http://127.0.0.1:8788/v1`.
- Cấu hình tham chiếu không-leak: `deploy/config.example.yaml`.
- Bảo mật: không bao giờ commit key thật / secret. Các admin route export chi tiết
  upstream yêu cầu step-up 2FA (xem chú thích trong `admin.go`).
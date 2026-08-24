# Hướng dẫn thêm nguồn cung token (Upstream) vào sub2api

> Tổng hợp quy trình thực tế đã chạy trên instance `127.0.0.1:8080`.
> Bản ghi trạng thái hiện tại (group/account/channel/giá) xem `SESSION_LOG_2026-08-24.md` §7–§9.

---

## 0. Khái niệm & chuẩn bị chung

Mỗi nguồn cung = 3 thứ trong sub2api:
1. **Group** — 1 phòng bán riêng biệt, khai báo `platform`.
2. **Account** — upstream thật (web/creds), gắn vào group.
3. **Channel** — bảng giá (`channel_mapped`) + `restrict_models` giới hạn model.

Sau đó **tạo API key** cho 1 user khách để test bán.

### Admin auth
Admin API key nằm trong `backend/data/local-dev.env` (`ADMIN_API_KEY=...`).
Gọi admin API với header **`x-api-key: <ADMIN_API_KEY>`** (không dùng `Authorization: Bearer` cho admin).
```bash
AK="admin-..."                    # từ backend/data/local-dev.env
H="x-api-key: $AK"
B="http://127.0.0.1:8080"         # gateway base
```

### Endpoint
```
POST /api/v1/admin/groups                      # tạo group
POST /api/v1/admin/accounts                    # tạo account
POST /api/v1/admin/channels                    # tạo channel
POST /api/v1/admin/users/:id/api-keys          # tạo key cho user
```

> ⚠️ Group **`rate_multiplier` phải > 0** (bỏ trống → 500 `rate_multiplier must be > 0`).
> Values platform hợp lệ: `anthropic openai gemini antigravity grok kimi zhipu deepseek composite`.

### Lưu ý bảo mật
- **Không commit key thật / secret.** Key thật nên để trong env (`backend/data/local-dev.env`) hoặc file riêng đã gitignore.
- Account `apikey` cho phép `base_url` override → dùng khi upstream không chuẩn hoặc qua proxy/bridge.

---

## 1. OpenRouter (model free)

### Chuẩn bị
- Key API OpenRouter. Model `:free` có giá upstream **$0** → margin ~100%.

### Tạo
```bash
# Group (platform openai)
curl -s -X POST $B/api/v1/admin/groups -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"Token Le","platform":"openai","rate_multiplier":1}'

# Account (openai/apikey, base_url override)
curl -s -X POST $B/api/v1/admin/accounts -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"OpenRouter Free","platform":"openai","type":"apikey",
  "credentials":{"api_key":"sk-or-v1-REPLACE","base_url":"https://openrouter.ai/api/v1"},
  "group_ids":[2]}'

# Channel (channel_mapped + restrict)
curl -s -X POST $B/api/v1/admin/channels -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"OpenRouter Free Pricing","billing_model_source":"channel_mapped","restrict_models":true,
  "group_ids":[2],
  "model_pricing":[{"platform":"openai","models":["stealth/ox-alpha","nvidia/nemotron-3-ultra-550b-a55b:free","nvidia/nemotron-3.5-lightning:free"],"billing_mode":"token","input_price":2e-7,"output_price":2e-7}]}'
```

### Verify
- `POST /v1/chat/completions` với key khách → model free trả 200, `usage` real.
- Restrict chặn model ngoài catalog → `503`.

---

## 2. Antigravity (OAuth — Pro sub, có Claude)

### Chuẩn bị
- 1 tài khoản Google **đã mua Antigravity Pro** (vào `antigravity.ai`).
- Antigravity dùng OAuth (không phải apikey); được ủy quyền qua browser + lưu refresh token trong `backend/data/antigravity-tokens.json`.

### Tạo
Cuộn OAuth đầy đủ xem `docs/ANTIGRAVITY_SETUP_VI.md`. Tóm tắt các bước admin:
```bash
# Group (platform antigravity)
curl -s -X POST $B/api/v1/admin/groups -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"Antigravity","platform":"antigravity","rate_multiplier":1}'

# Account (type oauth)
curl -s -X POST $B/api/v1/admin/accounts -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"<google-email> (Antigravity)","platform":"antigravity","type":"oauth",
  "group_ids":[4]}'
```
- Sau tạo, hoàn tất browser OAuth để nạp refresh token; login phải có scope
  `cloud-platform`, `cclog`, `gmail` (check `extra.privacy_mode`, `plan_type`).
- Account `allow_messages_dispatch: true` để bán được.

### Verify
- Call model Claude qua group → 200.
- Check `credentials.plan_type == "Pro"` (Claude yêu cầu Pro+). Hết sub → OAuth refresh fail, account pausne.

---

## 3. Qwen Token Plan (DashScope)

### Chuẩn bị
- Key **token-plan** DashScope (khác key openai thường). Trong instance này lưu ở env
  `$BAILIAN_TOKEN_PLAN_API_KEY` (114 ký tự; key cũ trong `~/.pi/config.json` bị 401).
- Base: `https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1`

### Tạo
```bash
curl -s -X POST $B/api/v1/admin/groups -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"Qwen Token Plan","platform":"openai","rate_multiplier":1}'

curl -s -X POST $B/api/v1/admin/accounts -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"Qwen Token Plan","platform":"openai","type":"apikey",
  "credentials":{"api_key":"'$BAILIAN_TOKEN_PLAN_API_KEY'",
                 "base_url":"https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"},
  "group_ids":[5]}'

# Channel: 7 model — qwen3.6-flash/deepseek-v4-flash/glm-5.2 $0.5/2, qwen3.7-max/deepseek-v4-pro $0.6/2.4, qwen3.8-max $0.8/3.2
```
- `rate_multiplier: 1` là bắt buộc (nếu để 0 → 500).

### Verify
- qwen3.7-plus/qwen3.8-max/glm-5.2/deepseek-v4-pro → 200 kèm `reasoning_content`.
- Restrict chặn `gpt-4o`, `qwen2.5-coder-32b-instruct` → 503.
- Billing: `quota_used` tăng sau call (token-plan tính theo token).

---

## 4. GLM Z.ai (Anthropic endpoint)

### Chuẩn bị
- Key **Anthropic-compat** của Z.ai (TRONG `~/.claude/settings.json`, key `0c87710c…`).
  ⚠️ Key OpenAI-compat (`/paas/v4`) trả **429 `1113` Insufficient balance** — dùng khác tier.
- Base: `https://api.z.ai/api/anthropic` → sub2api gọi `{base}/v1/messages`.
- Model: `glm-4.5-air`, `glm-4.6`, `glm-4.7` (đổi tên 4.5-air/5.3 → serve 4.7/5.3 trên 1 số key).

### Tạo (platform **anthropic**, KHÔNG phải openai)
```bash
curl -s -X POST $B/api/v1/admin/groups -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"GLM Z.ai","platform":"anthropic","rate_multiplier":1}'

curl -s -X POST $B/api/v1/admin/accounts -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"GLM Z.ai","platform":"anthropic","type":"apikey",
  "credentials":{"api_key":"0c87710c-REPLACE","base_url":"https://api.z.ai/api/anthropic"},
  "group_ids":[6]}'

# Channel: 5 model — glm-4.5-air $0.4/1.2, glm-4.6 $1/3, glm-4.7 $1.2/3.6, glm-5.2/5.3 $1.4/4.4
```
- Vì khác `base_url`, cần `api_base_urls[anthropic]`/base_url override (Z.ai ≠ zhipu CN `open.bigmodel.cn`).

### Verify
- `POST /v1/messages` (Anthropic shape) glm-5.2/glm-4.6 → 200 "OK".
- Restrict `claude-3-5-sonnet` → 503.
- Khách dùng `platform anthropic` sẽ thấy response dạng Anthropic.

---

## 5. Command Code (qua cc_bridge `/alpha/generate`) — Go plan

### Tổng quan
Provider API `/provider/v1` Go-plan → `403 upgrade_required`. Nhưng CLI protocol
**`POST /alpha/generate`** chạy được → mình mở **bridge** `cc_bridge/` (Go) đứng giữa.

```
end-user ─► sub2api ─► cc_bridge (:8788) ─► /alpha/generate (Command Code)
```

sub2api chỉ thấy bridge như 1 upstream OpenAI. **Key thật ở env bridge, không vào DB.**

### Chuẩn bị
- Chạy bridge (xem `cc_bridge/README.md`):
```bash
cd cc_bridge
go build -o cc-bridge.exe .
export COMMANDCODE_API_KEY='user_...'   # Go/Provider key
export CC_BRIDGE_ADDR=':8788'
./cc-bridge.exe > cc-bridge.log 2>&1 &
```
- (Tuỳ chọn) `python map_catalog.py` để biết model nào key bán được (`OK`/`FAIL`).

### Tạo (account platform **openai**, trỏ bridge)
```bash
curl -s -X POST $B/api/v1/admin/groups -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"Command Code OSS","platform":"openai","rate_multiplier":1}'

curl -s -X POST $B/api/v1/admin/accounts -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"Command Code OSS (bridge)","platform":"openai","type":"apikey",
  "credentials":{"api_key":"sk-cc-bridge","base_url":"http://127.0.0.1:8788/v1"},
  "group_ids":[7]}'

# Channel: 12 model (giá bán xem SESSION_LOG §8/§9)
```
- `api_key` là **placeholder** (`sk-cc-bridge`); key thật nằm trong env bridge.
- Model bán: DeepSeek-Qwen-GLM + Kimi-K3/K2.7, MiniMax-M3, mimo, Step, Tencent,
  Nemotron, Inkling, laguna/ox-alpha.
- **KHÔNG bán được** Claude/GPT/Gemini/Grok (cần Pro/GOAT/Provider — không bypass).

### Verify
- Model trong channel → 200 (stream + non-stream, tool-call round-trip).
- Restrict model ngoài → 503.
- ⚠️ usage `prompt_tokens` phồng (~7.7k/req do command code chèn system) — cân đối giá.

---

## 6. Tạo API key khách hàng + test bán

Sau khi có group, tạo key cho user khách (vd user_id=2 `khach1`):
```bash
curl -s -X POST $B/api/v1/admin/users/2/api-keys -H "$H" -H 'Content-Type: application/json' -d '{
  "name":"khach1 test","group_id":5,"quota":0.5}'
# → trả về {data:{id, key:"sk-...", quota_used:0}}
```
Test:
```bash
curl $B/v1/chat/completions -H "Authorization: Bearer sk-..." -H 'Content-Type: application/json' \
  -d '{"model":"<model-in-channel>","messages":[{"role":"user","content":"hi"}],"max_tokens":20}'
```
Kiểm tra billing: `quota_used` tăng → lệ phí tính theo `input_price`/`output_price` × tokens.

---

## 7. Checklist chung

- [ ] Group `rate_multiplier > 0`, đúng `platform`.
- [ ] Account đúng `type` (apikey vs oauth), `base_url` override đúng (APIs không zhipu/openai-cn cần file).
- [ ] Channel `channel_mapped` + `restrict_models: true`, giá **USD/token** (giá/$M ÷ 1e6).
- [ ] Key thật không commit — để env/file gitignore.
- [ ] Verify 200 + restrict 503 + `quota_used` đổi.
- [ ] Model premium cần plan đúng (vd Claude→Antigravity Pro, GLM→Z.ai Anthropic tier, CC→Provider/Go tùy model) — không "hack" qua được.
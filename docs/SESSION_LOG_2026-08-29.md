# Nhật ký phiên làm việc — 2026-08-29

> Bản ghi phiên vận hành `sub2api`. Đọc kèm
> [`ANTIGRAVITY_SETUP_VI.md`](ANTIGRAVITY_SETUP_VI.md) (runbook OAuth/gateway) và
> [`SESSION_LOG_2026-08-27.md`](SESSION_LOG_2026-08-27.md) (phiên trước, cùng lỗi §2.4).

## Mục tiêu phiên

1. Test toàn bộ account Antigravity xem còn hoạt động.
2. Chẩn đoán nếu fail; khôi phục bằng restart kèm env `daily`.
3. Kiểm tra tổng thể các nguồn cung API của dự án.

---

## 1. Kết quả test khi bắt đầu (6 acc Antigravity)

Test bằng `POST /api/v1/admin/accounts/:id/test` (model mặc định `claude-sonnet-4-5`):

| ID | Account | Test (server hiện tại) | Kết luận |
|----|---------|------------------------|----------|
| 4 | hanngoziratech | ❌ 429 RESOURCE_EXHAUSTED | Acc sống, sai endpoint forward |
| 5 | phuchcm2006@gmail.com | ❌ 429 RESOURCE_EXHAUSTED | Như trên |
| 6 | bechovang@gmail.com | ❌ 429 RESOURCE_EXHAUSTED | Như trên |
| 14 | phong007m@gmail.com | ❌ 429 RESOURCE_EXHAUSTED | Như trên |
| 13 | fbphuchcm2006@gmail.com | ❌ 401 UNAUTHENTICATED | Chưa verify (§4.1 runbook) |
| 15 | thanhthuymaplearn@gmail.com | ❌ 401 UNAUTHENTICATED | Như trên |

- Token OAuth vẫn refresh đều (`token_refresh.account_refreshed` — log 17:06:41, các acc
  4/5/6/14) → **không phải acc chết**.
- Toàn bộ acc cùng lúc 429 body trống RetryInfo → signature quen thuộc: **mất env daily**.

---

## 2. Chẩn đoán — server restart không kèm env `daily` (tái diễn lần 3)

### 2.1 Bằng chứng
- Server đang chạy (`server.exe`, PID 22836) start **hôm nay 17:06**, command line trần
  `server.exe` — không env `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL`.
- Log forward ghi rõ: `[antigravity-Forward] ... status=429 rate_limited
  base_url=https://cloudcode-pa.googleapis.com` (prod).
- Phiên 08-27 đã chứng minh 4 acc này `success:true` khi restart kèm env daily.

### 2.2 Phát hiện mới — watchdog tự restart server (nguyên nhân tái diễn thực sự)

Sau khi kill server cũ để restart kèm env, port 8080 bị chiếm lại trong **vài phút**:
truy vết process tree thấy một **phiên agent khác** (`agy.exe`, PID 16092, chạy từ 16:58
cùng ngày) đang quản lý:

```
agy.exe (PID 16092, phiên agent/terminal khác)
├── powershell → "php artisan serve --port=8011"        (shop Laravel — không đụng)
└── powershell → "$env:DATA_DIR=...; .\server.exe"      (watchdog server sub2api — thiếu env daily)
```

- Mỗi lần server tắt, watchdog này spawn lại `server.exe` với **chỉ `DATA_DIR`**,
  **không có** `GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily` → gateway quay về prod →
  429 hàng loạt trở lại sau mỗi restart.
- Đây giải thích tại sao env "mất" lặp đi lặp lại dù runbook đã cảnh báo: không chỉ
  restart tay quên env, mà còn có **process manager ngoài** tự khởi động lại server.

### 2.3 Cách xử lý
1. Kill nhánh watchdog của server (powershell spawner + `server.exe`), **giữ nguyên**
   nhánh `php artisan serve` (shop).
2. Start server tự quản (`bash -lc` qua hub) kèm env:
   ```bash
   cd backend
   set -a; source data/local-dev.env; set +a
   export DATA_DIR=$PWD/data
   export GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL=daily
   ./server.exe
   ```
3. Retest từng account.

> ⚠️ **Bẫy Windows:** `local-dev.env` có **BOM** (`EF BB BF`) ở dòng đầu — `source` trực
> tiếp sẽ chạy sai dòng đầu (`$'\357\273\277JWT_SECRET=...': command not found`) khiến
> biến đó không được set. Phải lọc: `source <(sed -e '1s/^\xEF\xBB\xBF//' -e 's/\r$//' file)`.

---

## 3. Kết quả sau restart kèm env daily

| ID | Account | gemini-2.5-flash | claude-sonnet-4-5 | Kết luận |
|----|---------|------------------|-------------------|----------|
| 4 | hanngoziratech | ✅ success | ❌ 404 | **OK** |
| 5 | phuchcm2006@gmail.com | ✅ success | ❌ 404 | **OK** |
| 6 | bechovang@gmail.com | ✅ success | ❌ 404 | **OK** |
| 14 | phong007m@gmail.com | ✅ success | ❌ 404 | **OK** |
| 13 | fbphuchcm2006@gmail.com | ❌ 401 | ❌ 401 | Chờ verify tay |
| 15 | thanhthuymaplearn@gmail.com | ❌ 401 | ❌ 401 | Chờ verify tay |

---

## 4. ⚠️ Phát hiện mới — model không tồn tại trên daily

- `claude-sonnet-4-5`, `gemini-3.6-flash` → **404 NOT_FOUND** ("Requested entity was not
  found") trên daily, dù trước đó (phiên 08-27) `claude-sonnet-4-5` test OK trên **prod**
  sau khi bật env.
- `gemini-2.5-flash` → 200 trên cả 4 acc.
- Log gateway 17:07 hôm nay cho thấy client **vẫn gọi `gemini-3.6-flash` qua group 4**
  → các request đó đang fail 404. **Cần map `gemini-3.6-flash → gemini-2.5-flash`
  (hoặc model khả dụng) trong model whitelist/mapping của group/account trước khi bán.**

---

## 5. Kiểm tra tổng thể nguồn cung API (10 accounts)

| Nguồn | Account | ID | Test 29/08 | Ghi chú |
|-------|---------|----|------------|---------|
| Antigravity OAuth | 4 acc đã verify | 4/5/6/14 | ✅ gemini-2.5-flash | OK |
| Antigravity OAuth | chưa verify | 13/15 | ❌ 401 | Verify tay qua `validation_url` |
| OpenRouter Free | OpenRouter Free (Token Le) | 9 | ✅ success | OK |
| Qwen Token Plan | Qwen Token Plan (bechovang) | 10 | ❌ "Model not found" mọi model | Nghi token-plan hết hạn/quota — cần kiểm tra |
| GLM Z.ai | GLM Z.ai (bechovang) | 11 | ✅ success | OK |
| Command Code OSS bridge | Command Code OSS (bridge) | 12 | ❌ không kết nối được `127.0.0.1:8788` | Bridge process chết, cần start `cc_bridge/cc-bridge.exe` |

---

## 6. Việc còn treo

| Việc | Trạng thái |
|------|-----------|
| Restart server kèm env daily | ✅ Đã làm (PID mới, self-managed qua hub) |
| Test lại 6 acc | ✅ 4/6 `success:true` (gemini-2.5-flash); 13/15 vẫn 401 |
| Map model `gemini-3.6-flash`/`claude-sonnet-4-5` sang model khả dụng trên daily | ⏳ Chưa làm — cần quyết định mapping |
| Verify account 13/15 qua `validation_url` | ⏳ Cần làm tay trên browser (runbook §4.1) |
| Watchdog `agy.exe` — cơ chế restart không kèm env | ⚠️ Đã cắt nhánh server; nếu phiên đó còn cơ chế khác (Task Scheduler/loop) có thể tái diễn. Đề xuất: đưa env vào script khởi động chính thức hoặc tắt watchdog |
| Qwen Token Plan (acc 10) "Model not found" | ⏳ Kiểm tra token/hạn dùng upstream |
| Command Code bridge chết | ⏳ Start `cc_bridge/cc-bridge.exe` nếu cần nguồn này |
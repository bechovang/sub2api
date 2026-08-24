# Command Code /alpha/generate bridge

`cc_bridge` is a small standalone HTTP service that lets **sub2api** resell Command
Code plans (including the entry-level **Go** plan) without upgrading to Provider.

Command Code's Provider API (`/provider/v1/...`) returns `403 upgrade_required`
for Go-plan keys. However the Command Code *CLI* protocol — `POST
/alpha/generate` — works with a Go-plan key and exposes the OSS model catalog
(DeepSeek-V4, Qwen 3.6/3.7/3.8, GLM-5.3, Kimi-K3/K2.7, MiniMax-M3, mimo, Step,
Tencent, Nemotron, Inkling, Meta Muse, and the free laguna/ox-alpha …).

The bridge exposes a standard **OpenAI Chat Completions** endpoint and translates
it to `/alpha/generate`; sub2api just points a normal `openai/apikey` account at it.

## Why a bridge (not a change inside sub2api core)

- Keeps the sub2api fork at upstream parity (no divergent core changes).
- Contains all the reverse-engineered, brittle protocol quirks (custom headers,
  wrapped body, SSE event shapes) in one isolated, testable service.
- The real Command Code key never enters sub2api's database — it lives in the
  bridge's environment only (`COMMANDCODE_API_KEY`).

## Building / running

```bash
# from cc_bridge/
go build -o cc-bridge . || go build -o cc-bridge.exe .

export COMMANDCODE_API_KEY='user_...'   # the Command Code Go/Provider key
export COMMANDCODE_API_BASE="https://api.commandcode.ai"  # default
export CC_BRIDGE_ADDR=":8788"           # default

./cc-bridge.exe > cc-bridge.log 2>&1 &
```

The bridge listens for `POST /v1/chat/completions` (stream + non-stream, text +
tool-calls + reasoning). `GET /healthz` returns `ok`.

## Wiring into sub2api

1. **Account** — `POST /api/v1/admin/accounts`: `platform: openai`, `type:
   apikey`, `credentials: {api_key: "sk-cc-bridge", base_url:
   "http://127.0.0.1:8788/v1"}`, `group_ids: [<group>]`. The `api_key` is a
   placeholder; the real key lives in the bridge env.
2. **Group** — `platform: openai`, `rate_multiplier: 1`.
3. **Channel** — `billing_model_source: channel_mapped`, `restrict_models: true`,
   `group_ids: [<group>]`, with `model_pricing` per model (per-token prices).
4. **API key** — `POST /api/v1/admin/users/:id/api-keys` `{group_id: <group>,
   quota: <usd>}`.

`setup_cmdcode.py` automates this. Run it with:

```bash
export SUB2API_BASE_URL=http://127.0.0.1:8080
export SUB2API_ADMIN_API_KEY=admin-...
python setup_cmdcode.py
```

## Catalog & pricing notes

Run `map_catalog.py` to probe which models this Command Code key can actually
serve via `/alpha/generate` (it prints `OK` / `FAIL <reason>`):

```bash
export COMMANDCODE_API_KEY='user_...'
python map_catalog.py
```

Startup findings (Go plan):
- **Works**: all DeepSeek-V4, Qwen 3.6–3.8, GLM-5.3/5.2/5, Kimi-K3/K2.7, MiniMax-M3,
  mimo, Step, Tencent hy3, Nemotron 3 Ultra, Inkling, laguna-s-2.1-free, ox-alpha.
- **Blocked on Go, need higher plans (cannot bypass)**: Claude models (Pro+),
  Gemini (GOAT), Grok/MiniMax/nemotron-`MODEL_NOT_IN_PLAN`, Fugu (Provider), Meta
  Muse Spark 1.1 (Pro). `claude-sonnet-5` → `403 MODEL_NOT_IN_PLAN`.

⚠️ **Token-billing caveat**: Command Code injects a large system prompt + tool
schemas into every request, so the upstream `prompt_tokens` reported by the
bridge is inflated (~7–8k tokens per request). Because the Go plan is a flat
subscription your *true* cost is the monthly fee, not per-token — set resale
prices accordingly, and be aware end-user `usage.prompt_tokens` will look larger
than the visible conversation.
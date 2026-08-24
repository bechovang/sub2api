import json, urllib.request, os

B = os.environ.get("SUB2API_BASE_URL", "http://127.0.0.1:8080")
AK = os.environ["SUB2API_ADMIN_API_KEY"]

def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(B + path, data=data, method=method,
                                 headers={"x-api-key": AK, "Content-Type": "application/json"})
    try:
        r = urllib.request.urlopen(req)
        return json.load(r)
    except urllib.error.HTTPError as e:
        print("HTTP", e.code, "->", path, e.read().decode()[:400]); raise

def price(m):
    P = {
      "moonshotai/Kimi-K3":(8e-7,3.2e-6),
      "moonshotai/Kimi-K2.7-Code":(8e-7,3.2e-6),
      "MiniMaxAI/MiniMax-M3":(8e-7,3.2e-6),
      "zai-org/GLM-5.3":(6e-7,2.4e-6),
      "xiaomi/mimo-v2.5":(5e-7,2e-6),
      "tencent/hy3-paid":(6e-7,2.4e-6),
      "nvidia/nemotron-3-ultra-550b-a55b":(8e-7,3.2e-6),
      "thinkingmachines/inkling":(8e-7,3.2e-6),
      "poolside/laguna-s-2.1-free":(1e-7,4e-7),
      "stealth/ox-alpha":(1e-7,4e-7),
      "Qwen/Qwen3.8-Max":(6e-7,2.4e-6),
      "deepseek/deepseek-v4-pro":(6e-7,2.4e-6),
    }
    i,o = P[m]
    return {"platform":"openai","models":[m],"billing_mode":"token",
            "input_price":i,"output_price":o,"cache_write_price":None,
            "cache_read_price":None}

def make_pricing():
    return [price(m) for m in ["moonshotai/Kimi-K3","moonshotai/Kimi-K2.7-Code",
      "MiniMaxAI/MiniMax-M3","zai-org/GLM-5.3","xiaomi/mimo-v2.5",
      "tencent/hy3-paid","nvidia/nemotron-3-ultra-550b-a55b",
      "thinkingmachines/inkling","poolside/laguna-s-2.1-free",
      "stealth/ox-alpha","Qwen/Qwen3.8-Max","deepseek/deepseek-v4-pro"]]

# 1) group
g = call("POST","/api/v1/admin/groups",{
  "name":"Command Code OSS","platform":"openai","rate_multiplier":1,
  "description":"Command Code /alpha/generate Go-plan OSS models via cc-bridge"})
gid = g["data"]["id"]
print("GROUP", gid, g["data"]["name"])

# 2) account -> bridge
a = call("POST","/api/v1/admin/accounts",{
  "name":"Command Code OSS (bridge)","platform":"openai","type":"apikey",
  "credentials":{"api_key":"sk-cc-bridge","base_url":"http://127.0.0.1:8788/v1"},
  "group_ids":[gid],"concurrency":1})
aid = a["data"]["id"]
print("ACCOUNT", aid, a["data"]["name"])

# 3) channel
ch = call("POST","/api/v1/admin/channels",{
  "name":"Command Code OSS Pricing","billing_model_source":"channel_mapped",
  "restrict_models":True,"group_ids":[gid],"model_pricing":make_pricing()})
cid = ch["data"]["id"]
print("CHANNEL", cid, ch["data"]["name"])

print("DONE group=%s account=%s channel=%s" % (gid,aid,cid))
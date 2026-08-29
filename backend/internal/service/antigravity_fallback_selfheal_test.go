//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Item 1: prod→daily URL fallback (auto, không cần env tay)
// ---------------------------------------------------------------------------

// TestAntigravityRetryLoop_Prod429_FallsBackToDaily 验证默认 (không env) 时
// prod 返回 URL 级 429（"Resource has been exhausted" 无 RetryInfo）→
// retry loop 自动切换到 daily → 200。
//
// Đây chính là kịch bản SESSION_LOG_2026-08-24/27/29: consumer account bị prod
// chặn 429，daily trả 200 → server restart mất env cũng không còn chết acc.
func TestAntigravityRetryLoop_Prod429_FallsBackToDaily(t *testing.T) {
	prodURL := "https://prod.test"
	dailyURL := "https://daily.test"
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
	}()
	antigravity.BaseURLs = []string{prodURL, dailyURL}

	prod429Body := []byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`)
	successBody := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}}`)

	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{
			{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(prod429Body))},
			{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(successBody))},
		},
		errors: []error{nil, nil},
	}

	account := &Account{
		ID:          1,
		Name:        "acc-consumer",
		Type:        AccountTypeOAuth,
		Platform:    PlatformAntigravity,
		Concurrency: 1,
	}

	svc := &AntigravityGatewayService{httpUpstream: upstream}
	result, err := svc.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx:         context.Background(),
		prefix:      "[test]",
		account:     account,
		accessToken: "token",
		action:      "generateContent",
		body:        []byte(`{"project":"p1","request":{}}`),
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.resp)
	require.Equal(t, http.StatusOK, result.resp.StatusCode)

	require.Len(t, upstream.calls, 2, "prod 429 → daily retry: phải gọi cả 2 URL")
	require.True(t, strings.Contains(upstream.calls[0], prodURL), "attempt 1 phải chạy prod: %s", upstream.calls[0])
	require.True(t, strings.Contains(upstream.calls[1], dailyURL), "attempt 2 phải chạy daily (fallback): %s", upstream.calls[1])
}

// TestAntigravityRetryLoop_ExplicitDaily_NoProdFallback env täy minh daily →
// chỉ gọi daily, không đụng prod (không fallback ngược).
func TestAntigravityRetryLoop_ExplicitDaily_NoProdFallback(t *testing.T) {
	prodURL := "https://prod.test"
	dailyURL := "https://daily.test"
	t.Setenv(antigravityForwardBaseURLEnv, "daily")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
	}()
	antigravity.BaseURLs = []string{prodURL, dailyURL}

	successBody := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}}`)
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{
			{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(successBody))},
		},
		errors: []error{nil},
	}

	account := &Account{ID: 1, Type: AccountTypeOAuth, Platform: PlatformAntigravity, Concurrency: 1}
	svc := &AntigravityGatewayService{httpUpstream: upstream}
	result, err := svc.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx:         context.Background(),
		prefix:      "[test]",
		account:     account,
		accessToken: "token",
		action:      "generateContent",
		body:        []byte(`{"project":"p1","request":{}}`),
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, result.resp.StatusCode)
	require.Len(t, upstream.calls, 1)
	require.True(t, strings.Contains(upstream.calls[0], dailyURL))
	require.NotContains(t, upstream.calls[0], prodURL)
}

// ---------------------------------------------------------------------------
// Item 2: 404 self-heal (refresh project_id + retry in place)
// ---------------------------------------------------------------------------

// TestAntigravityRetryLoop_404_ProjectRefresh_RetryInPlace 404 带 projects/<id>
// resourceName → token provider refresh project_id → rewrap body → retry cùng
// account → 200。
func TestAntigravityRetryLoop_404_ProjectRefresh_RetryInPlace(t *testing.T) {
	prodURL := "https://prod.test"
	t.Setenv(antigravityForwardBaseURLEnv, "")
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
	}()
	antigravity.BaseURLs = []string{prodURL}

	notFoundBody := []byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","resourceName":"projects/old-project"}]}}`)
	successBody := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}}`)

	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{
			{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(notFoundBody))},
			{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(successBody))},
		},
		errors: []error{nil, nil},
	}

	account := &Account{
		ID:          1,
		Name:        "acc-404",
		Type:        AccountTypeOAuth,
		Platform:    PlatformAntigravity,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "old-project"},
	}

	// project refresher stub: RefreshProjectID trả project mới
	tp := &stubAntigravityTokenProvider{newProjectID: "new-project"}
	svc := &AntigravityGatewayService{httpUpstream: upstream, projectRefresher: tp}

	result, err := svc.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx:         context.Background(),
		prefix:      "[test]",
		account:     account,
		accessToken: "token",
		action:      "generateContent",
		body:        []byte(`{"project":"old-project","request":{"x":1}}`),
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, result.resp.StatusCode, "refresh project xong phải retry thành công")

	require.Equal(t, 1, tp.refreshCalls, "chỉ refresh project 1 lần/request")
	require.Len(t, upstream.calls, 2)
}

// TestAntigravityRetryLoop_404_NoProjectResource_NoRefresh 404 không có
// projects/<id> — ví dụ model không tồn tại (gemini-3.6-flash trên daily) —
// KHÔNG refresh project, trả 404 nguyên trạng (để failover/handler xử lý).
func TestAntigravityRetryLoop_404_NoProjectResource_NoRefresh(t *testing.T) {
	prodURL := "https://prod.test"
	t.Setenv(antigravityForwardBaseURLEnv, "")
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
	}()
	antigravity.BaseURLs = []string{prodURL}

	notFoundBody := []byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`)
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{
			{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(notFoundBody))},
		},
		errors: []error{nil},
	}

	account := &Account{ID: 1, Type: AccountTypeOAuth, Platform: PlatformAntigravity, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "project_id": "p1"}}
	tp := &stubAntigravityTokenProvider{newProjectID: "new-project"}
	svc := &AntigravityGatewayService{httpUpstream: upstream, projectRefresher: tp}

	result, err := svc.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx:         context.Background(),
		prefix:      "[test]",
		account:     account,
		accessToken: "token",
		action:      "generateContent",
		body:        []byte(`{"project":"p1","request":{}}`),
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusNotFound, result.resp.StatusCode)
	require.Equal(t, 0, tp.refreshCalls, "model-404 không được gọi refresh project")
	require.Len(t, upstream.calls, 1)
}

// TestExtractAntigravity404ProjectName 提取 project id từ resourceName.
func TestExtractAntigravity404ProjectName(t *testing.T) {
	require.Equal(t, "old-project", extractAntigravity404ProjectName(
		[]byte(`{"error":{"details":[{"resourceName":"projects/old-project/locations/global"}]}}`)))
	require.Equal(t, "abc123", extractAntigravity404ProjectName(
		[]byte(`{"error":{"message":"projects/abc123 was not found"}}`)))
	require.Equal(t, "", extractAntigravity404ProjectName(
		[]byte(`{"error":{"message":"Requested entity was not found."}}`)))
	require.Equal(t, "", extractAntigravity404ProjectName(nil))
}

// TestRewrapAntigravityProject đổi project trong body đã wrap, giữ request.
func TestRewrapAntigravityProject(t *testing.T) {
	svc := &AntigravityGatewayService{}
	out, err := svc.rewrapAntigravityProject(
		[]byte(`{"project":"old","request":{"x":1}}`), "new-proj")
	require.NoError(t, err)
	require.Contains(t, string(out), `"new-proj"`)
	require.NotContains(t, string(out), `"old"`)
	require.Contains(t, string(out), `"request":{"x":1}`)
}

// ---------------------------------------------------------------------------
// Item 3: ambiguous RESOURCE_EXHAUSTED → 30s cooldown ngắn
// ---------------------------------------------------------------------------

func TestIsAmbiguousAntigravityResourceExhausted(t *testing.T) {
	// 429 trống RetryInfo, không quota keyword → ambiguous
	require.True(t, isAmbiguousAntigravityResourceExhausted(
		[]byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`)))

	// Có RetryInfo → không ambiguous (upstream chỉ định thời gian chờ)
	require.False(t, isAmbiguousAntigravityResourceExhausted(
		[]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[
			{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"30s"},
			{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED","metadata":{"model":"gemini-2.5-flash"}}]}}`)))

	// Quota rõ ràng → không ambiguous
	require.False(t, isAmbiguousAntigravityResourceExhausted(
		[]byte(`{"error":{"code":429,"message":"quota exhausted","status":"RESOURCE_EXHAUSTED"}}`)))

	// Không phải RESOURCE_EXHAUSTED (vd 500) → không ambiguous
	require.False(t, isAmbiguousAntigravityResourceExhausted(
		[]byte(`{"error":{"code":500,"message":"Internal error","status":"INTERNAL"}}`)))
	require.False(t, isAmbiguousAntigravityResourceExhausted(nil))
}

// TestAntigravityDefaultRateLimitDuration_AmbiguousUses30s handleUpstreamError
// với 429 ambiguous (không parse được reset time) → model rate limit reset
// khoảng 30s, không phải config fallback dài (vd 30 phút).
// cần build model key；dùng account có model_mapping để resolveFinalAntigravityModelKey 回 đúng.
func TestAntigravityDefaultRateLimitDuration_AmbiguousUses30s(t *testing.T) {
	repo := &stubAntigravityAccountRepo{}
	svc := &AntigravityGatewayService{accountRepo: repo}
	account := &Account{
		ID:       42,
		Name:     "acc-amb",
		Type:     AccountTypeOAuth,
		Platform: PlatformAntigravity,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "p1",
			"model_mapping": map[string]any{
				"claude-sonnet-4-5": "claude-sonnet-4-5",
			},
		},
		Extra: map[string]any{},
	}

	ambiguousBody := []byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`)

	// Không parse được reset time → 30s
	svc.handleUpstreamError(context.Background(), "[test]", account, http.StatusTooManyRequests,
		http.Header{}, ambiguousBody, "claude-sonnet-4-5", 1, "sess", false)

	require.Len(t, repo.modelRateLimitCalls, 1, "ambiguous 429 phải đặt model rate limit")
	call := repo.modelRateLimitCalls[0]
	require.Equal(t, int64(42), call.accountID)
	resetIn := time.Until(call.resetAt)
	require.Greater(t, resetIn, 0*time.Second)
	require.LessOrEqual(t, resetIn, 45*time.Second,
		"ambiguous RESOURCE_EXHAUSTED phải dùng cooldown ngắn (~30s), không khóa dài: reset_in=%v", resetIn)
}

// stubAntigravityTokenProvider implements *AntigravityTokenProvider subset
// used by antigravityRetryLoop 404 self-heal path.
type stubAntigravityTokenProvider struct {
	newProjectID string
	refreshCalls int
}

func (s *stubAntigravityTokenProvider) RefreshProjectID(ctx context.Context, account *Account) (string, error) {
	s.refreshCalls++
	return s.newProjectID, nil
}
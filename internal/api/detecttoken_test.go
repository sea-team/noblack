package api

import (
	"net/http"
	"strings"
	"testing"
)

// hdr 构造带令牌的请求头。
func hdr(token string) map[string]string {
	return map[string]string{"X-Auth-Token": token}
}

const checkBody = `{"text":"挖矿"}`

// 未设检测令牌时 /check 与 /stats 保持开放, 与升级前行为一致。
func TestDetectAuth_DisabledByDefault(t *testing.T) {
	h := newTestHandler(t, "")
	if rec := do(h, http.MethodPost, "/check", checkBody, nil); rec.Code != 200 {
		t.Errorf("未启用检测鉴权时 /check 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/stats", "", nil); rec.Code != 200 {
		t.Errorf("未启用检测鉴权时 /stats 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	rec := do(h, http.MethodGet, "/auth/status", "", nil)
	if !strings.Contains(rec.Body.String(), `"detect_required":false`) {
		t.Errorf("status 应报告 detect_required:false, 实际 %s", rec.Body)
	}
}

// 设了检测令牌后, 无令牌和错令牌都必须被拒绝。
func TestDetectAuth_BlocksMissingAndWrongToken(t *testing.T) {
	h := newTestHandler(t, "")
	h.SetDetectToken("detect-secret")

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"无令牌", nil},
		{"错令牌", hdr("wrong")},
		{"空令牌", hdr("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := do(h, http.MethodPost, "/check", checkBody, tc.headers); rec.Code != http.StatusUnauthorized {
				t.Errorf("/check 应 401, 实际 %d: %s", rec.Code, rec.Body)
			}
			if rec := do(h, http.MethodGet, "/stats", "", tc.headers); rec.Code != http.StatusUnauthorized {
				t.Errorf("/stats 应 401, 实际 %d: %s", rec.Code, rec.Body)
			}
		})
	}

	if rec := do(h, http.MethodPost, "/check", checkBody, hdr("detect-secret")); rec.Code != 200 {
		t.Errorf("正确检测令牌应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/stats", "", hdr("detect-secret")); rec.Code != 200 {
		t.Errorf("正确检测令牌 /stats 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
}

// Authorization: Bearer 写法同样适用于检测令牌。
func TestDetectAuth_AcceptsBearerHeader(t *testing.T) {
	h := newTestHandler(t, "")
	h.SetDetectToken("detect-secret")

	headers := map[string]string{"Authorization": "Bearer detect-secret"}
	if rec := do(h, http.MethodPost, "/check", checkBody, headers); rec.Code != 200 {
		t.Errorf("Bearer 令牌应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
}

// 管理令牌权限更高, 应能直接调用检测接口 (管理页面只需持有一个令牌)。
func TestDetectAuth_AdminTokenGrantsDetect(t *testing.T) {
	h := newTestHandler(t, "admin-secret")
	h.SetDetectToken("detect-secret")

	if rec := do(h, http.MethodPost, "/check", checkBody, hdr("admin-secret")); rec.Code != 200 {
		t.Errorf("管理令牌调用 /check 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/stats", "", hdr("admin-secret")); rec.Code != 200 {
		t.Errorf("管理令牌调用 /stats 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
}

// 反向不成立: 检测令牌不得获得词库写权限, 这是拆分两个令牌的核心目的。
func TestDetectAuth_DetectTokenCannotWrite(t *testing.T) {
	h := newTestHandler(t, "admin-secret")
	h.SetDetectToken("detect-secret")

	writes := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/words", `{"word":"z","levels":["A"]}`},
		{http.MethodPut, "/words/挖矿", `{"levels":["A"]}`},
		{http.MethodDelete, "/words/挖矿", ""},
		{http.MethodPost, "/reload", ""},
		{http.MethodPost, "/stats/reset", ""},
	}
	for _, wr := range writes {
		rec := do(h, wr.method, wr.path, wr.body, hdr("detect-secret"))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 用检测令牌应 401, 实际 %d: %s", wr.method, wr.path, rec.Code, rec.Body)
		}
	}
}

// 只配检测令牌 (未配管理令牌) 时, 空令牌不能因两个空串相等而蒙混过关。
// 此时写操作仍按原语义不鉴权, 但检测必须拦住。
func TestDetectAuth_OnlyDetectTokenConfigured(t *testing.T) {
	h := newTestHandler(t, "") // 管理令牌为空
	h.SetDetectToken("detect-secret")

	if rec := do(h, http.MethodPost, "/check", checkBody, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("管理令牌为空时无令牌 /check 仍应 401, 实际 %d: %s", rec.Code, rec.Body)
	}

	rec := do(h, http.MethodGet, "/auth/status", "", nil)
	if !strings.Contains(rec.Body.String(), `"detect_required":true`) {
		t.Errorf("status 应报告 detect_required:true, 实际 %s", rec.Body)
	}
	// 前端据 required||detect_required 决定是否显示令牌框, 这里 required 仍为 false。
	if !strings.Contains(rec.Body.String(), `"required":false`) {
		t.Errorf("未配管理令牌时 required 应为 false, 实际 %s", rec.Body)
	}
}

// /auth/verify 对两种令牌都应给出正确结论, 且错误令牌不能被 "未启用即通过" 放过。
func TestDetectAuth_VerifyEndpoint(t *testing.T) {
	t.Run("仅配检测令牌", func(t *testing.T) {
		h := newTestHandler(t, "")
		h.SetDetectToken("detect-secret")

		if rec := do(h, http.MethodPost, "/auth/verify", "", hdr("detect-secret")); rec.Code != 200 {
			t.Errorf("正确检测令牌 verify 应 200, 实际 %d: %s", rec.Code, rec.Body)
		}
		if rec := do(h, http.MethodPost, "/auth/verify", "", hdr("wrong")); rec.Code != http.StatusUnauthorized {
			t.Errorf("错令牌 verify 应 401, 实际 %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("仅配管理令牌", func(t *testing.T) {
		h := newTestHandler(t, "admin-secret")

		if rec := do(h, http.MethodPost, "/auth/verify", "", hdr("admin-secret")); rec.Code != 200 {
			t.Errorf("正确管理令牌 verify 应 200, 实际 %d: %s", rec.Code, rec.Body)
		}
		if rec := do(h, http.MethodPost, "/auth/verify", "", hdr("wrong")); rec.Code != http.StatusUnauthorized {
			t.Errorf("错令牌 verify 应 401, 实际 %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("两种令牌都配", func(t *testing.T) {
		h := newTestHandler(t, "admin-secret")
		h.SetDetectToken("detect-secret")

		for _, tok := range []string{"admin-secret", "detect-secret"} {
			if rec := do(h, http.MethodPost, "/auth/verify", "", hdr(tok)); rec.Code != 200 {
				t.Errorf("令牌 %s verify 应 200, 实际 %d: %s", tok, rec.Code, rec.Body)
			}
		}
		if rec := do(h, http.MethodPost, "/auth/verify", "", hdr("wrong")); rec.Code != http.StatusUnauthorized {
			t.Errorf("错令牌 verify 应 401, 实际 %d: %s", rec.Code, rec.Body)
		}
	})
}

// /health 必须保持公开: 部署脚本与负载均衡探针依赖它。
func TestDetectAuth_HealthStaysPublic(t *testing.T) {
	h := newTestHandler(t, "admin-secret")
	h.SetDetectToken("detect-secret")

	if rec := do(h, http.MethodGet, "/health", "", nil); rec.Code != 200 {
		t.Errorf("/health 应保持公开 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/levels", "", nil); rec.Code != 200 {
		t.Errorf("/levels 未纳入检测鉴权, 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
}

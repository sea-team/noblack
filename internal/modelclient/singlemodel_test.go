package modelclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 服务端可通过 NB_MODELS=lite 只启用单个模型, 此时 /health 的 parallel 为 false、
// models 只有一项。客户端不得因此判定服务不可用。
func TestHealthAcceptsSingleModel(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{
			name: "单模型 lite (parallel=false)",
			body: `{"ok":true,"device":"cpu","models":["lite"],"parallel":false,"combine_policy":"max"}`,
			ok:   true,
		},
		{
			name: "双模型 (parallel=true)",
			body: `{"ok":true,"device":"cpu","models":["lite","macbert"],"parallel":true,"combine_policy":"max"}`,
			ok:   true,
		},
		{
			name: "无模型加载",
			body: `{"ok":true,"device":"cpu","models":[],"parallel":false,"combine_policy":"max"}`,
			ok:   false,
		},
		{
			name: "服务自报未就绪",
			body: `{"ok":false,"device":"cpu","models":["lite"],"parallel":false,"combine_policy":"max"}`,
			ok:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			err := New(srv.URL, 5*time.Second).Health(context.Background())
			if tc.ok && err != nil {
				t.Errorf("应视为就绪, 实际报错: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("应报错, 实际通过")
			}
		})
	}
}

// 推理响应同样可能只含一个模型结果。
func TestCheckAcceptsSingleModelResult(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{
			name: "仅 lite 返回一个结果",
			body: `{"ok":true,"request_id":"x","device":"cpu","parallel":false,` +
				`"models":[{"model":"lite","action":"pass","sexual_harm_probability":0.01,"latency_ms":4.0}],` +
				`"combined_action":"pass","combine_policy":"max","latency_ms":4.2}`,
			ok: true,
		},
		{
			name: "双模型返回两个结果",
			body: `{"ok":true,"request_id":"x","device":"cpu","parallel":true,` +
				`"models":[{"model":"lite","action":"pass","sexual_harm_probability":0.01,"latency_ms":4.0},` +
				`{"model":"macbert","action":"block","sexual_harm_probability":0.9,"latency_ms":47.0}],` +
				`"combined_action":"block","combine_policy":"max","latency_ms":48.0}`,
			ok: true,
		},
		{
			name: "空结果视为不完整",
			body: `{"ok":true,"request_id":"x","device":"cpu","parallel":false,` +
				`"models":[],"combined_action":"pass","combine_policy":"max","latency_ms":1.0}`,
			ok: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, err := New(srv.URL, 5*time.Second).Check(context.Background(), "测试文本")
			if tc.ok {
				if err != nil {
					t.Fatalf("应成功, 实际报错: %v", err)
				}
				if len(got.Models) == 0 {
					t.Error("模型结果不应为空")
				}
			} else if err == nil {
				t.Error("应报错, 实际通过")
			}
		})
	}
}

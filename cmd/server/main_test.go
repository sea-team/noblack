package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"

	"noblack/internal/api"
)

func TestServerUsesDataWordDatabaseByDefault(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestServerDefaultWordsHelper")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "NOBLACK_SERVER_DEFAULT_WORDS_HELPER=1")

	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("server unexpectedly started without a word database")
	}
	if !strings.Contains(string(output), "data/words.json") {
		t.Fatalf("server output = %q, want data/words.json", output)
	}
}

func TestServerDefaultWordsHelper(t *testing.T) {
	if os.Getenv("NOBLACK_SERVER_DEFAULT_WORDS_HELPER") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{os.Args[0]}
	main()
	os.Exit(0)
}

func TestModelServiceURLDefaultsToDisabled(t *testing.T) {
	t.Setenv("NB_MODEL_SERVICE_URL", "")
	if got := configuredModelServiceURL(); got != "" {
		t.Fatalf("configuredModelServiceURL() = %q, want disabled empty URL", got)
	}
}

func TestModelServiceURLPreservesExplicitURL(t *testing.T) {
	t.Setenv("NB_MODEL_SERVICE_URL", "http://127.0.0.1:8091")
	if got := configuredModelServiceURL(); got != "http://127.0.0.1:8091" {
		t.Fatalf("configuredModelServiceURL() = %q, want explicit URL", got)
	}
}

// 发布包的 config.env 只提供 NB_MODEL_PORT (模型服务恒定监听本机),
// 不据此推导地址的话, 直接运行可执行文件时依赖模型的检测模式会启动失败。
func TestModelServiceURLDerivedFromPort(t *testing.T) {
	cases := []struct {
		name string
		url  string
		host string
		port string
		want string
	}{
		{"仅端口时推导本机地址", "", "", "18091", "http://127.0.0.1:18091"},
		{"显式 URL 优先于端口", "http://10.0.0.1:9", "", "18091", "http://10.0.0.1:9"},
		{"自定义主机", "", "192.168.1.5", "18091", "http://192.168.1.5:18091"},
		{"两者皆空时禁用模型", "", "", "", ""},
		{"端口非法时忽略", "", "", "abc", ""},
		{"端口两侧空白被裁剪", "", "", "  18091  ", "http://127.0.0.1:18091"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NB_MODEL_SERVICE_URL", tc.url)
			t.Setenv("NB_MODEL_HOST", tc.host)
			t.Setenv("NB_MODEL_PORT", tc.port)
			if got := configuredModelServiceURL(); got != tc.want {
				t.Errorf("configuredModelServiceURL() = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

// -config 需要在 flag.Parse 之前被识别, 否则配置文件会晚于 flag 默认值求值。
func TestPreParseConfigPath(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"未指定", []string{"-addr", ":8080"}, ""},
		{"等号写法", []string{"-config=/etc/nb.env"}, "/etc/nb.env"},
		{"空格写法", []string{"-config", "/etc/nb.env"}, "/etc/nb.env"},
		{"双横线等号", []string{"--config=/etc/nb.env"}, "/etc/nb.env"},
		{"双横线空格", []string{"--config", "/etc/nb.env"}, "/etc/nb.env"},
		{"夹在其他参数中", []string{"-addr", ":80", "-config", "a.env", "-ci"}, "a.env"},
		{"缺少值", []string{"-config"}, ""},
		{"-- 之后不解析", []string{"--", "-config", "x.env"}, ""},
		{"含空格的路径", []string{"-config", "/opt/my dir/nb.env"}, "/opt/my dir/nb.env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preParseConfigPath(tc.args); got != tc.want {
				t.Errorf("preParseConfigPath(%q) = %q, 期望 %q", tc.args, got, tc.want)
			}
		})
	}
}

// NB_DETECT_MODE 必须被读取: 启动脚本通过 config.env 导出该变量,
// 若程序忽略它, 配置会静默失效 (表现为始终使用默认的 both)。
func TestDetectModeFromEnvironment(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"未设置时回落默认", "", string(api.DefaultDetectMode)},
		{"仅模型", "model_only", "model_only"},
		{"模型优先", "model_first", "model_first"},
		{"仅词库", "word_only", "word_only"},
		{"词库优先", "word_first", "word_first"},
		{"两者全跑", "both", "both"},
		{"两侧空白被裁剪", "  word_first  ", "word_first"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NB_DETECT_MODE", tc.env)
			if got := configuredDetectMode(); got != tc.want {
				t.Fatalf("configuredDetectMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// 非法值不在这里拦截, 而是交给 ParseDetectMode 统一报错,
// 保证命令行与环境变量两条路径的校验行为一致。
func TestDetectModeFromEnvironmentRejectsInvalidLater(t *testing.T) {
	t.Setenv("NB_DETECT_MODE", "bogus")
	raw := configuredDetectMode()
	if raw != "bogus" {
		t.Fatalf("configuredDetectMode() = %q, want passthrough for later validation", raw)
	}
	if _, err := api.ParseDetectMode(raw); err == nil {
		t.Fatal("ParseDetectMode 应当拒绝非法环境变量值")
	}
}

func TestRecallOnMissFromEnvironment(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"未设置时为假", "", false},
		{"true", "true", true},
		{"false", "false", false},
		{"1", "1", true},
		{"0", "0", false},
		{"大写 TRUE", "TRUE", true},
		{"两侧空白被裁剪", "  true  ", true},
		// 拼写错误必须按 false 处理, 不能被当成 true 而静默开启召回。
		{"拼写错误按假处理", "ture", false},
		{"任意字符串按假处理", "yes please", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NB_RECALL_ON_MISS", tc.env)
			if got := configuredRecallOnMiss(); got != tc.want {
				t.Fatalf("configuredRecallOnMiss() = %v, want %v", got, tc.want)
			}
		})
	}
}

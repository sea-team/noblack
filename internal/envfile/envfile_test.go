package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DefaultName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesKeys(t *testing.T) {
	path := writeConfig(t, `# 注释行
NB_DETECT_MODE=model_only
NB_ADDR=:18080

NB_RECALL_ON_MISS=true
`)
	t.Setenv("NB_DETECT_MODE", "")
	t.Setenv("NB_ADDR", "")
	t.Setenv("NB_RECALL_ON_MISS", "")

	count, err := Load(path)
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if count != 3 {
		t.Errorf("生效项数 = %d, 期望 3", count)
	}
	for key, want := range map[string]string{
		"NB_DETECT_MODE":    "model_only",
		"NB_ADDR":           ":18080",
		"NB_RECALL_ON_MISS": "true",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, 期望 %q", key, got, want)
		}
	}
}

// 已存在的环境变量优先, 与启动脚本行为一致。
func TestLoadDoesNotOverrideExistingEnvironment(t *testing.T) {
	path := writeConfig(t, "NB_DETECT_MODE=model_only\n")
	t.Setenv("NB_DETECT_MODE", "word_first")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if got := os.Getenv("NB_DETECT_MODE"); got != "word_first" {
		t.Errorf("NB_DETECT_MODE = %q, 已存在的环境变量应当优先", got)
	}
}

// 非 NB_ 前缀的键不得写入环境, 避免配置文件污染进程环境。
func TestLoadIgnoresForeignKeys(t *testing.T) {
	path := writeConfig(t, `PATH=/evil
HOME=/evil
nb_lowercase=x
NB_VALID=ok
`)
	t.Setenv("NB_VALID", "")
	originalPath := os.Getenv("PATH")

	count, err := Load(path)
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if count != 1 {
		t.Errorf("生效项数 = %d, 期望 1 (只有 NB_VALID)", count)
	}
	if os.Getenv("PATH") != originalPath {
		t.Error("PATH 被配置文件篡改")
	}
	if os.Getenv("NB_VALID") != "ok" {
		t.Error("NB_VALID 未生效")
	}
}

func TestLoadHandlesEdgeCases(t *testing.T) {
	path := writeConfig(t, `NB_EMPTY=
NB_SPACES=  值两侧有空白
NB_EQUALS=a=b=c
# NB_COMMENTED=x
NB_NOEQUALS
=NB_LEADING
`)
	for _, key := range []string{"NB_EMPTY", "NB_SPACES", "NB_EQUALS", "NB_COMMENTED"} {
		t.Setenv(key, "")
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if got := os.Getenv("NB_SPACES"); got != "值两侧有空白" {
		t.Errorf("NB_SPACES = %q, 期望裁剪两侧空白", got)
	}
	// 值中的等号必须保留 (令牌等场景可能含 =)。
	if got := os.Getenv("NB_EQUALS"); got != "a=b=c" {
		t.Errorf("NB_EQUALS = %q, 期望保留值内等号", got)
	}
	if got := os.Getenv("NB_COMMENTED"); got != "" {
		t.Errorf("NB_COMMENTED = %q, 注释行不应生效", got)
	}
}

// Windows 记事本另存可能带 BOM, 不应导致首个键失效。
func TestLoadToleratesBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultName)
	if err := os.WriteFile(path, []byte("\xef\xbb\xbfNB_MODEL_PORT=18091\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NB_MODEL_PORT", "")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if got := os.Getenv("NB_MODEL_PORT"); got != "18091" {
		t.Errorf("NB_MODEL_PORT = %q, BOM 应被忽略", got)
	}
}

// Windows 编辑器保存的 CRLF 行尾不应残留在值里。
func TestLoadToleratesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultName)
	if err := os.WriteFile(path, []byte("NB_MODEL_PORT=18091\r\nNB_ADDR=:18080\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NB_MODEL_PORT", "")
	t.Setenv("NB_ADDR", "")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if got := os.Getenv("NB_MODEL_PORT"); got != "18091" {
		t.Errorf("NB_MODEL_PORT = %q, CRLF 不应残留", got)
	}
	if got := os.Getenv("NB_ADDR"); got != ":18080" {
		t.Errorf("NB_ADDR = %q, CRLF 不应残留", got)
	}
}

// 配置文件是可选的, 不存在时不应报错。
func TestLoadMissingFileIsNotAnError(t *testing.T) {
	count, err := Load(filepath.Join(t.TempDir(), "nonexistent.env"))
	if err != nil {
		t.Errorf("文件不存在时不应报错, 得到: %v", err)
	}
	if count != 0 {
		t.Errorf("生效项数 = %d, 期望 0", count)
	}
}

// chdir 切换工作目录并在测试结束时还原。
// 不用 t.Chdir: 该 API 需要 Go 1.24, 而本模块声明的是 go1.21。
func chdir(t *testing.T, directory string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original) })
}

// Resolve 应能在当前工作目录找到 config.env。
func TestResolveFindsWorkingDirectoryConfig(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, DefaultName)
	if err := os.WriteFile(configPath, []byte("NB_X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, directory)

	got := Resolve()
	if got == "" {
		t.Fatal("Resolve() 返回空, 期望找到工作目录下的 config.env")
	}
	gotResolved, _ := filepath.EvalSymlinks(got)
	wantResolved, _ := filepath.EvalSymlinks(configPath)
	if gotResolved != wantResolved {
		t.Errorf("Resolve() = %q, 期望 %q", gotResolved, wantResolved)
	}
}

func TestResolveReturnsEmptyWhenAbsent(t *testing.T) {
	chdir(t, t.TempDir())
	// 可执行文件目录 (go test 的临时二进制) 也不会有 config.env。
	if got := Resolve(); got != "" {
		t.Errorf("Resolve() = %q, 无配置文件时期望空串", got)
	}
}

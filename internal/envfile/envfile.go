// Package envfile 加载发布包中的 config.env。
//
// 存在意义: 启动脚本 (start.sh / noblack-control.ps1) 会解析 config.env 并导出
// 为环境变量, 但用户直接运行可执行文件时不经过脚本, 配置会静默失效。
// 让程序自身也能读取该文件, 两种启动方式行为一致。
package envfile

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// keyPattern 与启动脚本保持一致: 只接受 NB_ 前缀的大写键。
// 这样可以避免 config.env 里的无关行污染进程环境。
var keyPattern = regexp.MustCompile(`^NB_[A-Z0-9_]+$`)

// DefaultName 是发布包中配置文件的固定名称。
const DefaultName = "config.env"

// Load 读取 path 指向的 config.env, 并把其中的键写入进程环境。
//
// 语义与启动脚本一致 —— 已存在的同名环境变量优先, 文件不会覆盖它,
// 因此优先级为: config.env < 环境变量 < 命令行参数。
//
// 返回实际写入的键数量。文件不存在时返回 (0, nil), 因为配置文件是可选的。
func Load(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	applied := 0
	scanner := bufio.NewScanner(file)
	firstLine := true
	for scanner.Scan() {
		text := scanner.Text()
		if firstLine {
			// Windows 记事本另存为 UTF-8 时会写入 BOM (U+FEFF), 若不剥离,
			// 首个键会变成 "<BOM>NB_XXX" 而被静默丢弃。
			text = strings.TrimPrefix(text, "\ufeff")
			firstLine = false
		}
		line := strings.TrimSpace(text)
		// 跳过空行与注释。
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		separator := strings.Index(line, "=")
		if separator < 1 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		if !keyPattern.MatchString(key) {
			continue
		}
		// 值保留原样 (仅裁剪两侧空白), 不做引号剥离:
		// 启动脚本同样不处理引号, 保持一致避免同一份文件在两条路径下解析不同。
		value := strings.TrimSpace(line[separator+1:])
		// 已存在的环境变量优先, 与启动脚本行为一致。
		if existing, ok := os.LookupEnv(key); ok && existing != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return applied, err
		}
		applied++
	}
	if err := scanner.Err(); err != nil {
		return applied, err
	}
	return applied, nil
}

// Resolve 推断 config.env 的位置。
//
// 依次尝试: 可执行文件所在目录、当前工作目录。前者覆盖双击运行和用绝对路径
// 调用的场景, 后者覆盖在包目录内用相对路径启动的场景。
// 都不存在时返回空串, 由调用方按 "无配置文件" 处理。
func Resolve() string {
	var candidates []string
	if executable, err := os.Executable(); err == nil {
		// 解析符号链接, 避免通过软链启动时定位到错误目录。
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), DefaultName))
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workingDirectory, DefaultName))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

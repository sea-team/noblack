package main

// 敏感词检测服务入口。
//
// 运行:
//   go run ./cmd/server -words ./data/words.json -addr :8080
//
// 启动流程:
//   1. 加载词库文件, 构建初始自动机。
//   2. 将其放入 Store (atomic.Value 持有)。
//   3. 启动 fsnotify 监听, 实现文件变更自动热加载。
//   4. 注册 HTTP 路由并启动服务。
//   5. 监听系统信号, 优雅关闭。

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"noblack/internal/api"
	"noblack/internal/envfile"
	"noblack/internal/matcher"
	"noblack/internal/modelclient"
	"noblack/internal/normalize"
	"noblack/internal/samples"
	"noblack/internal/stats"
	"noblack/internal/store"
)

// configuredModelServiceURL 解析模型服务地址。
//
// 优先使用显式的 NB_MODEL_SERVICE_URL; 未设置时, 若 config.env 里配了
// NB_MODEL_PORT 则据此推导出本机地址。发布包的 config.env 只提供端口
// (模型服务恒定监听 127.0.0.1), 不推导的话直接运行可执行文件时
// 依赖模型的检测模式会因为拿不到地址而启动失败。
func configuredModelServiceURL() string {
	if explicit := strings.TrimSpace(os.Getenv("NB_MODEL_SERVICE_URL")); explicit != "" {
		return explicit
	}
	port := strings.TrimSpace(os.Getenv("NB_MODEL_PORT"))
	if port == "" {
		return ""
	}
	// 端口非法时不静默拼出坏地址, 交由后续健康检查暴露问题。
	if _, err := strconv.Atoi(port); err != nil {
		log.Printf("[config] NB_MODEL_PORT 不是有效端口, 已忽略: %q", port)
		return ""
	}
	host := strings.TrimSpace(os.Getenv("NB_MODEL_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// configuredDetectMode 返回 NB_DETECT_MODE 指定的默认检测模式。
// 环境变量为空时回落到 both, 与历史行为一致。
func configuredDetectMode() string {
	if raw := strings.TrimSpace(os.Getenv("NB_DETECT_MODE")); raw != "" {
		return raw
	}
	return string(api.DefaultDetectMode)
}

// configuredRecallOnMiss 返回 NB_RECALL_ON_MISS 指定的默认召回开关。
// 只接受明确的布尔字面量; 无法解析时回落到 false 并在启动日志中告警,
// 避免把拼写错误 (如 "ture") 静默当成 true。
func configuredRecallOnMiss() bool {
	return envOrBool("NB_RECALL_ON_MISS", false)
}

// envOrString 返回环境变量的值, 为空时回落到 fallback。
func envOrString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// envOrBool 解析布尔型环境变量。无法解析时回落到 fallback 并告警,
// 避免拼写错误 (如 "ture") 被静默当成 true。
func envOrBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		log.Printf("[config] %s 值无法解析为布尔值, 已按 %v 处理: %q", name, fallback, raw)
		return fallback
	}
	return value
}

// envOrFloat 解析浮点型环境变量。无法解析时回落到 fallback 并告警。
func envOrFloat(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		log.Printf("[config] %s 值无法解析为数字, 已按 %v 处理: %q", name, fallback, raw)
		return fallback
	}
	return value
}

// preParseConfigPath 在 flag.Parse 之前从原始参数中提取 -config 的值。
// 支持 -config=路径 与 -config 路径 两种写法, 以及 -- 前缀。
// 未指定时返回空串。
func preParseConfigPath(arguments []string) string {
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		trimmed := strings.TrimPrefix(strings.TrimPrefix(argument, "-"), "-")
		if value, found := strings.CutPrefix(trimmed, "config="); found {
			return value
		}
		if trimmed == "config" && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func main() {
	// 必须在声明 flag 之前加载: 下面的 configuredXxx() 会在 flag.String/Bool
	// 调用时立即求值作为默认值, 晚于此处加载则读不到 config.env 的内容。
	//
	// -config 需要在 flag.Parse 之前拿到, 因此这里先手动扫一遍 os.Args;
	// 该标志随后仍会正式注册, 以便出现在 -h 输出中并接受常规校验。
	configPath := preParseConfigPath(os.Args[1:])
	explicitConfig := configPath != ""
	if configPath == "" {
		configPath = envfile.Resolve()
	}
	loadedKeys := 0
	if configPath != "" {
		count, err := envfile.Load(configPath)
		if err != nil {
			log.Fatalf("加载配置文件失败 %s: %v", configPath, err)
		}
		loadedKeys = count
	} else if explicitConfig {
		log.Fatalf("指定的配置文件不存在: %s", configPath)
	}

	var (
		configFlag   = flag.String("config", configPath, "配置文件路径 (config.env); 默认在可执行文件目录与当前目录查找")
		wordsPath    = flag.String("words", "./data/words.json", "敏感词库文件路径 (JSON)")
		addr         = flag.String("addr", envOrString("NB_ADDR", ":8080"), "HTTP 监听地址; 亦可用环境变量 NB_ADDR 设置")
		watch        = flag.Bool("watch", envOrBool("NB_WATCH", true), "是否启用 fsnotify 文件监听热加载; 亦可用环境变量 NB_WATCH 设置")
		caseIns      = flag.Bool("ci", envOrBool("NB_CI", false), "匹配是否大小写不敏感 (主要影响英文词, 如 Bilibili≈bilibili); 亦可用环境变量 NB_CI 设置")
		pinyinIn     = flag.Bool("pinyin", envOrBool("NB_PINYIN", true), "启用拼音匹配, 对抗用拼音替换汉字的绕过 (zha yào -> 炸药); 需同时开启 -normalize; 亦可用环境变量 NB_PINYIN 设置")
		normalizeIn  = flag.Bool("normalize", envOrBool("NB_NORMALIZE", true), "归一化输入以对抗变体绕过 (去标点/空白/零宽字符, 繁简与全半角折叠), 使 炸.药 能命中 炸药; 亦可用环境变量 NB_NORMALIZE 设置")
		samplesFile  = flag.String("samples-file", envOrString("NB_SAMPLES", ""), "语义样本库文件路径 (JSON), 用于补足模型漏报; 留空则禁用; 亦可用环境变量 NB_SAMPLES 设置")
		sampleThresh = flag.Float64("sample-threshold", envOrFloat("NB_SAMPLE_THRESHOLD", samples.DefaultThreshold), "语义样本相似度阈值 (0-1), 越高越严格; 亦可用环境变量 NB_SAMPLE_THRESHOLD 设置")
		defLevel     = flag.String("default-level", "Low", "词条未标注 level/levels 时使用的默认等级")
		statsFile    = flag.String("stats-file", envOrString("NB_STATS", ""), "统计持久化文件路径 (JSON); 留空则不持久化, 重启后统计归零; 亦可用环境变量 NB_STATS 设置")
		statsIvl     = flag.Duration("stats-flush-interval", 30*time.Second, "统计后台落盘间隔 (仅在 -stats-file 非空时生效)")
		token        = flag.String("token", envOrString("NB_TOKEN", ""), "词条写操作(新增/修改/删除)的鉴权令牌; 留空则不鉴权; 亦可用环境变量 NB_TOKEN 设置")
		detectToken  = flag.String("detect-token", envOrString("NB_DETECT_TOKEN", ""), "检测接口(/check, /stats)的鉴权令牌; 留空则检测不鉴权; -token 令牌同样可调用检测接口; 亦可用环境变量 NB_DETECT_TOKEN 设置")
		modelURL     = flag.String("model-service-url", configuredModelServiceURL(), "AI 模型服务地址; 留空则禁用模型 (启用哪些模型由模型服务的 NB_MODELS 决定)")
		modelTimeout = flag.Duration("model-timeout", 45*time.Second, "模型推理超时")
		detectMode   = flag.String("detect-mode", configuredDetectMode(), "检测模式: model_only|model_first|word_only|word_first|both; 亦可用环境变量 NB_DETECT_MODE 设置; 请求体 mode 可覆盖")
		recallOnMiss = flag.Bool("recall-on-miss", configuredRecallOnMiss(), "优先链路未命中时补跑另一条链路以提高召回 (注意: 与技术失败降级无关); 亦可用环境变量 NB_RECALL_ON_MISS 设置; 请求体 recall_on_miss 可覆盖")
	)
	flag.Parse()

	// 明确告知配置来源: 之前配置静默失效时, 日志里看不出有没有读到文件。
	if configPath != "" {
		log.Printf("已加载配置文件: %s (生效 %d 项)", configPath, loadedKeys)
	} else {
		log.Printf("未找到 config.env, 使用默认配置与命令行参数 (可用 -config 指定路径)")
	}
	// -config 与预扫描结果不一致, 说明用户在 flag 里写了另一个路径,
	// 但此时环境已按预扫描的文件加载完毕, 必须提示而非静默忽略。
	if *configFlag != configPath {
		log.Printf("[config] 警告: -config=%q 未生效, 实际加载的是 %q", *configFlag, configPath)
	}

	mode, err := api.ParseDetectMode(*detectMode)
	if err != nil {
		log.Fatalf("-detect-mode 无效: %v", err)
	}
	if *modelURL == "" && (mode == api.ModeModelOnly || mode == api.ModeModelFirst) {
		log.Fatalf("-detect-mode=%s 需要模型服务, 请同时设置 -model-service-url", mode)
	}

	if *statsFile != "" && *statsIvl <= 0 {
		log.Fatalf("设置 -stats-file 时，-stats-flush-interval 必须大于 0")
	}

	// 并行度诊断: GOMAXPROCS 决定 Go 能同时用几个核。
	log.Printf("并行度: GOMAXPROCS=%d, NumCPU=%d", runtime.GOMAXPROCS(0), runtime.NumCPU())

	opts := matcher.Options{
		CaseInsensitive: *caseIns,
		DefaultLevel:    *defLevel,
		Normalize:       *normalizeIn,
		Pinyin:          *pinyinIn,
	}
	if *normalizeIn {
		log.Printf("已启用输入归一化 (对抗 炸.药 这类变体绕过)")
	}
	if *normalizeIn && *pinyinIn {
		log.Printf("已启用拼音匹配 (对抗 zha yào 这类拼音绕过)")
	} else if *pinyinIn {
		log.Printf("拼音匹配需要同时开启归一化 (-normalize), 本次未生效")
	}

	// 1. 加载词条。
	entries, err := matcher.LoadEntries(*wordsPath, opts)
	if err != nil {
		log.Fatalf("初始化词库失败: %v", err)
	}

	// 2. 放入 Store (内部构建自动机)。
	st := store.New(*wordsPath, entries, opts)
	log.Printf("初始化完成, 加载词条数: %d, 等级集合: %v", st.Current().Size(), st.Current().Levels())
	if n := st.Current().PinyinSize(); n > 0 {
		log.Printf("拼音索引词条数: %d (仅收录拼音长度 >= %d 的词条, 短拼音同音词过多会误报)",
			n, normalize.MinPinyinLength)
	}

	// 3. 统计收集器 (可选持久化)。
	metrics := stats.New()
	done := make(chan struct{})
	var persister *stats.Persister
	if *statsFile != "" {
		persister = stats.NewPersister(metrics, *statsFile, *statsIvl)
		if err := persister.LoadInto(); err != nil {
			// 恢复失败不致命: 从空统计开始, 仅告警。
			log.Printf("[stats] 恢复历史统计失败, 将从零开始: %v", err)
		} else {
			s := metrics.Snapshot(0)
			log.Printf("[stats] 已启用持久化: %s (间隔 %s), 恢复 check_requests=%d", *statsFile, *statsIvl, s.CheckRequests)
		}
		go persister.Run(done) // 后台定期落盘
	}

	// 4. 启动文件监听 (可选)。
	if *watch {
		go func() {
			if err := st.Watch(done); err != nil {
				log.Printf("文件监听启动失败 (仅影响自动热加载, 不影响 /reload): %v", err)
			}
		}()
	}

	// 5. 注册路由 (含前端静态页与 API)。
	mux := http.NewServeMux()
	handler := api.NewHandler(st, metrics, *token)
	handler.SetDetectToken(*detectToken)
	handler.SetDetectMode(mode, *recallOnMiss)
	// 语义样本库: 用整句负面样本补足模型漏报, 通过 /samples 接口在线维护。
	if *samplesFile != "" {
		sampleStore := samples.New(*samplesFile, *sampleThresh)
		if err := sampleStore.Load(); err != nil {
			// 样本库损坏不应阻止服务启动 —— 词库与模型仍可正常工作。
			log.Printf("[samples] 加载失败, 将以空库启动: %v", err)
		}
		handler.SetSampleStore(sampleStore)
		log.Printf("已启用语义样本库: %s (样本数 %d, 相似度阈值 %.2f)",
			*samplesFile, sampleStore.Size(), sampleStore.Threshold())
	}
	log.Printf("检测模式: %s, 未命中召回: %v", mode, *recallOnMiss)
	if *modelURL != "" {
		client := modelclient.New(*modelURL, *modelTimeout)
		handler.SetModelClient(client)
		healthCtx, healthCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := client.Health(healthCtx); err != nil {
			log.Printf("[models] service not ready at startup; /check will degrade gracefully: %v", err)
		} else {
			log.Printf("[models] CPU 模型服务就绪: %s", *modelURL)
		}
		healthCancel()
	}
	handler.Register(mux)
	if *token != "" {
		log.Printf("已启用词条写操作鉴权 (新增/修改/删除需令牌)")
	}
	if *detectToken != "" {
		log.Printf("已启用检测接口鉴权 (/check, /stats 需令牌)")
	} else {
		log.Printf("检测接口未鉴权, 任何人均可调用 /check; 如需限制请设置 NB_DETECT_TOKEN")
	}

	srv := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 5. 异步启动 + 信号优雅关闭。
	go func() {
		log.Printf("敏感词检测服务已启动, 监听 %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	// os.Interrupt (Ctrl+C) 跨平台可捕获; SIGTERM 在类 Unix 下由编排器 (k8s/systemd) 发送。
	// 注意: Windows 不投递可捕获的 SIGTERM, 优雅关闭主要靠 Ctrl+C; 统计不丢则靠后台定期落盘。
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Printf("收到关闭信号, 正在优雅关闭...")

	close(done) // 停止文件监听与统计后台循环
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("优雅关闭超时: %v", err)
	}
	// 等待后台持久化循环完成唯一一次退出落盘。
	if persister != nil {
		persister.Wait()
	}
	log.Printf("服务已关闭")
}

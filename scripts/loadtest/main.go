// noblack 并发压测工具
//
// 单文件、零第三方依赖。用法见 -h。
//
// 典型用法 (Windows):
//
//	go run . -url https://noblack.guanliyuangong.com -token <令牌>
//	go run . -url https://noblack.guanliyuangong.com -token <令牌> -scene modes
//	go build -o loadtest.exe . && loadtest.exe -url ... -token ...
//
// 默认走阶梯递增并带熔断: 失败率或 P99 超阈值时立即停止, 避免把线上压挂。
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------- 配置 ----------

type config struct {
	baseURL    string
	token      string
	scene      string
	steps      []int
	stepDur    time.Duration
	timeout    time.Duration
	maxFailPct float64
	maxP99     time.Duration
	noAbort    bool
	insecure   bool
	soakConc   int
	soakDur    time.Duration
	soakEvery  time.Duration
}

// ---------- 统计 ----------

type result struct {
	label    string
	conc     int
	elapsed  time.Duration
	ok       int64
	lats     []time.Duration
	errs     map[string]int64
	non200   map[int]int64
	bytesIn  int64
	modelMS  []float64 // 服务端自报的模型推理耗时
	wordOnly int64     // 未走模型的响应数
}

func (r *result) qps() float64 {
	if r.elapsed <= 0 {
		return 0
	}
	return float64(r.ok) / r.elapsed.Seconds()
}

func (r *result) pct(p float64) time.Duration {
	if len(r.lats) == 0 {
		return 0
	}
	i := int(float64(len(r.lats)-1) * p)
	return r.lats[i]
}

func (r *result) avg() time.Duration {
	if len(r.lats) == 0 {
		return 0
	}
	var s time.Duration
	for _, l := range r.lats {
		s += l
	}
	return s / time.Duration(len(r.lats))
}

func (r *result) failed() int64 {
	var n int64
	for _, v := range r.errs {
		n += v
	}
	for _, v := range r.non200 {
		n += v
	}
	return n
}

func (r *result) failPct() float64 {
	total := r.ok + r.failed()
	if total == 0 {
		return 0
	}
	return float64(r.failed()) / float64(total) * 100
}

// avgModelMS 返回服务端自报的平均推理耗时, 用于把网络开销从服务耗时里剥离。
func (r *result) avgModelMS() float64 {
	if len(r.modelMS) == 0 {
		return 0
	}
	var s float64
	for _, v := range r.modelMS {
		s += v
	}
	return s / float64(len(r.modelMS))
}

// ---------- 样本 ----------

var fillers = []string{
	"今天天气不错我们一起去公园散步吧",
	"这个产品的用户体验做得非常好推荐大家试试",
	"会议定在下周三上午十点请准时参加",
	"麻烦帮我查一下这个订单的物流状态谢谢",
	"最近在学习编程感觉还挺有意思的",
	"周末打算去看场电影有人一起吗",
	"这家餐厅的菜品味道相当不错价格也公道",
}

// buildTexts 生成指定长度的样本。length<=0 表示用天然长度的混合样本。
func buildTexts(n, length int, seed int64) []string {
	rng := rand.New(rand.NewSource(seed))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if length > 0 {
			var b strings.Builder
			for len([]rune(b.String())) < length {
				b.WriteString(fillers[rng.Intn(len(fillers))])
			}
			rs := []rune(b.String())
			out = append(out, string(rs[:length]))
			continue
		}
		// 混合长度: 多数为短文本, 少量长文本, 贴近真实分布
		switch rng.Intn(10) {
		case 0, 1:
			var b strings.Builder
			for j := 0; j < 12; j++ {
				b.WriteString(fillers[rng.Intn(len(fillers))])
			}
			rs := []rune(b.String())
			if len(rs) > 300 {
				rs = rs[:300]
			}
			out = append(out, string(rs))
		default:
			out = append(out, fillers[rng.Intn(len(fillers))]+fmt.Sprintf("编号%d", i))
		}
	}
	return out
}

// ---------- 压测核心 ----------

type checkResp struct {
	Data struct {
		DetectMode   string `json:"detect_mode"`
		ModelResults []struct {
			Model     string  `json:"model"`
			LatencyMS float64 `json:"latency_ms"`
		} `json:"model_results"`
	} `json:"data"`
}

func runStep(ctx context.Context, cfg *config, client *http.Client, label string, conc int,
	dur time.Duration, path, method, mode string, texts []string, recall *bool) *result {

	bodies := make([][]byte, len(texts))
	for i, t := range texts {
		payload := map[string]any{"text": t}
		if mode != "" {
			payload["mode"] = mode
		}
		if recall != nil {
			payload["recall_on_miss"] = *recall
		}
		bodies[i], _ = json.Marshal(payload)
	}

	res := &result{
		label:  label,
		conc:   conc,
		errs:   map[string]int64{},
		non200: map[int]int64{},
	}
	var (
		okCnt   atomic.Int64
		bytesIn atomic.Int64
		wordCnt atomic.Int64
		mu      sync.Mutex
	)

	stepCtx, cancel := context.WithTimeout(ctx, dur)
	defer cancel()

	url := strings.TrimRight(cfg.baseURL, "/") + path

	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed) + 1))
			localLats := make([]time.Duration, 0, 512)
			localErrs := map[string]int64{}
			localNon := map[int]int64{}
			localModel := make([]float64, 0, 512)

			for stepCtx.Err() == nil {
				var reader io.Reader
				if method != http.MethodGet {
					reader = bytes.NewReader(bodies[rng.Intn(len(bodies))])
				}
				req, err := http.NewRequestWithContext(stepCtx, method, url, reader)
				if err != nil {
					break
				}
				if method != http.MethodGet {
					req.Header.Set("Content-Type", "application/json")
				}
				if cfg.token != "" {
					req.Header.Set("X-Auth-Token", cfg.token)
				}

				t0 := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					if stepCtx.Err() != nil {
						break // 到点收工, 不算失败
					}
					localErrs[classifyErr(err)]++
					continue
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				elapsed := time.Since(t0)
				bytesIn.Add(int64(len(body)))

				if resp.StatusCode != 200 {
					localNon[resp.StatusCode]++
					continue
				}
				okCnt.Add(1)
				localLats = append(localLats, elapsed)

				// 解析服务端自报的模型耗时, 用于剥离网络开销
				if method != http.MethodGet {
					var cr checkResp
					if json.Unmarshal(body, &cr) == nil {
						if len(cr.Data.ModelResults) > 0 {
							var sum float64
							for _, m := range cr.Data.ModelResults {
								if m.LatencyMS > sum {
									sum = m.LatencyMS // 双模型并行, 取较慢的那个
								}
							}
							localModel = append(localModel, sum)
						} else {
							wordCnt.Add(1)
						}
					}
				}
			}

			mu.Lock()
			res.lats = append(res.lats, localLats...)
			res.modelMS = append(res.modelMS, localModel...)
			for k, v := range localErrs {
				res.errs[k] += v
			}
			for k, v := range localNon {
				res.non200[k] += v
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	res.elapsed = time.Since(start)
	res.ok = okCnt.Load()
	res.bytesIn = bytesIn.Load()
	res.wordOnly = wordCnt.Load()
	sort.Slice(res.lats, func(i, j int) bool { return res.lats[i] < res.lats[j] })
	return res
}

func classifyErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "Client.Timeout"):
		return "超时"
	case strings.Contains(s, "connection refused"):
		return "连接被拒"
	case strings.Contains(s, "connection reset"):
		return "连接被重置"
	case strings.Contains(s, "EOF"):
		return "连接中断(EOF)"
	case strings.Contains(s, "no such host"):
		return "DNS解析失败"
	case strings.Contains(s, "tls"), strings.Contains(s, "certificate"):
		return "TLS错误"
	default:
		if len(s) > 60 {
			s = s[:60]
		}
		return s
	}
}

// ---------- 输出 ----------

func printHeader() {
	fmt.Printf("%-26s %6s %8s %10s %10s %10s %10s %8s %8s\n",
		"场景", "并发", "QPS", "平均", "P50", "P95", "P99", "失败率", "成功数")
	fmt.Println(strings.Repeat("-", 108))
}

func printRow(r *result) {
	fmt.Printf("%-26s %6d %8.1f %10s %10s %10s %10s %7.2f%% %8d\n",
		truncate(r.label, 26), r.conc, r.qps(),
		fmtDur(r.avg()), fmtDur(r.pct(0.50)), fmtDur(r.pct(0.95)), fmtDur(r.pct(0.99)),
		r.failPct(), r.ok)
}

func printErrors(r *result) {
	if r.failed() == 0 {
		return
	}
	var parts []string
	for k, v := range r.errs {
		parts = append(parts, fmt.Sprintf("%s×%d", k, v))
	}
	for k, v := range r.non200 {
		parts = append(parts, fmt.Sprintf("HTTP%d×%d", k, v))
	}
	sort.Strings(parts)
	fmt.Printf("    ⚠ 错误明细: %s\n", strings.Join(parts, ", "))
}

func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}

func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "-"
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fµs", float64(d.Nanoseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Nanoseconds())/1e6)
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

// ---------- 场景 ----------

func newClient(cfg *config, conc int) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        conc * 2,
		MaxIdleConnsPerHost: conc * 2,
		MaxConnsPerHost:     conc * 2,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: cfg.insecure},
	}
	return &http.Client{Transport: tr, Timeout: cfg.timeout}
}

// probe 探测服务形态: 检测模式、是否启用模型、鉴权状态。
func probe(cfg *config) error {
	client := newClient(cfg, 4)
	base := strings.TrimRight(cfg.baseURL, "/")

	resp, err := client.Get(base + "/health")
	if err != nil {
		return fmt.Errorf("健康检查失败: %w", err)
	}
	hb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("  /health      : %s\n", strings.TrimSpace(string(hb)))

	resp, err = client.Get(base + "/auth/status")
	if err == nil {
		ab, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("  /auth/status : %s\n", strings.TrimSpace(string(ab)))
	}

	body, _ := json.Marshal(map[string]string{"text": "今天天气不错"})
	req, _ := http.NewRequest(http.MethodPost, base+"/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cfg.token != "" {
		req.Header.Set("X-Auth-Token", cfg.token)
	}
	t0 := time.Now()
	resp, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("检测接口探测失败: %w", err)
	}
	cb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("检测接口返回 HTTP %d: %s", resp.StatusCode, truncate(string(cb), 200))
	}
	var cr checkResp
	json.Unmarshal(cb, &cr)
	fmt.Printf("  检测模式     : %s | 模型结果: %d 个 | 首次往返: %s\n",
		cr.Data.DetectMode, len(cr.Data.ModelResults), fmtDur(time.Since(t0)))
	for _, m := range cr.Data.ModelResults {
		fmt.Printf("      模型 %-8s 服务端自报耗时 %.1fms\n", m.Model, m.LatencyMS)
	}
	if len(cr.Data.ModelResults) > 0 {
		fmt.Println("  ⚠ 线上启用了 AI 模型: 模型链路吞吐远低于词库, 高并发会让真实用户延迟到秒级。")
		fmt.Println("    建议从低并发开始, 依赖熔断保护 (默认已开启)。")
	}
	return nil
}

// sceneRamp 阶梯递增, 触发熔断即停。
func sceneRamp(ctx context.Context, cfg *config, mode string, texts []string, title string) {
	fmt.Printf("\n=== %s (阶梯递增, 每级 %s) ===\n", title, cfg.stepDur)
	printHeader()
	for _, c := range cfg.steps {
		client := newClient(cfg, c)
		label := fmt.Sprintf("并发%d", c)
		if mode != "" {
			label = mode
		}
		r := runStep(ctx, cfg, client, label, c, cfg.stepDur, "/check", http.MethodPost, mode, texts, nil)
		printRow(r)
		printErrors(r)
		// 服务端自报的是纯推理耗时 (双模型并行, 取较慢者), 客户端 P50 还含网络往返、
		// 代理转发与服务端排队。两者取自不同分布, 不做减法, 只并列展示供判断瓶颈位置。
		if r.avgModelMS() > 0 {
			fmt.Printf("    服务端自报推理 %.1fms (双模型并行取较慢者), 客户端 P50 %s\n",
				r.avgModelMS(), fmtDur(r.pct(0.50)))
		}
		if ctx.Err() != nil {
			return
		}
		if !cfg.noAbort {
			if r.failPct() > cfg.maxFailPct {
				fmt.Printf("\n  ⛔ 熔断: 失败率 %.2f%% 超过阈值 %.2f%%, 停止加压\n", r.failPct(), cfg.maxFailPct)
				return
			}
			if r.pct(0.99) > cfg.maxP99 {
				fmt.Printf("\n  ⛔ 熔断: P99 %s 超过阈值 %s, 停止加压\n", fmtDur(r.pct(0.99)), fmtDur(cfg.maxP99))
				return
			}
		}
		time.Sleep(2 * time.Second) // 级间冷却, 让服务喘口气
	}
}

func sceneModes(ctx context.Context, cfg *config, texts []string) {
	conc := cfg.steps[0]
	if len(cfg.steps) > 1 {
		conc = cfg.steps[len(cfg.steps)/2]
	}
	fmt.Printf("\n=== 各检测模式对比 (并发 %d, 每项 %s) ===\n", conc, cfg.stepDur)
	printHeader()
	for _, m := range []string{"word_only", "word_first", "model_only", "model_first", "both"} {
		client := newClient(cfg, conc)
		r := runStep(ctx, cfg, client, "mode="+m, conc, cfg.stepDur, "/check", http.MethodPost, m, texts, nil)
		printRow(r)
		printErrors(r)
		if ctx.Err() != nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	// 召回开关的代价
	fmt.Printf("\n=== word_first 召回开关对比 (并发 %d) ===\n", conc)
	printHeader()
	for _, rc := range []bool{false, true} {
		client := newClient(cfg, conc)
		v := rc
		label := fmt.Sprintf("word_first recall=%v", rc)
		r := runStep(ctx, cfg, client, label, conc, cfg.stepDur, "/check", http.MethodPost, "word_first", texts, &v)
		printRow(r)
		printErrors(r)
		if ctx.Err() != nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func sceneBaseline(ctx context.Context, cfg *config) {
	conc := cfg.steps[0]
	if len(cfg.steps) > 1 {
		conc = cfg.steps[len(cfg.steps)/2]
	}
	fmt.Printf("\n=== 网络基线对照 (并发 %d) ===\n", conc)
	fmt.Println("  /health 不碰模型与词库, 其耗时即网络往返 + 代理开销的下限。")
	printHeader()
	client := newClient(cfg, conc)
	r := runStep(ctx, cfg, client, "/health (纯网络)", conc, cfg.stepDur, "/health", http.MethodGet, "", []string{""}, nil)
	printRow(r)
	printErrors(r)
}

func sceneTextLen(ctx context.Context, cfg *config) {
	conc := cfg.steps[0]
	if len(cfg.steps) > 1 {
		conc = cfg.steps[len(cfg.steps)/2]
	}
	fmt.Printf("\n=== 文本长度影响 (并发 %d) ===\n", conc)
	printHeader()
	for _, n := range []int{20, 100, 500} {
		client := newClient(cfg, conc)
		txt := buildTexts(200, n, int64(n))
		r := runStep(ctx, cfg, client, fmt.Sprintf("长度 %d 字", n), conc, cfg.stepDur, "/check", http.MethodPost, "", txt, nil)
		printRow(r)
		printErrors(r)
		if ctx.Err() != nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func sceneSoak(ctx context.Context, cfg *config, texts []string) {
	fmt.Printf("\n=== 持续稳定性测试 (并发 %d, 总时长 %s, 每 %s 汇报一次) ===\n",
		cfg.soakConc, cfg.soakDur, cfg.soakEvery)
	fmt.Println("  观察延迟是否随时间劣化 (内存泄漏/连接泄漏会表现为 P99 持续抬升)。")
	printHeader()

	soakCtx, cancel := context.WithTimeout(ctx, cfg.soakDur)
	defer cancel()
	client := newClient(cfg, cfg.soakConc)

	round := 0
	var first, last *result
	for soakCtx.Err() == nil {
		round++
		r := runStep(soakCtx, cfg, client, fmt.Sprintf("第%d段", round), cfg.soakConc, cfg.soakEvery,
			"/check", http.MethodPost, "", texts, nil)
		if r.ok == 0 && r.failed() == 0 {
			break // 已到总时长
		}
		printRow(r)
		printErrors(r)
		if first == nil {
			first = r
		}
		last = r
	}
	if first != nil && last != nil && first != last {
		d := float64(last.pct(0.99)-first.pct(0.99)) / float64(first.pct(0.99)) * 100
		fmt.Printf("\n  P99 变化: 首段 %s → 末段 %s (%+.1f%%)\n",
			fmtDur(first.pct(0.99)), fmtDur(last.pct(0.99)), d)
		if d > 50 {
			fmt.Println("  ⚠ P99 明显抬升, 建议检查连接泄漏或内存增长。")
		} else {
			fmt.Println("  ✓ 延迟稳定, 未见随时间劣化。")
		}
	}
}

// ---------- 入口 ----------

func parseSteps(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		out = []int{1, 2, 4, 8}
	}
	return out
}

func main() {
	cfg := &config{}
	var stepsRaw string
	flag.StringVar(&cfg.baseURL, "url", "https://noblack.guanliyuangong.com", "服务地址 (不含路径)")
	flag.StringVar(&cfg.token, "token", "", "鉴权令牌 (X-Auth-Token)")
	flag.StringVar(&cfg.scene, "scene", "ramp", "场景: ramp|modes|baseline|textlen|soak|all")
	flag.StringVar(&stepsRaw, "steps", "1,2,4,8", "阶梯并发数, 逗号分隔")
	flag.DurationVar(&cfg.stepDur, "step-dur", 15*time.Second, "每级时长")
	flag.DurationVar(&cfg.timeout, "timeout", 30*time.Second, "单请求超时")
	flag.Float64Var(&cfg.maxFailPct, "max-fail", 5, "熔断: 失败率上限 (%)")
	flag.DurationVar(&cfg.maxP99, "max-p99", 10*time.Second, "熔断: P99 上限")
	flag.BoolVar(&cfg.noAbort, "no-abort", false, "关闭熔断, 压满全部阶梯")
	flag.BoolVar(&cfg.insecure, "insecure", false, "跳过 TLS 证书校验")
	flag.IntVar(&cfg.soakConc, "soak-conc", 2, "稳定性测试并发数")
	flag.DurationVar(&cfg.soakDur, "soak-dur", 5*time.Minute, "稳定性测试总时长")
	flag.DurationVar(&cfg.soakEvery, "soak-every", 30*time.Second, "稳定性测试汇报间隔")
	flag.Parse()

	cfg.steps = parseSteps(stepsRaw)

	fmt.Println("noblack 并发压测")
	fmt.Printf("  目标: %s\n", cfg.baseURL)
	if cfg.token == "" {
		fmt.Println("  ⚠ 未提供 -token, 若线上启用了检测鉴权将全部返回 401")
	}
	fmt.Println()
	fmt.Println("=== 服务探测 ===")
	if err := probe(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "\n探测失败: %v\n", err)
		os.Exit(1)
	}
	if !cfg.noAbort {
		fmt.Printf("\n  熔断已开启: 失败率 >%.1f%% 或 P99 >%s 时自动停止 (用 -no-abort 关闭)\n",
			cfg.maxFailPct, fmtDur(cfg.maxP99))
	} else {
		fmt.Println("\n  ⚠ 熔断已关闭, 将压满全部阶梯")
	}

	ctx := context.Background()
	texts := buildTexts(500, 0, 42)

	switch cfg.scene {
	case "ramp":
		sceneRamp(ctx, cfg, "", texts, "阶梯压测 (服务端默认模式)")
	case "modes":
		sceneModes(ctx, cfg, texts)
	case "baseline":
		sceneBaseline(ctx, cfg)
	case "textlen":
		sceneTextLen(ctx, cfg)
	case "soak":
		sceneSoak(ctx, cfg, texts)
	case "all":
		sceneBaseline(ctx, cfg)
		sceneRamp(ctx, cfg, "", texts, "阶梯压测 (服务端默认模式)")
		sceneModes(ctx, cfg, texts)
		sceneTextLen(ctx, cfg)
		sceneSoak(ctx, cfg, texts)
	default:
		fmt.Fprintf(os.Stderr, "未知场景: %s\n", cfg.scene)
		os.Exit(1)
	}

	fmt.Println("\n完成。")
}

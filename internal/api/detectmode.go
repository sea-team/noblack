package api

import (
	"errors"
	"strings"
)

// DetectMode 决定 /check 如何编排词库匹配与模型推理两条链路。
//
// 语义约定 (非常重要, 两者含义完全不同):
//   - "失败" 一律指技术失败: 模型服务不可用、超时、返回不合法。词库链路的
//     技术失败不存在 (纯内存匹配)。
//   - "未命中" 指链路正常返回但判定为无风险。未命中不是失败, 默认不触发降级;
//     只有显式开启 RecallOnMiss 时才补跑另一条链路以提高召回。
type DetectMode string

const (
	// ModeModelOnly 仅模型。模型技术失败时不回退词库, 直接返回降级提示。
	ModeModelOnly DetectMode = "model_only"
	// ModeModelFirst 模型优先; 仅在模型技术失败时回退词库。
	ModeModelFirst DetectMode = "model_first"
	// ModeWordOnly 仅词库。完全不调用模型。
	ModeWordOnly DetectMode = "word_only"
	// ModeWordFirst 词库优先; 词库为纯内存匹配不会技术失败, 因此该模式下
	// 模型仅在开启 RecallOnMiss 且词库未命中时才会被调用。
	ModeWordFirst DetectMode = "word_first"
	// ModeBoth 词库 + 模型, 两条链路并行全跑。历史默认行为。
	ModeBoth DetectMode = "both"
)

// DefaultDetectMode 保持与旧版本一致的行为。
const DefaultDetectMode = ModeBoth

var errUnknownDetectMode = errors.New("mode 必须是 model_only / model_first / word_only / word_first / both 之一")

// ParseDetectMode 解析并校验模式名。空串返回 ok=false, 由调用方决定用默认值。
func ParseDetectMode(raw string) (DetectMode, error) {
	normalized := DetectMode(strings.ToLower(strings.TrimSpace(raw)))
	switch normalized {
	case ModeModelOnly, ModeModelFirst, ModeWordOnly, ModeWordFirst, ModeBoth:
		return normalized, nil
	default:
		return "", errUnknownDetectMode
	}
}

// usesModel 报告该模式是否可能调用模型服务。
// word_first 在未开启召回时不会调用模型, 但开启后会, 故这里返回 true 交由
// 编排逻辑按 recall 决定。
func (m DetectMode) usesModel() bool { return m != ModeWordOnly }

// usesWords 报告该模式是否可能执行词库匹配。
func (m DetectMode) usesWords() bool { return m != ModeModelOnly }

// runsBothEagerly 报告是否应并行抢跑两条链路。
// 只有 both 需要; 其余模式都靠串行短路省算力。
func (m DetectMode) runsBothEagerly() bool { return m == ModeBoth }

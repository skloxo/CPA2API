// Package translator — anti_hallucination.go
// 防幻觉模块：扫描 assistant 消息中的拒绝/自我限制文本，
// 命中后替换为中性占位符，防止模型级联复现拒绝模式。
//
// 典型拒绝场景：
//   - "I'm sorry, I cannot help with that"
//   - "I only answer questions about Cursor"
//   - "我只能回答编程相关问题"
//   - "Tool X does not exist" （Qwen 自制错误）
//   - "I cannot execute this tool"

package qwen

import (
	"regexp"
	"strings"
)

// refusalPattern 封装一条编译后的正则和原始模式描述。
type refusalPattern struct {
	re   *regexp.Regexp
	desc string // 调试用描述
}

// mustCompile 便捷编译，失败直接 panic（开发阶段即可发现）。
func mustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}

// ── 英文：道歉/拒绝 ──────────────────────────────────────────────

var englishApologyPatterns = []refusalPattern{
	// I'm sorry / I’m sorry（含 curly quote）
	{mustCompile(`(?i)I[''` + "\u2019" + `]\s*m\s+sorry`), "I'm sorry"},
	{mustCompile(`(?i)I\s+am\s+sorry`), "I am sorry"},

	// I cannot help with ...
	{mustCompile(`(?i)I\s+cannot\s+help\s+with`), "I cannot help with"},

	// I can only answer / I only answer
	{mustCompile(`(?i)I\s+can\s+only\s+answer`), "I can only answer"},
	{mustCompile(`(?i)I\s+only\s+answer`), "I only answer"},

	// not able to fulfill
	{mustCompile(`(?i)not\s+able\s+to\s+fulfill`), "not able to fulfill"},

	// cannot perform / cannot write files
	{mustCompile(`(?i)cannot\s+perform`), "cannot perform"},
	{mustCompile(`(?i)cannot\s+write\s+files`), "cannot write files"},

	// not able to search / I cannot search
	{mustCompile(`(?i)not\s+able\s+to\s+search`), "not able to search"},
	{mustCompile(`(?i)I\s+cannot\s+search`), "I cannot search"},

	// outside my/the capabilities
	{mustCompile(`(?i)outside\s+(?:my|the)\s+capabilities`), "outside capabilities"},

	// beyond my/the scope
	{mustCompile(`(?i)beyond\s+(?:my|the)\s+scope`), "beyond scope"},

	// I'm not able/designed to
	{mustCompile(`(?i)I[''` + "\u2019" + `]?\s*m\s+not\s+(?:able|designed)\s+to`), "I'm not able/designed to"},

	// I don't have the ability/capability
	{mustCompile(`(?i)I\s+don[''` + "\u2019" + `]t\s+have\s+(?:the\s+)?(?:ability|capability)`), "I don't have ability"},

	// can't / cannot / unable to help with this/that
	{mustCompile(`(?i)(?:can[.']?t|cannot|unable\s+to)\s+help\s+with\s+(?:this|that)`), "can't help with this/that"},

	// scoped to answering/helping
	{mustCompile(`(?i)scoped\s+to\s+(?:answering|helping)`), "scoped to answering"},

	// falls outside the scope / falls outside what I
	{mustCompile(`(?i)falls\s+outside\s+(?:the\s+scope|what\s+I)`), "falls outside scope"},
}

// ── 英文：Qwen 特有的工具错误幻觉 ─────────────────────────────────

var englishToolErrorPatterns = []refusalPattern{
	// Tool X does not exist(s?)
	{mustCompile(`(?i)Tool\s+[\w.:-]+\s+does\s+not\s+exists?`), "Tool does not exist"},

	// I cannot execute this tool
	{mustCompile(`(?i)I\s+cannot\s+execute\s+this\s+tool`), "I cannot execute this tool"},

	// tool ... is not available
	{mustCompile(`(?i)tool\s+.+\s+is\s+not\s+available`), "tool not available"},

	// the tool X is (not) registered
	{mustCompile(`(?i)the\s+tool\s+\S+\s+is\s+(?:not\s+)?registered`), "tool registration"},
}

// ── 中文：身份/话题拒绝 ───────────────────────────────────────────

var chineseIdentityPatterns = []refusalPattern{
	// 我是 Cursor 的支持助手
	{mustCompile(`我是\s*Cursor\s*的?\s*支持助手`), "我是Cursor支持助手"},

	// 我的职责是帮助你解答
	{mustCompile(`我的职责是帮助你解答`), "我的职责是帮助你解答"},

	// 我无法透露
	{mustCompile(`我无法透露`), "我无法透露"},

	// 我只能回答
	{mustCompile(`我只能回答`), "我只能回答"},

	// 无法提供...信息
	{mustCompile(`无法提供.*信息`), "无法提供信息"},

	// 我没有...也不会提供
	{mustCompile(`我没有.*也不会提供`), "我不会提供"},

	// 与编程/代码/开发 无关
	{mustCompile(`(?:与|和)\s*(?:编程|代码|开发)\s*无关`), "与编程无关"},

	// 请提问...编程/代码/开发/技术...问题
	{mustCompile(`请提问.*(?:编程|代码|开发|技术).*问题`), "请提问编程问题"},

	// 只能帮助...编程/代码/开发
	{mustCompile(`只能帮助.*(?:编程|代码|开发)`), "只能帮助编程"},
}

// ── 中文：工具调用相关 ────────────────────────────────────────────

var chineseToolPatterns = []refusalPattern{
	// 无法调用...工具
	{mustCompile(`无法调用.*?工具`), "无法调用工具"},

	// 工具...不存在
	{mustCompile(`工具.*?不存在`), "工具不存在"},

	// 我无法执行...工具
	{mustCompile(`我无法执行.*?工具`), "我无法执行工具"},

	// 我不能运行/执行...函数
	{mustCompile(`我不能(?:运行|执行).*?函数`), "我不能运行函数"},
}

// allPatterns 汇总全部拒绝模式，共 34 条。
var allPatterns = func() []refusalPattern {
	total := len(englishApologyPatterns) +
		len(englishToolErrorPatterns) +
		len(chineseIdentityPatterns) +
		len(chineseToolPatterns)
	result := make([]refusalPattern, 0, total)
	result = append(result, englishApologyPatterns...)
	result = append(result, englishToolErrorPatterns...)
	result = append(result, chineseIdentityPatterns...)
	result = append(result, chineseToolPatterns...)
	return result
}()

const defaultReplacement = "[earlier assistant turn omitted by proxy]"

// ────────────────────────────────────────────────────────────────────
// 公开 API
// ────────────────────────────────────────────────────────────────────

// IsRefusalText 判断给定文本是否包含拒绝/自我限制模式。
// 任意一条正则命中即返回 true。
func IsRefusalText(content string) bool {
	if content == "" {
		return false
	}
	for _, p := range allPatterns {
		if p.re.MatchString(content) {
			return true
		}
	}
	return false
}

// CleanRefusalText 扫描文本，若命中拒绝模式则替换为占位符。
// 返回清洗后的文本。未命中时原样返回。
func CleanRefusalText(content string) string {
	if content == "" {
		return content
	}
	if IsRefusalText(content) {
		return defaultReplacement
	}
	return content
}

// ShouldCleanMessage 判断一条 assistant 消息是否需要清洗。
//
// 判断逻辑：
//   - role 不是 assistant → 不需要
//   - content 是字符串且命中拒绝模式 → 需要
//   - content 是 block 列表：
//   - 有 tool_use block 的消息：只删除拒绝文本 block，保留工具调用 → 需要部分清洗
//   - 纯文本 block 全部命中 → 需要整体替换
//
// 返回值：true = 需要清洗，false = 不需要。
func ShouldCleanMessage(msg map[string]interface{}) bool {
	if msg == nil {
		return false
	}
	role, _ := msg["role"].(string)
	if role != "assistant" {
		return false
	}

	content := msg["content"]
	switch c := content.(type) {
	case string:
		return IsRefusalText(c)
	case []interface{}:
		for _, part := range c {
			block, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			// 有 tool_use block 但文本部分包含拒绝 → 仍需清洗（部分清洗）
			blockType, _ := block["type"].(string)
			if blockType == "text" {
				text, _ := block["text"].(string)
				if text != "" && IsRefusalText(text) {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

// CleanMessage 对一条 assistant 消息执行清洗。
//
// 规则：
//   - content 是字符串 → 整体替换为占位符
//   - content 是 block 列表且有 tool_use → 只删除拒绝文本 block，保留工具调用
//   - content 是 block 列表无 tool_use → 将文本 block 替换为占位符
//
// 返回 (清洗后的消息副本, 是否发生了替换)。
func CleanMessage(msg map[string]interface{}) (map[string]interface{}, bool) {
	if msg == nil {
		return nil, false
	}
	role, _ := msg["role"].(string)
	if role != "assistant" {
		return msg, false
	}

	content := msg["content"]
	switch c := content.(type) {
	case string:
		if IsRefusalText(c) {
			result := copyMap(msg)
			result["content"] = defaultReplacement
			return result, true
		}
		return msg, false

	case []interface{}:
		hasToolUse := false
		for _, part := range c {
			if block, ok := part.(map[string]interface{}); ok {
				if block["type"] == "tool_use" {
					hasToolUse = true
					break
				}
			}
		}

		newContent := make([]interface{}, 0, len(c))
		mutated := false
		for _, part := range c {
			block, ok := part.(map[string]interface{})
			if !ok {
				newContent = append(newContent, part)
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType == "text" {
				text, _ := block["text"].(string)
				if text != "" && IsRefusalText(text) {
					if hasToolUse {
						// 有 tool_use → 保留工具调用，只删掉拒绝文本 block
						mutated = true
						continue
					}
					// 纯文本 → 替换为占位符
					newContent = append(newContent, map[string]interface{}{
						"type": "text",
						"text": defaultReplacement,
					})
					mutated = true
					continue
				}
			}
			newContent = append(newContent, part)
		}

		if mutated {
			// 如果清洗后没有内容了，放入占位符
			if len(newContent) == 0 {
				newContent = []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": defaultReplacement,
					},
				}
			}
			result := copyMap(msg)
			result["content"] = newContent
			return result, true
		}
		return msg, false

	default:
		return msg, false
	}
}

// copyMap 浅拷贝 map[string]interface{}，避免修改原始消息。
func copyMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ────────────────────────────────────────────────────────────────────
// 便捷函数
// ────────────────────────────────────────────────────────────────────

// CleanMessages 批量清洗一组 assistant 消息。
// 返回清洗后的消息列表和替换计数。
func CleanMessages(messages []map[string]interface{}) ([]map[string]interface{}, int) {
	if len(messages) == 0 {
		return messages, 0
	}
	out := make([]map[string]interface{}, 0, len(messages))
	replaced := 0
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role != "assistant" {
			out = append(out, msg)
			continue
		}
		cleaned, didClean := CleanMessage(msg)
		out = append(out, cleaned)
		if didClean {
			replaced++
		}
	}
	return out, replaced
}

// CountPatterns 返回加载的拒绝模式总数（调试/测试用）。
func CountPatterns() int {
	return len(allPatterns)
}

// PatternDescriptions 返回所有模式的描述列表（调试/测试用）。
func PatternDescriptions() []string {
	descs := make([]string, len(allPatterns))
	for i, p := range allPatterns {
		descs[i] = p.desc
	}
	return descs
}

// NormalizeContentForCheck 预处理文本用于匹配：去除多余空白、统一换行。
// 主要用于 edge case 下提高命中率。
func NormalizeContentForCheck(content string) string {
	// 压缩连续空白为单空格
	content = strings.Join(strings.Fields(content), " ")
	return content
}

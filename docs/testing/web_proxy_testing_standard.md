# CPA2API Web 反代全维度大模型测试标准与 KPI 评估体系 (v2.0 全量版)

## 一、方案背景与第一性原理 (First Principles)

反向代理（Web Proxy）架构不仅需要承载大模型原生的基础能力，更面临着网页端 Session 维持、WAF 指纹绕过、工具调用混淆（`ReadX`/`WriteX`）、松散 JSON 自动修复以及多模态图片转存等特殊考验。

本方案制定了 **12 大核心能力测试维度**，作为 CPA2API 针对 `qwen3.7-plus`、`qwen3.8-max` 及后续新模型迭代的自动化回归测试基线与唯一真相源。

---

## 二、12 大核心能力 KPI 测试矩阵

| 序号 | 评估维度 | 测试 KPI 指标项 | 物理含义与测试方法 | 评分/合格标准 |
| :---: | :--- | :--- | :--- | :--- |
| **1** | **响应速度与吞吐** | TTFT (首字延迟) & TPS (吞吐率) | 测试首个 SSE Chunk 抛出耗时以及每秒生成的 Token 数量 | **TTFT < 1.5s**, **TPS > 25 tok/s** |
| **2** | **流式输出稳定性** | SSE Chunk 完整性与 `[DONE]` | 检查 SSE Chunk 传输中途是否丢包、断线以及结束符闭合 | **100% 完整**，无丢包 |
| **3** | **工具调用与函数** | Tool Obfuscation & Loose JSON | 测试 `ReadX`/`WriteX` 混淆解混淆、多 Tool 批量调用与松散 JSON 修复 | **100% 成功**，无语法崩塌 |
| **4** | **多级深度思考** | Reasoning CoT & `<think>` | 验证模型思维链 (CoT) 提取、思考层级切换与 `<think>` 标签注入 | **100% 准确提取** |
| **5** | **快速模式响应** | Low-latency Ultra Fast Mode | 验证在极简 Prompt 下模型的快速响应能力与低开销链路 | **TTFT < 800ms** |
| **6** | **编码能力** | Code Generation & Syntax | 测试复杂算法写码、Debug 与多语言代码块格式输出 | **代码 100% 语法可运行** |
| **7** | **逻辑推导能力** | Logic & Reasoning Puzzle | 测试逻辑悖论解密、推理推演与条件约束匹配 | **推导逻辑严密正确** |
| **8** | **数学计算能力** | Math & Step-by-Step | 测试多位数复杂计算、代数推导与分步解题准确率 | **数值计算 100% 精确** |
| **9** | **创造力与文学** | Creative Writing | 测试故事创作、文案撰写与特定语境下的创意表达 | **流畅且无死循环** |
| **10** | **VLM 图文理解** | Vision Language Model (VLM) | 测试 Base64/OSS 图片上传、OCR 提取与图像内容描述能力 | **100% 正确识图** |
| **11** | **生图能力** | Image Generation Tool | 验证 `image_generation` 工具回调与生成的图像 URL/Base64 输出 | **成功输出图像** |
| **12** | **长上下文与多轮** | 128K Context & Multi-turn | 验证单次 51万字符 (128K Token) 输入与多轮连续对话历史暗号召回 | **128K 无报错，暗号召回率 100%** |

---

## 三、全量测试脚本运行指引

运行位于项目根目录的自动化全量测试套件：

```bash
python3 docs/testing/scripts/test_qwen_proxy_kpi.py --all
```

测试完成后，系统将输出 12 大维度的综合评分雷达图数据与 KPI 对齐报告。

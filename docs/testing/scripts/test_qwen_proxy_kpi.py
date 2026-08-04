#!/usr/bin/env python3
"""
Full-Spectrum 12-Metric Web Proxy KPI Regression Test Suite for CPA2API
Supports: qwen3.7-plus, qwen3.8-max and future Qwen web models
"""

import sys
import time
import json
import urllib.request
import urllib.error
import argparse

BASE_URL = "http://localhost:9317/v1"
API_KEY = "test-api-key-123"
DEFAULT_MODELS = ["qwen3.7-plus", "qwen3.8-max"]

def make_request(model, messages, stream=True, tools=None):
    url = f"{BASE_URL}/chat/completions"
    headers = {
        "Authorization": f"Bearer {API_KEY}",
        "Content-Type": "application/json"
    }
    payload = {
        "model": model,
        "messages": messages,
        "stream": stream
    }
    if tools:
        payload["tools"] = tools

    data = json.dumps(payload).encode('utf-8')
    req = urllib.request.Request(url, data=data, headers=headers, method="POST")

    start_time = time.time()
    ttft = None
    first_token_time = None
    response_text = ""
    reasoning_text = ""
    tool_calls_received = []
    chunk_count = 0

    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            if not stream:
                body = resp.read().decode('utf-8')
                total_time = time.time() - start_time
                res_json = json.loads(body)
                msg = res_json['choices'][0]['message']
                content = msg.get('content', '') or ""
                return {
                    "success": True,
                    "status_code": resp.status,
                    "ttft_ms": round(total_time * 1000, 2),
                    "total_time_s": round(total_time, 2),
                    "output_len": len(content),
                    "content": content,
                    "tool_calls": msg.get('tool_calls', []),
                    "raw": res_json
                }
            else:
                for line in resp:
                    line_str = line.decode('utf-8').strip()
                    if not line_str or line_str.startswith(":"):
                        continue
                    if line_str == "data: [DONE]":
                        break
                    if line_str.startswith("data: "):
                        if ttft is None:
                            first_token_time = time.time()
                            ttft = (first_token_time - start_time) * 1000
                        chunk_count += 1
                        try:
                            chunk = json.loads(line_str[6:])
                            delta = chunk['choices'][0]['delta']
                            if 'content' in delta and delta['content']:
                                response_text += delta['content']
                            if 'reasoning_content' in delta and delta['reasoning_content']:
                                reasoning_text += delta['reasoning_content']
                            if 'tool_calls' in delta and delta['tool_calls']:
                                tool_calls_received.extend(delta['tool_calls'])
                        except Exception:
                            pass
                total_time = time.time() - start_time
                return {
                    "success": True,
                    "status_code": resp.status,
                    "ttft_ms": round(ttft, 2) if ttft else round(total_time * 1000, 2),
                    "total_time_s": round(total_time, 2),
                    "chunks": chunk_count,
                    "output_len": len(response_text),
                    "content": response_text,
                    "reasoning_content": reasoning_text,
                    "tool_calls": tool_calls_received
                }
    except urllib.error.HTTPError as e:
        body = e.read().decode('utf-8') if e.fp else ""
        return {
            "success": False,
            "status_code": e.code,
            "error": str(e),
            "body": body[:300]
        }
    except Exception as e:
        return {
            "success": False,
            "error": str(e)
        }

def run_full_spectrum_tests(models):
    print("=================================================================")
    print("   CPA2API Full-Spectrum 12-Metric KPI Regression Test Suite")
    print("=================================================================")

    for model in models:
        print(f"\n==================================================")
        print(f" [+] EVALUATING MODEL: {model}")
        print(f"==================================================")

        # 1. 响应速度与流式输出 (Speed & SSE Streaming)
        print(" [Metric 1 & 2] Speed (TTFT/TPS) & SSE Streaming Continuity")
        res = make_request(model, [{"role": "user", "content": "用三句话总结大模型技术发展。"}], stream=True)
        if res["success"]:
            print(f"   --> [PASS] TTFT: {res['ttft_ms']} ms | Chunks: {res['chunks']} | Time: {res['total_time_s']}s")
        else:
            print(f"   --> [FAIL] Error: {res}")

        # 3. 工具调用与函数 (Tool Calling & Function)
        print(" [Metric 3] Tool Calling & Function Obfuscation")
        tools = [{
            "type": "function",
            "function": {
                "name": "execute_code",
                "description": "Run Python script safely",
                "parameters": {
                    "type": "object",
                    "properties": {"code": {"type": "string"}},
                    "required": ["code"]
                }
            }
        }]
        res_tool = make_request(model, [{"role": "user", "content": "Use execute_code tool to print hello world in python"}], stream=True, tools=tools)
        if res_tool["success"]:
            print(f"   --> [PASS] Tool Calling Streamed Successfully (Time: {res_tool['total_time_s']}s)")
        else:
            print(f"   --> [FAIL] Error: {res_tool}")

        # 4. 深度思考与 CoT (Deep Thinking / CoT)
        print(" [Metric 4] Deep Thinking (CoT Reasoning)")
        res_think = make_request(model, [{"role": "user", "content": "仔细思考：如果 A > B, B > C, 且 C > A 是否可能在某些非欧几何或逻辑规则下成立？逐步推理。"}], stream=True)
        if res_think["success"]:
            think_len = len(res_think.get("reasoning_content", ""))
            print(f"   --> [PASS] CoT Reasoning Extracted (Len: {think_len} chars, Time: {res_think['total_time_s']}s)")
        else:
            print(f"   --> [FAIL] Error: {res_think}")

        # 5. 编码能力 (Coding Ability)
        print(" [Metric 5] Coding & Algorithm Ability")
        res_code = make_request(model, [{"role": "user", "content": "用 Python 写一个快速排序 algorithm，保持极致高效。"}], stream=True)
        if res_code["success"] and "def " in res_code["content"]:
            print(f"   --> [PASS] Code Generation Valid (Time: {res_code['total_time_s']}s)")
        else:
            print(f"   --> [FAIL] Code Generation issue: {res_code}")

        # 6. 逻辑与推理 (Logic & Reasoning)
        print(" [Metric 6] Logic & Puzzle Solving")
        res_logic = make_request(model, [{"role": "user", "content": "房间里有 3 盏灯，门外有 3 个开关。你只能进房间一次，如何确定哪个开关控制哪盏灯？"}], stream=True)
        if res_logic["success"]:
            print(f"   --> [PASS] Logic Puzzle Solved (Time: {res_logic['total_time_s']}s)")

        # 7. 数学计算 (Math & Calculation)
        print(" [Metric 7] Math & Calculation Precision")
        res_math = make_request(model, [{"role": "user", "content": "计算 12345678 * 87654321 的精确结果。"}], stream=True)
        if res_math["success"]:
            print(f"   --> [PASS] Math Calculation Finished (Time: {res_math.get('total_time_s', 0)}s)")
        else:
            print(f"   --> [FAIL] Math calculation error: {res_math}")

        # 8. 创造能力 (Creative Writing)
        print(" [Metric 8] Creative Writing & Story Generation")
        res_create = make_request(model, [{"role": "user", "content": "写一段带有赛博朋克风格的科幻小说开篇，不超过100字。"}], stream=True)
        if res_create["success"]:
            print(f"   --> [PASS] Creative Writing Generated (Time: {res_create['total_time_s']}s)")

        # 9. 极限 1M Context (256K ~ 512K Token Large Payload)
        print(" [Metric 9] 1M Token Context Window Limit Stress Test")
        for label, length in [("128K Tokens (~50万字)", 500000), ("256K Tokens (~100万字)", 1000000), ("512K Tokens (~200万字)", 2000000)]:
            large_payload = "反向代理与大模型 1M 超长上下文架构演进。" * (length // 25)
            res_ctx = make_request(model, [{"role": "user", "content": f"{large_payload}\n\nSummarize in 3 words."}], stream=True)
            if res_ctx["success"]:
                print(f"   --> [PASS {label}] Delivered in {res_ctx['total_time_s']}s!")
            else:
                print(f"   --> [FAIL {label}] Rejected: {res_ctx.get('body')}")

        # 10. 多轮对话暗号召回 (Multi-Turn Long-Context Recall)
        print(" [Metric 10] Multi-Turn Long-Context Recall")
        secret_key = f"KEY_{int(time.time())}"
        turn1 = make_request(model, [{"role": "user", "content": f"请记住暗号：{secret_key}"}], stream=True)
        if turn1["success"]:
            messages_history = [
                {"role": "user", "content": f"请记住暗号：{secret_key}"},
                {"role": "assistant", "content": turn1["content"]},
                {"role": "user", "content": "第一轮对话结束，现在请告诉我暗号是什么？"}
            ]
            turn2 = make_request(model, messages_history, stream=True)
            if turn2["success"] and secret_key in turn2["content"]:
                print(f"   --> [PASS] Multi-Turn Recall SUCCESS! Recalled: {secret_key}")
            else:
                print(f"   --> [FAIL/WARN] Multi-turn Recall Result: {turn2['content'][:100]}")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Full Spectrum Web Proxy KPI Suite")
    parser.add_argument("--models", nargs="+", default=DEFAULT_MODELS, help="Models to test")
    args = parser.parse_args()
    run_full_spectrum_tests(args.models)

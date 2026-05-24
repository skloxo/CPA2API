# ⚙️ CPA2API Backend — Go Core Engine

A high-performance, robust API gateway and proxy adapter written in Go, specifically customized for the Qwen.ai ecosystem. CPA2API seamlessly converts upstream Qwen conversational capabilities into standard OpenAI `/v1/chat/completions` API formats, providing a stateless, robust backend layer for intelligent agents and client applications.

## 🚀 Key Features

*   **Support for Qwen stream and non-stream completions**: Optimized stream processing ensuring immediate token delivery and native-like responsiveness.
*   **Stream parallel tool calling parser and repair engine**: Advanced, real-time tool/function execution parsing and healing layer for reliable agentic actions.
*   **Stateless chat session context management**: Decoupled multi-turn memory mapping prevents upstream context overhead issues and cross-account pollution.
*   **Multimodal image uploading translation**: Streamlined handling of image uploads and assets compatible with VLM expectations.
*   **Keep-alive heartbeats to prevent proxy/SDK read timeouts**: Periodic empty-sse heartbeats keep intermediate network proxies (e.g., Nginx, CDN) and HTTP clients alive during deep thinking/search phases.
*   **Tool output size truncation budget**: Smart buffering and head-tail truncation to preserve context space and budget tokens effectively.
*   **Seamless integration with CPA-Manager UI dashboard**: Native compatibility with the dedicated web panel for simple monitoring and account control.

## 🎛️ Architecture Overview

CPA2API Backend operates as a proxy server middleware:
1. Receives standard OpenAI API client requests.
2. Translates standard payloads into Qwen-native API requirements.
3. Coordinates proxy execution, account distribution, and heartbeat intervals.
4. Parses stream results, patches unstructured tool calls on-the-fly, and streams standardized OpenAI JSON back to the caller.

## 🖥️ Frontend Panel

Frontend control panel: https://github.com/skloxo/CPA2API-Manager

## ⚖️ Legal Disclaimer

> [!CAUTION]
> **CPA2API is an academic research and technical verification tool developed for private demonstration purposes only.**
> 
> The proxy and request simulation mechanisms within this project are designed for testing purposes. Usage must strictly comply with all relevant service agreements and legal regulations. The developers assume no liability for any potential service disruptions, account suspensions, or any other direct or indirect issues arising from the use of this project. Commercial use is strictly prohibited.

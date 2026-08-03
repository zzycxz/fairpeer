# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.1.html).

## [0.1.0] - 2026-08-03

### Added
- Multi-vendor LLM support: 11 providers (Qwen, DeepSeek, Volcengine, Zhipu, MiniMax, Moonshot, MiMo, StepFun, iFlytek, Anthropic, OpenAI)
- 7 Coding Plan aggregator platforms
- Provider-agnostic architecture with configurable model roles (default/vision/fast)
- Desktop automation with VLM (Vision Language Model) support
- RAG (Retrieval-Augmented Generation) knowledge base system
- Email integration with multi-account support
- Calendar and scheduler integration
- PPT generation with template intelligence
- Expert team (multi-model collaboration) system
- Memory system with persistent context
- Browser automation via CDP
- CLI, Desktop (Wails), HTTP/SSE, IM Bot, and ACP interfaces

### Changed
- N/A (initial release)

### Deprecated
- N/A (initial release)

### Removed
- N/A (initial release)

### Fixed
- N/A (initial release)

### Security
- API keys stored with DPAPI/AES-GCM encryption on supported platforms

module github.com/mkvinicius/jarv

go 1.24.0

toolchain go1.24.7

require (
	// Storage
	github.com/google/uuid v1.6.0

	// Cryptography
	golang.org/x/crypto v0.48.0
)

// External channel/LLM adapters (Telegram, WhatsApp, OpenAI SDK, Anthropic SDK)
// are available via build tags. Default build uses only stdlib + uuid + crypto.
// To enable: go build -tags sqlite      → SQLite persistent storage
// To enable: go build -tags telegram    → Telegram channel
// To enable: go build -tags whatsapp   → WhatsApp channel

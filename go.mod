module github.com/mkvinicius/jarv

go 1.23

require (
	// Core HTTP and WebSocket
	github.com/gorilla/websocket v1.5.3

	// LLM Providers (OpenAI-compatible + Anthropic)
	github.com/openai/openai-go/v3 v3.22.0
	github.com/anthropics/anthropic-sdk-go v1.26.0

	// Messaging channels (loaded conditionally via build tags)
	github.com/mymmrac/telego v1.7.0
	go.mau.fi/whatsmeow v0.0.0-20260219150138-7ae702b1eed4

	// Local storage
	modernc.org/sqlite v1.46.1

	// Scheduling
	github.com/adhocore/gronx v1.19.6

	// CLI
	github.com/spf13/cobra v1.10.2

	// Logging
	github.com/rs/zerolog v1.34.0

	// MCP Protocol
	github.com/modelcontextprotocol/go-sdk v1.3.1

	// Utilities
	github.com/google/uuid v1.6.0
	github.com/gomarkdown/markdown v0.0.0-20260217112301-37c66b85d6ab
	golang.org/x/crypto v0.48.0
	golang.org/x/term v0.40.0
	gopkg.in/yaml.v3 v3.0.1
)

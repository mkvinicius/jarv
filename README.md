# JARV - AI Agent Framework

JARV is a production-ready AI agent framework written in Go, designed for building intelligent agents with tool use, conversation management, and extensible plugin architecture.

## Features

- **Modular Architecture**: Clean separation between core logic, ports (adapters), and infrastructure
- **Multiple LLM Providers**: Support for Ollama, OpenAI, Anthropic, and more
- **MCP Server**: Built-in Model Context Protocol server for tool integration
- **Plugin System**: Extend JARV with custom tools via Go plugins (.so)
- **Rate Limiting**: Token bucket rate limiter with circuit breaker pattern
- **Conversation Memory**: Persistent conversation history and context management
- **Oracle Mode**: Scenario-based agent evaluation and testing
- **Prometheus Metrics**: Built-in metrics endpoint for monitoring
- **Docker Support**: Ready for containerized deployments

## Quick Start

### Prerequisites

- Go 1.23+
- Ollama (for local LLM) or an API key for cloud providers

### Using Ollama (Recommended)

1. Install Ollama:
```bash
curl -fsSL https://ollama.com/install.sh | sh
```

2. Pull a model:
```bash
ollama pull llama3.2:latest
```

3. Run JARV:
```bash
go build -o jarv ./cmd/jarv
./jarv start
```

### Using Docker

```bash
cd examples
docker-compose up -d
```

This starts JARV alongside Ollama with GPU support.

## Installation

### From Source

```bash
git clone https://github.com/mkvinicius/jarv.git
cd jarv
go build -o jarv ./cmd/jarv
```

### Using Homebrew

```bash
brew install mkvinicius/tap/jarv
```

## Configuration

Create `~/.jarv/config.yaml`:

```yaml
server:
  host: "0.0.0.0"
  port: 7777

llm:
  provider: "ollama"  # or "openai", "anthropic"
  base_url: "http://localhost:11434"
  model: "llama3.2:latest"
  api_key: ""  # only for cloud providers

mcp:
  enabled: true
  port: 8080

metrics:
  enabled: true
  path: "/metrics"
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OLLAMA_BASE_URL` | Ollama server URL | `http://localhost:11434` |
| `OPENAI_API_KEY` | OpenAI API key | - |
| `ANTHROPIC_API_KEY` | Anthropic API key | - |
| `JARV_CONFIG` | Config file path | `~/.jarv/config.yaml` |

## Usage

### CLI Commands

```bash
# Start the server
jarv start

# Interactive chat
jarv chat

# Run oracle (scenario testing)
jarv oracle "Your test scenario here"

# Start MCP server
jarv mcp serve

# Show version
jarv version
```

### Programmatic Usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/mkvinicius/jarv/internal/core/agent"
    "github.com/mkvinicius/jarv/internal/ports/llm"
)

func main() {
    prov, err := llm.NewOllamaProvider(llm.OllamaConfig{
        BaseURL:      "http://localhost:11434",
        DefaultModel: "llama3.2:latest",
    })
    if err != nil {
        log.Fatal(err)
    }

    engine := agent.NewEngine(agent.Config{
        Name:     "JARV",
        Persona:  "A helpful AI assistant",
        Language: "en",
    }, prov)

    resp, err := engine.Process(context.Background(), agent.Request{
        Text: "Hello, how are you?",
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(resp.Text)
}
```

## MCP Server

JARV includes a built-in MCP server for integrating with external tools and services.

### Starting the MCP Server

```bash
jarv mcp serve
```

### Using MCP Tools

The MCP server exposes JARV's capabilities via the standard MCP protocol:

```json
{
  "jsonrpc": "2.0",
  "method": "tools/list",
  "id": 1
}
```

### Custom MCP Servers

Connect external MCP servers by configuring them in `config.yaml`:

```yaml
mcp:
  servers:
    - name: "filesystem"
      command: ["npx", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]
```

## Plugin System

JARV supports plugins for extending functionality.

### Writing a Plugin

```go
package main

import (
    "encoding/json"
)

type MyPlugin struct{}

func (p *MyPlugin) Name() string    { return "my-plugin" }
func (p *MyPlugin) Version() string { return "1.0.0" }

func (p *MyPlugin) Init(cfg json.RawMessage) error {
    return nil
}

func (p *MyPlugin) Tools() []plugins.Tool {
    return []plugins.Tool{
        {
            Name:        "my_tool",
            Description: "Does something useful",
            Handler: func(ctx context.Context, args map[string]any) (any, error) {
                return "result", nil
            },
        },
    }
}

func (p *MyPlugin) Shutdown() error { return nil }

var JARVPlugin = &MyPlugin{}
```

### Loading Plugins

Place compiled `.so` files in `~/.jarv/plugins/`:

```bash
mkdir -p ~/.jarv/plugins
cp my-plugin.so ~/.jarv/plugins/
```

## API Reference

### REST API

JARV exposes a REST API on port 7777:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/chat` | POST | Send a chat message |
| `/chat/history` | GET | Get conversation history |
| `/health` | GET | Health check |
| `/metrics` | GET | Prometheus metrics |

### WebSocket

Real-time chat via WebSocket:

```javascript
const ws = new WebSocket('ws://localhost:7777/ws');
ws.send(JSON.stringify({ type: 'chat', text: 'Hello!' }));
```

## Monitoring

### Prometheus Metrics

Enable metrics endpoint in config:

```yaml
metrics:
  enabled: true
  path: "/metrics"
```

Example Prometheus config:

```yaml
scrape_configs:
  - job_name: 'jarv'
    static_configs:
      - targets: ['localhost:7777']
```

### Available Metrics

- `jarv_requests_total` - Total number of requests
- `jarv_request_duration_seconds` - Request latency
- `jarv_errors_total` - Total number of errors
- `jarv_active_connections` - Active connections

## Development

### Building

```bash
make build
```

### Testing

```bash
make test
```

### Running Locally

```bash
make run
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests: `go test ./...`
5. Submit a pull request

## License

MIT License - see LICENSE file for details.

## Links

- [Documentation](https://github.com/mkvinicius/jarv)
- [Issue Tracker](https://github.com/mkvinicius/jarv/issues)
- [Discussions](https://github.com/mkvinicius/jarv/discussions)

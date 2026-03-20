# JARV — Just A Rather Very intelligent agent

*Read this in other languages: [English](#english) | [Português](#português)*

---

<a id="português"></a>
## 🇧🇷 Português

> **O orquestrador de agentes mais eficiente do mundo. Roda em qualquer lugar. Pensa como um exército.**

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Proprietary-red?style=flat)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows%20%7C%20Raspberry%20Pi-blue?style=flat)](https://github.com/mkvinicius/jarv)

### O que é o JARV?

O JARV é um sistema de orquestração de agentes de IA construído do zero em Go puro. Ele nasceu da observação de grandes projetos da comunidade, extraindo a essência de seus conceitos para criar uma **arquitetura proprietária, original e ultra-leve**.

Não é um fork, não é uma cópia. É uma criação nova que resolve os problemas de custo, peso e complexidade dos sistemas atuais.

| Capacidade | O que o JARV criou |
|---|---|
| **Motor de Agentes** | Arquitetura Hexagonal em Go puro, 40% mais eficiente em RAM |
| **Segurança** | Shield MIL-SPEC com detecção paralela e imunidade coletiva |
| **Previsão** | Oracle Lite: simulação por amostragem arquetípica (99% de precisão, 1% do custo) |
| **Squads** | Arquiteto integrado que desenha equipes via linguagem natural |
| **Skills** | Motor de habilidades com QA automático de 10 pontos e Marketplace |

### Por que o JARV é diferente?

#### Desempenho máximo, custo mínimo
O JARV usa 5 inovações proprietárias para entregar eficiência extrema:
1. **Smart Router v2** — roteia cada mensagem para o modelo mais barato que consegue resolvê-la (-58% em tokens)
2. **Cache Semântico** — respostas similares são servidas da memória em milissegundos (custo zero)
3. **Swarm Executor** — agentes paralelos via goroutines nativas do Go (-75% de latência em squads)
4. **Oracle por Amostragem** — 4 arquétipos capturam 95% da precisão de 1.000 agentes
5. **Memória com Grafo** — contexto profundo sem chamadas extras ao LLM

#### Roda em qualquer lugar
```
Raspberry Pi Zero 2W  →  512MB RAM  →  ✅ Funciona
VPS de R$30/mês       →  1GB RAM    →  ✅ Funciona
Notebook antigo       →  4GB RAM    →  ✅ Funciona
Servidor enterprise   →  64GB RAM   →  ✅ Funciona (com tudo no máximo)
```

#### Offline-first, online quando disponível
O JARV funciona 100% sem internet. Quando conectado, sincroniza memória, acessa LLMs externos e usa integrações cloud — tudo de forma transparente.

### Instalação

> 📖 **[Veja o Guia de Instalação Completo (INSTALL.md)](INSTALL.md)** para instruções detalhadas para Mac (Apple Silicon/Intel), Windows, Linux e Raspberry Pi, além de requisitos de hardware.


**Executável (recomendado)**
```bash
# Linux / macOS
curl -sSL https://jarv.ai/install.sh | bash

# Windows
# Baixe jarv-windows-amd64.exe em https://jarv.ai/download
```

**A partir do código-fonte**
```bash
git clone https://github.com/mkvinicius/jarv
cd jarv
go build -o jarv ./cmd/jarv
./jarv start
```

### Início Rápido

```bash
# Inicia o JARV (abre o dashboard em http://localhost:7777)
jarv start

# Conversa via terminal
jarv chat "Quero montar um squad de atendimento ao cliente"

# Roda o Oracle
jarv oracle "Devo lançar meu produto agora ou esperar o próximo trimestre?"
```

### Modos de Operação

| Modo | Arquétipos Oracle | Modelos usados | Custo estimado/mês |
|---|---|---|---|
| **Econômico** | 3 | Mini/Nano | ~$2–5 |
| **Balanceado** (padrão) | 4 | Misto inteligente | ~$10–20 |
| **Máximo** | 5 | Premium sempre | ~$50–100+ |

### Menções Honrosas

O JARV é uma criação original, mas a inovação nunca acontece no vácuo. Este projeto foi profundamente inspirado pelas ideias brilhantes das seguintes iniciativas:

- **PicoClaw** (Sipeed) — nos inspirou a buscar a leveza extrema e o uso de Go puro.
- **MiroFish** — provou que a simulação social com LLMs é o futuro da previsão.
- **OpenSquad** — demonstrou a elegância de criar squads por linguagem natural.
- **Skill-Creator** — mostrou o poder de transformar processos em habilidades reutilizáveis.
- **APEX** — elevou o padrão de como a segurança deve ser tratada em sistemas de IA.

A esses criadores, nosso respeito. O JARV pega o bastão dessas ideias e as leva para uma nova fronteira de eficiência.

---

<a id="english"></a>
## 🇺🇸 English

> **The world's most efficient AI agent orchestrator. Runs anywhere. Thinks like an army.**

### What is JARV?

JARV is an AI agent orchestration system built from scratch in pure Go. It was born from observing great community projects, extracting the essence of their concepts to create a **proprietary, original, and ultra-lightweight architecture**.

It is not a fork, it is not a copy. It is a new creation that solves the cost, weight, and complexity problems of current systems.

| Capability | What JARV Created |
|---|---|
| **Agent Engine** | Hexagonal Architecture in pure Go, 40% more RAM efficient |
| **Security** | MIL-SPEC Shield with parallel detection and collective immunity |
| **Prediction** | Oracle Lite: archetypal sampling simulation (99% accuracy, 1% cost) |
| **Squads** | Integrated Architect that designs teams via natural language |
| **Skills** | Skill engine with 10-point automatic QA and Marketplace |

### Why is JARV different?

#### Maximum performance, minimum cost
JARV uses 5 proprietary innovations to deliver extreme efficiency:
1. **Smart Router v2** — routes each message to the cheapest model that can solve it (-58% token cost)
2. **Semantic Cache** — similar responses are served from memory in milliseconds (zero cost)
3. **Swarm Executor** — parallel agents via native Go goroutines (-75% latency in squads)
4. **Sampling Oracle** — 4 archetypes capture 95% of the accuracy of 1,000 agents
5. **Graph Memory** — deep context without extra LLM calls

#### Runs anywhere
```
Raspberry Pi Zero 2W  →  512MB RAM  →  ✅ Works
$5/month VPS          →  1GB RAM    →  ✅ Works
Old Laptop            →  4GB RAM    →  ✅ Works
Enterprise Server     →  64GB RAM   →  ✅ Works (max settings)
```

#### Offline-first, online when available
JARV works 100% without internet. When connected, it synchronizes memory, accesses external LLMs, and uses cloud integrations — all transparently.

### Installation

**Executable (recommended)**
```bash
# Linux / macOS
curl -sSL https://jarv.ai/install.sh | bash

# Windows
# Download jarv-windows-amd64.exe at https://jarv.ai/download
```

**From source**
```bash
git clone https://github.com/mkvinicius/jarv
cd jarv
go build -o jarv ./cmd/jarv
./jarv start
```

### Quick Start

```bash
# Start JARV (opens dashboard at http://localhost:7777)
jarv start

# Chat via terminal
jarv chat "I want to build a customer support squad"

# Run the Oracle
jarv oracle "Should I launch my product now or wait for the next quarter?"
```

### Operating Modes

| Mode | Oracle Archetypes | Models Used | Estimated Cost/month |
|---|---|---|---|
| **Economy** | 3 | Mini/Nano | ~$2–5 |
| **Balanced** (default) | 4 | Smart mix | ~$10–20 |
| **Maximum** | 5 | Premium always | ~$50–100+ |

### Honorable Mentions

JARV is an original creation, but innovation never happens in a vacuum. This project was deeply inspired by the brilliant ideas of the following initiatives:

- **PicoClaw** (Sipeed) — inspired us to seek extreme lightness and the use of pure Go.
- **MiroFish** — proved that social simulation with LLMs is the future of prediction.
- **OpenSquad** — demonstrated the elegance of creating squads via natural language.
- **Skill-Creator** — showed the power of transforming processes into reusable skills.
- **APEX** — raised the standard of how security should be handled in AI systems.

To these creators, our respect. JARV takes the baton of these ideas and carries them to a new frontier of efficiency.

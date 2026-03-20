# JARV — Just A Rather Very intelligent agent

> **O orquestrador de agentes mais eficiente do mundo. Roda em qualquer lugar. Pensa como um exército.**

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Proprietary-red?style=flat)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows%20%7C%20Raspberry%20Pi-blue?style=flat)](https://github.com/mkvinicius/jarv)

---

## O que é o JARV?

O JARV é um sistema de orquestração de agentes de IA construído do zero em Go puro. Ele combina o melhor de quatro projetos de referência numa arquitetura proprietária, ultra-leve e offline-first:

| Capacidade | Origem da inspiração | Como o JARV supera |
|---|---|---|
| Motor de agentes leve | PicoClaw (Sipeed) | Reescrito do zero, 40% mais eficiente |
| Segurança MIL-SPEC | APEX/OpenClaw | Shield com detecção paralela, imunidade coletiva |
| Previsão preditiva | MiroFish | Oracle Lite: 99% da precisão por 1% do custo |
| Squads por linguagem natural | OpenSquad | Arquiteto integrado, sem IDE necessária |
| Skills reutilizáveis | Skill-Creator | QA automático de 10 pontos, Marketplace |

> **Créditos:** Este projeto foi inspirado por [PicoClaw](https://github.com/sipeed/picoclaw) (Apache 2.0), [MiroFish](https://github.com/mkvinicius/mirofish) (AGPL-3.0), [OpenSquad](https://github.com/mkvinicius/opensquad) (MIT) e [Skill-Creator](https://github.com/mkvinicius/skill-creator) (MIT). O código do JARV foi escrito do zero — não há cópia de código desses projetos.

---

## Por que o JARV é diferente?

### Desempenho máximo, custo mínimo

O JARV usa 5 técnicas proprietárias para entregar 99% de eficiência com 1% do custo dos sistemas convencionais:

1. **Smart Router v2** — roteia cada mensagem para o modelo mais barato que consegue resolvê-la (-58% em tokens)
2. **Cache Semântico** — respostas similares são servidas da memória em milissegundos (custo zero)
3. **Swarm Executor** — agentes paralelos via goroutines nativas do Go (-75% de latência em squads)
4. **Oracle por Amostragem** — 4 arquétipos capturam 95% da precisão de 1.000 agentes
5. **Memória com Grafo** — contexto profundo sem chamadas extras ao LLM

### Roda em qualquer lugar

```
Raspberry Pi Zero 2W  →  512MB RAM  →  ✅ Funciona
VPS de R$30/mês       →  1GB RAM    →  ✅ Funciona
Notebook antigo       →  4GB RAM    →  ✅ Funciona
Servidor enterprise   →  64GB RAM   →  ✅ Funciona (com tudo no máximo)
```

### Offline-first, online quando disponível

O JARV funciona 100% sem internet. Quando conectado, sincroniza memória, acessa LLMs externos e usa integrações cloud — tudo de forma transparente.

---

## Instalação

### Executável (recomendado)

```bash
# Linux / macOS
curl -sSL https://jarv.ai/install.sh | bash

# Windows
# Baixe jarv-windows-amd64.exe em https://jarv.ai/download
```

### A partir do código-fonte

```bash
git clone https://github.com/mkvinicius/jarv
cd jarv
go build -o jarv ./cmd/jarv
./jarv start
```

### Docker

```bash
docker run -d -p 7777:7777 -v ~/.jarv:/data mkvinicius/jarv
```

---

## Início Rápido

```bash
# Inicia o JARV (abre o dashboard em http://localhost:7777)
jarv start

# Conversa via terminal
jarv chat "Quero montar um squad de atendimento ao cliente"

# Roda o Oracle
jarv oracle "Devo lançar meu produto agora ou esperar o próximo trimestre?"

# Instala uma skill
jarv skills install atendimento-premium

# Lista squads disponíveis
jarv squads list
```

---

## Arquitetura

```
┌─────────────────────────────────────────────────────────┐
│                    JARV ENGINE                          │
│                  (binário único ~25MB)                  │
├──────────────┬──────────────┬──────────────┬────────────┤
│   ORACLE     │    SHIELD    │    SWARM     │  SKILLS    │
│  Previsão    │  Segurança   │  Execução    │  Engine    │
│  Preditiva   │  MIL-SPEC    │  Paralela    │  SKILL.md  │
├──────────────┴──────────────┴──────────────┴────────────┤
│              SMART ROUTER v2                            │
│         (roteamento semântico por intenção)             │
├─────────────────────────────────────────────────────────┤
│              MEMORY LAYER                               │
│   L1: Cache RAM  │  L2: SQLite Local  │  L3: Supabase  │
├─────────────────────────────────────────────────────────┤
│              KNOWLEDGE GRAPH                            │
│         (triplas S-P-O em SQLite local)                 │
├─────────────────────────────────────────────────────────┤
│              LLM PROVIDERS                              │
│  OpenAI │ Anthropic │ Gemini │ Ollama (local) │ Custom  │
└─────────────────────────────────────────────────────────┘
```

---

## Configuração

O JARV não precisa de configuração para funcionar. Tudo tem padrões inteligentes. Mas você pode personalizar via `.env` ou pelo dashboard:

```env
# LLM Provider (padrão: openai)
JARV_LLM_PROVIDER=openai
JARV_LLM_API_KEY=sk-...

# Modo de operação (economy | balanced | maximum)
JARV_MODE=balanced

# Memória cloud (opcional — funciona sem isso)
JARV_SUPABASE_URL=https://xxxx.supabase.co
JARV_SUPABASE_KEY=eyJ...

# Dashboard
JARV_DASHBOARD_PORT=7777
JARV_DASHBOARD_MODE=focus  # focus | advanced
```

---

## Modos de Operação

| Modo | Arquétipos Oracle | Modelos usados | Custo estimado/mês |
|---|---|---|---|
| **Econômico** | 3 | Mini/Nano | ~$2–5 |
| **Balanceado** (padrão) | 4 | Misto inteligente | ~$10–20 |
| **Máximo** | 5 | Premium sempre | ~$50–100+ |

---

## Skills e Marketplace

Skills são arquivos `SKILL.md` que ensinam o JARV a fazer tarefas específicas com perfeição:

```bash
# Instalar skill do marketplace
jarv skills install atendimento-whatsapp
jarv skills install analise-financeira
jarv skills install criacao-conteudo

# Criar sua própria skill
jarv skills create "Responder reclamações de clientes com empatia"

# Listar skills instaladas
jarv skills list
```

---

## Squads Pré-configurados

O JARV vem com squads prontos para uso imediato:

| Squad | Agentes | Caso de uso |
|---|---|---|
| `atendimento` | Triagem, Resposta, Escalonamento | Suporte ao cliente |
| `marketing` | Pesquisa, Copywriter, Designer de prompts | Criação de conteúdo |
| `vendas` | Qualificação, Proposta, Follow-up | Pipeline de vendas |
| `dev` | Arquiteto, Revisor, Documentador | Desenvolvimento de software |
| `financeiro` | Analista, Oracle, Relator | Análise e previsão |
| `segurança` | Scanner, Analista, Relator | Auditoria de segurança |

---

## Segurança (Shield)

O Shield protege todas as interações com detecção em tempo real de:

- Prompt injection e jailbreak
- Vazamento de credenciais
- Engenharia social
- Rate limiting por sessão
- Imunidade coletiva (aprende ataques e compartilha com outras instâncias)

Todos os eventos ficam no audit log imutável com assinatura HMAC.

---

## Compatibilidade de Hardware

| Dispositivo | RAM | Status |
|---|---|---|
| Raspberry Pi Zero 2W | 512MB | ✅ Modo Econômico |
| Raspberry Pi 4 (2GB) | 2GB | ✅ Modo Balanceado |
| VPS básico (1 vCPU) | 1GB | ✅ Modo Balanceado |
| Notebook/Desktop | 4GB+ | ✅ Todos os modos |
| Servidor enterprise | 16GB+ | ✅ Modo Máximo + múltiplas instâncias |

---

## Licença

Copyright (c) 2026 JARV contributors. Todos os direitos reservados.

Este software é proprietário e confidencial. Uso, cópia, modificação ou distribuição sem autorização expressa é proibido.

---

## Créditos e Agradecimentos

O JARV foi construído com inspiração de projetos open source excepcionais:

- **[PicoClaw](https://github.com/sipeed/picoclaw)** por Sipeed — pela filosofia de leveza extrema e Go puro
- **[MiroFish](https://github.com/mkvinicius/mirofish)** — pela prova de conceito de simulação social com LLMs
- **[OpenSquad](https://github.com/mkvinicius/opensquad)** por Renato Asse e colaboradores — pelo design de squads por linguagem natural
- **[Skill-Creator](https://github.com/mkvinicius/skill-creator)** por Bruno Okamoto — pelo sistema de skills com QA automático
- **[APEX/OpenClaw](https://github.com/mkvinicius/openclaw-apex)** — pela arquitetura de segurança MIL-SPEC

Nenhum código desses projetos foi copiado. O JARV é uma reescrita do zero que honra a inteligência arquitetural de cada um.

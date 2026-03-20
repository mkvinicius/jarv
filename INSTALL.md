# Guia de Instalação do JARV

Bem-vindo ao guia de instalação do JARV. Este documento explica como instalar e rodar o JARV em qualquer sistema operacional, desde um MacBook moderno até um Raspberry Pi.

O JARV foi projetado para ser **extremamente simples de instalar**. Ele é um único arquivo executável, sem necessidade de instalar Python, Node.js ou bancos de dados complexos.

---

## Requisitos Mínimos de Hardware

O JARV se adapta ao hardware disponível. Aqui está o que você precisa:

| Dispositivo | RAM Mínima | Espaço em Disco | Modo Recomendado |
|---|---|---|---|
| **MacBook (M1/M2/M3 ou Intel)** | 4GB | 100MB | Todos os modos |
| **PC com Windows** | 4GB | 100MB | Todos os modos |
| **Linux (Desktop ou VPS)** | 1GB | 100MB | Balanceado |
| **Raspberry Pi (Zero 2W ou 4)** | 512MB | 100MB | Econômico |

---

## Instalação no macOS (Apple Silicon M1/M2/M3 ou Intel)

Se você usa um Mac, a instalação é muito rápida.

### Opção 1: Via Terminal (Mais fácil)
Abra o aplicativo **Terminal** (você pode encontrá-lo buscando na lupa do Spotlight) e cole o seguinte comando:

```bash
curl -sSL https://jarv.ai/install.sh | bash
```
*Este comando baixa a versão correta para o seu Mac (seja ele com chip M ou Intel) e coloca o JARV na pasta correta.*

### Opção 2: Download Manual
1. Acesse [https://jarv.ai/download](https://jarv.ai/download) (link ilustrativo)
2. Baixe a versão para macOS:
   - Se seu Mac for mais novo (M1/M2/M3): Baixe `jarv-macos-arm64`
   - Se seu Mac for mais antigo (Intel): Baixe `jarv-macos-amd64`
3. Abra o Terminal, vá até a pasta onde baixou e dê permissão de execução:
   ```bash
   chmod +x ~/Downloads/jarv-macos-*
   mv ~/Downloads/jarv-macos-* /usr/local/bin/jarv
   ```

---

## Instalação no Windows

### Opção 1: Download Direto (Para usuários comuns)
1. Acesse [https://jarv.ai/download](https://jarv.ai/download) (link ilustrativo)
2. Baixe o arquivo `jarv-windows-amd64.exe`
3. Crie uma pasta chamada `JARV` no seu disco `C:\` e coloque o arquivo lá.
4. Dê um duplo clique no arquivo `jarv-windows-amd64.exe` para iniciar.

### Opção 2: Via PowerShell (Para usuários avançados)
Abra o PowerShell como Administrador e rode:
```powershell
iwr -useb https://jarv.ai/install.ps1 | iex
```

---

## Instalação no Linux (Ubuntu, Debian, VPS)

Ideal para rodar o JARV em servidores na nuvem ou no seu desktop Linux.

Abra o terminal e rode:
```bash
curl -sSL https://jarv.ai/install.sh | bash
```

---

## Instalação no Raspberry Pi

O JARV é tão leve que roda até num Raspberry Pi Zero 2W.

Abra o terminal do seu Raspberry Pi e rode:
```bash
curl -sSL https://jarv.ai/install.sh | bash
```
*O script detectará automaticamente a arquitetura ARM e baixará a versão correta.*

---

## Como Iniciar o JARV

Após a instalação, iniciar o JARV é muito simples.

Abra o seu Terminal (ou Prompt de Comando no Windows) e digite:

```bash
jarv start
```

**O que vai acontecer:**
1. O JARV vai criar os arquivos de memória local automaticamente (você não precisa configurar nenhum banco de dados).
2. Ele vai iniciar o servidor web local.
3. **O seu navegador de internet vai abrir automaticamente** na página do Dashboard do JARV (geralmente em `http://localhost:7777`).

A partir daí, toda a interação é feita pela interface bonita e amigável no seu navegador.

---

## Configuração Inicial (Opcional)

Na primeira vez que você abrir o Dashboard, o JARV fará 3 perguntas simples:

1. **Qual IA você quer usar?** (OpenAI, Anthropic, Gemini ou Local/Ollama)
2. **Qual a sua chave de API?** (Se escolher uma IA na nuvem)
3. **Qual modo de operação?** (Econômico, Balanceado ou Máximo)

Você só precisa responder isso uma vez. O JARV salva essas configurações de forma segura na sua máquina.

---

## Dúvidas Frequentes

**Preciso de internet para instalar?**
Sim, para baixar o executável.

**Preciso de internet para usar?**
Depende da IA que você escolher. Se você configurar o JARV para usar o Ollama (IA local), ele funcionará 100% sem internet. Se usar OpenAI/Claude, precisará de internet.

**Onde ficam salvos meus dados?**
Tudo fica salvo localmente na sua máquina, na pasta `~/.jarv/`. Nenhuma conversa sua é enviada para servidores do JARV.

**Como eu atualizo o JARV?**
Basta rodar o comando `jarv update` no terminal.

# DevEx Agent

Daemon de infraestrutura que roda em instâncias EC2 e executa deploys de aplicações Docker sob comando da **DevEx Platform**.

Dois modos de execução:

- **runtime** — gerencia containers Docker, portas e health checks em instâncias de workload
- **gateway** — mantém as rotas HTTP/HTTPS do Caddy atualizadas nas instâncias de gateway

### Documentação de fluxos

| Documento | Descrição |
|---|---|
| [docs/runtime-agent.md](docs/runtime-agent.md) | Fluxos do Runtime Agent: boot, loops concorrentes, ciclo de comandos, deploy blue/green, máquinas de estado de deployments e portas, reconciliação no startup |
| [docs/gateway-agent.md](docs/gateway-agent.md) | Fluxos do Gateway Agent: boot, reconcile loop, geração do caddy.json, aplicação via `/load`, validação de rotas, rollback para última config boa |

---

## Arquitetura

```
DevEx Platform  →  decide o estado desejado
DevEx Agent     →  executa e reporta o resultado
Docker          →  roda os containers
Caddy           →  roteia o tráfego HTTP/HTTPS
Route 53        →  resolve o DNS
```

O agente faz polling na Platform API, reivindica comandos atomicamente e reporta o resultado. Ele nunca expõe uma API pública.

---

## Pré-requisitos

| Componente | Versão mínima |
|---|---|
| Go | 1.21 |
| Docker | 20.10 |
| Linux (systemd) | — |

Para o modo **gateway**, o Caddy precisa estar rodando com a Admin API disponível em `http://127.0.0.1:2019`.

---

## Build

### Build local (mesma plataforma)

```bash
go build -o devex-agent ./cmd/devex-agent
```

### Build para Linux AWS (cross-compilation a partir do macOS ou Windows)

As instâncias EC2 na AWS usam Linux x86_64. Para gerar o binário a partir de outra plataforma:

```bash
GOOS=linux GOARCH=amd64 go build -o devex-agent-linux-amd64 ./cmd/devex-agent
```

Para gerar um binário menor e sem símbolos de debug (recomendado para produção):

```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o devex-agent-linux-amd64 ./cmd/devex-agent
```

Após gerar o binário, copie para a instância EC2 via `scp`:

```bash
scp -i sua-chave.pem devex-agent-linux-amd64 ec2-user@<IP_DA_INSTANCIA>:/tmp/devex-agent
```

Na instância EC2, mova para o local definitivo:

```bash
sudo mv /tmp/devex-agent /usr/local/bin/devex-agent
sudo chmod 755 /usr/local/bin/devex-agent
```

### Verificar versão

```bash
devex-agent --version
```

### Validar config sem iniciar

```bash
./devex-agent --validate-config --config /etc/devex-agent/config.yaml
```

---

## Instalação em uma instância EC2

### Deploy automatizado (recomendado)

O script `scripts/deploy.sh` executa todo o processo em um único comando a partir da sua máquina local: compila o binário, envia os artefatos via SCP, instala o serviço e verifica se subiu corretamente.

**Pré-requisito:** salve o token do agente em um arquivo local.

```bash
echo "SEU_TOKEN_AQUI" > ~/.devex-token
chmod 600 ~/.devex-token
```

**Deploy do Runtime Agent:**

```bash
bash scripts/deploy-agent.sh \
  --host <IP_DA_INSTANCIA> \
  --key ~/.ssh/sua-chave.pem \
  --mode runtime \
  --token-file ~/.devex-token
```

**Deploy do Gateway Agent:**

```bash
bash scripts/deploy-agent.sh \
  --host <IP_DA_INSTANCIA> \
  --key ~/.ssh/sua-chave.pem \
  --mode gateway \
  --token-file ~/.devex-token
```

**Flags disponíveis:**

| Flag | Descrição |
|---|---|
| `--host` | IP ou hostname da instância EC2 |
| `--key` | Caminho para o arquivo `.pem` do Key Pair |
| `--mode` | `runtime` ou `gateway` |
| `--token-file` | Arquivo com o token do agente (recomendado) |
| `--token` | Token como string direta (evite em produção) |
| `--user` | Usuário SSH (padrão: `ec2-user`) |
| `--skip-build` | Reutiliza o binário `devex-agent-linux-amd64` já existente sem recompilar |

---

### Deploy manual (passo a passo)

O script `install-systemd.sh` precisa de três artefatos presentes na instância:

- O **binário** `devex-agent` compilado para Linux x86_64
- O arquivo **`scripts/devex-agent.service`** (unit systemd)
- Um dos arquivos de configuração: **`scripts/config-runtime.yaml`** ou **`scripts/config-gateway.yaml`**

O procedimento completo está descrito abaixo.

---

### 1. Na sua máquina local: compilar o binário para Linux

```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o devex-agent-linux-amd64 ./cmd/devex-agent
```

---

### 2. Na sua máquina local: copiar os artefatos para a EC2

```bash
# Binário
scp -i sua-chave.pem devex-agent-linux-amd64 ec2-user@<IP_DA_INSTANCIA>:/tmp/devex-agent

# Unit systemd
scp -i sua-chave.pem scripts/devex-agent.service ec2-user@<IP_DA_INSTANCIA>:/tmp/devex-agent.service

# Script de instalação
scp -i sua-chave.pem scripts/install-systemd.sh ec2-user@<IP_DA_INSTANCIA>:/tmp/install-systemd.sh

# Arquivo de configuração (escolha um)
scp -i sua-chave.pem scripts/config-runtime.yaml ec2-user@<IP_DA_INSTANCIA>:/tmp/config.yaml   # Runtime Agent
scp -i sua-chave.pem scripts/config-gateway.yaml ec2-user@<IP_DA_INSTANCIA>:/tmp/config.yaml   # Gateway Agent
```

---

### 3. Na instância EC2: preparar os arquivos

```bash
# Organizar na estrutura esperada pelo script
mkdir -p /tmp/devex-install/scripts

mv /tmp/devex-agent          /tmp/devex-install/devex-agent
mv /tmp/devex-agent.service  /tmp/devex-install/scripts/devex-agent.service
mv /tmp/install-systemd.sh   /tmp/devex-install/scripts/install-systemd.sh
mv /tmp/config.yaml          /tmp/devex-install/scripts/config.yaml

chmod +x /tmp/devex-install/scripts/install-systemd.sh
```

---

### 4. Na instância EC2: executar o script de instalação

```bash
cd /tmp/devex-install
sudo AGENT_BIN=./devex-agent bash scripts/install-systemd.sh
```

O script:
- Instala o binário em `/usr/local/bin/devex-agent`
- Cria os diretórios `/etc/devex-agent` e `/var/lib/devex-agent`
- Registra e habilita o serviço systemd

---

### 5. Na instância EC2: copiar o arquivo de configuração

```bash
sudo cp /tmp/devex-install/scripts/config.yaml /etc/devex-agent/config.yaml
sudo chmod 600 /etc/devex-agent/config.yaml
```

Edite o arquivo para ajustar os valores do seu ambiente:

```bash
sudo vi /etc/devex-agent/config.yaml
```

Campos obrigatórios a revisar: `agent.mode`, `agent.environment`, `platform.base_url`.

---

### 6. Na instância EC2: configurar o token

```bash
echo "SEU_TOKEN_AQUI" | sudo tee /etc/devex-agent/token > /dev/null
sudo chmod 600 /etc/devex-agent/token
```

---

### 7. Na instância EC2: iniciar o serviço

```bash
sudo systemctl start devex-agent
sudo systemctl status devex-agent
```

Acompanhar os logs em tempo real:

```bash
sudo journalctl -u devex-agent -f
```

---

## Configuração

### Runtime Agent

```yaml
agent:
  mode: "runtime"
  environment: "prod"
  role: "api"

platform:
  base_url: "https://platform.useclarus.app"
  token_file: "/etc/devex-agent/token"

runtime:
  max_active_containers: 10
  draining_grace_period_seconds: 300
  command_poll_interval_seconds: 10

ports:
  from: 4100
  to: 4114

docker:
  command: "docker"
  default_stop_timeout_seconds: 30

health_check:
  timeout_seconds: 2
  interval_seconds: 5
  retries: 6

state:
  dir: "/var/lib/devex-agent"

logging:
  level: "info"
  format: "json"
```

### Gateway Agent

```yaml
agent:
  mode: "gateway"
  environment: "prod"
  role: "gateway"

platform:
  base_url: "https://platform.useclarus.app"
  token_file: "/etc/devex-agent/token"

caddy:
  admin_url: "http://127.0.0.1:2019"
  current_config_path: "/var/lib/devex-agent/gateway/current-caddy.json"
  previous_config_path: "/var/lib/devex-agent/gateway/previous-caddy.json"
  last_good_config_path: "/var/lib/devex-agent/gateway/last-good-caddy.json"

reconcile:
  interval_seconds: 10

state:
  dir: "/var/lib/devex-agent"

logging:
  level: "info"
  format: "json"
```

### Campos obrigatórios

| Campo | Descrição |
|---|---|
| `agent.mode` | `runtime` ou `gateway` |
| `agent.environment` | Identificador do ambiente (`dev`, `prod`, …) |
| `platform.base_url` | URL base da DevEx Platform API |
| `platform.token_file` | Caminho para o arquivo com o token do agente |
| `state.dir` | Diretório para persistência de estado local |

---

## Operação

### Comandos systemd

```bash
sudo systemctl start devex-agent      # iniciar
sudo systemctl stop devex-agent       # parar (graceful)
sudo systemctl restart devex-agent    # reiniciar
sudo systemctl status devex-agent     # ver status
```

### Logs

```bash
sudo journalctl -u devex-agent -f          # logs em tempo real
sudo journalctl -u devex-agent -b          # logs desde o boot
sudo journalctl -u devex-agent -n 100      # últimas 100 linhas
```

Os logs são emitidos em JSON estruturado. Campos presentes em toda operação relevante: `agent_id`, `mode`, `environment`, `command_id`, `deployment_id`, `application`, `container_name`.

### Atualização do binário

```bash
sudo systemctl stop devex-agent
sudo cp devex-agent-novo /usr/local/bin/devex-agent
sudo chmod 755 /usr/local/bin/devex-agent
sudo systemctl start devex-agent
sudo systemctl status devex-agent
```

Para rollback, mantenha uma cópia do binário anterior:

```bash
sudo cp /usr/local/bin/devex-agent /usr/local/bin/devex-agent.prev
```

### Desinstalação

```bash
sudo bash scripts/uninstall-systemd.sh
```

O script para o serviço, remove o unit file e o binário. Os diretórios `/etc/devex-agent` e `/var/lib/devex-agent` são preservados.

---

## Desenvolvimento

### Testes unitários

```bash
go test ./...
```

Nenhuma dependência externa necessária. Todos os testes usam fakes e `httptest.Server`.

### Testes de integração

Requerem Docker instalado e o daemon acessível:

```bash
RUN_DOCKER_INTEGRATION_TESTS=true go test ./internal/docker/...
```

Requerem Caddy rodando com Admin API em `http://127.0.0.1:2019`:

```bash
RUN_CADDY_INTEGRATION_TESTS=true go test ./internal/caddy/...
```

### Estrutura de pacotes

```
internal/
├── agent/      loops principais, processamento de comandos
├── caddy/      geração de caddy.json, Caddy Admin API client
├── config/     carregamento e validação de configuração
├── docker/     interface e implementação CLI do Docker
├── errors/     erros tipados com código e campo retryable
├── health/     health checks HTTP e de container
├── logger/     logger estruturado (slog/JSON)
├── platform/   client HTTP da DevEx Platform API
├── ports/      alocação e reconciliação de portas
└── state/      persistência de estado local (JSON atômico)
```

---

## Troubleshooting

### Serviço não inicia

```bash
sudo systemctl status devex-agent
sudo journalctl -u devex-agent -n 50
```

Erros comuns nos logs:

| Código | Causa provável |
|---|---|
| `CONFIG_INVALID` | Campo obrigatório ausente ou valor inválido em `config.yaml` |
| `STATE_STORE_FAILED` | Permissão negada em `/var/lib/devex-agent` |
| `PLATFORM_API_UNAVAILABLE` | `platform.base_url` inacessível ou token inválido |
| `DOCKER_UNAVAILABLE` | Daemon Docker não está rodando |

### Docker não disponível (modo runtime)

```bash
sudo systemctl status docker
docker ps
```

### Caddy não disponível (modo gateway)

```bash
curl -s http://127.0.0.1:2019/config/ | head -c 200
docker ps | grep caddy
```

### Token inválido

O agente registra `AUTHENTICATION_FAILED` e para. Verifique o conteúdo do token:

```bash
sudo cat /etc/devex-agent/token
```

Nunca compartilhe ou logue o conteúdo deste arquivo.

---

## Segurança

- O token é lido de arquivo e nunca aparece nos logs.
- O Runtime Agent não expõe nenhuma porta ou API pública.
- A Caddy Admin API é acessível apenas via `127.0.0.1:2019` — nunca exposta na rede pública.
- O diretório `/etc/devex-agent` deve ter permissão `700`; os arquivos `config.yaml` e `token`, permissão `600`.
- O Docker socket é altamente privilegiado; trate o processo do agente como confiável no host.

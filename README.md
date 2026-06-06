# DevEx Agent

Daemon de infraestrutura que roda em instâncias EC2 e executa deploys de aplicações Docker sob comando da **DevEx Platform**.

Dois modos de execução:

- **runtime** — gerencia containers Docker, portas e health checks em instâncias de workload
- **gateway** — mantém as rotas HTTP/HTTPS do Caddy atualizadas nas instâncias de gateway

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

```bash
go build -o devex-agent ./cmd/devex-agent
```

Verificar versão:

```bash
./devex-agent --version
```

Validar config sem iniciar:

```bash
./devex-agent --validate-config --config /etc/devex-agent/config.yaml
```

---

## Instalação

### 1. Criar diretórios e instalar binário

```bash
sudo bash scripts/install-systemd.sh
```

O script instala o binário em `/usr/local/bin/devex-agent`, cria os diretórios necessários e registra o serviço systemd.

### 2. Copiar o arquivo de configuração

Para Runtime Agent:

```bash
sudo cp scripts/config-runtime.yaml /etc/devex-agent/config.yaml
```

Para Gateway Agent:

```bash
sudo cp scripts/config-gateway.yaml /etc/devex-agent/config.yaml
```

### 3. Configurar o token

```bash
echo "SEU_TOKEN_AQUI" | sudo tee /etc/devex-agent/token > /dev/null
sudo chmod 600 /etc/devex-agent/token
sudo chmod 600 /etc/devex-agent/config.yaml
```

### 4. Iniciar o serviço

```bash
sudo systemctl start devex-agent
sudo systemctl status devex-agent
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

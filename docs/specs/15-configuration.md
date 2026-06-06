# 15 — Configuração

## Objetivo deste documento

Este documento define o modelo de configuração do **DevEx Agent**.

A configuração controla:

- Identidade do agent.
- Modo de execução.
- Ambiente.
- Comunicação com Platform API.
- Docker runtime.
- Port management.
- Caddy integration.
- Health checks.
- Retry.
- Timeouts.
- Estado local.
- Logs.

Este documento deve ser lido junto com:

- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/12-security.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

Configuração inválida deve falhar rápido.

O agent não deve iniciar em estado ambíguo.

Regra:

```text
Se a configuração obrigatória estiver ausente ou inválida, o processo deve encerrar com CONFIG_INVALID.
```

---

## Arquivo padrão

Caminho padrão:

```text
/etc/devex-agent/config.yaml
```

O caminho pode ser sobrescrito via flag:

```bash
devex-agent --config /path/to/config.yaml
```

---

## Formato

Formato recomendado:

```text
YAML
```

Motivos:

- Legível para humanos.
- Fácil de versionar.
- Suporta estrutura hierárquica.
- Adequado para configuração operacional.

---

## Configuração completa de exemplo — Runtime Agent

```yaml
agent:
  id: ""
  mode: "runtime"
  environment: "dev"
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
  pull_timeout_seconds: 300
  start_timeout_seconds: 60
  stop_timeout_seconds: 30
  remove_timeout_seconds: 30
  inspect_timeout_seconds: 10
  list_timeout_seconds: 10
  default_restart_policy: "unless-stopped"

health_check:
  timeout_seconds: 2
  interval_seconds: 5
  retries: 6

retry:
  max_attempts: 3
  initial_interval_seconds: 1
  max_interval_seconds: 10
  multiplier: 2.0
  jitter: true

state:
  dir: "/var/lib/devex-agent"

logging:
  level: "info"
  format: "json"
```

---

## Configuração completa de exemplo — Gateway Agent

```yaml
agent:
  id: ""
  mode: "gateway"
  environment: "dev"
  role: "gateway"

platform:
  base_url: "https://platform.useclarus.app"
  token_file: "/etc/devex-agent/token"

caddy:
  admin_url: "http://127.0.0.1:2019"
  current_config_path: "/var/lib/devex-agent/gateway/current-caddy.json"
  previous_config_path: "/var/lib/devex-agent/gateway/previous-caddy.json"
  last_good_config_path: "/var/lib/devex-agent/gateway/last-good-caddy.json"
  load_timeout_seconds: 10
  route_validation_timeout_seconds: 3

reconcile:
  interval_seconds: 10

retry:
  max_attempts: 3
  initial_interval_seconds: 1
  max_interval_seconds: 10
  multiplier: 2.0
  jitter: true

state:
  dir: "/var/lib/devex-agent"

logging:
  level: "info"
  format: "json"
```

---

## Seção agent

```yaml
agent:
  id: ""
  mode: "runtime"
  environment: "dev"
  role: "api"
```

### id

Identificador do agent.

Pode ser vazio no primeiro boot.

Quando vazio, o agent deve registrar-se na Platform API e persistir o `agent_id` recebido em:

```text
/var/lib/devex-agent/agent.json
```

### mode

Valores permitidos:

```text
runtime
gateway
```

Obrigatório.

### environment

Ambiente do agent.

Exemplos:

```text
dev
stage
prod
```

Obrigatório.

### role

Papel do agent.

Exemplos:

```text
api
frontend
worker
gateway
```

Obrigatório.

---

## Seção platform

```yaml
platform:
  base_url: "https://platform.useclarus.app"
  token_file: "/etc/devex-agent/token"
```

### base_url

URL base da DevEx Platform API.

Obrigatório.

Deve usar HTTPS em produção.

### token_file

Caminho do arquivo contendo token do agent.

Obrigatório.

O conteúdo do token não deve ser logado.

---

## Seção runtime

Aplicável ao Runtime Agent.

```yaml
runtime:
  max_active_containers: 10
  draining_grace_period_seconds: 300
  command_poll_interval_seconds: 10
```

### max_active_containers

Quantidade máxima de deployments ativos na instância.

### draining_grace_period_seconds

Tempo para manter versão antiga após troca de rota.

### command_poll_interval_seconds

Intervalo de polling para buscar comandos.

---

## Seção ports

Aplicável ao Runtime Agent.

```yaml
ports:
  from: 4100
  to: 4114
```

Regras:

```text
from deve ser menor ou igual a to.
range deve conter portas suficientes.
portas devem ser maiores que 1024, salvo decisão explícita.
```

---

## Seção docker

Aplicável ao Runtime Agent.

```yaml
docker:
  command: "docker"
  pull_timeout_seconds: 300
  start_timeout_seconds: 60
  stop_timeout_seconds: 30
  remove_timeout_seconds: 30
  inspect_timeout_seconds: 10
  list_timeout_seconds: 10
  default_restart_policy: "unless-stopped"
```

### command

Binário Docker.

Padrão:

```text
docker
```

### default_restart_policy

Padrão recomendado:

```text
unless-stopped
```

---

## Seção caddy

Aplicável ao Gateway Agent.

```yaml
caddy:
  admin_url: "http://127.0.0.1:2019"
  current_config_path: "/var/lib/devex-agent/gateway/current-caddy.json"
  previous_config_path: "/var/lib/devex-agent/gateway/previous-caddy.json"
  last_good_config_path: "/var/lib/devex-agent/gateway/last-good-caddy.json"
  load_timeout_seconds: 10
  route_validation_timeout_seconds: 3
```

### admin_url

URL da Caddy Admin API.

Deve ser local:

```text
http://127.0.0.1:2019
```

Rejeitar em produção:

```text
http://0.0.0.0:2019
URLs públicas
```

---

## Seção health_check

```yaml
health_check:
  timeout_seconds: 2
  interval_seconds: 5
  retries: 6
```

Usada para health checks locais.

---

## Seção retry

```yaml
retry:
  max_attempts: 3
  initial_interval_seconds: 1
  max_interval_seconds: 10
  multiplier: 2.0
  jitter: true
```

Usada para operações retryable.

---

## Seção reconcile

```yaml
reconcile:
  interval_seconds: 10
```

Usada principalmente pelo Gateway Agent e loops periódicos.

---

## Seção state

```yaml
state:
  dir: "/var/lib/devex-agent"
```

Diretório local de estado.

Obrigatório.

---

## Seção logging

```yaml
logging:
  level: "info"
  format: "json"
```

### level

Valores permitidos:

```text
debug
info
warn
error
```

### format

Valores permitidos:

```text
json
text
```

Para produção, preferir:

```text
json
```

---

## Variáveis de ambiente

A configuração pode permitir override por variáveis de ambiente.

Sugestões:

```text
DEVEX_AGENT_CONFIG
DEVEX_AGENT_ID
DEVEX_AGENT_MODE
DEVEX_AGENT_ENVIRONMENT
DEVEX_AGENT_ROLE
DEVEX_PLATFORM_BASE_URL
DEVEX_PLATFORM_TOKEN_FILE
DEVEX_LOG_LEVEL
```

Precedência recomendada:

```text
flags CLI > env vars > config file > defaults
```

---

## Flags CLI

Flags mínimas:

```text
--config
--version
--validate-config
```

Exemplos:

```bash
devex-agent --config /etc/devex-agent/config.yaml
devex-agent --validate-config --config /etc/devex-agent/config.yaml
devex-agent --version
```

---

## Defaults

Defaults seguros:

```yaml
docker:
  command: "docker"
  default_restart_policy: "unless-stopped"

health_check:
  timeout_seconds: 2
  interval_seconds: 5
  retries: 6

retry:
  max_attempts: 3
  initial_interval_seconds: 1
  max_interval_seconds: 10
  multiplier: 2.0
  jitter: true

logging:
  level: "info"
  format: "json"

state:
  dir: "/var/lib/devex-agent"
```

Não definir defaults para:

```text
agent.mode
agent.environment
agent.role
platform.base_url
platform.token_file
```

Esses campos devem ser explícitos.

---

## Validações obrigatórias

Validações gerais:

```text
agent.mode obrigatório e válido.
agent.environment obrigatório.
agent.role obrigatório.
platform.base_url obrigatório.
platform.token_file obrigatório.
state.dir obrigatório.
logging.level válido.
logging.format válido.
```

Validações Runtime Agent:

```text
ports.from <= ports.to.
range de portas não vazio.
runtime.max_active_containers > 0.
runtime.max_active_containers <= quantidade de portas disponíveis.
docker.command não vazio.
```

Validações Gateway Agent:

```text
caddy.admin_url obrigatório.
caddy.admin_url deve apontar para localhost/127.0.0.1.
paths de config do Caddy obrigatórios.
```

---

## Erros de configuração

Código:

```text
CONFIG_INVALID
```

Exemplo:

```json
{
  "code": "CONFIG_INVALID",
  "message": "agent.mode is required and must be runtime or gateway"
}
```

---

## Segurança da configuração

Não armazenar secrets no config.yaml sempre que possível.

Preferir:

```text
token_file
IAM Role
Secrets Manager
arquivos protegidos
```

Permissões:

```bash
chmod 600 /etc/devex-agent/config.yaml
chmod 600 /etc/devex-agent/token
```

---

## Testes de configuração

Cenários:

```text
config runtime válida
config gateway válida
config sem mode
config com mode inválido
config runtime sem ports
config gateway sem caddy.admin_url
env var sobrescrevendo log level
flag --config funcionando
--validate-config com sucesso
--validate-config com falha
```

---

## Critérios de aceite

Configuração estará correta quando:

```text
1. Agent carregar YAML válido.
2. Agent falhar rápido com CONFIG_INVALID.
3. Runtime Agent validar portas e runtime config.
4. Gateway Agent validar Caddy config.
5. Token for lido de arquivo.
6. Secrets não forem logados.
7. Flags básicas funcionarem.
8. Env vars puderem sobrescrever valores permitidos.
9. Defaults seguros forem aplicados.
10. Testes cobrirem configs válidas e inválidas.
```

---

## Regra final

Configuração define o comportamento operacional do agent.

Config inválida não deve gerar comportamento parcial.

Falhe rápido, com erro claro, antes de executar qualquer operação sensível.

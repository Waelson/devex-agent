# 13 — Observabilidade

## Objetivo deste documento

Este documento define os requisitos de observabilidade do **DevEx Agent**, incluindo logs, eventos, métricas futuras, rastreabilidade operacional e informações necessárias para diagnóstico.

O DevEx Agent executa operações críticas:

- Deploy de containers.
- Alocação de portas.
- Health checks.
- Atualização de rotas no Caddy.
- Rollback.
- Reconciliação.
- Comunicação com a DevEx Platform.

Por isso, toda ação relevante deve ser observável, auditável e correlacionável.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

O agente deve sempre responder três perguntas:

```text
O que está acontecendo?
Onde está acontecendo?
Por que falhou?
```

Para isso, logs e reports devem conter contexto suficiente para correlacionar:

```text
agent
ambiente
comando
deployment
aplicação
container
rota
porta
erro
```

---

## Escopo da observabilidade

O MVP deve implementar:

```text
logs estruturados
eventos operacionais
reports para Platform API
status em heartbeat
correlation ids básicos
```

Fora do MVP:

```text
servidor Prometheus completo
tracing distribuído
dashboard completo
coleta centralizada automática
alertas automáticos
```

A implementação deve, porém, facilitar essas evoluções.

---

## Logs estruturados

O agente deve usar logs estruturados, preferencialmente JSON.

Exemplo:

```json
{
  "timestamp": "2026-06-05T18:00:00Z",
  "level": "info",
  "component": "runtime-agent",
  "agent_id": "agent-dev-api-001",
  "environment": "dev",
  "role": "api",
  "command_id": "cmd_123",
  "deployment_id": "dep_456",
  "application": "billing-api",
  "container_name": "billing-api-dev-v42",
  "message": "container started"
}
```

---

## Níveis de log

Níveis suportados:

```text
debug
info
warn
error
```

### debug

Usado para detalhes técnicos úteis em investigação.

Exemplos:

```text
payload sanitizado recebido
tentativa de health check
detalhes de reconciliação
stdout/stderr truncado de comandos
```

### info

Usado para eventos normais importantes.

Exemplos:

```text
agent iniciado
heartbeat enviado
comando recebido
container iniciado
rota aplicada
deploy concluído
```

### warn

Usado para situações anormais, mas não fatais.

Exemplos:

```text
heartbeat falhou temporariamente
porta inconsistente detectada
container órfão encontrado
retry agendado
```

### error

Usado para falhas operacionais.

Exemplos:

```text
docker pull falhou
health check falhou
caddy load falhou
state store corrompido
```

---

## Campos obrigatórios de log

Sempre que aplicável, incluir:

```text
agent_id
mode
environment
role
component
operation
command_id
deployment_id
application
container_name
route_host
upstream
host_port
error_code
```

Nem todos os campos são obrigatórios em todos os logs, mas devem ser usados quando o contexto existir.

---

## Componentes de log

Componentes esperados:

```text
agent
runtime-agent
gateway-agent
platform-client
docker-runtime
port-manager
state-store
health-checker
caddy-client
caddy-generator
reconciler
config-loader
```

---

## Operações importantes

Cada log relevante deve informar uma `operation`.

Exemplos:

```text
agent.start
agent.shutdown
platform.register
platform.heartbeat
command.fetch
command.claim
command.execute
docker.pull
docker.run
docker.stop
docker.rm
port.allocate
port.release
health.http
health.container
caddy.generate
caddy.load
caddy.validate_route
state.load
state.save
reconcile.runtime
reconcile.gateway
```

---

## Eventos operacionais

Eventos são registros de alto nível enviados ou reportáveis para a Platform API.

Eventos recomendados:

```text
agent_started
agent_registered
heartbeat_sent
heartbeat_failed
command_fetched
command_claimed
command_started
command_succeeded
command_failed
image_pull_started
image_pull_completed
image_pull_failed
port_allocated
port_released
container_started
container_stopped
container_removed
health_check_started
health_check_succeeded
health_check_failed
deployment_draining
deployment_removed
caddy_config_generated
caddy_load_started
caddy_load_succeeded
caddy_load_failed
route_validation_succeeded
route_validation_failed
reconciliation_started
reconciliation_completed
reconciliation_failed
```

---

## Reports para Platform API

Além de logs locais, o agent deve reportar resultados relevantes para a Platform API.

Exemplo de sucesso:

```json
{
  "status": "succeeded",
  "deployment_id": "dep_456",
  "result": {
    "application": "billing-api",
    "container_name": "billing-api-dev-v42",
    "runtime_private_ip": "10.0.2.25",
    "host_port": 4102,
    "health": "healthy"
  }
}
```

Exemplo de falha:

```json
{
  "status": "failed",
  "deployment_id": "dep_456",
  "error": {
    "code": "HEALTH_CHECK_FAILED",
    "message": "Application did not return a successful health response"
  }
}
```

---

## Heartbeat observável

O heartbeat deve incluir um resumo operacional.

Exemplo:

```json
{
  "status": "online",
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "version": "0.1.0",
  "running_containers": 4,
  "active_deployments": 4,
  "allocated_ports": 5,
  "last_command_id": "cmd_123",
  "last_successful_command_id": "cmd_123"
}
```

Para Gateway Agent:

```json
{
  "status": "online",
  "mode": "gateway",
  "environment": "dev",
  "version": "0.1.0",
  "caddy_status": "healthy",
  "routes_total": 12,
  "last_applied_desired_state_version": 43
}
```

---

## Correlation IDs

Sempre que possível, usar:

```text
command_id
deployment_id
desired_state_version
agent_id
```

Esses campos devem aparecer em logs, reports e eventos.

Isso permite rastrear o fluxo completo:

```text
UI request -> deployment -> command -> agent execution -> route update -> final status
```

---

## Logs de comandos Docker

Ao executar Docker CLI, registrar:

```text
operação
imagem
container_name
exit_code
duração
erro sanitizado
```

Não logar:

```text
env vars sensíveis
tokens
registry credentials
comandos com secrets inline
```

Stdout/stderr devem ser truncados quando grandes.

Exemplo:

```json
{
  "level": "error",
  "component": "docker-runtime",
  "operation": "docker.pull",
  "image": "ghcr.io/useclarus/billing-api:v42",
  "duration_ms": 12450,
  "error_code": "IMAGE_PULL_FAILED",
  "message": "docker pull failed"
}
```

---

## Logs de health check

Campos recomendados:

```text
health_check_type
target
attempt
max_attempts
duration_ms
status_code
error_code
```

Exemplo:

```json
{
  "level": "warn",
  "component": "health-checker",
  "operation": "health.http",
  "deployment_id": "dep_456",
  "application": "billing-api",
  "target": "http://127.0.0.1:4102/health",
  "attempt": 2,
  "max_attempts": 6,
  "error_code": "HEALTH_CHECK_CONNECTION_REFUSED",
  "message": "health check attempt failed"
}
```

---

## Logs de Caddy

Campos recomendados:

```text
desired_state_version
route_host
upstream
caddy_admin_url
duration_ms
error_code
```

Exemplo:

```json
{
  "level": "info",
  "component": "caddy-client",
  "operation": "caddy.load",
  "desired_state_version": 43,
  "routes_total": 12,
  "duration_ms": 180,
  "message": "caddy configuration loaded"
}
```

---

## Métricas futuras

Embora o MVP possa começar apenas com logs, as seguintes métricas devem ser consideradas:

### Runtime Agent

```text
runtime_agent_heartbeat_total
runtime_agent_commands_processed_total
runtime_agent_command_errors_total
runtime_agent_deploy_duration_seconds
runtime_agent_running_containers
runtime_agent_active_deployments
runtime_agent_allocated_ports
runtime_agent_port_allocation_errors_total
runtime_agent_health_check_failures_total
runtime_agent_reconciliation_errors_total
```

### Gateway Agent

```text
gateway_agent_heartbeat_total
gateway_agent_routes_total
gateway_agent_caddy_load_total
gateway_agent_caddy_load_errors_total
gateway_agent_route_validation_failures_total
gateway_agent_desired_state_version
```

### Platform Client

```text
platform_client_requests_total
platform_client_request_errors_total
platform_client_request_duration_seconds
```

---

## Formato de métricas

Quando implementado, o formato recomendado é Prometheus.

Endpoint futuro:

```text
http://127.0.0.1:<metrics_port>/metrics
```

Para o MVP, não é necessário expor endpoint de métricas.

---

## Auditoria

A Platform API deve manter histórico auditável dos eventos relevantes.

O agent deve fornecer dados suficientes para auditoria:

```text
quem solicitou
qual comando
qual agent executou
qual aplicação
qual imagem
qual container
qual porta
qual rota
qual resultado
qual erro
quando iniciou
quando terminou
```

O agent não precisa armazenar auditoria completa localmente.

---

## Sanitização

Antes de logar ou reportar, sanitizar:

```text
tokens
secrets
authorization headers
passwords
env vars sensíveis
```

Nomes sensíveis:

```text
PASSWORD
SECRET
TOKEN
KEY
CREDENTIAL
AUTH
```

---

## Critérios de aceite

Observabilidade estará adequada para o MVP quando:

```text
1. Logs forem estruturados.
2. Logs incluírem agent_id, command_id e deployment_id quando aplicável.
3. Falhas tiverem error_code.
4. Reports para Platform API incluírem sucesso/falha estruturados.
5. Heartbeat incluir status operacional.
6. Docker, health check e Caddy tiverem logs próprios.
7. Secrets não forem logados.
8. Logs permitirem reconstruir o fluxo de deploy.
9. Eventos importantes forem registráveis.
10. A implementação permitir evolução para métricas.
```

---

## Regra final

O agente deve ser diagnosticável em produção.

Se algo falhar, os logs e reports devem indicar:

```text
onde falhou
por que falhou
qual recurso foi afetado
qual ação foi tomada
qual correlação com deployment/comando
```

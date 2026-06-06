# 04 — Contratos da Platform API

## Objetivo deste documento

Este documento define os contratos HTTP entre o **DevEx Agent** e a **DevEx Platform API**.

Ele cobre:

- Registro de agents.
- Heartbeat.
- Busca de comandos.
- Claim de comandos.
- Report de comandos.
- Busca de desired state.
- Report de desired state.
- Payloads esperados.
- Códigos de resposta.
- Regras de autenticação.
- Regras de idempotência.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/13-observability.md`
- `docs/specs/14-error-handling-and-retry.md`
- `docs/specs/15-configuration.md`

---

## Convenções gerais

Base URL exemplo:

```text
https://platform.useclarus.app
```

Todos os endpoints devem usar JSON.

Headers:

```http
Content-Type: application/json
Accept: application/json
Authorization: Bearer <agent-token>
```

Todas as respostas de erro devem seguir formato estruturado.

---

## Autenticação

O agent deve autenticar usando token.

Header:

```http
Authorization: Bearer <token>
```

O token é lido de:

```text
/etc/devex-agent/token
```

Respostas esperadas:

```text
401 Unauthorized -> token ausente ou inválido
403 Forbidden    -> token válido, mas sem permissão
```

O agent não deve logar o token.

---

## Formato padrão de erro

Resposta de erro:

```json
{
  "error": {
    "code": "AUTHENTICATION_FAILED",
    "message": "Invalid agent token",
    "retryable": false
  }
}
```

Campos:

```text
code: código estável para máquina
message: mensagem sanitizada
retryable: indica se retry pode ser aplicado
details: opcional
```

---

## POST /api/agents/register

Registra um agent ou recupera registro existente.

### Request — Runtime Agent

```json
{
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "hostname": "ip-10-0-2-25",
  "instance_id": "i-abc123",
  "private_ip": "10.0.2.25",
  "public_ip": null,
  "version": "0.1.0",
  "capabilities": {
    "workload_types": ["api"],
    "max_active_containers": 10,
    "port_range": {
      "from": 4100,
      "to": 4114
    }
  }
}
```

### Request — Gateway Agent

```json
{
  "mode": "gateway",
  "environment": "dev",
  "role": "gateway",
  "hostname": "ip-10-0-1-10",
  "instance_id": "i-gateway123",
  "private_ip": "10.0.1.10",
  "public_ip": "54.233.10.20",
  "version": "0.1.0",
  "capabilities": {
    "gateway": true,
    "caddy_admin_url": "http://127.0.0.1:2019"
  }
}
```

### Response 200/201

```json
{
  "agent_id": "agent-dev-api-001",
  "status": "registered"
}
```

### Regras

- O endpoint deve ser idempotente por `instance_id`, `mode` e `environment`.
- Se o agent já existir, retornar o mesmo `agent_id`.
- A Platform API deve validar token, ambiente e permissões.

---

## POST /api/agents/{agent_id}/heartbeat

Recebe heartbeat do agent.

### Request — Runtime Agent

```json
{
  "status": "online",
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "version": "0.1.0",
  "private_ip": "10.0.2.25",
  "running_containers": 4,
  "active_deployments": 4,
  "allocated_ports": 5,
  "last_command_id": "cmd_123",
  "last_successful_command_id": "cmd_123"
}
```

### Request — Gateway Agent

```json
{
  "status": "online",
  "mode": "gateway",
  "environment": "dev",
  "role": "gateway",
  "version": "0.1.0",
  "caddy_status": "healthy",
  "routes_total": 12,
  "last_applied_desired_state_version": 43,
  "last_successful_desired_state_version": 43
}
```

### Response 200

```json
{
  "status": "accepted",
  "server_time": "2026-06-05T18:00:00Z"
}
```

### Regras

- Heartbeat deve atualizar `last_seen_at`.
- Heartbeat não deve alterar comandos.
- Falha temporária de heartbeat é retryable.

---

## GET /api/agents/{agent_id}/commands/pending

Retorna comandos pendentes direcionados ao agent.

### Response 200

```json
[
  {
    "id": "cmd_123",
    "type": "DEPLOY_APPLICATION",
    "deployment_id": "dep_456",
    "target_agent_id": "agent-dev-api-001",
    "status": "pending",
    "timeout_seconds": 600,
    "created_at": "2026-06-05T18:00:00Z",
    "payload": {
      "application": "billing-api",
      "environment": "dev",
      "image": "ghcr.io/useclarus/billing-api:v42",
      "container_name": "billing-api-dev-v42",
      "container_internal_port": 3000,
      "health_check_path": "/health",
      "requires_route": true,
      "environment_variables": {
        "NODE_ENV": "development"
      },
      "labels": {
        "devex.application": "billing-api",
        "devex.environment": "dev",
        "devex.deployment_id": "dep_456"
      }
    }
  }
]
```

### Sem comandos

```json
[]
```

### Regras

- Retornar apenas comandos cujo `target_agent_id` corresponde ao agent.
- Não retornar comandos já claimed/running/succeeded/failed.
- Pode limitar a quantidade de comandos retornados.
- Para MVP, o agent pode processar um comando por vez.

---

## POST /api/agents/{agent_id}/commands/{command_id}/claim

Faz claim atômico de um comando.

### Request

```json
{
  "status": "claimed"
}
```

### Response 200

```json
{
  "id": "cmd_123",
  "status": "claimed",
  "claimed_by": "agent-dev-api-001",
  "claimed_at": "2026-06-05T18:00:05Z"
}
```

### Response 409

```json
{
  "error": {
    "code": "COMMAND_ALREADY_CLAIMED",
    "message": "Command is no longer pending",
    "retryable": false
  }
}
```

### Regra de atomicidade

A Platform API deve executar transição:

```text
pending -> claimed
```

somente se:

```text
command.status == pending
command.target_agent_id == agent_id
```

Pseudo-SQL:

```sql
UPDATE commands
SET status = 'claimed',
    claimed_by = :agent_id,
    claimed_at = NOW()
WHERE id = :command_id
  AND target_agent_id = :agent_id
  AND status = 'pending';
```

Se nenhuma linha for alterada, retornar 409.

---

## POST /api/agents/{agent_id}/commands/{command_id}/start

Marca início da execução.

### Request

```json
{
  "status": "running"
}
```

### Response 200

```json
{
  "id": "cmd_123",
  "status": "running",
  "started_at": "2026-06-05T18:00:06Z"
}
```

### Observação

Para MVP, este endpoint pode ser opcional se o claim já marcar como running. Porém, manter `claimed` e `running` separados melhora auditoria.

---

## POST /api/agents/{agent_id}/commands/{command_id}/report

Reporta sucesso ou falha de comando.

### Request — sucesso

```json
{
  "status": "succeeded",
  "deployment_id": "dep_456",
  "result": {
    "application": "billing-api",
    "environment": "dev",
    "container_name": "billing-api-dev-v42",
    "image": "ghcr.io/useclarus/billing-api:v42",
    "runtime_private_ip": "10.0.2.25",
    "host_port": 4102,
    "container_internal_port": 3000,
    "health": "healthy",
    "requires_route": true
  }
}
```

### Request — falha

```json
{
  "status": "failed",
  "deployment_id": "dep_456",
  "error": {
    "code": "HEALTH_CHECK_FAILED",
    "message": "Application did not return a successful health response",
    "operation": "health.http",
    "retryable": false
  }
}
```

### Response 200

```json
{
  "status": "accepted"
}
```

### Regras

- Report deve ser idempotente por `command_id`.
- Se o mesmo report for enviado novamente, a Platform API deve aceitar sem duplicar efeitos.
- Não aceitar alteração de succeeded para failed, salvo fluxo administrativo explícito.
- Não aceitar report de agent diferente do `target_agent_id`.

---

## GET /api/agents/{agent_id}/desired-state

Retorna desired state para o agent.

### Runtime Agent — resposta opcional

Para MVP, Runtime Agent pode operar principalmente por comandos. Ainda assim, o endpoint pode existir para reconciliação.

```json
{
  "version": 12,
  "type": "runtime_deployments",
  "environment": "dev",
  "deployments": [
    {
      "deployment_id": "dep_456",
      "application": "billing-api",
      "container_name": "billing-api-dev-v42",
      "image": "ghcr.io/useclarus/billing-api:v42",
      "host_port": 4102,
      "container_internal_port": 3000,
      "status": "active"
    }
  ]
}
```

### Gateway Agent — resposta

```json
{
  "version": 43,
  "type": "gateway_routes",
  "environment": "dev",
  "routes": [
    {
      "id": "route_001",
      "host": "billing-api.dev.useclarus.app",
      "path": "/",
      "upstream": "10.0.2.25:4102",
      "deployment_id": "dep_456",
      "health_check_path": "/health"
    },
    {
      "id": "route_002",
      "host": "orders-api.dev.useclarus.app",
      "path": "/",
      "upstream": "10.0.2.31:4103",
      "deployment_id": "dep_789",
      "health_check_path": "/health"
    }
  ]
}
```

### Sem alterações

A API pode retornar o mesmo desired state com a mesma versão.

O agent decide não reaplicar se:

```text
remote.version == last_successful_applied_version
```

---

## POST /api/agents/{agent_id}/desired-state/report

Reporta resultado da aplicação de desired state.

### Request — sucesso

```json
{
  "status": "applied",
  "desired_state_version": 43,
  "type": "gateway_routes",
  "routes_total": 12,
  "validated_routes": 12,
  "failed_routes": 0,
  "applied_at": "2026-06-05T18:00:30Z"
}
```

### Request — falha

```json
{
  "status": "failed",
  "desired_state_version": 43,
  "type": "gateway_routes",
  "error": {
    "code": "CADDY_ROUTE_VALIDATION_FAILED",
    "message": "Route billing-api.dev.useclarus.app did not pass health check",
    "operation": "caddy.validate_route",
    "retryable": false
  }
}
```

### Response 200

```json
{
  "status": "accepted"
}
```

---

## Tipos de comando

Tipos mínimos:

```text
DEPLOY_APPLICATION
STOP_APPLICATION
REMOVE_DEPLOYMENT
CLEANUP_DRAINING
APPLY_GATEWAY_ROUTES
RECONCILE
```

---

## Payload DEPLOY_APPLICATION

```json
{
  "application": "billing-api",
  "environment": "dev",
  "image": "ghcr.io/useclarus/billing-api:v42",
  "container_name": "billing-api-dev-v42",
  "container_internal_port": 3000,
  "health_check_path": "/health",
  "requires_route": true,
  "environment_variables": {
    "NODE_ENV": "development"
  },
  "labels": {
    "devex.application": "billing-api",
    "devex.environment": "dev",
    "devex.deployment_id": "dep_456"
  }
}
```

---

## Payload STOP_APPLICATION

```json
{
  "container_name": "billing-api-dev-v42",
  "stop_timeout_seconds": 30
}
```

---

## Payload REMOVE_DEPLOYMENT

```json
{
  "deployment_id": "dep_456",
  "container_name": "billing-api-dev-v42",
  "release_port": true
}
```

---

## Payload CLEANUP_DRAINING

```json
{
  "older_than_seconds": 300
}
```

---

## Payload APPLY_GATEWAY_ROUTES

```json
{
  "desired_state_version": 43
}
```

No MVP, o Gateway Agent pode operar por polling do desired state sem precisar desse comando.

---

## Status HTTP

### 200 OK

Operação executada com sucesso.

### 201 Created

Registro criado.

### 400 Bad Request

Payload inválido.

### 401 Unauthorized

Token ausente ou inválido.

### 403 Forbidden

Agent sem permissão.

### 404 Not Found

Recurso não encontrado.

### 409 Conflict

Conflito de estado, exemplo: comando já reivindicado.

### 422 Unprocessable Entity

Payload bem formado, mas semanticamente inválido.

### 500 Internal Server Error

Erro interno da Platform API.

### 503 Service Unavailable

Platform API temporariamente indisponível.

---

## Retry por status HTTP

Retry recomendado:

```text
408
429
500
502
503
504
timeout
connection refused
```

Não retry:

```text
400
401
403
404
409
422
```

---

## Idempotência

Endpoints que devem ser idempotentes:

```text
POST /api/agents/register
POST /api/agents/{agent_id}/heartbeat
POST /api/agents/{agent_id}/commands/{command_id}/report
POST /api/agents/{agent_id}/desired-state/report
```

Claim não é idempotente no sentido de reexecutar, mas deve retornar estado consistente.

Se o mesmo agent repetir claim de um comando já claimed por ele, a API pode retornar 200 com o estado atual ou 409. A decisão deve ser consistente.

Recomendação:

```text
Retornar 200 se claimed_by == agent_id.
Retornar 409 se claimed_by != agent_id ou status não permitir execução.
```

---

## Segurança

Regras:

```text
Validar agent_id contra token.
Não permitir agent acessar comando de outro agent.
Não retornar comandos de outro agent.
Não aceitar reports de agent diferente.
Sanitizar mensagens de erro.
Não retornar secrets.
```

---

## Observabilidade

A Platform API deve registrar eventos:

```text
agent_registered
heartbeat_received
command_created
command_claimed
command_started
command_succeeded
command_failed
desired_state_requested
desired_state_applied
desired_state_failed
```

Cada evento deve conter:

```text
agent_id
command_id quando aplicável
deployment_id quando aplicável
desired_state_version quando aplicável
timestamp
status
error_code quando aplicável
```

---

## Versionamento dos contratos

A API pode usar versionamento por path no futuro:

```text
/api/v1/agents/...
```

Para MVP, usar:

```text
/api/agents/...
```

Se houver breaking changes, introduzir `/api/v2`.

---

## Critérios de aceite

Os contratos estarão adequados quando:

```text
1. Agent conseguir registrar-se.
2. Agent conseguir enviar heartbeat.
3. Runtime Agent buscar comandos pendentes.
4. Claim for atômico.
5. Agent reportar sucesso/falha.
6. Gateway Agent buscar desired state.
7. Gateway Agent reportar aplicação do desired state.
8. Respostas de erro forem estruturadas.
9. Autenticação for obrigatória.
10. Agent não conseguir acessar comandos de outro agent.
```

---

## Regra final

A Platform API é a fonte da verdade.

Agents só executam o que a Platform API direciona.

Contratos devem ser explícitos, idempotentes quando possível e seguros por padrão.

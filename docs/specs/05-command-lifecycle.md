# 05 — Ciclo de Vida dos Comandos

## Objetivo deste documento

Este documento define o ciclo de vida dos comandos executados pelos agentes da DevEx Platform.

Os comandos representam ordens emitidas pela **DevEx Platform API** para um agente específico, como:

- Fazer deploy de uma aplicação.
- Parar uma aplicação.
- Remover um deployment.
- Limpar containers antigos.
- Atualizar rotas no gateway.
- Reconciliar estado local.

O objetivo deste documento é garantir que comandos sejam processados de forma segura, auditável, idempotente e previsível.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

O agente só deve executar comandos que foram explicitamente atribuídos a ele pela Platform API.

O agente não deve decidir sozinho se um comando é seu.

A Platform API deve retornar apenas comandos direcionados ao `agent_id` que está consultando.

Regra:

```text
Platform cria e direciona comandos.
Agent busca, reivindica e executa.
Platform mantém o estado e a auditoria.
```

---

## O que é um comando

Um comando é uma instrução operacional persistida pela Platform API e direcionada a um agent específico.

Exemplo:

```json
{
  "id": "cmd_123",
  "type": "DEPLOY_APPLICATION",
  "target_agent_id": "agent-dev-api-001",
  "deployment_id": "dep_456",
  "status": "pending",
  "version": 1,
  "payload": {
    "application": "billing-api",
    "environment": "dev",
    "image": "ghcr.io/useclarus/billing-api:v42",
    "container_name": "billing-api-dev-v42",
    "container_internal_port": 3000,
    "health_check_path": "/health",
    "requires_route": true
  },
  "created_at": "2026-06-05T18:00:00Z"
}
```

---

## Estados do comando

Estados principais:

```text
pending
claimed
running
succeeded
failed
cancelled
expired
```

---

## pending

Estado inicial de um comando.

Significa que a Platform API criou a ordem, mas nenhum agent ainda reivindicou sua execução.

Um comando `pending` pode ir para:

```text
claimed
cancelled
expired
```

---

## claimed

Significa que um agent conseguiu reivindicar o comando de forma atômica.

A transição esperada é:

```text
pending -> claimed
```

O claim impede que dois agents executem o mesmo comando.

Após `claimed`, o comando pode ir para:

```text
running
failed
```

---

## running

Significa que o agent iniciou a execução efetiva do comando.

A transição esperada é:

```text
claimed -> running
```

Após `running`, o comando pode ir para:

```text
succeeded
failed
```

---

## succeeded

Estado final de sucesso.

Significa que o agent concluiu a operação e reportou sucesso para a Platform API.

Exemplo:

```json
{
  "status": "succeeded",
  "result": {
    "container_name": "billing-api-dev-v42",
    "runtime_private_ip": "10.0.2.25",
    "host_port": 4102,
    "health": "healthy"
  }
}
```

---

## failed

Estado final de falha.

Significa que o agent tentou executar o comando, mas ocorreu um erro.

Exemplo:

```json
{
  "status": "failed",
  "error": {
    "code": "HEALTH_CHECK_FAILED",
    "message": "Container did not respond successfully on /health"
  }
}
```

---

## cancelled

Estado final para comandos cancelados antes da execução.

Normalmente usado quando:

- O usuário cancelou o deploy.
- A Platform API substituiu o comando por outro.
- O deployment foi invalidado.
- O ambiente entrou em modo bloqueado.

Um comando já em `running` não deve ser simplesmente marcado como `cancelled` sem uma ação compensatória.

---

## expired

Estado final para comandos que ficaram pendentes tempo demais.

Exemplo:

```text
Comando criado há 30 minutos.
Nenhum agent fez claim.
Platform marca como expired.
```

Esse estado indica problema operacional:

- Agent offline.
- Agent sem heartbeat.
- Erro de scheduling.
- Falha na comunicação entre agent e plataforma.

---

## Máquina de estados

```text
pending
   │
   ├──> claimed
   │       │
   │       └──> running
   │               │
   │               ├──> succeeded
   │               └──> failed
   │
   ├──> cancelled
   └──> expired
```

Transições inválidas:

```text
pending -> running
pending -> succeeded
claimed -> succeeded
succeeded -> running
failed -> running
cancelled -> running
expired -> running
```

---

## Claim atômico

O claim deve ser atômico na Platform API.

Endpoint esperado:

```http
POST /api/agents/{agent_id}/commands/{command_id}/claim
```

Regra:

```text
Só pode mudar pending -> claimed se o status atual ainda for pending.
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

Se zero linhas forem alteradas, o claim falhou.

O agent não deve executar o comando.

---

## Início de execução

Após claim bem-sucedido, o agent deve marcar o comando como `running`.

Endpoint sugerido:

```http
POST /api/agents/{agent_id}/commands/{command_id}/start
```

Payload:

```json
{
  "status": "running",
  "started_at": "2026-06-05T18:00:05Z"
}
```

Para simplificar o MVP, o endpoint de claim pode já retornar o comando como `running`, mas é preferível manter `claimed` e `running` separados para auditoria.

---

## Report de sucesso

Endpoint:

```http
POST /api/agents/{agent_id}/commands/{command_id}/report
```

Payload:

```json
{
  "status": "succeeded",
  "result": {
    "deployment_id": "dep_456",
    "application": "billing-api",
    "container_name": "billing-api-dev-v42",
    "runtime_private_ip": "10.0.2.25",
    "host_port": 4102,
    "container_internal_port": 3000,
    "health": "healthy"
  }
}
```

A Platform API deve registrar:

```text
finished_at
duration
result
status = succeeded
```

---

## Report de falha

Endpoint:

```http
POST /api/agents/{agent_id}/commands/{command_id}/report
```

Payload:

```json
{
  "status": "failed",
  "error": {
    "code": "IMAGE_PULL_FAILED",
    "message": "Could not pull image ghcr.io/useclarus/billing-api:v42"
  }
}
```

A Platform API deve registrar:

```text
finished_at
duration
error_code
error_message
status = failed
```

---

## Tipos de comando

Tipos mínimos para o MVP:

```text
DEPLOY_APPLICATION
STOP_APPLICATION
REMOVE_DEPLOYMENT
CLEANUP_DRAINING
APPLY_GATEWAY_ROUTES
RECONCILE
```

---

## DEPLOY_APPLICATION

Executado pelo Runtime Agent.

Objetivo:

```text
Executar uma nova versão de uma aplicação Docker.
```

Resultado esperado:

```json
{
  "container_name": "billing-api-dev-v42",
  "runtime_private_ip": "10.0.2.25",
  "host_port": 4102,
  "container_internal_port": 3000,
  "health": "healthy",
  "requires_route": true
}
```

---

## STOP_APPLICATION

Executado pelo Runtime Agent.

Objetivo:

```text
Parar um container gerenciado.
```

Payload:

```json
{
  "container_name": "billing-api-dev-v42",
  "stop_timeout_seconds": 30
}
```

---

## REMOVE_DEPLOYMENT

Executado pelo Runtime Agent.

Objetivo:

```text
Parar/remover container e liberar porta.
```

Payload:

```json
{
  "deployment_id": "dep_456",
  "container_name": "billing-api-dev-v42",
  "release_port": true
}
```

---

## CLEANUP_DRAINING

Executado pelo Runtime Agent.

Objetivo:

```text
Remover versões antigas em estado draining após a janela de segurança.
```

Payload:

```json
{
  "older_than_seconds": 300
}
```

---

## APPLY_GATEWAY_ROUTES

Executado pelo Gateway Agent.

Objetivo:

```text
Forçar aplicação do desired state de rotas no Caddy.
```

No MVP, o Gateway Agent pode operar primariamente via desired state polling, sem comando explícito.

Ainda assim, este comando pode ser usado para operações manuais ou emergenciais.

---

## RECONCILE

Executado por Runtime Agent ou Gateway Agent.

Objetivo:

```text
Reconciliar estado local com estado real.
```

Para Runtime Agent:

```text
state.json + ports.json + docker ps
```

Para Gateway Agent:

```text
desired state + caddy.json local + Caddy active config
```

---

## Idempotência

Comandos devem ser idempotentes sempre que possível.

Exemplo:

Se o agent receber novamente um comando `DEPLOY_APPLICATION` já executado:

```text
1. Verificar deployment_id.
2. Verificar se container já existe.
3. Verificar se estado local já contém o deployment.
4. Evitar subir container duplicado.
5. Reportar estado atual, se apropriado.
```

O agent deve usar:

```text
command_id
deployment_id
container_name
labels Docker
estado local
```

para evitar duplicidade.

---

## Deduplicação

A Platform API não deve criar comandos duplicados para o mesmo deployment ativo.

Mesmo assim, o agent deve ser defensivo.

Regras:

```text
Não executar dois comandos simultâneos para o mesmo deployment_id.
Não criar dois containers com o mesmo container_name.
Não alocar duas portas para o mesmo deployment_id.
```

---

## Concorrência

Para o MVP, cada agent pode executar apenas um comando mutável por vez.

Comandos mutáveis:

```text
DEPLOY_APPLICATION
STOP_APPLICATION
REMOVE_DEPLOYMENT
CLEANUP_DRAINING
APPLY_GATEWAY_ROUTES
```

O agent ainda pode manter loops independentes para:

```text
heartbeat
polling
cleanup
reconciliation
```

Mas operações que alteram estado local, Docker, portas ou Caddy devem ser serializadas.

---

## Timeout

Cada comando deve possuir timeout.

Exemplo:

```json
{
  "timeout_seconds": 600
}
```

Se o timeout expirar:

```text
1. Agent interrompe a operação se possível.
2. Executa cleanup local se necessário.
3. Reporta failed com COMMAND_TIMEOUT.
```

---

## Retry

O retry deve ser definido por tipo de erro.

Erros potencialmente retryable:

```text
PLATFORM_API_UNAVAILABLE
TEMPORARY_NETWORK_ERROR
DOCKER_PULL_TEMPORARY_FAILURE
CADDY_ADMIN_TEMPORARILY_UNAVAILABLE
```

Erros normalmente definitivos:

```text
IMAGE_NOT_FOUND
INVALID_COMMAND_PAYLOAD
PORT_ALLOCATION_FAILED
CONFIG_INVALID
HEALTH_CHECK_FAILED
```

Detalhes em:

- `docs/specs/14-error-handling-and-retry.md`

---

## Eventos de auditoria

Cada transição relevante deve gerar evento.

Exemplos:

```text
command_created
command_claimed
command_started
command_succeeded
command_failed
command_cancelled
command_expired
```

Payload mínimo:

```json
{
  "command_id": "cmd_123",
  "agent_id": "agent-dev-api-001",
  "deployment_id": "dep_456",
  "from_status": "running",
  "to_status": "succeeded",
  "timestamp": "2026-06-05T18:02:00Z"
}
```

---

## Regras de segurança

O agent deve validar:

```text
O comando pertence ao seu agent_id.
O comando foi reivindicado com sucesso.
O payload contém campos obrigatórios.
O container a ser manipulado é gerenciado pela plataforma.
```

O agent não deve:

```text
Executar comandos não atribuídos.
Executar comandos sem claim.
Manipular containers sem label devex.managed=true, salvo regra explícita.
Logar secrets.
```

---

## Contratos relacionados

Os contratos HTTP completos devem estar em:

- `docs/specs/04-platform-api-contracts.md`

Este documento define somente o ciclo de vida conceitual e as regras de transição.

---

## Critérios de aceite

O ciclo de comandos estará correto quando:

```text
1. Comandos forem criados como pending.
2. Agents só executarem comandos atribuídos.
3. Claim for atômico.
4. Comando sem claim não for executado.
5. Toda execução terminar em succeeded ou failed.
6. Falhas forem reportadas com código estruturado.
7. Transições inválidas forem bloqueadas.
8. Comandos duplicados não criarem recursos duplicados.
9. Eventos de auditoria forem registrados.
10. Timeouts e expirations forem tratados.
```

---

## Regra final

Nenhum comando deve ser executado sem claim.

Nenhum comando deve ser executado por agent diferente do `target_agent_id`.

Nenhum erro deve ser escondido.

A Platform API mantém o ciclo de vida.

O agent executa e reporta.

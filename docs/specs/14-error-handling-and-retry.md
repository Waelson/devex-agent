# 14 — Tratamento de Erros e Retry

## Objetivo deste documento

Este documento define como o DevEx Agent deve tratar erros, classificar falhas, aplicar retry, executar rollback e reportar problemas para a DevEx Platform.

O agente executa operações sujeitas a falhas:

- Chamada para Platform API.
- Docker pull.
- Docker run.
- Alocação de portas.
- Health check.
- Caddy `/load`.
- Escrita de estado local.
- Reconciliação.

O objetivo é garantir comportamento previsível, seguro e auditável.

Este documento deve ser lido junto com:

- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`

---

## Princípio central

Falhas devem ser explícitas.

O agente não deve esconder erros nem marcar sucesso parcial como sucesso completo.

Regra:

```text
Se falhou, reporte.
Se é recuperável, tente novamente.
Se não é recuperável, falhe rápido.
Se afetou deploy, preserve a versão anterior.
```

---

## Modelo de erro

Todo erro operacional deve conter:

```text
code
message
operation
retryable
cause opcional
```

Modelo conceitual:

```json
{
  "code": "HEALTH_CHECK_FAILED",
  "message": "Application did not return successful response",
  "operation": "health.http",
  "retryable": false
}
```

---

## Códigos de erro gerais

```text
CONFIG_INVALID
AUTHENTICATION_FAILED
AUTHORIZATION_FAILED
PLATFORM_API_UNAVAILABLE
PLATFORM_API_ERROR
COMMAND_INVALID
COMMAND_CLAIM_FAILED
COMMAND_TIMEOUT
STATE_STORE_FAILED
STATE_LOAD_FAILED
STATE_WRITE_FAILED
STATE_CORRUPTED
LOCK_ACQUIRE_FAILED
```

---

## Códigos de erro Docker

```text
DOCKER_UNAVAILABLE
DOCKER_COMMAND_TIMEOUT
IMAGE_PULL_FAILED
IMAGE_NOT_FOUND
CONTAINER_ALREADY_EXISTS
CONTAINER_START_FAILED
CONTAINER_STOP_FAILED
CONTAINER_REMOVE_FAILED
CONTAINER_INSPECT_FAILED
CONTAINER_LIST_FAILED
CONTAINER_NOT_FOUND
CONTAINER_NOT_MANAGED
```

---

## Códigos de erro de portas

```text
PORT_ALLOCATION_FAILED
PORT_RANGE_EXHAUSTED
PORT_ALREADY_RESERVED
PORT_ALREADY_IN_USE
PORT_STATE_INCONSISTENT
PORT_RELEASE_FAILED
PORT_LOCK_FAILED
```

---

## Códigos de erro de health check

```text
HEALTH_CHECK_FAILED
HEALTH_CHECK_TIMEOUT
HEALTH_CHECK_CONNECTION_REFUSED
HEALTH_CHECK_INVALID_RESPONSE
HEALTH_CHECK_UNEXPECTED_STATUS
HEALTH_CHECK_CONTAINER_NOT_RUNNING
HEALTH_CHECK_GATEWAY_ROUTE_FAILED
```

---

## Códigos de erro Caddy

```text
CADDY_ADMIN_UNAVAILABLE
CADDY_CONFIG_GENERATION_FAILED
CADDY_CONFIG_INVALID
CADDY_LOAD_FAILED
CADDY_ROUTE_VALIDATION_FAILED
CADDY_LAST_GOOD_RESTORE_FAILED
DESIRED_STATE_FETCH_FAILED
```

---

## Códigos de erro de segurança

```text
INVALID_AGENT_TOKEN
INVALID_CONTAINER_NAME
INVALID_IMAGE_NAME
INVALID_HOST
INVALID_UPSTREAM
CONTAINER_NOT_MANAGED
SECRET_DETECTED_IN_LOG_CONTEXT
```

---

## Erros retryable

Erros potencialmente retryable:

```text
PLATFORM_API_UNAVAILABLE
PLATFORM_API_ERROR quando 5xx
DOCKER_UNAVAILABLE temporário
DOCKER_COMMAND_TIMEOUT dependendo da operação
IMAGE_PULL_FAILED por falha de rede
CADDY_ADMIN_UNAVAILABLE temporário
CADDY_LOAD_FAILED por indisponibilidade temporária
HEALTH_CHECK_CONNECTION_REFUSED durante readiness
HEALTH_CHECK_TIMEOUT durante readiness
LOCK_ACQUIRE_FAILED temporário
```

---

## Erros não retryable

Erros normalmente definitivos:

```text
CONFIG_INVALID
COMMAND_INVALID
AUTHENTICATION_FAILED
AUTHORIZATION_FAILED
IMAGE_NOT_FOUND
INVALID_IMAGE_NAME
INVALID_CONTAINER_NAME
INVALID_HOST
INVALID_UPSTREAM
PORT_RANGE_EXHAUSTED
CONTAINER_NOT_MANAGED
CADDY_CONFIG_INVALID
STATE_CORRUPTED
```

---

## Política de retry

Retry deve ser usado somente quando fizer sentido.

Configuração sugerida:

```yaml
retry:
  max_attempts: 3
  initial_interval_seconds: 1
  max_interval_seconds: 10
  multiplier: 2.0
  jitter: true
```

Para MVP, pode usar intervalo fixo simples.

---

## Retry com backoff

Exemplo:

```text
tentativa 1: imediato
tentativa 2: +1s
tentativa 3: +2s
tentativa 4: +4s
```

Com jitter:

```text
evita que vários agents façam retry ao mesmo tempo
```

---

## Context cancellation

Todas as operações bloqueantes devem aceitar `context.Context`.

Se o contexto for cancelado:

```text
interromper operação
retornar COMMAND_TIMEOUT ou contexto cancelado
executar cleanup se necessário
```

---

## Timeout por operação

Sugestões:

```yaml
timeouts:
  platform_request_seconds: 10
  docker_pull_seconds: 300
  docker_start_seconds: 60
  docker_stop_seconds: 30
  docker_remove_seconds: 30
  health_check_seconds: 2
  caddy_load_seconds: 10
  state_write_seconds: 5
```

---

## Tratamento de falhas no deploy

### Falha no docker pull

Ação:

```text
Não alocar porta, se ainda não alocou.
Não iniciar container.
Reportar IMAGE_PULL_FAILED.
Manter versão atual.
```

### Falha na alocação de porta

Ação:

```text
Não iniciar container.
Reportar PORT_ALLOCATION_FAILED ou PORT_RANGE_EXHAUSTED.
Manter versão atual.
```

### Falha ao iniciar container

Ação:

```text
Remover container parcial, se existir.
Liberar porta reservada.
Reportar CONTAINER_START_FAILED.
Manter versão atual.
```

### Falha no health check local

Ação:

```text
Parar/remover container novo.
Liberar porta.
Reportar HEALTH_CHECK_FAILED.
Não atualizar rota.
Manter versão atual.
```

### Falha ao aplicar Caddy

Ação:

```text
Restaurar last-good config.
Manter rota anterior.
Manter versão anterior ativa.
Reportar CADDY_LOAD_FAILED.
```

### Falha na validação da rota

Ação:

```text
Restaurar rota anterior.
Reaplicar last-good config.
Reportar CADDY_ROUTE_VALIDATION_FAILED.
Manter versão anterior ativa.
```

---

## Rollback

Rollback deve preservar disponibilidade.

Regras:

```text
Não remover versão antiga antes da nova estar validada.
Manter versão antiga até rota nova estar validada.
Se falhar, restaurar rota antiga.
Remover versão nova se ela não deve permanecer.
Liberar porta da versão nova se removida.
```

---

## Cleanup após falha

Após falha de deploy, o Runtime Agent deve limpar recursos parciais.

Exemplo:

```text
container criado mas unhealthy -> remover container
porta reserved -> liberar
state parcial -> atualizar como failed ou remover
```

Não deixar recursos zumbis.

---

## Falha ao reportar resultado

Se a operação local terminou, mas o report para Platform API falhou:

```text
Persistir resultado local.
Tentar reportar novamente depois.
Não repetir operação local desnecessariamente.
```

Isso evita duplicar deploys.

O estado local deve indicar:

```text
pending_report=true
```

---

## Falha de state store

Se não conseguir salvar estado local após uma operação crítica:

```text
Reportar STATE_STORE_FAILED.
Logar erro.
Evitar prosseguir com operações que dependem desse estado.
```

Se possível, priorizar consistência:

```text
não marcar sucesso se estado local crítico não foi persistido
```

---

## Falha de reconciliação

Se reconciliação falhar:

```text
Logar erro.
Reportar evento.
Tentar novamente no próximo ciclo.
Não derrubar agent necessariamente.
```

Erro:

```text
RECONCILIATION_FAILED
```

---

## Classificação por severidade

### low

Falhas transitórias sem impacto imediato.

Exemplo:

```text
heartbeat falhou uma vez
```

### medium

Falhas que afetam uma operação, mas não disponibilidade atual.

Exemplo:

```text
docker pull falhou para nova versão
```

### high

Falhas que podem afetar tráfego ou estado crítico.

Exemplo:

```text
caddy load falhou
state store corrompido
```

### critical

Falhas que impedem operação do agent.

Exemplo:

```text
config inválida
token ausente
docker indisponível no Runtime Agent
```

---

## Report de erro

Formato:

```json
{
  "status": "failed",
  "error": {
    "code": "CONTAINER_START_FAILED",
    "message": "Container could not be started",
    "operation": "docker.run",
    "retryable": false
  }
}
```

Mensagens devem ser úteis, mas não vazar secrets.

---

## Logs de erro

Todo erro deve ser logado com:

```text
error_code
operation
component
command_id
deployment_id
application
container_name quando aplicável
retryable
attempt
```

---

## Retry em Platform API

Para chamadas à Platform API:

Retry em:

```text
timeout
connection refused
HTTP 5xx
```

Não retry em:

```text
HTTP 400
HTTP 401
HTTP 403
HTTP 404 para recurso inválido
HTTP 409 em claim
```

Claim com 409 significa que outro processo reivindicou ou o comando mudou de estado.

O agent não deve executar o comando.

---

## Retry em Docker pull

Retry em:

```text
falha temporária de rede
registry temporariamente indisponível
timeout transitório
```

Não retry em:

```text
image not found
authentication denied
invalid reference format
```

---

## Retry em health check

Health check deve ter retry como parte do comportamento normal de readiness.

Exemplo:

```text
6 tentativas
5 segundos entre tentativas
```

Após esgotar tentativas:

```text
HEALTH_CHECK_FAILED
```

---

## Retry em Caddy /load

Retry em:

```text
Caddy Admin API temporariamente indisponível
timeout transitório
HTTP 5xx
```

Não retry em:

```text
CADDY_CONFIG_INVALID
INVALID_HOST
INVALID_UPSTREAM
```

---

## Idempotência em retries

Retries não devem criar duplicidades.

Exemplo:

```text
Se docker run foi executado e o retry ocorre, verificar se o container já existe antes de criar outro.
```

Usar:

```text
deployment_id
container_name
labels
state local
docker inspect
```

---

## Circuit breaker futuro

Fora do MVP, mas recomendado para Platform API e Caddy Admin API.

Exemplo:

```text
muitas falhas consecutivas -> pausar tentativas por intervalo curto
```

---

## Critérios de aceite

Tratamento de erros estará correto quando:

```text
1. Erros tiverem códigos estruturados.
2. Erros retryable forem diferenciados de erros definitivos.
3. Comandos falhos forem reportados como failed.
4. Falhas parciais fizerem cleanup.
5. Versão antiga for preservada em falhas de atualização.
6. Caddy restaurar last-good em falhas.
7. Retry não criar duplicidade.
8. Context timeout for respeitado.
9. Secrets não aparecerem em mensagens de erro.
10. Falha de report for persistida para retry posterior.
```

---

## Regra final

Erro operacional deve ser tratado como parte normal do sistema.

Falhar de forma clara é melhor que esconder inconsistência.

Retry deve ser seguro.

Rollback deve preservar disponibilidade.

Reports devem ser honestos.

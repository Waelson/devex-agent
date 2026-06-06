# 06 — Fluxo de Deploy

## Objetivo deste documento

Este documento descreve o fluxo completo de deploy de aplicações na DevEx Platform usando Runtime Agent, Gateway Agent, Docker, Caddy e Route 53.

Ele cobre:

- Deploy inicial.
- Atualização de imagem.
- Alocação de portas.
- Health checks.
- Atualização de rotas no Caddy.
- Draining.
- Rollback.
- Cleanup.
- Diferenças entre APIs, frontends e workers.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

Deploy é a transformação de um estado desejado em estado real.

A DevEx Platform define:

```text
Aplicação
Ambiente
Imagem
Instância alvo
Domínio
Estratégia
```

O Runtime Agent executa:

```text
Docker pull
Port allocation
Docker run
Health check
Report
```

O Gateway Agent executa:

```text
Gerar caddy.json
Aplicar /load
Validar rota
Report
```

---

## Entidades envolvidas

### Application

Representa a aplicação cadastrada na plataforma.

Exemplo:

```json
{
  "name": "billing-api",
  "workload_type": "api",
  "default_internal_port": 3000,
  "default_health_check_path": "/health",
  "requires_route": true
}
```

### Deployment

Representa uma execução específica de uma versão da aplicação.

Exemplo:

```json
{
  "id": "dep_456",
  "application": "billing-api",
  "environment": "dev",
  "image": "ghcr.io/useclarus/billing-api:v42",
  "status": "deploying"
}
```

### Command

Representa uma ordem enviada a um agent.

Exemplo:

```json
{
  "id": "cmd_123",
  "type": "DEPLOY_APPLICATION",
  "target_agent_id": "agent-dev-api-001"
}
```

### Route

Representa a rota pública desejada.

Exemplo:

```json
{
  "host": "billing-api.dev.useclarus.app",
  "upstream": "10.0.2.25:4102"
}
```

---

## Tipos de workload

A plataforma deve tratar workloads de forma diferente.

### API

Características:

```text
requires_route = true
health_check HTTP
exposta via Caddy
```

Exemplo:

```text
billing-api.dev.useclarus.app -> 10.0.2.25:4102
```

### Frontend

Características:

```text
requires_route = true
health_check HTTP
exposto via Caddy
```

Exemplo:

```text
clarus-web.dev.useclarus.app -> 10.0.2.30:3101
```

### Worker

Características:

```text
requires_route = false
não entra no Caddy
health check pode ser processo/container/heartbeat
```

Exemplo:

```text
invoice-worker-dev-v7
```

---

## Fluxo geral de deploy

```text
1. Desenvolvedor solicita deploy.
2. Platform API valida a solicitação.
3. Platform API escolhe o Runtime Agent.
4. Platform API cria Deployment.
5. Platform API cria Command DEPLOY_APPLICATION.
6. Runtime Agent busca comando.
7. Runtime Agent faz claim.
8. Runtime Agent executa Docker pull.
9. Runtime Agent aloca porta, se necessário.
10. Runtime Agent sobe container.
11. Runtime Agent executa health check.
12. Runtime Agent reporta resultado.
13. Platform API atualiza estado do Deployment.
14. Se requires_route=true, Platform API atualiza desired state de rotas.
15. Gateway Agent aplica configuração no Caddy.
16. Gateway Agent valida rota.
17. Platform API marca Deployment como healthy.
```

---

## Deploy inicial de API ou frontend

### 1. Solicitação

Exemplo de solicitação feita pela UI:

```json
{
  "application": "billing-api",
  "environment": "dev",
  "image": "ghcr.io/useclarus/billing-api:v42",
  "domain": "billing-api.dev.useclarus.app"
}
```

---

### 2. Validação pela Platform API

A Platform API deve validar:

```text
Aplicação existe.
Ambiente existe.
Imagem/tag foi informada.
Usuário tem permissão.
Domínio pertence ao ambiente.
Aplicação possui workload_type compatível.
Existe agent online compatível.
Há capacidade disponível.
```

---

### 3. Seleção do Runtime Agent

Critérios do MVP:

```text
environment compatível
role compatível
agent online
capacidade disponível
menor número de containers ativos
```

Exemplo:

```text
billing-api workload_type=api
agent-dev-api-001 role=api
```

---

### 4. Criação do Deployment

A Platform API cria:

```json
{
  "id": "dep_456",
  "application": "billing-api",
  "environment": "dev",
  "image": "ghcr.io/useclarus/billing-api:v42",
  "target_agent_id": "agent-dev-api-001",
  "domain": "billing-api.dev.useclarus.app",
  "status": "requested"
}
```

---

### 5. Criação do comando

```json
{
  "id": "cmd_123",
  "type": "DEPLOY_APPLICATION",
  "target_agent_id": "agent-dev-api-001",
  "deployment_id": "dep_456",
  "status": "pending",
  "payload": {
    "application": "billing-api",
    "environment": "dev",
    "image": "ghcr.io/useclarus/billing-api:v42",
    "container_name": "billing-api-dev-v42",
    "container_internal_port": 3000,
    "health_check_path": "/health",
    "requires_route": true
  }
}
```

---

### 6. Execução pelo Runtime Agent

Fluxo local:

```text
1. Claim do comando.
2. docker pull.
3. Alocar porta.
4. docker run.
5. Health check local.
6. Persistir estado local.
7. Reportar sucesso.
```

Exemplo de container:

```bash
docker run -d   --name billing-api-dev-v42   --restart unless-stopped   -p 4102:3000   --label devex.managed=true   --label devex.application=billing-api   --label devex.environment=dev   --label devex.deployment_id=dep_456   ghcr.io/useclarus/billing-api:v42
```

---

### 7. Report do Runtime Agent

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

---

### 8. Atualização do desired state de rotas

A Platform API cria ou atualiza rota:

```json
{
  "host": "billing-api.dev.useclarus.app",
  "path": "/",
  "upstream": "10.0.2.25:4102",
  "deployment_id": "dep_456",
  "status": "active"
}
```

Incrementa a versão do desired state do gateway.

---

### 9. Gateway Agent aplica Caddy

O Gateway Agent busca:

```json
{
  "version": 43,
  "routes": [
    {
      "host": "billing-api.dev.useclarus.app",
      "upstream": "10.0.2.25:4102",
      "health_check_path": "/health"
    }
  ]
}
```

Gera `caddy.json` e aplica:

```http
POST http://127.0.0.1:2019/load
Content-Type: application/json
```

---

### 10. Validação via Caddy

Validação local:

```bash
curl -f   -H "Host: billing-api.dev.useclarus.app"   http://127.0.0.1/health
```

Se sucesso:

```text
route = healthy
deployment = healthy
```

---

## Atualização de imagem

Atualizar imagem não deve substituir o container atual em linha.

Use fluxo blue/green local.

### Estado inicial

```text
billing-api-dev-v41 -> 10.0.2.25:4101
Caddy -> 10.0.2.25:4101
```

### Nova versão

```text
billing-api-dev-v42 -> 10.0.2.25:4102
```

Durante a validação, o Caddy continua apontando para v41.

### Após sucesso

```text
Caddy -> 10.0.2.25:4102
v41 -> draining
v42 -> active
```

---

## Fluxo detalhado de atualização

```text
1. Dev solicita deploy da nova imagem.
2. Platform API identifica versão atual ativa.
3. Platform API cria novo Deployment.
4. Runtime Agent sobe nova versão em nova porta.
5. Runtime Agent valida health local.
6. Platform API atualiza rota para nova versão.
7. Gateway Agent aplica Caddy.
8. Gateway Agent valida rota.
9. Platform API marca nova versão como active.
10. Versão antiga entra em draining.
11. Runtime Agent remove versão antiga após grace period.
```

---

## Draining

Draining é o estado temporário da versão antiga após troca de rota.

Objetivo:

```text
Permitir rollback rápido.
Evitar remoção imediata.
Dar tempo para conexões antigas encerrarem.
```

Exemplo:

```json
{
  "deployment_id": "dep_455",
  "container_name": "billing-api-dev-v41",
  "status": "draining",
  "host_port": 4101,
  "draining_started_at": "2026-06-05T18:00:00Z"
}
```

Após `draining_grace_period_seconds`, o Runtime Agent pode remover o container.

---

## Rollback

Rollback pode ocorrer em diferentes fases.

### Falha antes do container novo subir

Exemplo:

```text
docker pull falhou
```

Ação:

```text
Manter versão atual.
Marcar novo deployment como failed.
```

### Falha no health check local

Ação:

```text
Remover container novo.
Liberar porta nova.
Manter rota antiga.
Marcar deployment como failed.
```

### Falha ao aplicar Caddy

Ação:

```text
Manter rota antiga ou restaurar last-good config.
Manter versão antiga ativa.
Marcar novo deployment como failed ou route_failed.
```

### Falha após troca de rota

Ação:

```text
Platform API restaura desired route para versão anterior.
Gateway Agent reaplica Caddy.
Runtime Agent mantém/remover nova versão conforme comando.
```

---

## Deploy de worker

Workers normalmente não precisam de Caddy.

Fluxo:

```text
1. Dev solicita deploy.
2. Platform API escolhe Runtime Agent role=worker.
3. Runtime Agent faz pull da imagem.
4. Runtime Agent sobe container.
5. Health check valida container/processo.
6. Runtime Agent reporta sucesso.
7. Platform marca worker como healthy.
```

Não há:

```text
route
Caddy
DNS
Gateway Agent
```

Health check de worker pode ser:

```text
container running
exit code
heartbeat interno
consumo de fila
```

---

## Gestão de portas no deploy

Para APIs/frontends:

```text
port host = alocada pelo Runtime Agent
port container = definida pela aplicação
```

Exemplo:

```text
host_port=4102
container_internal_port=3000
```

Para workers:

```text
porta pode não ser necessária
```

Se worker expuser endpoint administrativo, a porta também deve ser gerenciada pelo agent.

---

## Health checks

### Local Runtime Agent

```text
http://127.0.0.1:{host_port}{health_check_path}
```

### Via Gateway Agent

```text
http://127.0.0.1/{health_check_path}
Host: billing-api.dev.useclarus.app
```

### Público

```text
https://billing-api.dev.useclarus.app/health
```

Para MVP, a validação via Gateway local é suficiente para confirmar roteamento.

---

## Status do deployment

Estados sugeridos:

```text
requested
scheduled
command_created
pulling_image
starting_container
checking_health
container_healthy
updating_route
route_updated
validating_route
healthy
failed
draining
rolled_back
removed
```

---

## Eventos do deployment

Eventos importantes:

```text
deployment_requested
agent_selected
command_created
command_claimed
image_pull_started
image_pull_completed
port_allocated
container_started
health_check_started
health_check_passed
health_check_failed
route_desired_state_updated
caddy_load_started
caddy_load_completed
route_validation_passed
route_validation_failed
deployment_healthy
deployment_failed
deployment_rolled_back
deployment_draining
deployment_removed
```

---

## Regras de capacidade

Antes de criar comando, a Platform API deve verificar:

```text
agent online
max_active_containers não excedido
há portas disponíveis
workload_type compatível
ambiente compatível
```

Durante blue/green, a instância precisa ter capacidade temporária para rodar duas versões.

Por isso, recomenda-se:

```text
max_active_containers = 10
port_range = 4100-4114
```

Permitindo:

```text
10 containers ativos
até 5 containers temporários/draining
```

---

## Idempotência do deploy

O deploy deve ser idempotente por `deployment_id`.

Se o Runtime Agent reiniciar no meio:

```text
1. Carrega estado local.
2. Inspeciona Docker.
3. Verifica containers com labels.
4. Reconstrói estado se possível.
5. Reporta estado atual.
```

Se o comando for recebido novamente:

```text
1. Verifica se deployment_id já existe.
2. Se já estiver healthy, reporta sucesso.
3. Se estiver parcial, tenta reconciliar.
4. Se estiver inconsistente, reporta falha estruturada.
```

---

## Segurança

Durante deploy:

```text
Não logar secrets.
Não expor portas publicamente.
Não manipular containers não gerenciados.
Validar payloads.
Aplicar labels obrigatórias.
Usar Security Groups para restringir acesso às portas runtime.
```

---

## Critérios de aceite

O fluxo de deploy estará correto quando:

```text
1. Um deploy inicial subir container e publicar rota.
2. Uma atualização subir nova versão sem derrubar a antiga antes da validação.
3. A porta for alocada automaticamente.
4. O health check local for executado.
5. O Gateway Agent aplicar a rota no Caddy.
6. O health check via Caddy validar a rota.
7. A versão antiga entrar em draining.
8. O rollback preservar ou restaurar a versão anterior.
9. Workers puderem ser deployados sem rota.
10. O status final for reportado claramente à Platform API.
```

---

## Regra final

Deploy deve ser seguro por padrão.

Não substituir container em execução diretamente.

Não remover versão antiga antes da nova estar validada.

Não expor portas de runtime publicamente.

Não depender do desenvolvedor para detalhes operacionais.

A plataforma define o estado desejado.

Os agents aplicam e reportam.

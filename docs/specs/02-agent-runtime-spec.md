# 02 — Especificação do Runtime Agent

## Objetivo deste documento

Este documento define a especificação funcional e técnica do **Runtime Agent**.

O Runtime Agent é o agente responsável por executar aplicações Docker em instâncias EC2 Runtime. Ele recebe comandos da DevEx Platform, executa containers, gerencia portas, faz health checks, mantém estado local e reporta o resultado das operações.

Este documento deve ser lido junto com:

- `docs/specs/00-product-overview.md`
- `docs/specs/01-architecture.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/07-port-management.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/12-security.md`
- `docs/specs/14-error-handling-and-retry.md`
- `docs/specs/15-configuration.md`

---

## Definição

O **Runtime Agent** é um processo instalado em uma EC2 responsável por executar workloads de aplicação.

Ele atua como executor local da DevEx Platform.

A Platform API decide:

```text
Qual aplicação deve ser executada.
Em qual ambiente.
Em qual instância.
Com qual imagem.
Com qual estratégia.
```

O Runtime Agent executa:

```text
docker pull
docker run
docker stop
docker rm
alocação de porta
health check
persistência local
reporte de status
```

---

## Princípio central

O Runtime Agent não é o cérebro do sistema.

A regra é:

```text
Platform decide.
Runtime Agent executa.
Docker roda.
```

O Runtime Agent deve ser previsível, seguro e idempotente sempre que possível.

---

## Responsabilidades

O Runtime Agent deve ser responsável por:

- Registrar a instância EC2 na DevEx Platform.
- Enviar heartbeat periódico.
- Buscar comandos pendentes direcionados a ele.
- Fazer claim atômico de comandos.
- Executar comandos após claim bem-sucedido.
- Fazer pull de imagens Docker.
- Subir containers Docker.
- Parar containers.
- Remover containers.
- Inspecionar containers.
- Listar containers gerenciados.
- Alocar portas do host.
- Liberar portas.
- Persistir estado local.
- Reconciliar estado local com Docker real.
- Executar health checks locais.
- Reportar sucesso ou falha para a Platform API.
- Manter versão anterior em draining quando necessário.
- Apoiar rollback local quando aplicável.
- Limpar containers antigos após janela de segurança.
- Registrar logs estruturados.

---

## Fora do escopo

O Runtime Agent não deve:

- Criar ou alterar registros DNS no Route 53.
- Aplicar configuração no Caddy.
- Gerar `caddy.json`.
- Fazer reload ou `/load` no Caddy.
- Decidir em qual EC2 uma aplicação deve rodar.
- Expor uma API pública.
- Implementar scheduler global.
- Implementar autoscaling.
- Fazer traffic splitting.
- Implementar canary deployment no MVP.
- Alterar Security Groups.
- Provisionar EC2.
- Armazenar secrets em logs ou estado local.

---

## Modos e roles

O Runtime Agent roda com:

```yaml
agent:
  mode: "runtime"
  environment: "dev"
  role: "api"
```

O campo `role` indica o tipo de workload aceito pela instância.

Exemplos:

```text
frontend
api
worker
```

A Platform API usa o `role` e as capabilities do agent para decidir onde um deploy deve ser executado.

O agent não deve decidir se uma aplicação é compatível com ele. Ele deve receber comandos já direcionados pela Platform API.

---

## Capabilities

Ao se registrar, o Runtime Agent deve informar suas capacidades.

Exemplo:

```json
{
  "agent_id": "agent-dev-api-001",
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "private_ip": "10.0.2.25",
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

Essas informações permitem que a Platform API faça o scheduling.

---

## Configuração

Arquivo padrão:

```text
/etc/devex-agent/config.yaml
```

Exemplo:

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

Regras:

- Configuração inválida deve impedir o startup.
- Secrets não devem ser logados.
- O agent deve falhar rápido quando campos obrigatórios estiverem ausentes.
- O `agent.id` pode ser vazio no primeiro boot e preenchido após registro.

---

## Registro do agent

No primeiro boot, o Runtime Agent deve registrar a instância na Platform API.

Endpoint esperado:

```http
POST /api/agents/register
```

Payload exemplo:

```json
{
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "hostname": "ip-10-0-2-25",
  "instance_id": "i-abc123",
  "private_ip": "10.0.2.25",
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

Resposta esperada:

```json
{
  "agent_id": "agent-dev-api-001",
  "status": "registered"
}
```

Após registro, o agent deve persistir o `agent_id` localmente.

Arquivo sugerido:

```text
/var/lib/devex-agent/agent.json
```

---

## Heartbeat

O Runtime Agent deve enviar heartbeat periódico.

Endpoint esperado:

```http
POST /api/agents/{agent_id}/heartbeat
```

Payload exemplo:

```json
{
  "status": "online",
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "private_ip": "10.0.2.25",
  "running_containers": 4,
  "active_deployments": 4,
  "allocated_ports": 5,
  "last_applied_command_id": "cmd_123"
}
```

O heartbeat deve ser enviado mesmo quando não há comandos pendentes.

Falhas temporárias no heartbeat devem ser logadas e retentadas, mas não devem derrubar o agent imediatamente.

---

## Busca de comandos

O Runtime Agent deve usar polling para buscar comandos.

Endpoint esperado:

```http
GET /api/agents/{agent_id}/commands/pending
```

A Platform API deve retornar apenas comandos direcionados ao agent solicitante.

Exemplo de resposta:

```json
[
  {
    "id": "cmd_123",
    "type": "DEPLOY_APPLICATION",
    "deployment_id": "dep_456",
    "version": 42,
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
]
```

O agent deve processar apenas comandos que conseguir reivindicar com sucesso.

---

## Claim de comando

Antes de executar um comando, o agent deve fazer claim.

Endpoint esperado:

```http
POST /api/agents/{agent_id}/commands/{command_id}/claim
```

Payload:

```json
{
  "status": "claimed"
}
```

A Platform API deve garantir transição atômica:

```text
pending -> claimed
```

Se o claim falhar, o agent não deve executar o comando.

---

## Tipos de comandos suportados no MVP

O MVP deve suportar inicialmente:

```text
DEPLOY_APPLICATION
STOP_APPLICATION
REMOVE_DEPLOYMENT
CLEANUP_DRAINING
```

### DEPLOY_APPLICATION

Executa uma nova versão de uma aplicação.

Responsabilidades:

- Fazer pull da imagem.
- Alocar porta.
- Subir container.
- Executar health check.
- Reportar endpoint saudável.

### STOP_APPLICATION

Para um container gerenciado pelo agent.

### REMOVE_DEPLOYMENT

Remove container, libera porta e atualiza estado local.

### CLEANUP_DRAINING

Remove containers antigos em estado draining após janela de segurança.

---

## Comando DEPLOY_APPLICATION

Payload esperado:

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

Fluxo:

```text
1. Validar payload.
2. Reconciliar estado local com Docker.
3. Verificar capacidade.
4. Fazer docker pull.
5. Alocar porta.
6. Criar container.
7. Executar health check.
8. Marcar porta como active.
9. Persistir estado local.
10. Reportar sucesso.
```

---

## Docker pull

O agent deve executar:

```bash
docker pull <image>
```

Regras:

- Usar timeout configurável.
- Capturar stdout/stderr.
- Logar início e fim da operação.
- Não logar credenciais.
- Em caso de falha, reportar `IMAGE_PULL_FAILED`.

---

## Alocação de porta

A porta do host deve ser alocada pelo Runtime Agent.

O desenvolvedor não escolhe porta.

A aplicação não define porta fixa do host.

Exemplo:

```text
container_internal_port = 3000
host_port = 4102
```

Comando Docker:

```bash
docker run -d   --name billing-api-dev-v42   --restart unless-stopped   -p 4102:3000   ghcr.io/useclarus/billing-api:v42
```

Estados de porta:

```text
available
reserved
active
draining
failed
released
unmanaged
```

O agent deve usar lock local antes de alocar porta.

Arquivo sugerido:

```text
/var/lib/devex-agent/ports.json
```

Detalhes em:

- `docs/specs/07-port-management.md`

---

## Execução do container

O agent deve iniciar o container com:

- Nome versionado.
- Labels da DevEx Platform.
- Restart policy.
- Porta alocada.
- Variáveis de ambiente permitidas.
- Network configurada quando aplicável.

Exemplo:

```bash
docker run -d   --name billing-api-dev-v42   --restart unless-stopped   -p 4102:3000   --label devex.managed=true   --label devex.application=billing-api   --label devex.environment=dev   --label devex.deployment_id=dep_456   -e NODE_ENV=development   ghcr.io/useclarus/billing-api:v42
```

O agent deve evitar conflito de nomes.

Se já existir container com o mesmo nome, deve inspecionar se pertence ao mesmo deployment.

---

## Health check local

Para workloads HTTP, o health check local deve usar:

```text
http://127.0.0.1:{host_port}{health_check_path}
```

Exemplo:

```bash
curl -f http://127.0.0.1:4102/health
```

O agent deve implementar health check nativamente em Go, não depender obrigatoriamente de `curl`.

Configuração:

```yaml
health_check:
  timeout_seconds: 2
  interval_seconds: 5
  retries: 6
```

Se o health check falhar:

```text
1. Parar/remover container novo.
2. Liberar porta.
3. Persistir estado.
4. Reportar HEALTH_CHECK_FAILED.
```

---

## Report de sucesso

Após deploy bem-sucedido:

```http
POST /api/agents/{agent_id}/commands/{command_id}/report
```

Payload:

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

A Platform API usará esse resultado para atualizar o estado desejado das rotas quando `requires_route = true`.

---

## Report de falha

Exemplo:

```json
{
  "status": "failed",
  "deployment_id": "dep_456",
  "error": {
    "code": "HEALTH_CHECK_FAILED",
    "message": "Container did not respond successfully on /health"
  }
}
```

Códigos esperados:

```text
IMAGE_PULL_FAILED
PORT_ALLOCATION_FAILED
CONTAINER_START_FAILED
HEALTH_CHECK_FAILED
DOCKER_UNAVAILABLE
STATE_STORE_FAILED
COMMAND_INVALID
COMMAND_CLAIM_FAILED
```

---

## Atualização de imagem

Uma atualização de imagem deve ser tratada como novo deployment.

Não alterar container existente em execução.

Fluxo:

```text
1. Manter versão atual ativa.
2. Fazer pull da nova imagem.
3. Alocar nova porta.
4. Subir novo container.
5. Executar health check local.
6. Reportar novo endpoint saudável.
7. Aguardar Gateway Agent atualizar rota.
8. Marcar versão antiga como draining.
9. Remover versão antiga após janela.
```

Exemplo:

```text
Antes:
billing-api-dev-v41 -> 10.0.2.25:4101

Novo:
billing-api-dev-v42 -> 10.0.2.25:4102

Depois:
billing-api.dev.useclarus.app -> 10.0.2.25:4102
```

---

## Draining

Após uma atualização bem-sucedida, a versão antiga deve entrar em estado `draining`.

Exemplo:

```json
{
  "container_name": "billing-api-dev-v41",
  "status": "draining",
  "host_port": 4101,
  "draining_started_at": "2026-06-05T18:00:00Z"
}
```

Após a janela configurada:

```yaml
runtime:
  draining_grace_period_seconds: 300
```

O agent pode remover o container antigo e liberar a porta.

---

## Rollback local

O Runtime Agent pode apoiar rollback local quando:

- A nova imagem não sobe.
- O health check local falha.
- A Platform API envia comando explícito para remover a nova versão.
- A rota não foi atualizada e a versão antiga ainda está ativa.

O Runtime Agent não deve decidir sozinho atualizar a rota de volta no Caddy.

Rollback de rota é responsabilidade da Platform API + Gateway Agent.

O Runtime Agent deve garantir que a versão antiga permaneça disponível durante a janela de segurança.

---

## STOP_APPLICATION

Payload esperado:

```json
{
  "container_name": "billing-api-dev-v42",
  "stop_timeout_seconds": 30
}
```

Fluxo:

```text
1. Validar se container é gerenciado.
2. Executar docker stop.
3. Atualizar estado local.
4. Reportar resultado.
```

O agent não deve parar containers não gerenciados, salvo instrução explícita e segura da Platform API.

---

## REMOVE_DEPLOYMENT

Payload esperado:

```json
{
  "container_name": "billing-api-dev-v41",
  "deployment_id": "dep_455",
  "release_port": true
}
```

Fluxo:

```text
1. Parar container, se estiver rodando.
2. Remover container.
3. Liberar porta.
4. Atualizar estado local.
5. Reportar resultado.
```

---

## CLEANUP_DRAINING

O agent deve executar periodicamente ou via comando a limpeza de deployments antigos em draining.

Critérios:

```text
status = draining
draining_started_at + grace_period < now
```

Ação:

```text
docker stop
docker rm
liberar porta
atualizar estado local
reportar evento
```

---

## Estado local

Diretório:

```text
/var/lib/devex-agent
```

Arquivos:

```text
agent.json
state.json
ports.json
locks/
```

Exemplo de `state.json`:

```json
{
  "agent_id": "agent-dev-api-001",
  "private_ip": "10.0.2.25",
  "deployments": [
    {
      "application": "billing-api",
      "environment": "dev",
      "deployment_id": "dep_456",
      "container_name": "billing-api-dev-v42",
      "image": "ghcr.io/useclarus/billing-api:v42",
      "host_port": 4102,
      "container_internal_port": 3000,
      "status": "active"
    }
  ]
}
```

Exemplo de `ports.json`:

```json
{
  "range": {
    "from": 4100,
    "to": 4114
  },
  "allocations": {
    "4102": {
      "status": "active",
      "container_name": "billing-api-dev-v42",
      "deployment_id": "dep_456"
    }
  }
}
```

---

## Reconciliação com Docker

O agent deve reconciliar estado local com Docker real.

Casos:

### Porta alocada localmente, container não existe

Ação:

```text
Liberar porta ou marcar como inconsistent.
Reportar evento.
```

### Container existe, mas não está no estado local

Se tiver label `devex.managed=true`:

```text
Importar para estado local ou marcar como orphaned.
```

Se não tiver label de gerenciamento:

```text
Marcar como unmanaged.
Não manipular automaticamente.
```

### Porta em uso por processo externo

Ação:

```text
Marcar porta como unmanaged.
Não alocar essa porta.
```

---

## Idempotência

O agent deve ser idempotente sempre que possível.

Exemplo:

Se receber novamente um comando já executado, deve identificar pelo `command_id` ou `deployment_id` e evitar duplicação.

Regras:

```text
Não subir dois containers iguais para o mesmo deployment_id.
Não alocar nova porta se o deployment já estiver ativo.
Não reportar sucesso duplicado sem necessidade.
```

---

## Concorrência

Para MVP, o Runtime Agent pode processar um comando por vez.

Isso simplifica:

- Alocação de portas.
- Manipulação de estado local.
- Operações Docker.
- Tratamento de rollback.

Evoluções futuras podem permitir concorrência controlada.

Mesmo processando um comando por vez, o agent pode manter loops independentes para:

```text
heartbeat
polling de comandos
cleanup draining
reconciliation
```

Mas operações mutáveis devem usar lock.

---

## Locks

Operações críticas devem usar lock local.

Exemplos:

```text
alocação de porta
alteração de state.json
alteração de ports.json
cleanup de containers
```

Diretório sugerido:

```text
/var/lib/devex-agent/locks/
```

Para Go, pode-se usar lock de arquivo ou mutex interno.

Se houver apenas um processo agent por EC2, mutex interno pode ser suficiente no MVP.

---

## Segurança

O Runtime Agent é um processo privilegiado porque interage com Docker.

Regras:

- Não expor API pública.
- Não logar secrets.
- Não logar tokens.
- Não logar variáveis de ambiente sensíveis.
- Validar payloads recebidos.
- Manipular apenas containers gerenciados, salvo instrução explícita.
- Usar labels para identificar containers gerenciados.
- Restringir portas de runtime por Security Group.
- Ler token da Platform API de arquivo protegido.

Arquivo de token:

```text
/etc/devex-agent/token
```

Permissões recomendadas:

```bash
chmod 600 /etc/devex-agent/token
```

---

## Labels obrigatórias em containers

Todo container criado pelo Runtime Agent deve conter labels de gerenciamento.

Labels mínimas:

```text
devex.managed=true
devex.agent_id=<agent_id>
devex.application=<application>
devex.environment=<environment>
devex.deployment_id=<deployment_id>
devex.command_id=<command_id>
```

Essas labels são essenciais para reconciliação.

---

## Logs

O Runtime Agent deve emitir logs estruturados.

Campos recomendados:

```text
level
timestamp
component
agent_id
environment
role
command_id
deployment_id
application
container_name
message
error_code
```

Exemplo:

```json
{
  "level": "info",
  "component": "runtime-agent",
  "agent_id": "agent-dev-api-001",
  "command_id": "cmd_123",
  "deployment_id": "dep_456",
  "application": "billing-api",
  "container_name": "billing-api-dev-v42",
  "message": "container started"
}
```

---

## Métricas futuras

O MVP pode começar com logs, mas a implementação não deve dificultar métricas futuras.

Métricas candidatas:

```text
runtime_agent_heartbeat_total
runtime_agent_commands_processed_total
runtime_agent_command_errors_total
runtime_agent_deploy_duration_seconds
runtime_agent_running_containers
runtime_agent_allocated_ports
runtime_agent_port_allocation_errors_total
runtime_agent_health_check_failures_total
```

---

## Erros esperados

Códigos mínimos:

```text
CONFIG_INVALID
PLATFORM_API_UNAVAILABLE
COMMAND_CLAIM_FAILED
COMMAND_INVALID
IMAGE_PULL_FAILED
PORT_ALLOCATION_FAILED
CONTAINER_START_FAILED
HEALTH_CHECK_FAILED
DOCKER_UNAVAILABLE
STATE_STORE_FAILED
CONTAINER_STOP_FAILED
CONTAINER_REMOVE_FAILED
RECONCILIATION_FAILED
```

Cada erro deve ser reportado com:

```json
{
  "code": "IMAGE_PULL_FAILED",
  "message": "Failed to pull image ghcr.io/useclarus/billing-api:v42"
}
```

---

## Inicialização

Fluxo de boot:

```text
1. Carregar configuração.
2. Inicializar logger.
3. Validar configuração.
4. Carregar token.
5. Criar diretórios locais.
6. Carregar estado local.
7. Registrar agent, se necessário.
8. Reconciliar estado local com Docker.
9. Iniciar loops:
   - heartbeat
   - command polling
   - reconciliation
   - cleanup draining
```

---

## Shutdown graceful

Ao receber SIGTERM/SIGINT:

```text
1. Parar de buscar novos comandos.
2. Aguardar comando em execução terminar ou atingir timeout.
3. Persistir estado local.
4. Enviar heartbeat/status final, se possível.
5. Encerrar processo.
```

O agent não deve parar containers de aplicação no shutdown do próprio agent.

Containers devem continuar rodando se o agent reiniciar.

---

## Requisitos de implementação

A implementação deve ser modular.

Pacotes esperados:

```text
internal/agent
internal/platform
internal/docker
internal/ports
internal/health
internal/state
internal/config
internal/logger
internal/errors
```

O Runtime Agent deve depender de interfaces, não de implementações concretas.

Exemplo:

```go
type DockerRuntime interface {
    PullImage(ctx context.Context, image string) error
    StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerInfo, error)
    StopContainer(ctx context.Context, name string, timeout time.Duration) error
    RemoveContainer(ctx context.Context, name string) error
    InspectContainer(ctx context.Context, name string) (*ContainerInfo, error)
    ListContainers(ctx context.Context) ([]ContainerInfo, error)
}
```

---

## Critérios de aceite

O Runtime Agent estará funcional para o MVP quando conseguir:

```text
1. Iniciar com arquivo de configuração válido.
2. Registrar-se na Platform API.
3. Enviar heartbeat.
4. Buscar comando DEPLOY_APPLICATION.
5. Fazer claim do comando.
6. Fazer pull da imagem Docker.
7. Alocar porta automaticamente.
8. Subir container com labels obrigatórias.
9. Executar health check local.
10. Persistir estado local.
11. Reportar sucesso para a Platform API.
12. Reportar falha estruturada em caso de erro.
13. Reconciliar estado local com Docker após restart.
14. Liberar portas de containers removidos.
15. Manter versão antiga em draining durante update.
```

---

## Relação com outros documentos

Detalhes complementares:

- `docs/specs/04-platform-api-contracts.md` define contratos HTTP.
- `docs/specs/05-command-lifecycle.md` define lifecycle dos comandos.
- `docs/specs/06-deployment-flow.md` define fluxos completos de deploy.
- `docs/specs/07-port-management.md` define alocação e reconciliação de portas.
- `docs/specs/09-docker-runtime.md` define abstração do Docker.
- `docs/specs/10-local-state.md` define persistência local.
- `docs/specs/11-health-checks.md` define health checks.
- `docs/specs/12-security.md` define requisitos de segurança.
- `docs/specs/14-error-handling-and-retry.md` define erros e retries.
- `docs/specs/15-configuration.md` define configuração do agent.

---

## Regra final

O Runtime Agent deve ser simples, seguro e previsível.

Ele deve executar apenas comandos atribuídos a ele.

Ele deve manipular apenas recursos que gerencia.

Ele deve reportar falhas de forma transparente.

Ele deve preservar a Platform API como fonte da verdade.

```text
A Platform decide.
O Runtime Agent executa.
O Docker roda.

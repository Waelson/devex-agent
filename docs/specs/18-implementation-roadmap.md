# 18 — Roadmap de Implementação

## Objetivo deste documento

Este documento define a ordem recomendada de implementação do projeto **DevEx Agent**.

O objetivo é orientar o Claude Code e os desenvolvedores a construírem o projeto de forma incremental, segura e testável.

A implementação deve começar pequena, com fundações sólidas, e evoluir para os fluxos completos de Runtime Agent e Gateway Agent.

Este documento deve ser lido junto com:

- `CLAUDE.md`
- `README.md`
- `docs/specs/00-product-overview.md`
- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/12-security.md`
- `docs/specs/13-observability.md`
- `docs/specs/14-error-handling-and-retry.md`
- `docs/specs/15-configuration.md`
- `docs/specs/16-systemd-installation.md`
- `docs/specs/17-testing-strategy.md`

---

## Princípio central

Implementar em milestones pequenos.

Cada milestone deve:

```text
entregar valor claro
ter testes
manter arquitetura limpa
não quebrar milestones anteriores
evitar dependências externas desnecessárias
```

Não começar pelo deploy completo.

Começar por configuração, estado, interfaces e testes.

---

## Milestone 0 — Preparação do projeto

Objetivo:

```text
Criar base inicial do repositório.
```

Tarefas:

```text
1. Criar go.mod.
2. Criar estrutura de diretórios.
3. Adicionar README.md.
4. Adicionar CLAUDE.md.
5. Adicionar docs/specs.
6. Criar Makefile básico.
7. Criar .gitignore.
```

Estrutura esperada:

```text
cmd/devex-agent
internal/agent
internal/platform
internal/docker
internal/ports
internal/caddy
internal/health
internal/state
internal/config
internal/logger
internal/errors
scripts
docs/specs
```

Critério de aceite:

```text
go test ./... executa, mesmo sem testes reais.
go run ./cmd/devex-agent --help funciona ou retorna mensagem básica.
```

---

## Milestone 1 — Configuração, logger e bootstrap

Objetivo:

```text
Permitir iniciar o agent com configuração YAML.
```

Tarefas:

```text
1. Implementar config loader.
2. Definir structs de configuração.
3. Validar campos obrigatórios.
4. Implementar logger estruturado.
5. Implementar leitura segura do token.
6. Implementar main.go mínimo.
7. Implementar graceful shutdown básico.
```

Arquivos principais:

```text
internal/config
internal/logger
cmd/devex-agent/main.go
```

Testes:

```text
config válida
config inválida
arquivo ausente
token ausente
valores padrão
```

Critério de aceite:

```text
Agent inicia com config válida.
Agent falha com CONFIG_INVALID para config inválida.
Logs saem em formato estruturado.
```

---

## Milestone 2 — Erros tipados

Objetivo:

```text
Padronizar erros operacionais.
```

Tarefas:

```text
1. Criar pacote internal/errors.
2. Definir error codes.
3. Criar tipo OperationalError.
4. Adicionar campo retryable.
5. Adicionar helpers para wrapping.
```

Códigos iniciais:

```text
CONFIG_INVALID
PLATFORM_API_UNAVAILABLE
COMMAND_CLAIM_FAILED
IMAGE_PULL_FAILED
PORT_ALLOCATION_FAILED
CONTAINER_START_FAILED
HEALTH_CHECK_FAILED
CADDY_LOAD_FAILED
STATE_STORE_FAILED
```

Testes:

```text
criação de erro
serialização
retryable true/false
wrapping mantém code
```

Critério de aceite:

```text
Erros possuem code, message, operation e retryable.
```

---

## Milestone 3 — Local State Store

Objetivo:

```text
Persistir estado local de forma segura.
```

Tarefas:

```text
1. Implementar state store JSON.
2. Implementar escrita atômica.
3. Criar agent.json.
4. Criar state.json.
5. Criar suporte a schema_version.
6. Criar diretórios se não existirem.
```

Arquivos:

```text
internal/state
```

Testes:

```text
salvar estado
carregar estado
arquivo inexistente
JSON corrompido
escrita atômica
schema version
```

Critério de aceite:

```text
Estado local persiste e carrega corretamente usando t.TempDir nos testes.
```

---

## Milestone 4 — Platform Client

Objetivo:

```text
Permitir comunicação com a DevEx Platform API.
```

Tarefas:

```text
1. Implementar HTTP client.
2. Implementar register.
3. Implementar heartbeat.
4. Implementar fetch pending commands.
5. Implementar claim command.
6. Implementar report command.
7. Implementar fetch desired state.
8. Implementar report desired state.
```

Arquivos:

```text
internal/platform
```

Testes:

```text
httptest.Server
register payload
heartbeat payload
fetch commands
claim success
claim conflict
report success
report failed
401 authentication failed
500 retryable
timeout
```

Critério de aceite:

```text
Platform Client coberto com testes e sem depender de API real.
```

---

## Milestone 5 — Port Manager

Objetivo:

```text
Alocar e liberar portas de forma segura.
```

Tarefas:

```text
1. Implementar modelo de portas.
2. Implementar ports.json.
3. Implementar allocate.
4. Implementar reserve.
5. Implementar mark active.
6. Implementar mark draining.
7. Implementar release.
8. Implementar range exhaustion.
9. Implementar validação de max_active_containers.
```

Arquivos:

```text
internal/ports
```

Testes:

```text
alocar porta livre
não usar porta active
não usar porta reserved
range esgotado
liberar porta
draining
max active
deployment idempotente
```

Critério de aceite:

```text
Port Manager não permite duas alocações para a mesma porta.
```

---

## Milestone 6 — Docker Runtime com CLI

Objetivo:

```text
Executar operações Docker atrás de interface.
```

Tarefas:

```text
1. Definir interface DockerRuntime.
2. Definir CommandExecutor.
3. Implementar CLI runtime.
4. Implementar PullImage.
5. Implementar StartContainer.
6. Implementar StopContainer.
7. Implementar RemoveContainer.
8. Implementar InspectContainer.
9. Implementar ListContainers.
```

Arquivos:

```text
internal/docker
```

Testes:

```text
fake executor
comandos gerados
parse inspect
erros de exit code
timeouts
labels obrigatórias
```

Critério de aceite:

```text
Unit tests não exigem Docker real.
```

---

## Milestone 7 — Health Checker

Objetivo:

```text
Validar containers, HTTP endpoints e rotas.
```

Tarefas:

```text
1. Implementar HTTP health check.
2. Implementar container health check.
3. Implementar retries.
4. Implementar timeout.
5. Implementar Host header para gateway.
6. Retornar resultado estruturado.
```

Arquivos:

```text
internal/health
```

Testes:

```text
httptest.Server
200 healthy
500 unhealthy
timeout
connection refused
retry até sucesso
retry até falha
Host header
```

Critério de aceite:

```text
Health checks retornam resultado estruturado e códigos de erro.
```

---

## Milestone 8 — Runtime Agent básico

Objetivo:

```text
Executar DEPLOY_APPLICATION com dependências fake ou reais.
```

Tarefas:

```text
1. Implementar command polling loop.
2. Implementar claim.
3. Implementar processamento de DEPLOY_APPLICATION.
4. Integrar Docker Runtime.
5. Integrar Port Manager.
6. Integrar Health Checker.
7. Persistir state.
8. Reportar sucesso/falha.
```

Arquivos:

```text
internal/agent
```

Fluxo:

```text
claim
pull
allocate port
run
health check
state save
report
```

Testes:

```text
deploy sucesso
pull falha
porta falha
start falha
health falha
report falha
```

Critério de aceite:

```text
Runtime Agent consegue processar DEPLOY_APPLICATION com fakes em teste.
```

---

## Milestone 9 — Reconciliation do Runtime Agent

Objetivo:

```text
Reconciliar estado local com Docker real.
```

Tarefas:

```text
1. Listar containers gerenciados.
2. Comparar com state.json.
3. Comparar com ports.json.
4. Detectar containers órfãos.
5. Detectar portas inconsistentes.
6. Marcar unmanaged.
7. Reportar eventos.
```

Testes:

```text
container ausente
container órfão
porta sem container
porta unmanaged
estado consistente
```

Critério de aceite:

```text
Agent recupera estado consistente após restart.
```

---

## Milestone 10 — Caddy Config Generator

Objetivo:

```text
Gerar caddy.json a partir do desired state.
```

Tarefas:

```text
1. Definir modelo de desired routes.
2. Validar host.
3. Validar upstream.
4. Gerar JSON completo.
5. Incluir admin listen.
6. Incluir listen :80/:443.
7. Ordenar rotas de forma determinística.
```

Arquivos:

```text
internal/caddy/generator.go
```

Testes:

```text
uma rota
múltiplas rotas
host inválido
upstream inválido
bloquear 169.254.169.254
bloquear 127.0.0.1 quando proibido
JSON determinístico
```

Critério de aceite:

```text
Gerador produz caddy.json válido e seguro.
```

---

## Milestone 11 — Caddy Client

Objetivo:

```text
Aplicar configuração no Caddy via Admin API.
```

Tarefas:

```text
1. Implementar POST /load.
2. Implementar GET /config opcional.
3. Implementar timeout.
4. Implementar erros tipados.
5. Implementar route validation via Host header.
```

Arquivos:

```text
internal/caddy/client.go
```

Testes:

```text
httptest.Server
/load sucesso
/load 500
timeout
route validation success
route validation failure
```

Critério de aceite:

```text
Caddy Client funciona com httptest.Server e erros estruturados.
```

---

## Milestone 12 — Gateway Agent básico

Objetivo:

```text
Aplicar desired state de rotas no Caddy.
```

Tarefas:

```text
1. Buscar desired state.
2. Gerar caddy.json.
3. Salvar current config.
4. Aplicar /load.
5. Validar rotas.
6. Salvar last-good.
7. Reportar resultado.
```

Testes:

```text
desired state sucesso
geração falha
/load falha
route validation falha
last-good restore
report sucesso
```

Critério de aceite:

```text
Gateway Agent aplica rotas via Caddy Client fake.
```

---

## Milestone 13 — Draining e cleanup

Objetivo:

```text
Remover versões antigas com segurança.
```

Tarefas:

```text
1. Marcar deployment antigo como draining.
2. Configurar grace period.
3. Implementar cleanup loop.
4. Parar container antigo.
5. Remover container antigo.
6. Liberar porta.
7. Reportar evento.
```

Testes:

```text
draining não vencido
draining vencido
stop falha
remove falha
porta liberada
```

Critério de aceite:

```text
Versões antigas são removidas apenas após grace period.
```

---

## Milestone 14 — Rollback básico

Objetivo:

```text
Garantir retorno seguro em falhas.
```

Tarefas:

```text
1. Runtime rollback antes de rota.
2. Gateway restore last-good.
3. Reportar rollback aplicado.
4. Manter versão antiga ativa.
5. Remover nova versão em falha.
```

Cenários:

```text
pull falha
health local falha
caddy load falha
route validation falha
```

Critério de aceite:

```text
Falhas não derrubam versão anterior saudável.
```

---

## Milestone 15 — Instalação systemd

Objetivo:

```text
Permitir instalação operacional do agent em EC2.
```

Tarefas:

```text
1. Criar install-systemd.sh.
2. Criar uninstall-systemd.sh.
3. Criar exemplo de config runtime.
4. Criar exemplo de config gateway.
5. Documentar comandos systemctl.
```

Critério de aceite:

```text
Agent instala, inicia, para e reinicia via systemd.
```

---

## Milestone 16 — Testes de integração opcionais

Objetivo:

```text
Validar com Docker e Caddy reais em ambiente local.
```

Tarefas:

```text
1. Teste opcional com Docker real.
2. Teste opcional com Caddy real.
3. Usar env vars para habilitar.
4. Garantir cleanup.
```

Variáveis:

```text
RUN_DOCKER_INTEGRATION_TESTS=true
RUN_CADDY_INTEGRATION_TESTS=true
```

Critério de aceite:

```text
go test ./... não depende de Docker/Caddy real por padrão.
```

---

## Ordem recomendada para Claude Code

A primeira tarefa do Claude Code deve ser:

```text
1. Ler CLAUDE.md.
2. Ler docs/specs.
3. Resumir arquitetura.
4. Identificar ambiguidades.
5. Propor estrutura inicial.
6. Aguardar aprovação.
```

Depois implementar nesta ordem:

```text
Milestone 0
Milestone 1
Milestone 2
Milestone 3
Milestone 4
Milestone 5
Milestone 6
Milestone 7
Milestone 8
```

Somente depois avançar para Gateway Agent:

```text
Milestone 10
Milestone 11
Milestone 12
```

---

## Não implementar no MVP

Evitar no MVP:

```text
Kubernetes
ECS
Nomad
Canary
Traffic splitting
Autoscaling
SQS
Long polling
mTLS
Prometheus server completo
UI
Route 53 dentro do agent
Docker SDK obrigatório
```

Esses itens podem ser evolução futura.

---

## Critérios de conclusão do MVP

O MVP estará concluído quando:

```text
1. Runtime Agent inicia via config.
2. Runtime Agent registra e envia heartbeat.
3. Runtime Agent busca comando.
4. Runtime Agent faz claim.
5. Runtime Agent executa deploy Docker.
6. Runtime Agent aloca porta.
7. Runtime Agent faz health check.
8. Runtime Agent reporta sucesso/falha.
9. Gateway Agent busca desired state.
10. Gateway Agent gera caddy.json.
11. Gateway Agent aplica /load.
12. Gateway Agent valida rota.
13. Estado local é persistido.
14. Logs estruturados existem.
15. systemd install funciona.
16. Testes unitários principais passam.
```

---

## Regra final

Construa de forma incremental.

Não pule fundações.

Não implemente deploy completo antes de estado local, erros, Docker abstraction, Port Manager e Health Checker.

A robustez do agente depende dessas bases.

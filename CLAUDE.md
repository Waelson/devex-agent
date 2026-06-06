# CLAUDE.md

## Projeto: DevEx Agent

Este repositório contém a implementação do **DevEx Agent**, um agente leve de infraestrutura responsável por executar deploys, gerenciar containers Docker, alocar portas de runtime, atualizar rotas no Caddy Gateway, executar health checks e reportar o status das operações para a **DevEx Platform**.

O agente faz parte de uma plataforma DevEx maior, cujo objetivo é simplificar o deploy de aplicações em instâncias EC2 usando Docker, Caddy, Route 53 e orquestração automatizada.

A experiência desejada para o desenvolvedor é:

> O desenvolvedor escolhe apenas a aplicação, o ambiente, a imagem Docker/tag e a URL desejada.  
> A plataforma e os agentes cuidam automaticamente de Docker, portas, rotas, Caddy, health checks, rollback e status.

---

## Leitura obrigatória antes da implementação

Antes de alterar código, leia os documentos relevantes em `docs/specs`.

Comece por estes arquivos:

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
- `docs/specs/18-implementation-roadmap.md`

Se houver conflito entre este arquivo e algum documento de spec, priorize o documento de spec.

Se houver conflito entre specs diferentes, pare e peça esclarecimento antes de implementar.

---

## Princípio central de arquitetura

O sistema segue esta separação de responsabilidades:

```text
DevEx Platform decide.
DevEx Agent executa.
Docker roda containers.
Caddy roteia tráfego HTTP/HTTPS.
Route 53 resolve DNS.
```

O agente não deve se tornar a fonte da verdade para decisões de negócio.

A fonte da verdade é a **DevEx Platform**.

O agente é um reconciliador/executor local que recebe comandos ou estado desejado e aplica esses comandos na instância EC2 local.

---

## Modos de execução do agente

O projeto deve suportar dois modos de execução:

```text
runtime
gateway
```

---

## Runtime Agent

O **Runtime Agent** roda em instâncias EC2 responsáveis por executar workloads de aplicação.

Responsabilidades:

- Registrar a instância EC2 na DevEx Platform.
- Enviar heartbeat.
- Buscar comandos pendentes.
- Fazer claim atômico de comandos.
- Fazer pull de imagens Docker.
- Subir containers.
- Parar e remover containers.
- Alocar portas no host.
- Executar health checks locais.
- Gerenciar deploys blue/green locais.
- Reportar resultados de deploy para a Platform API.
- Reconciliar estado local com o estado real do Docker.

O Runtime Agent não deve:

- Criar registros no Route 53.
- Aplicar configuração no Caddy.
- Decidir globalmente em qual EC2 uma aplicação deve rodar.
- Expor uma API pública.

Consulte:

- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/07-port-management.md`
- `docs/specs/09-docker-runtime.md`

---

## Gateway Agent

O **Gateway Agent** roda em instâncias EC2 responsáveis pelo Caddy Gateway.

Responsabilidades:

- Manter o Caddy operacional.
- Buscar o estado desejado de rotas na DevEx Platform.
- Gerar o arquivo completo `caddy.json`.
- Validar a configuração do Caddy.
- Aplicar a configuração usando a Caddy Admin API `/load`.
- Validar o comportamento das rotas.
- Reportar o status do gateway e das rotas para a Platform API.

O Gateway Agent não deve:

- Executar containers de aplicação.
- Fazer pull de imagens Docker de aplicações.
- Alocar portas de aplicações.
- Decidir qual EC2 deve receber um workload.

Consulte:

- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/08-caddy-integration.md`

---

## Tecnologia recomendada

O agente deve ser implementado em **Go**.

Motivos:

- Geração de binário único.
- Boa aderência para execução como daemon/systemd.
- Baixo consumo de memória.
- Modelo de concorrência simples e robusto.
- Boa biblioteca padrão para HTTP, JSON, arquivos, processos e timeouts.
- Facilidade de distribuição em hosts Linux/EC2.

Para o MVP, a integração com Docker pode usar a Docker CLI via `os/exec`.

Posteriormente, a implementação pode evoluir para Docker SDK, mas as operações Docker devem permanecer atrás de uma interface interna.

---

## Estrutura esperada do projeto

Use a estrutura abaixo, salvo se as specs exigirem algo diferente:

```text
devex-agent/
├── CLAUDE.md
├── README.md
├── go.mod
├── go.sum
├── cmd/
│   └── devex-agent/
│       └── main.go
├── internal/
│   ├── agent/
│   │   ├── loop.go
│   │   ├── heartbeat.go
│   │   ├── registration.go
│   │   └── command_processor.go
│   ├── platform/
│   │   ├── client.go
│   │   ├── commands.go
│   │   ├── heartbeat.go
│   │   └── models.go
│   ├── docker/
│   │   ├── runtime.go
│   │   ├── cli_runtime.go
│   │   └── models.go
│   ├── ports/
│   │   ├── manager.go
│   │   ├── store.go
│   │   └── models.go
│   ├── caddy/
│   │   ├── client.go
│   │   ├── generator.go
│   │   └── models.go
│   ├── health/
│   │   ├── checker.go
│   │   └── models.go
│   ├── state/
│   │   ├── store.go
│   │   └── models.go
│   ├── config/
│   │   ├── loader.go
│   │   └── models.go
│   ├── logger/
│   │   └── logger.go
│   └── errors/
│       └── codes.go
├── docs/
│   └── specs/
└── scripts/
    ├── install-systemd.sh
    └── uninstall-systemd.sh
```

Não coloque lógica de negócio em `cmd/devex-agent/main.go`.

O arquivo `main.go` deve apenas:

- Carregar configuração.
- Inicializar logger.
- Inicializar dependências.
- Iniciar o loop do agente.
- Tratar shutdown graceful.

---

## Configuração

O agente deve carregar configuração a partir de um arquivo YAML.

Caminho padrão:

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

ports:
  from: 4100
  to: 4114

reconcile:
  interval_seconds: 10

state:
  dir: "/var/lib/devex-agent"

caddy:
  admin_url: "http://127.0.0.1:2019"
```

Comportamento esperado:

- Campos obrigatórios devem ser validados no startup.
- Configuração inválida deve causar falha imediata.
- Secrets não devem ser logados.
- Variáveis de ambiente podem sobrescrever valores de configuração apenas quando explicitamente suportadas.

Consulte:

- `docs/specs/15-configuration.md`

---

## Comunicação com a Platform API

O agente se comunica com a DevEx Platform por requisições HTTP outbound.

O agente não deve expor API pública no MVP.

Operações mínimas esperadas:

```http
POST /api/agents/register
POST /api/agents/{agent_id}/heartbeat
GET  /api/agents/{agent_id}/commands/pending
POST /api/agents/{agent_id}/commands/{command_id}/claim
POST /api/agents/{agent_id}/commands/{command_id}/report
GET  /api/agents/{agent_id}/desired-state
POST /api/agents/{agent_id}/desired-state/report
```

Todas as chamadas para a Platform API devem usar:

- `context.Context` com timeout.
- Tratamento de erro estruturado.
- Retry somente onde permitido pelas specs.
- Token de autenticação lido de `platform.token_file`.

Consulte:

- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Modelo de processamento de comandos

O Runtime Agent deve usar polling.

Loop básico:

```text
1. Enviar heartbeat.
2. Buscar comandos pendentes.
3. Fazer claim de um comando.
4. Executar o comando.
5. Reportar resultado.
6. Persistir estado local.
7. Aguardar próximo ciclo com jitter.
```

O agente deve executar apenas comandos atribuídos ao seu próprio `agent_id`.

O agente não deve decidir se um comando pertence a ele.

A Platform API deve retornar apenas comandos direcionados ao agente solicitante.

O claim de comando deve ser tratado como uma transição atômica:

```text
pending -> claimed -> running -> succeeded
pending -> claimed -> running -> failed
```

Nunca execute um comando que não tenha sido reivindicado com sucesso.

Consulte:

- `docs/specs/05-command-lifecycle.md`

---

## Regras de deploy

Ao atualizar a imagem de uma aplicação, não modifique o container existente em execução.

Use um fluxo local similar a blue/green:

```text
1. Manter a versão atual rodando.
2. Fazer pull da nova imagem.
3. Alocar uma nova porta no host.
4. Subir o novo container com nome versionado.
5. Executar health check contra o novo container.
6. Reportar o novo endpoint saudável para a Platform API.
7. Aguardar o Gateway Agent atualizar o Caddy.
8. Marcar o container antigo como draining.
9. Remover o container antigo após a janela de segurança configurada.
```

Os nomes dos containers devem incluir aplicação, ambiente e versão/deployment id.

Exemplo:

```text
billing-api-dev-v42
```

Evite nomes genéricos como:

```text
billing-api
```

Consulte:

- `docs/specs/06-deployment-flow.md`

---

## Regras de gerenciamento de portas

O desenvolvedor nunca deve escolher a porta do host.

A aplicação nunca deve definir uma porta fixa do host.

O Runtime Agent é o dono da alocação de portas.

Exemplo:

```yaml
runtime:
  max_active_containers: 10

ports:
  from: 4100
  to: 4114
```

Estados possíveis de uma porta:

```text
available
reserved
active
draining
failed
released
unmanaged
```

Fluxo de alocação:

```text
1. Bloquear estado de portas.
2. Reconciliar estado local com Docker.
3. Encontrar porta disponível.
4. Marcar porta como reserved.
5. Subir container.
6. Se o container subir e passar no health check, marcar porta como active.
7. Se o deploy falhar, liberar porta.
8. Desbloquear estado de portas.
```

O agente deve reconciliar `ports.json` com o estado real do Docker antes de alocar portas.

Arquivo local:

```text
/var/lib/devex-agent/ports.json
```

Consulte:

- `docs/specs/07-port-management.md`

---

## Regras do Docker Runtime

Operações Docker devem ser isoladas atrás de uma interface.

Interface sugerida:

```go
type Runtime interface {
    PullImage(ctx context.Context, image string) error
    StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerInfo, error)
    StopContainer(ctx context.Context, name string, timeout time.Duration) error
    RemoveContainer(ctx context.Context, name string) error
    InspectContainer(ctx context.Context, name string) (*ContainerInfo, error)
    ListContainers(ctx context.Context) ([]ContainerInfo, error)
}
```

Para o MVP, a implementação pode chamar a Docker CLI.

Comandos Docker devem:

- Usar context com timeout.
- Capturar stdout/stderr.
- Retornar erros tipados sempre que possível.
- Evitar logar secrets.
- Ser testáveis por mocks/fakes.

Consulte:

- `docs/specs/09-docker-runtime.md`

---

## Regras de integração com Caddy

O Gateway Agent deve aplicar a configuração Caddy completa usando `/load`.

Não use mutações incrementais de rotas como mecanismo principal de deploy.

A Platform ou o Gateway Agent deve gerar o arquivo completo `caddy.json`.

A Caddy Admin API deve estar disponível apenas localmente:

```text
http://127.0.0.1:2019
```

O Docker deve publicar a porta da Admin API assim:

```yaml
127.0.0.1:2019:2019
```

A configuração do Caddy deve conter:

```json
{
  "admin": {
    "listen": "0.0.0.0:2019"
  }
}
```

O Gateway Agent deve:

```text
1. Buscar rotas desejadas.
2. Gerar caddy.json completo.
3. Validar a configuração gerada.
4. Enviar a configuração para /load.
5. Validar o comportamento da rota.
6. Reportar o resultado.
```

Consulte:

- `docs/specs/08-caddy-integration.md`

---

## Estado local

O agente deve persistir estado operacional local.

Diretório padrão:

```text
/var/lib/devex-agent
```

Arquivos sugeridos:

```text
/var/lib/devex-agent/agent.json
/var/lib/devex-agent/state.json
/var/lib/devex-agent/ports.json
/var/lib/devex-agent/locks/
```

O estado local não é a fonte da verdade global.

Ele é usado para:

- Recuperação.
- Reconciliação.
- Alocação de portas.
- Evitar trabalho duplicado.
- Rastrear containers locais.
- Tratar deployments em draining.

Consulte:

- `docs/specs/10-local-state.md`

---

## Health checks

Para aplicações HTTP:

```text
GET http://127.0.0.1:{host_port}{health_check_path}
```

Para aplicações expostas via IP privado da EC2:

```text
GET http://{private_ip}:{host_port}{health_check_path}
```

Para workers:

- Verificar status do container.
- Verificar exit code.
- Opcionalmente verificar heartbeat do worker pela Platform API.

Health checks devem suportar:

- Timeout.
- Retry.
- Backoff.
- Códigos de erro claros.

Consulte:

- `docs/specs/11-health-checks.md`

---

## Tratamento de erros

Use erros tipados/codificados para falhas operacionais.

Códigos sugeridos:

```text
IMAGE_PULL_FAILED
PORT_ALLOCATION_FAILED
CONTAINER_START_FAILED
HEALTH_CHECK_FAILED
CADDY_LOAD_FAILED
PLATFORM_API_UNAVAILABLE
DOCKER_UNAVAILABLE
CONFIG_INVALID
STATE_STORE_FAILED
COMMAND_CLAIM_FAILED
```

Todo report de comando deve incluir:

```text
status = succeeded
```

ou:

```text
status = failed
error.code
error.message
```

Não esconda falhas parciais.

Consulte:

- `docs/specs/14-error-handling-and-retry.md`

---

## Observabilidade

Use logs estruturados.

Prefira logs JSON ou logs key-value consistentes.

Toda operação importante deve incluir:

```text
agent_id
mode
environment
command_id
deployment_id
application
container_name
```

Exemplo:

```json
{
  "level": "info",
  "component": "runtime-agent",
  "agent_id": "agent-dev-api-001",
  "command_id": "cmd_123",
  "application": "billing-api",
  "container_name": "billing-api-dev-v42",
  "message": "container started"
}
```

Métricas podem ser adicionadas depois do MVP, mas o código não deve dificultar essa evolução.

Consulte:

- `docs/specs/13-observability.md`

---

## Segurança

O Runtime Agent não deve expor endpoints públicos.

O Gateway Agent não deve expor publicamente a Caddy Admin API.

O token do agente deve ser lido de arquivo:

```text
/etc/devex-agent/token
```

Nunca logue:

- Tokens.
- Secrets.
- Variáveis de ambiente com credenciais.
- Senhas de registry Docker.
- Headers de autorização.

O Docker socket é altamente privilegiado. Trate o agente como um processo confiável de host.

Para AWS:

- Prefira EC2 IAM Role quando acesso AWS for necessário.
- Não armazene credenciais AWS long-lived em disco.
- Restrinja Security Groups para que portas de runtime sejam acessíveis apenas pelo Security Group do Caddy Gateway.

Consulte:

- `docs/specs/12-security.md`

---

## Expectativas de testes

Priorize testes para lógica determinística.

Áreas de maior prioridade:

```text
1. Port Manager
2. Ciclo de vida dos comandos
3. Local State Store
4. Gerador de configuração Caddy
5. Health Checker
6. Docker Runtime wrapper com fake executor
7. Platform Client com HTTP test server
```

Testes não devem depender de recursos reais da AWS.

Testes de integração com Docker podem ser opcionais e habilitados por variável de ambiente.

Exemplo:

```bash
RUN_DOCKER_INTEGRATION_TESTS=true go test ./...
```

Consulte:

- `docs/specs/17-testing-strategy.md`

---

## Roadmap de implementação

Siga esta ordem, salvo instrução em contrário.

### Milestone 1 — Fundação do projeto

- Inicializar módulo Go.
- Criar estrutura do projeto.
- Implementar config loader.
- Implementar logger.
- Implementar local state store.

### Milestone 2 — Platform Client

- Registrar agent.
- Enviar heartbeat.
- Buscar comandos pendentes.
- Fazer claim de comando.
- Reportar resultado de comando.

### Milestone 3 — Port Manager

- Configurar faixa de portas.
- Alocar porta.
- Reservar porta.
- Marcar porta como active.
- Liberar porta.
- Reconciliar com estado real do Docker.

### Milestone 4 — Docker Runtime

- Pull image.
- Start container.
- Stop container.
- Remove container.
- Inspect container.
- List containers.

### Milestone 5 — Runtime Agent

- Processar comando `DEPLOY_APPLICATION`.
- Alocar porta.
- Subir container.
- Executar health check.
- Reportar resultado.

### Milestone 6 — Gateway Agent

- Buscar rotas desejadas.
- Gerar `caddy.json`.
- Aplicar `/load`.
- Validar rota.
- Reportar resultado.

### Milestone 7 — Rollback e draining

- Manter versão anterior.
- Marcar versão antiga como draining.
- Limpar containers antigos.
- Restaurar rota anterior quando necessário.

### Milestone 8 — Instalação

- Criar unit systemd.
- Criar script de instalação.
- Criar script de remoção.
- Criar README operacional.

Consulte:

- `docs/specs/18-implementation-roadmap.md`

---

## Diretrizes de código

Use Go idiomático.

Regras gerais:

- Mantenha funções pequenas e focadas.
- Use interfaces para sistemas externos.
- Passe `context.Context` para operações bloqueantes.
- Evite estado global mutável.
- Não use panic para erros operacionais esperados.
- Retorne erros e trate-os explicitamente.
- Use table-driven tests quando fizer sentido.
- Separe lógica de orquestração de adapters de baixo nível.

Limites de pacotes:

```text
internal/platform  -> cliente HTTP da Platform API
internal/docker    -> operações Docker
internal/ports     -> alocação e reconciliação de portas
internal/caddy     -> Caddy Admin API e geração de configuração
internal/health    -> health checks
internal/state     -> persistência de estado local
internal/agent     -> loops principais e orquestração
```

---

## O que não fazer

Não faça:

- Não coloque orquestração de deploy em `main.go`.
- Não deixe a aplicação escolher portas do host.
- Não atualize containers em execução diretamente.
- Não exponha o agente publicamente.
- Não exponha a Caddy Admin API publicamente.
- Não trate o autosave do Caddy como fonte primária da verdade.
- Não trate o estado local como fonte global da verdade.
- Não hardcode valores específicos de ambiente.
- Não logue secrets.
- Não implemente alterações de Route 53 dentro do Runtime Agent.
- Não faça o Gateway Agent executar containers de aplicação.

---

## Escopo do MVP

O MVP deve suportar:

```text
- Modo Runtime Agent.
- Modo Gateway Agent.
- Busca de comandos por polling.
- Docker runtime via Docker CLI.
- Alocação de portas.
- Comando de deploy de aplicação.
- Health check HTTP.
- Geração de configuração Caddy.
- Integração com Caddy /load.
- Persistência de estado local.
- Logs estruturados.
- Instalação via systemd.
```

Fora do MVP:

```text
- Kubernetes.
- Multi-cloud.
- Scheduling avançado.
- Canary deployment.
- Traffic splitting.
- Service mesh.
- Autoscaling de runtime.
- Servidor completo de métricas.
- API pública do agente.
```

---

## Primeira tarefa para Claude Code

Ao iniciar a implementação, não escreva código imediatamente.

Primeiro:

```text
1. Leia este arquivo CLAUDE.md.
2. Leia todos os arquivos em docs/specs.
3. Resuma a arquitetura.
4. Identifique ambiguidades ou decisões pendentes.
5. Proponha a estrutura inicial de pacotes Go.
6. Aguarde aprovação antes de implementar.
```

Após aprovação, comece pelo Milestone 1 descrito em:

- `docs/specs/18-implementation-roadmap.md`

---

## Regra final de design

Mantenha o agente simples.

A Platform é dona do estado desejado e das decisões de scheduling.

O agente aplica comandos com segurança, reporta resultados honestamente e reconcilia o runtime local.

```text
Platform = cérebro
Agent = executor
Docker = runtime
Caddy = gateway
Route 53 = DNS
```

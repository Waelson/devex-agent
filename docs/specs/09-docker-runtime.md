# 09 — Docker Runtime

## Objetivo deste documento

Este documento define como o **Runtime Agent** deve interagir com o Docker para executar aplicações.

Ele cobre:

- Abstração interna do Docker Runtime.
- Uso inicial da Docker CLI.
- Operações obrigatórias.
- Modelos de dados.
- Execução de containers.
- Labels obrigatórias.
- Timeouts.
- Tratamento de erros.
- Testabilidade.

Este documento deve ser lido junto com:

- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/07-port-management.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

O Runtime Agent não deve espalhar comandos Docker pelo código.

Todas as operações Docker devem ficar atrás de uma interface interna.

Isso permite:

```text
Começar com Docker CLI no MVP.
Evoluir para Docker SDK no futuro.
Testar com mocks/fakes.
Isolar detalhes de infraestrutura.
```

---

## Estratégia do MVP

Para o MVP, a implementação pode usar Docker CLI via `os/exec`.

Exemplo:

```bash
docker pull ghcr.io/useclarus/billing-api:v42
docker run ...
docker stop ...
docker rm ...
docker inspect ...
docker ps ...
```

Mesmo usando CLI, o restante do código deve depender de interface, não de `exec.Command` diretamente.

---

## Interface sugerida

```go
type Runtime interface {
    PullImage(ctx context.Context, image string) error
    StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerInfo, error)
    StopContainer(ctx context.Context, name string, timeout time.Duration) error
    RemoveContainer(ctx context.Context, name string) error
    InspectContainer(ctx context.Context, name string) (*ContainerInfo, error)
    ListContainers(ctx context.Context, filter ContainerFilter) ([]ContainerInfo, error)
}
```

---

## ContainerSpec

Modelo sugerido:

```go
type ContainerSpec struct {
    Name              string
    Image             string
    HostPort          int
    ContainerPort     int
    Env               map[string]string
    Labels            map[string]string
    RestartPolicy     string
    Network           string
    Command           []string
    Args              []string
    WorkingDir        string
    MemoryLimitMB     int
    CPUQuota          string
}
```

Para o MVP, nem todos os campos precisam ser implementados.

Campos mínimos:

```text
Name
Image
HostPort
ContainerPort
Env
Labels
RestartPolicy
```

---

## ContainerInfo

Modelo sugerido:

```go
type ContainerInfo struct {
    ID                string
    Name              string
    Image             string
    Status            string
    Running           bool
    ExitCode          int
    HostPort          int
    ContainerPort     int
    Labels            map[string]string
    CreatedAt         time.Time
}
```

---

## ContainerFilter

Modelo sugerido:

```go
type ContainerFilter struct {
    ManagedOnly   bool
    Labels        map[string]string
    NamePrefix    string
}
```

Para listar containers gerenciados pela plataforma:

```text
label devex.managed=true
```

---

## PullImage

Operação:

```go
PullImage(ctx, image)
```

Comando CLI:

```bash
docker pull <image>
```

Regras:

- Usar context com timeout.
- Capturar stdout/stderr.
- Logar início e fim.
- Não logar credenciais.
- Retornar `IMAGE_PULL_FAILED` em falha.
- Diferenciar falha temporária de imagem inexistente quando possível.

Exemplo de erro:

```json
{
  "code": "IMAGE_PULL_FAILED",
  "message": "Failed to pull image ghcr.io/useclarus/billing-api:v42"
}
```

---

## StartContainer

Operação:

```go
StartContainer(ctx, spec)
```

Comando exemplo:

```bash
docker run -d   --name billing-api-dev-v42   --restart unless-stopped   -p 4102:3000   --label devex.managed=true   --label devex.agent_id=agent-dev-api-001   --label devex.application=billing-api   --label devex.environment=dev   --label devex.deployment_id=dep_456   --label devex.command_id=cmd_123   -e NODE_ENV=development   ghcr.io/useclarus/billing-api:v42
```

Regras:

- Nome do container deve ser único.
- Labels obrigatórias devem ser aplicadas.
- Restart policy padrão: `unless-stopped`.
- Porta do host vem do Port Manager.
- Porta interna vem da definição da aplicação.
- Variáveis sensíveis não devem ser logadas.
- Em falha, retornar `CONTAINER_START_FAILED`.

---

## StopContainer

Operação:

```go
StopContainer(ctx, name, timeout)
```

Comando CLI:

```bash
docker stop --time 30 billing-api-dev-v42
```

Regras:

- Usar timeout configurável.
- Verificar se container existe.
- Não falhar se container já estiver parado, salvo se política exigir.
- Retornar erro estruturado em falha.

---

## RemoveContainer

Operação:

```go
RemoveContainer(ctx, name)
```

Comando CLI:

```bash
docker rm billing-api-dev-v42
```

Se necessário:

```bash
docker rm -f billing-api-dev-v42
```

Regras:

- Preferir stop graceful antes de remove.
- Remover apenas containers gerenciados, salvo instrução explícita.
- Validar label `devex.managed=true`.
- Liberar porta somente após confirmação da remoção.

---

## InspectContainer

Operação:

```go
InspectContainer(ctx, name)
```

Comando CLI:

```bash
docker inspect billing-api-dev-v42
```

Dados relevantes:

```text
ID
Name
Image
State.Running
State.Status
State.ExitCode
NetworkSettings.Ports
Config.Labels
Created
```

O parser deve converter a saída para `ContainerInfo`.

---

## ListContainers

Operação:

```go
ListContainers(ctx, filter)
```

Comando CLI:

```bash
docker ps -a --filter label=devex.managed=true --format ...
```

Para MVP, pode ser mais simples usar:

```bash
docker ps -a --format '{{json .}}'
```

e depois inspecionar containers relevantes.

Regras:

- Listar containers gerenciados.
- Permitir filtros por labels.
- Ser usado na reconciliação.
- Não manipular containers unmanaged automaticamente.

---

## Labels obrigatórias

Todo container criado pelo Runtime Agent deve incluir:

```text
devex.managed=true
devex.agent_id=<agent_id>
devex.application=<application>
devex.environment=<environment>
devex.deployment_id=<deployment_id>
devex.command_id=<command_id>
```

Labels opcionais:

```text
devex.workload_type=api
devex.version=v42
devex.created_by=devex-agent
```

Essas labels permitem:

- Reconciliação.
- Auditoria.
- Identificação de containers gerenciados.
- Cleanup seguro.
- Proteção contra manipulação de containers externos.

---

## Nome de container

Formato recomendado:

```text
<application>-<environment>-<version-or-deployment>
```

Exemplo:

```text
billing-api-dev-v42
```

Alternativa mais segura:

```text
billing-api-dev-dep-456
```

O nome deve:

- Ser determinístico.
- Evitar colisões.
- Permitir múltiplas versões lado a lado.
- Ser compatível com Docker.

---

## Port binding

Para APIs/frontends:

```text
host_port:container_port
```

Exemplo:

```bash
-p 4102:3000
```

Para workers, pode não haver porta.

O Docker Runtime não deve escolher porta.

A porta deve vir do Port Manager.

---

## Variáveis de ambiente

O Runtime Agent pode receber env vars no payload do comando.

Exemplo:

```json
{
  "environment_variables": {
    "NODE_ENV": "development",
    "LOG_LEVEL": "info"
  }
}
```

Regras:

- Não logar valores sensíveis.
- Permitir lista de variáveis.
- Em evolução futura, secrets devem vir de mecanismo seguro.
- Não persistir secrets em state local.

---

## Restart policy

Padrão recomendado:

```text
unless-stopped
```

Motivo:

- Container reinicia se falhar.
- Container volta após restart do Docker.
- Container não reinicia se parado manualmente pelo agente.

Configuração:

```bash
--restart unless-stopped
```

---

## Network

Para o modelo com Caddy em outra EC2, o container precisa publicar porta no host.

Network Docker pode ser padrão `bridge`.

Para cenários em que Caddy e app estejam na mesma EC2, pode-se usar uma network Docker compartilhada.

Para MVP:

```text
bridge + host port publishing
```

---

## Timeouts

Cada operação Docker deve ter timeout.

Sugestões:

```yaml
docker:
  pull_timeout_seconds: 300
  start_timeout_seconds: 60
  stop_timeout_seconds: 30
  remove_timeout_seconds: 30
  inspect_timeout_seconds: 10
  list_timeout_seconds: 10
```

Timeout deve resultar em erro estruturado.

Exemplo:

```text
DOCKER_COMMAND_TIMEOUT
```

---

## Executor de comandos

A implementação com CLI deve usar um executor encapsulado.

Interface sugerida:

```go
type CommandExecutor interface {
    Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}
```

Modelo:

```go
type CommandResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
}
```

Isso permite testes sem executar Docker real.

---

## Tratamento de stdout/stderr

Regras:

- Capturar stdout/stderr.
- Logar stderr em nível debug ou error, sem secrets.
- Incluir trecho resumido em erros.
- Evitar logs gigantes.
- Truncar saída quando necessário.

---

## Erros esperados

Códigos Docker:

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

## Segurança

Regras:

```text
Não manipular containers sem label devex.managed=true, exceto quando explicitamente permitido.
Não logar secrets.
Não executar comandos arbitrários vindos da Platform API.
Validar nome de imagem.
Validar nome de container.
Validar labels.
Validar portas.
```

O Runtime Agent tem poder elevado porque interage com Docker.

A Platform API deve ser autenticada.

O payload dos comandos deve ser validado defensivamente.

---

## Validação de imagem

O agent deve aceitar imagens válidas.

Exemplos:

```text
ghcr.io/useclarus/billing-api:v42
123456789012.dkr.ecr.sa-east-1.amazonaws.com/billing-api:v42
billing-api:v42
```

Rejeitar imagens com caracteres claramente inválidos.

A política de registries permitidos deve vir da Platform API ou configuração.

---

## Validação de nome de container

Nome deve conter apenas caracteres seguros para Docker.

Exemplo permitido:

```text
billing-api-dev-v42
```

Evitar:

```text
nomes com espaços
nomes com shell metacharacters
```

Como o código deve usar `exec.Command` com args separados, risco de shell injection é reduzido, mas a validação ainda é necessária.

---

## Reconciliação

O Docker Runtime fornece dados para reconciliador.

Fluxo:

```text
1. Listar containers gerenciados.
2. Inspecionar containers relevantes.
3. Comparar com state.json.
4. Comparar com ports.json.
5. Detectar órfãos, inconsistências e containers removidos.
```

Containers sem `devex.managed=true` devem ser considerados unmanaged.

---

## Testes

Testes prioritários:

```text
PullImage com fake executor
StartContainer gerando argumentos corretos
StopContainer com timeout
RemoveContainer validando managed label
InspectContainer parseando JSON
ListContainers filtrando labels
Tratamento de erros Docker
```

Os testes unitários não devem exigir Docker real.

Testes de integração com Docker real devem ser opcionais:

```bash
RUN_DOCKER_INTEGRATION_TESTS=true go test ./...
```

---

## Critérios de aceite

O Docker Runtime estará correto quando:

```text
1. Todas as operações Docker estiverem atrás de interface.
2. MVP usar Docker CLI sem espalhar os comandos pelo código.
3. Pull de imagem funcionar com timeout.
4. Container subir com nome, labels, restart policy e porta corretos.
5. Stop e remove funcionarem com validação de managed labels.
6. Inspect retornar informações estruturadas.
7. ListContainers permitir reconciliação.
8. Erros forem tipados/codificados.
9. Testes puderem usar fake executor.
10. Nenhum secret for logado.
```

---

## Regra final

Docker é detalhe de runtime.

O restante do sistema não deve conhecer comandos Docker específicos.

O Runtime Agent usa uma interface.

A implementação inicial pode ser Docker CLI.

A arquitetura deve permitir evolução para Docker SDK.

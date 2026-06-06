# 17 — Estratégia de Testes

## Objetivo deste documento

Este documento define a estratégia de testes do projeto **DevEx Agent**.

O agente executa operações sensíveis, como:

- Docker pull.
- Docker run.
- Alocação de portas.
- Health checks.
- Manipulação de estado local.
- Atualização de rotas no Caddy.
- Comunicação com Platform API.

Por isso, a implementação deve ter uma base sólida de testes unitários, testes de contrato e testes de integração controlados.

Este documento deve ser lido junto com:

- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

Testar primeiro a lógica determinística.

Evitar que testes unitários dependam de:

```text
Docker real
AWS real
Caddy real
rede externa
arquivos globais do sistema
```

Dependências externas devem ser isoladas por interfaces e fakes.

---

## Pirâmide de testes

Prioridade:

```text
1. Unit tests
2. Contract tests
3. Integration tests locais
4. End-to-end tests opcionais
```

Para o MVP, unit tests e testes com fakes são obrigatórios.

Testes com Docker real e Caddy real são opcionais e devem ser habilitados explicitamente.

---

## Comando padrão

```bash
go test ./...
```

Esse comando deve executar todos os testes unitários sem depender de Docker real.

---

## Testes de integração opcionais

Testes que dependem de Docker real devem usar variável de ambiente.

Exemplo:

```bash
RUN_DOCKER_INTEGRATION_TESTS=true go test ./...
```

Se a variável não estiver definida, esses testes devem ser ignorados com `t.Skip`.

---

## Áreas prioritárias

Prioridade máxima:

```text
Port Manager
Command lifecycle
Local State Store
Docker Runtime wrapper
Health Checker
Caddy config generator
Platform Client
Error classification
```

---

## Testes do Port Manager

O Port Manager é crítico.

Cenários obrigatórios:

```text
alocar primeira porta disponível
não alocar porta já active
não alocar porta reserved
não alocar porta unmanaged
liberar porta active
marcar porta como draining
liberar porta após draining
falhar quando range estiver esgotado
respeitar max_active_containers
reutilizar porta para deployment_id existente
```

Exemplo de teste:

```text
Dado range 4100-4102
E porta 4100 active
Quando alocar nova porta
Então retornar 4101
```

---

## Testes de estado local

Cenários:

```text
criar state.json se não existir
carregar state.json existente
falhar com STATE_CORRUPTED em JSON inválido
escrever estado de forma atômica
preservar schema_version
salvar deployment active
salvar deployment draining
remover deployment
```

Usar diretório temporário com `t.TempDir()`.

---

## Testes do Docker Runtime wrapper

Não usar Docker real nos unit tests.

Criar fake executor:

```go
type FakeCommandExecutor struct {
    Calls []Call
    Results map[string]CommandResult
}
```

Cenários:

```text
PullImage monta comando correto
StartContainer monta docker run correto
StopContainer monta docker stop correto
RemoveContainer monta docker rm correto
InspectContainer parseia saída JSON
ListContainers parseia containers
erro de exit code vira erro tipado
timeout vira DOCKER_COMMAND_TIMEOUT
```

---

## Testes do Health Checker

Cenários HTTP:

```text
200 = healthy
204 = healthy
500 = unhealthy
404 = unhealthy
timeout = HEALTH_CHECK_TIMEOUT
connection refused = HEALTH_CHECK_CONNECTION_REFUSED
retry até sucesso
retry até falha final
Host header no gateway check
```

Usar `httptest.Server`.

---

## Testes do Caddy config generator

Cenários:

```text
gerar configuração vazia válida
gerar rota para um host
gerar múltiplas rotas
rejeitar host inválido
rejeitar upstream inválido
incluir admin listen 0.0.0.0:2019
incluir listen :80 e :443
ordenar rotas de forma determinística
```

Validar JSON gerado estruturalmente.

---

## Testes do Caddy client

Usar `httptest.Server`.

Cenários:

```text
POST /load com JSON correto
erro 500 vira CADDY_LOAD_FAILED
timeout vira CADDY_ADMIN_UNAVAILABLE ou timeout específico
resposta 2xx é sucesso
```

---

## Testes do Platform Client

Usar `httptest.Server`.

Cenários:

```text
register envia payload correto
heartbeat envia payload correto
fetch pending commands parseia resposta
claim trata sucesso
claim trata conflito
report succeeded envia payload correto
report failed envia error_code
desired state parseia rotas
erro 401 vira AUTHENTICATION_FAILED
erro 500 é retryable
timeout é retryable
```

---

## Testes de command lifecycle

Cenários:

```text
não executar comando sem claim
claim falhou -> comando não executado
pending -> claimed -> running -> succeeded
pending -> claimed -> running -> failed
comando duplicado não cria segundo container
command_id já processado retorna estado atual
```

---

## Testes do Runtime Agent

Com fakes:

```text
recebe DEPLOY_APPLICATION
faz claim
faz pull
aloca porta
sobe container
executa health check
salva estado
reporta sucesso
```

Cenários de falha:

```text
pull falha -> não aloca porta ou libera
porta falha -> não inicia container
container start falha -> libera porta
health check falha -> remove container e libera porta
report falha -> mantém estado local para reconciliação
```

---

## Testes do Gateway Agent

Com fakes:

```text
busca desired state
gera caddy.json
aplica /load
valida rota
reporta sucesso
```

Cenários de falha:

```text
desired state falha
geração de config falha
/load falha
route validation falha
restore last-good executado
restore last-good falha
```

---

## Testes de segurança

Cenários:

```text
não logar token
não logar Authorization header
rejeitar container_name inválido
rejeitar image inválida
rejeitar host inválido
rejeitar upstream 169.254.169.254
rejeitar upstream 127.0.0.1 proibido
não manipular container sem devex.managed=true
```

---

## Testes de retry

Cenários:

```text
erro retryable tenta novamente
erro non-retryable falha imediatamente
respeitar max attempts
respeitar backoff
context cancellation interrompe retry
```

---

## Testes de integração com Docker real

Opcionais.

Pré-requisitos:

```text
Docker instalado
RUN_DOCKER_INTEGRATION_TESTS=true
```

Cenários:

```text
pull de imagem pequena
run de container nginx/alpine
inspect container
stop container
remove container
port binding funciona
labels aplicadas corretamente
```

Esses testes devem limpar recursos após execução.

---

## Testes de integração com Caddy real

Opcionais.

Pré-requisitos:

```text
Caddy rodando localmente
Caddy Admin API disponível em 127.0.0.1:2019
RUN_CADDY_INTEGRATION_TESTS=true
```

Cenários:

```text
aplicar caddy.json via /load
consultar /config
validar rota local
restaurar configuração anterior
```

Cuidado para não sobrescrever configuração real de ambiente produtivo.

Esses testes devem ser usados apenas em ambiente local/controlado.

---

## Testes end-to-end

Fora do MVP obrigatório.

Cenário futuro:

```text
1. Subir Platform API fake.
2. Subir Runtime Agent.
3. Subir Gateway Agent.
4. Subir Caddy.
5. Solicitar deploy.
6. Validar URL.
7. Solicitar atualização.
8. Validar troca de rota.
9. Validar draining.
```

---

## Cobertura mínima esperada

Para MVP:

```text
Port Manager >= 90%
State Store >= 85%
Health Checker >= 85%
Caddy Generator >= 90%
Platform Client >= 80%
Docker Runtime wrapper >= 80%
```

Cobertura não deve ser usada como única métrica de qualidade, mas ajuda a garantir segurança em componentes críticos.

---

## Fakes e mocks

Preferir fakes simples a mocks complexos.

Interfaces importantes para fake:

```text
PlatformClient
DockerRuntime
PortManager
HealthChecker
StateStore
CaddyClient
```

---

## Test data

Usar fixtures pequenas.

Diretório sugerido:

```text
testdata/
```

Exemplos:

```text
testdata/docker-inspect-running.json
testdata/docker-inspect-exited.json
testdata/desired-state-routes.json
testdata/caddy-config-valid.json
```

---

## Critérios de aceite

A estratégia de testes estará adequada quando:

```text
1. go test ./... roda sem Docker real.
2. Port Manager tem cobertura dos cenários críticos.
3. Docker Runtime é testado com fake executor.
4. Platform Client é testado com httptest.Server.
5. Health Checker é testado com httptest.Server.
6. Caddy generator valida hosts e upstreams.
7. Runtime Agent é testado com dependências fake.
8. Gateway Agent é testado com dependências fake.
9. Testes de integração são opcionais por env var.
10. Testes não dependem de AWS real.
```

---

## Regra final

Testes devem proteger o que pode derrubar produção:

```text
portas
containers
rotas
estado local
health checks
erros
rollback
```

Se uma falha pode causar indisponibilidade, ela deve ter teste.

# 10 — Estado Local

## Objetivo deste documento

Este documento define como o **DevEx Agent** deve persistir e gerenciar estado local na instância EC2.

O estado local é necessário para:

- Recuperação após restart do agente.
- Reconciliação com Docker e Caddy.
- Controle de portas.
- Controle de deployments ativos.
- Controle de versões em draining.
- Evitar execução duplicada de comandos.
- Diagnóstico operacional.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

O estado local não é a fonte da verdade global.

A fonte da verdade global é a **DevEx Platform**.

O estado local é um cache operacional e mecanismo de recuperação.

Regra:

```text
Platform API = fonte da verdade global
Estado local = memória operacional do agent
Docker/Caddy = estado real do runtime
```

O agent deve reconciliar continuamente esses três níveis:

```text
estado desejado
estado local
estado real
```

---

## Diretório padrão

O estado local deve ser armazenado em:

```text
/var/lib/devex-agent
```

Estrutura sugerida:

```text
/var/lib/devex-agent/
├── agent.json
├── state.json
├── ports.json
├── gateway/
│   ├── current-caddy.json
│   ├── previous-caddy.json
│   └── last-good-caddy.json
└── locks/
    ├── state.lock
    ├── ports.lock
    └── caddy.lock
```

---

## Permissões

Diretórios e arquivos devem ter permissões restritivas.

Sugestão:

```bash
sudo mkdir -p /var/lib/devex-agent
sudo chmod 700 /var/lib/devex-agent
```

Arquivos de estado:

```bash
chmod 600 /var/lib/devex-agent/*.json
```

O estado local não deve conter secrets.

---

## agent.json

Arquivo usado para armazenar identidade local do agent.

Caminho:

```text
/var/lib/devex-agent/agent.json
```

Exemplo:

```json
{
  "agent_id": "agent-dev-api-001",
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "instance_id": "i-abc123",
  "private_ip": "10.0.2.25",
  "registered_at": "2026-06-05T18:00:00Z",
  "last_seen_at": "2026-06-05T18:05:00Z"
}
```

Uso:

- Evitar novo registro desnecessário.
- Identificar o agent após restart.
- Enviar heartbeat com o mesmo `agent_id`.
- Associar estado local à instância correta.

---

## state.json

Arquivo principal de estado operacional do Runtime Agent.

Caminho:

```text
/var/lib/devex-agent/state.json
```

Exemplo:

```json
{
  "agent_id": "agent-dev-api-001",
  "mode": "runtime",
  "environment": "dev",
  "role": "api",
  "last_applied_command_id": "cmd_123",
  "last_successful_command_id": "cmd_123",
  "deployments": [
    {
      "deployment_id": "dep_456",
      "application": "billing-api",
      "environment": "dev",
      "image": "ghcr.io/useclarus/billing-api:v42",
      "container_name": "billing-api-dev-v42",
      "host_port": 4102,
      "container_internal_port": 3000,
      "status": "active",
      "created_at": "2026-06-05T18:00:00Z",
      "updated_at": "2026-06-05T18:01:00Z"
    }
  ]
}
```

---

## Estados de deployment local

Estados possíveis:

```text
reserved
starting
checking_health
active
draining
failed
removed
orphaned
inconsistent
```

---

## reserved

O deployment possui recursos reservados, como porta, mas o container ainda não foi iniciado.

---

## starting

O container está sendo iniciado.

---

## checking_health

O container iniciou e está aguardando validação de health check.

---

## active

O container está saudável e representa uma versão ativa do deployment.

---

## draining

O container é uma versão antiga mantida temporariamente para rollback ou encerramento gracioso.

---

## failed

O deployment falhou e aguarda cleanup.

---

## removed

O deployment foi removido localmente.

---

## orphaned

O container existe no Docker com labels da DevEx Platform, mas não foi encontrado no estado local.

---

## inconsistent

Há divergência entre estado local, Docker ou Platform API.

---

## ports.json

Arquivo responsável por registrar portas alocadas.

Caminho:

```text
/var/lib/devex-agent/ports.json
```

Exemplo:

```json
{
  "range": {
    "from": 4100,
    "to": 4114
  },
  "allocations": {
    "4102": {
      "status": "active",
      "deployment_id": "dep_456",
      "application": "billing-api",
      "container_name": "billing-api-dev-v42",
      "container_internal_port": 3000,
      "allocated_at": "2026-06-05T18:00:00Z"
    },
    "4101": {
      "status": "draining",
      "deployment_id": "dep_455",
      "application": "billing-api",
      "container_name": "billing-api-dev-v41",
      "container_internal_port": 3000,
      "allocated_at": "2026-06-05T17:00:00Z",
      "draining_started_at": "2026-06-05T18:05:00Z"
    }
  }
}
```

Detalhes em:

- `docs/specs/07-port-management.md`

---

## Estado local do Gateway Agent

O Gateway Agent deve manter arquivos próprios para configuração Caddy.

Diretório:

```text
/var/lib/devex-agent/gateway
```

Arquivos sugeridos:

```text
current-caddy.json
previous-caddy.json
last-good-caddy.json
```

---

## current-caddy.json

Última configuração candidata gerada pelo Gateway Agent.

---

## previous-caddy.json

Configuração imediatamente anterior à última tentativa de aplicação.

---

## last-good-caddy.json

Última configuração aplicada e validada com sucesso.

Essa configuração deve ser usada para rollback se uma nova configuração falhar.

---

## locks

Locks devem proteger alterações concorrentes.

Diretório:

```text
/var/lib/devex-agent/locks
```

Locks sugeridos:

```text
state.lock
ports.lock
caddy.lock
```

Para o MVP, se o agent executar apenas uma operação mutável por vez, mutex em memória pode ser suficiente. Ainda assim, o design deve permitir lock de arquivo no futuro.

---

## Escrita atômica

Arquivos de estado devem ser escritos de forma atômica.

Fluxo recomendado:

```text
1. Escrever conteúdo em arquivo temporário.
2. Fazer fsync, quando aplicável.
3. Renomear arquivo temporário para o arquivo final.
```

Exemplo:

```text
state.json.tmp -> state.json
```

Isso reduz risco de corromper estado em caso de crash.

---

## Versão do schema

Cada arquivo de estado deve conter uma versão de schema.

Exemplo:

```json
{
  "schema_version": 1,
  "agent_id": "agent-dev-api-001"
}
```

Isso permite migrações futuras.

---

## Reconciliação do Runtime Agent

Ao iniciar e periodicamente, o Runtime Agent deve reconciliar:

```text
state.json
ports.json
docker ps
docker inspect
Platform API
```

Casos:

### Container no estado local, mas ausente no Docker

Ação:

```text
Marcar deployment como inconsistent ou removed.
Liberar porta se seguro.
Reportar evento.
```

### Container no Docker, mas ausente no estado local

Se tiver label `devex.managed=true`:

```text
Importar como orphaned ou reconciliar com deployment existente.
```

Se não tiver label gerenciada:

```text
Ignorar ou marcar como unmanaged.
```

### Porta em ports.json, mas sem container associado

Ação:

```text
Liberar porta ou marcar inconsistente.
```

### Porta usada no Docker, mas ausente em ports.json

Ação:

```text
Registrar se container for gerenciado.
Marcar como unmanaged se não for gerenciado.
```

---

## Reconciliação do Gateway Agent

O Gateway Agent deve reconciliar:

```text
desired state da Platform API
current-caddy.json
last-good-caddy.json
config ativa do Caddy
```

Casos:

### Config ativa diferente do desired state

Ação:

```text
Gerar e aplicar nova config.
```

### Falha ao aplicar nova config

Ação:

```text
Restaurar last-good-caddy.json.
Reportar erro.
```

### Caddy indisponível

Ação:

```text
Reportar CADDY_ADMIN_UNAVAILABLE.
Tentar novamente conforme política de retry.
```

---

## Estado local e idempotência

O estado local deve ajudar a evitar duplicidade.

Exemplo:

Se um comando `DEPLOY_APPLICATION` for recebido novamente:

```text
1. Verificar deployment_id em state.json.
2. Verificar container no Docker.
3. Verificar porta em ports.json.
4. Se já estiver active e saudável, reportar sucesso.
5. Se estiver parcial, reconciliar ou falhar de forma estruturada.
```

---

## Estado local não deve armazenar secrets

Não armazenar:

```text
Tokens
Senhas
AWS credentials
Docker registry credentials
Environment variables sensíveis
Headers Authorization
```

Secrets devem vir de mecanismos seguros, como:

```text
arquivos protegidos
AWS IAM Role
Secrets Manager
variáveis de ambiente controladas
```

---

## Backup e recuperação

Para o MVP, o estado local pode ser tratado como recuperável.

Se o estado local for perdido:

```text
1. Agent registra novamente ou recupera agent_id.
2. Inspeciona Docker.
3. Recria estado local a partir de labels.
4. Busca estado desejado da Platform API.
5. Reconcila.
```

A perda do estado local não deve ser fatal, mas pode exigir reconciliação cuidadosa.

---

## Erros esperados

Códigos:

```text
STATE_STORE_FAILED
STATE_LOAD_FAILED
STATE_WRITE_FAILED
STATE_CORRUPTED
STATE_SCHEMA_UNSUPPORTED
STATE_RECONCILIATION_FAILED
LOCK_ACQUIRE_FAILED
LOCK_RELEASE_FAILED
```

Exemplo:

```json
{
  "code": "STATE_CORRUPTED",
  "message": "Could not parse /var/lib/devex-agent/state.json"
}
```

---

## Critérios de aceite

O estado local estará correto quando:

```text
1. O agent persistir agent_id após registro.
2. Deployments ativos forem registrados em state.json.
3. Portas forem registradas em ports.json.
4. Escritas forem atômicas.
5. O agent recuperar estado após restart.
6. O agent reconciliar Docker real com estado local.
7. Containers órfãos forem detectados.
8. Portas inconsistentes forem tratadas.
9. Estado local não armazenar secrets.
10. Gateway Agent manter last-good-caddy.json.
```

---

## Regra final

O estado local é memória operacional.

Ele ajuda o agent a se recuperar, reconciliar e evitar duplicidade.

Mas a fonte da verdade global continua sendo a DevEx Platform.

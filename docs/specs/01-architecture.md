# 01 — Arquitetura

## Objetivo deste documento

Este documento descreve a arquitetura técnica do **DevEx Agent** dentro da **DevEx Platform**.

Ele explica:

- Componentes principais da solução.
- Responsabilidades de cada componente.
- Fluxos de deploy.
- Comunicação entre Platform API, Runtime Agent e Gateway Agent.
- Integração com Docker, Caddy e Route 53.
- Modelo de rede.
- Modelo de estado desejado versus estado real.
- Decisões arquiteturais do MVP.
- Limites de responsabilidade do agente.

Este documento deve ser lido junto com:

- `docs/specs/00-product-overview.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`

---

## Visão geral

A DevEx Platform permite que desenvolvedores façam deploy de aplicações Docker em instâncias EC2 sem precisar manipular diretamente Docker, Caddy, DNS, portas ou arquivos de configuração.

A arquitetura é baseada em quatro ideias centrais:

```text
1. A Platform API mantém o estado desejado.
2. Os agents executam localmente esse estado desejado.
3. O Caddy Gateway recebe tráfego público e roteia para backends privados.
4. O Route 53 resolve domínios públicos para o Caddy Gateway.
```

A separação principal é:

```text
Platform = cérebro
Agent = executor
Docker = runtime
Caddy = gateway
Route 53 = DNS
```

---

## Componentes principais

A arquitetura possui os seguintes componentes:

```text
DevEx Platform UI
DevEx Platform API
Banco da DevEx Platform
Runtime Agent
Gateway Agent
Docker Engine
Caddy Gateway
Route 53
EC2 Runtime
EC2 Gateway
```

---

## Diagrama lógico

```text
┌──────────────────────────┐
│ Desenvolvedor            │
└─────────────┬────────────┘
              │
              ▼
┌──────────────────────────┐
│ DevEx Platform UI        │
└─────────────┬────────────┘
              │
              ▼
┌──────────────────────────┐
│ DevEx Platform API       │
│                          │
│ - apps                   │
│ - environments           │
│ - deployments            │
│ - commands               │
│ - routes                 │
│ - agents                 │
└─────────────┬────────────┘
              │
              ▼
┌──────────────────────────┐
│ Banco da Platform        │
│                          │
│ Estado desejado          │
│ Histórico                │
│ Auditoria                │
└─────────────┬────────────┘
              │
      ┌───────┴────────┐
      │                │
      ▼                ▼
┌───────────────┐  ┌────────────────┐
│ Runtime Agent │  │ Gateway Agent  │
│ EC2 Runtime   │  │ EC2 Gateway    │
└───────┬───────┘  └───────┬────────┘
        │                  │
        ▼                  ▼
┌───────────────┐  ┌────────────────┐
│ Docker Engine │  │ Caddy Gateway  │
└───────────────┘  └───────┬────────┘
                           │
                           ▼
                    ┌─────────────┐
                    │ Aplicações  │
                    │ via IP:porta│
                    └─────────────┘
```

---

## Diagrama de tráfego público

```text
Usuário/Navegador
      │
      ▼
Route 53
      │
      ▼
IP público da EC2 Gateway
      │
      ▼
Caddy Gateway :80/:443
      │
      ▼
IP privado da EC2 Runtime : porta alocada
      │
      ▼
Container Docker da aplicação
```

Exemplo:

```text
billing-api.dev.useclarus.app
      ↓
Route 53 resolve para 54.233.10.20
      ↓
Caddy Gateway na EC2 pública
      ↓
reverse_proxy para 10.0.2.25:4102
      ↓
billing-api-dev-v42
```

---

## DevEx Platform UI

A **DevEx Platform UI** é a interface usada por desenvolvedores e administradores.

Responsabilidades:

- Solicitar deploys.
- Selecionar aplicação.
- Selecionar ambiente.
- Selecionar imagem/tag.
- Configurar ou visualizar domínio.
- Exibir status de deploy.
- Exibir health check.
- Exibir histórico.
- Permitir rollback.
- Permitir visualização de logs e eventos.

A UI não fala diretamente com agents.

Toda interação deve passar pela Platform API.

---

## DevEx Platform API

A **DevEx Platform API** é o cérebro da solução.

Responsabilidades:

- Manter o estado desejado.
- Registrar aplicações.
- Registrar ambientes.
- Registrar agents.
- Receber heartbeats.
- Criar comandos de deploy.
- Fazer scheduling de workloads.
- Direcionar comandos para agents específicos.
- Controlar ciclo de vida dos comandos.
- Atualizar estado de deployments.
- Atualizar estado desejado das rotas.
- Expor desired state para Gateway Agent.
- Guardar histórico e auditoria.

A Platform API deve decidir:

```text
Qual aplicação deve rodar.
Em qual ambiente.
Em qual agent.
Com qual imagem.
Com qual domínio.
Com qual estratégia.
```

O agent apenas executa.

---

## Banco da DevEx Platform

O banco da plataforma é a fonte da verdade global.

Ele deve armazenar, no mínimo:

```text
applications
environments
agents
agent_capabilities
deployments
deployment_versions
commands
routes
route_versions
events
audit_logs
```

Exemplo de estado de deployment:

```json
{
  "application": "billing-api",
  "environment": "dev",
  "image": "ghcr.io/useclarus/billing-api:v42",
  "target_agent_id": "agent-dev-api-001",
  "runtime_private_ip": "10.0.2.25",
  "host_port": 4102,
  "domain": "billing-api.dev.useclarus.app",
  "status": "healthy"
}
```

---

## Runtime Agent

O **Runtime Agent** roda nas EC2s que executam aplicações.

Responsabilidades:

- Registrar a instância na Platform API.
- Enviar heartbeat.
- Buscar comandos pendentes.
- Fazer claim de comandos.
- Executar Docker pull.
- Executar Docker run.
- Parar/remover containers.
- Alocar portas.
- Persistir estado local.
- Reconciliar estado local com Docker.
- Executar health checks.
- Reportar resultados.

O Runtime Agent trabalha com workloads como:

```text
frontend
api
worker
```

Frontends e APIs normalmente exigem rota no Caddy.

Workers normalmente não exigem rota pública.

---

## Gateway Agent

O **Gateway Agent** roda na EC2 Gateway, onde o Caddy está instalado.

Responsabilidades:

- Buscar o desired state de rotas.
- Gerar o arquivo completo `caddy.json`.
- Validar configuração.
- Aplicar configuração no Caddy via `/load`.
- Validar rotas.
- Reportar status das rotas.
- Manter fallback operacional.

O Gateway Agent não executa aplicações.

O Gateway Agent não faz Docker pull de workloads de produto.

Ele gerencia exclusivamente o Caddy Gateway.

---

## Caddy Gateway

O **Caddy** é o gateway HTTP/HTTPS da plataforma.

Responsabilidades:

- Receber tráfego público nas portas 80 e 443.
- Emitir certificados HTTPS automaticamente.
- Encerrar TLS.
- Roteiar por host.
- Fazer reverse proxy para as aplicações em EC2s runtime.
- Expor Admin API localmente para o Gateway Agent.

Portas públicas:

```text
80/tcp
443/tcp
443/udp opcional para HTTP/3
```

Porta administrativa:

```text
2019/tcp somente localhost
```

A Admin API deve ficar acessível apenas localmente:

```text
http://127.0.0.1:2019
```

O Docker deve publicar:

```yaml
127.0.0.1:2019:2019
```

No Caddy config:

```json
{
  "admin": {
    "listen": "0.0.0.0:2019"
  }
}
```

Essa combinação permite que o Caddy escute dentro do container e, ao mesmo tempo, restrinja o acesso ao host local.

---

## Route 53

O **Route 53** é usado como DNS público.

Ele deve apontar os domínios da plataforma para o Caddy Gateway.

Exemplo recomendado para ambientes não produtivos:

```text
*.dev.useclarus.app    -> IP público do Caddy Gateway dev
*.stage.useclarus.app  -> IP público do Caddy Gateway stage
```

Para produção, pode-se usar registros explícitos:

```text
api.useclarus.app      -> IP público do Caddy Gateway prod
app.useclarus.app      -> IP público do Caddy Gateway prod
billing.useclarus.app  -> IP público do Caddy Gateway prod
```

O Route 53 não deve apontar diretamente para EC2s runtime de aplicação.

A regra é:

```text
DNS leva o usuário até o Caddy Gateway.
Caddy leva a requisição até a aplicação correta.
```

---

## EC2 Gateway

A **EC2 Gateway** é a instância que recebe tráfego público.

Ela executa:

```text
Caddy
Gateway Agent
```

Ela deve possuir:

- IP público ou Elastic IP.
- Security Group liberando 80/443 publicamente.
- Caddy Admin API apenas local.
- Acesso de rede privada às EC2s runtime.
- Permissão mínima necessária para operar, se aplicável.

Security Group recomendado:

```text
Inbound:
80/tcp   de 0.0.0.0/0
443/tcp  de 0.0.0.0/0
443/udp  de 0.0.0.0/0 opcional
22/tcp   apenas IP administrativo, se necessário

Não expor:
2019/tcp
```

---

## EC2 Runtime

A **EC2 Runtime** é a instância que executa aplicações.

Ela executa:

```text
Runtime Agent
Docker Engine
Containers de aplicação
```

Ela preferencialmente não deve ter tráfego público direto.

Security Group recomendado:

```text
Inbound:
Port range de runtime, por exemplo 4100-4114,
somente a partir do Security Group da EC2 Gateway.

Sem exposição pública das portas de aplicação.
```

Exemplo:

```text
Caddy Gateway SG -> TCP 4100-4114 -> EC2 Runtime SG
```

---

## Modelo de rede

O modelo básico é:

```text
VPC
├── Subnet pública
│   └── EC2 Gateway
│       ├── Caddy
│       └── Gateway Agent
│
└── Subnet privada ou restrita
    └── EC2 Runtime
        ├── Runtime Agent
        └── Docker Containers
```

Para MVP, a EC2 Runtime pode estar em subnet pública, desde que as portas de aplicação estejam restritas por Security Group.

A recomendação de longo prazo é deixar as EC2s runtime privadas.

---

## Modelo de comunicação

### Agent para Platform

O modelo recomendado é comunicação outbound:

```text
Agent -> Platform API
```

Evitar no MVP:

```text
Platform API -> Agent
```

Motivos:

- Não exige expor porta no agent.
- Funciona melhor atrás de NAT.
- Reduz superfície de ataque.
- Simplifica firewall/security group.
- Simplifica operação.

---

## Polling de comandos

O Runtime Agent usa polling:

```text
A cada N segundos:
1. Envia heartbeat.
2. Busca comandos pendentes.
3. Faz claim de um comando.
4. Executa.
5. Reporta resultado.
```

Exemplo:

```http
GET /api/agents/{agent_id}/commands/pending
```

O Gateway Agent também pode usar polling para desired state:

```http
GET /api/agents/{agent_id}/desired-state
```

---

## Command lifecycle

O comando segue uma máquina de estados.

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

Transições esperadas:

```text
pending -> claimed
claimed -> running
running -> succeeded
running -> failed
pending -> expired
```

O claim deve ser atômico na Platform API.

O agent nunca deve executar um comando que não tenha conseguido reivindicar.

Detalhes em:

- `docs/specs/05-command-lifecycle.md`

---

## Desired State

O desired state é o estado desejado calculado pela Platform API.

Para Runtime Agent, pode conter:

```json
{
  "version": 42,
  "deployments": [
    {
      "application": "billing-api",
      "image": "ghcr.io/useclarus/billing-api:v42",
      "container_name": "billing-api-dev-v42",
      "container_internal_port": 3000,
      "health_check_path": "/health"
    }
  ]
}
```

Para Gateway Agent, pode conter:

```json
{
  "version": 43,
  "routes": [
    {
      "host": "billing-api.dev.useclarus.app",
      "upstream": "10.0.2.25:4102"
    }
  ]
}
```

O agent aplica o desired state e reporta o resultado.

---

## Estado local do agente

O agent deve persistir estado local.

Diretório padrão:

```text
/var/lib/devex-agent
```

Arquivos sugeridos:

```text
agent.json
state.json
ports.json
locks/
```

O estado local serve para:

- Recuperação.
- Reconciliação.
- Controle de portas.
- Controle de deployments em andamento.
- Controle de containers em draining.
- Evitar duplicidade.

O estado local não é fonte da verdade global.

A fonte global permanece sendo a Platform API.

---

## Reconciliação

O agent deve reconciliar periodicamente:

```text
estado desejado
estado local
estado real
```

Para Runtime Agent:

```text
Platform desired deployments
Local state
docker ps / docker inspect
```

Para Gateway Agent:

```text
Platform desired routes
Generated caddy.json
Caddy active config
```

Se houver divergência, o agent deve tentar corrigir ou reportar erro.

---

## Fluxo de deploy inicial

```text
1. Dev solicita deploy na UI.
2. Platform API cria deployment.
3. Platform API escolhe Runtime Agent compatível.
4. Platform API cria comando DEPLOY_APPLICATION.
5. Runtime Agent busca comando.
6. Runtime Agent faz claim.
7. Runtime Agent faz docker pull.
8. Runtime Agent aloca porta.
9. Runtime Agent sobe container.
10. Runtime Agent executa health check local.
11. Runtime Agent reporta endpoint saudável.
12. Platform API atualiza routes desired state.
13. Gateway Agent busca desired state.
14. Gateway Agent gera caddy.json.
15. Gateway Agent aplica /load no Caddy.
16. Gateway Agent valida rota.
17. Platform API marca deployment como healthy.
```

---

## Fluxo de atualização de imagem

```text
1. Aplicação v41 está ativa.
2. Dev solicita deploy da imagem v42.
3. Runtime Agent mantém v41 rodando.
4. Runtime Agent baixa v42.
5. Runtime Agent aloca nova porta.
6. Runtime Agent sobe container v42.
7. Runtime Agent valida v42 localmente.
8. Runtime Agent reporta endpoint v42.
9. Gateway Agent atualiza Caddy para v42.
10. Gateway Agent valida rota.
11. Platform marca v42 como ativa.
12. v41 entra em draining.
13. v41 é removida após janela de segurança.
```

Exemplo:

```text
Antes:
billing-api.dev.useclarus.app -> 10.0.2.25:4101

Durante:
v41 -> 10.0.2.25:4101
v42 -> 10.0.2.25:4102

Depois:
billing-api.dev.useclarus.app -> 10.0.2.25:4102
```

---

## Fluxo de rollback

O rollback pode ocorrer quando:

- Nova imagem não sobe.
- Health check local falha.
- Caddy não aplica configuração.
- Health check via Gateway falha.
- Erro operacional inesperado.

Fluxo:

```text
1. Detectar falha.
2. Manter versão antiga ativa, se ainda não houve troca.
3. Se houve troca, restaurar rota anterior.
4. Remover container novo se necessário.
5. Liberar porta reservada.
6. Reportar failed ou rolled_back para a Platform API.
```

---

## Gestão de capacidade

Cada Runtime Agent declara suas capacidades.

Exemplo:

```yaml
agent:
  mode: runtime
  environment: dev
  role: api

runtime:
  max_active_containers: 10

ports:
  from: 4100
  to: 4114
```

A Platform API usa essas informações para scheduling.

Critérios iniciais para MVP:

```text
environment compatível
role compatível
agent online
capacidade disponível
menor quantidade de containers ativos
```

O agent não escolhe qual aplicação deve rodar nele.

A Platform API cria comandos direcionados para um `target_agent_id`.

---

## Workload types

A aplicação deve declarar um tipo:

```text
frontend
api
worker
```

### Frontend

Normalmente:

```text
requires_route = true
```

### API

Normalmente:

```text
requires_route = true
```

### Worker

Normalmente:

```text
requires_route = false
```

Workers não entram na configuração do Caddy, salvo se expuserem endpoints específicos.

---

## Integração com Caddy

A integração com Caddy deve usar configuração completa.

Evitar como mecanismo principal:

```text
PATCH/POST incremental por rota
```

Preferir:

```text
gerar caddy.json completo
POST /load
```

Motivo:

- Mais previsível.
- Mais auditável.
- Mais simples de versionar.
- Facilita rollback.
- Evita drift de configuração.

O Gateway Agent deve gerar uma configuração completa a partir do desired state da Platform API.

---

## Autosave do Caddy

O Caddy salva a configuração ativa em:

```text
/config/caddy/autosave.json
```

Esse arquivo pode ser usado como fallback com:

```bash
caddy run --resume
```

Porém, o autosave não deve ser tratado como fonte primária da verdade.

A fonte primária continua sendo a DevEx Platform.

Estratégia recomendada:

```text
1. Platform API mantém estado desejado.
2. Gateway Agent gera caddy.json.
3. Gateway Agent aplica /load.
4. Caddy salva autosave.json.
5. Em falha de boot, autosave pode ser fallback.
```

---

## Route 53 e DNS

Para reduzir complexidade operacional, ambientes dev e stage podem usar wildcard DNS.

Exemplo:

```text
*.dev.useclarus.app    -> IP público do Caddy Gateway dev
*.stage.useclarus.app  -> IP público do Caddy Gateway stage
```

Assim, a plataforma não precisa criar DNS para cada aplicação em dev/stage.

Para produção, usar registros explícitos pode ser mais seguro e auditável.

Exemplo:

```text
api.useclarus.app      -> IP público do Caddy Gateway prod
app.useclarus.app      -> IP público do Caddy Gateway prod
```

---

## Segurança

Princípios:

```text
Runtime Agent não expõe API pública.
Gateway Agent não expõe Caddy Admin API publicamente.
Agents usam comunicação outbound.
Tokens não são logados.
Docker socket é considerado privilegiado.
Security Groups restringem tráfego runtime.
```

A EC2 Runtime deve aceitar tráfego nas portas de aplicação somente vindo do Caddy Gateway.

A EC2 Gateway deve expor apenas 80/443 publicamente.

---

## Observabilidade

Logs estruturados são obrigatórios.

Campos recomendados:

```text
agent_id
mode
environment
role
command_id
deployment_id
application
container_name
status
error_code
```

Eventos importantes:

```text
agent_started
agent_registered
heartbeat_sent
command_claimed
docker_pull_started
docker_pull_completed
container_started
port_allocated
health_check_passed
health_check_failed
command_reported
caddy_config_generated
caddy_config_loaded
route_validated
```

---

## Decisões arquiteturais do MVP

### Decisão 1 — Go como linguagem do agent

O agent será implementado em Go.

Motivos:

- Binário único.
- Baixo overhead.
- Fácil operação com systemd.
- Boa biblioteca padrão.
- Simplicidade para processos long-running.

---

### Decisão 2 — Polling em vez de chamada direta ao agent

O agent busca comandos na Platform API.

Motivos:

- Não expõe porta pública.
- Reduz superfície de ataque.
- Funciona melhor em redes restritas.
- Simplifica o MVP.

---

### Decisão 3 — Docker CLI no MVP

O MVP pode usar Docker CLI via `os/exec`.

Motivos:

- Mais simples.
- Menos acoplamento inicial.
- Fácil de depurar.
- Pode evoluir para Docker SDK depois.

---

### Decisão 4 — Caddy via `/load`

O Gateway Agent aplica a configuração completa usando `/load`.

Motivos:

- Evita drift.
- Facilita auditoria.
- Facilita rollback.
- Facilita geração declarativa.

---

### Decisão 5 — Caddy Gateway centralizado

O DNS aponta para o Caddy Gateway.

O Caddy Gateway roteia para EC2s Runtime via IP privado e porta.

Motivos:

- Centraliza entrada HTTP/HTTPS.
- Mantém aplicações privadas.
- Simplifica certificados.
- Simplifica controle de tráfego.

---

### Decisão 6 — Porta alocada pelo Runtime Agent

O host port é alocado automaticamente.

Motivos:

- Evita conflito.
- Permite blue/green local.
- Remove responsabilidade do desenvolvedor.
- Facilita automação.

---

## Limites da arquitetura

Esta arquitetura não pretende substituir Kubernetes, ECS, Nomad ou outros orquestradores completos.

Ela é adequada para:

- MVP de plataforma DevEx.
- Deploys controlados em EC2.
- Baixa/média escala inicial.
- Ambientes dev/stage/prod simples.
- Times que querem abstrair Docker/Caddy/EC2.

Limitações:

- Scheduling inicial simples.
- Sem autoscaling nativo.
- Sem service discovery avançado.
- Sem traffic splitting avançado.
- Sem recuperação automática complexa multi-node.
- Sem bin packing sofisticado no MVP.

---

## Evoluções futuras

Possíveis evoluções:

- Long polling ou fila SQS.
- Docker SDK.
- Scheduler com CPU/memória.
- Suporte a canary.
- Suporte a traffic splitting.
- Métricas Prometheus.
- Logs centralizados.
- Integração com Secrets Manager.
- Provisionamento automático de EC2.
- Integração com Auto Scaling Groups.
- Suporte a ECS/Kubernetes no futuro.
- GitOps para `caddy.json`.

---

## Relação com os próximos documentos

Este documento apresenta a arquitetura técnica geral.

Detalhes específicos devem ser consultados em:

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

---

## Regra final da arquitetura

A arquitetura deve manter responsabilidades claras.

```text
A Platform decide.
O Agent executa.
O Docker roda.
O Caddy roteia.
O Route 53 resolve.
```

Sempre que surgir uma dúvida de design, prefira a solução que:

```text
1. Simplifica a experiência do desenvolvedor.
2. Mantém o agent simples.
3. Preserva a Platform como fonte da verdade.
4. Evita exposição pública desnecessária.
5. Facilita rollback e auditoria.

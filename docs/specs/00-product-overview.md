# 00 — Visão Geral do Produto

## Produto

O **DevEx Agent** é um componente de infraestrutura da **DevEx Platform** responsável por executar, em instâncias EC2, as ações necessárias para transformar uma solicitação de deploy em uma aplicação efetivamente publicada e operacional.

Ele atua como executor local da plataforma, realizando operações como:

- Execução de containers Docker.
- Atualização de imagens de aplicações.
- Alocação de portas no host.
- Execução de health checks.
- Aplicação de rotas no Caddy Gateway.
- Reporte de status para a DevEx Platform.
- Reconciliação entre estado desejado e estado real.
- Suporte a rollback e limpeza de versões antigas.

O produto foi desenhado para simplificar a vida do desenvolvedor, escondendo detalhes operacionais como Caddy, Docker, portas, IPs privados, Route 53, health checks, reload de configuração e rollback.

---

## Objetivo do produto

O objetivo principal do DevEx Agent é permitir que a DevEx Platform ofereça uma experiência de deploy simples e transparente.

O desenvolvedor deve se preocupar apenas com informações de alto nível:

```text
Aplicação
Ambiente
Imagem Docker/tag
Domínio ou subdomínio desejado
Porta interna da aplicação
Health check
```

A plataforma e os agentes devem cuidar automaticamente de:

```text
Selecionar a instância correta
Executar o container
Alocar porta no host
Atualizar a rota no Caddy
Validar o deploy
Publicar a URL
Fazer rollback em caso de falha
Reportar o status
```

A experiência esperada é:

```text
O desenvolvedor solicita um deploy.
A plataforma executa todo o fluxo.
Ao final, o desenvolvedor recebe uma URL funcional.
```

---

## Problema que o produto resolve

Sem uma plataforma DevEx, o deploy em EC2 com Docker e Caddy exige que o desenvolvedor ou operador saiba lidar manualmente com:

- EC2.
- Docker.
- Docker Compose.
- Portas do host.
- IP privado da instância.
- Caddyfile ou `caddy.json`.
- Caddy Admin API.
- DNS no Route 53.
- Certificados HTTPS.
- Health checks.
- Rollback.
- Logs.
- Estado atual dos containers.

Esse modelo gera problemas:

```text
Alto acoplamento entre desenvolvimento e operação.
Maior chance de erro humano.
Baixa rastreabilidade.
Baixa padronização.
Dificuldade para rollback.
Dificuldade para saber o que está rodando em cada EC2.
Dificuldade para gerenciar portas e rotas.
```

O DevEx Agent resolve esse problema criando uma camada automatizada de execução local, controlada pela DevEx Platform.

---

## Princípio central

O sistema deve seguir a regra:

```text
Platform = cérebro
Agent = executor
Docker = runtime
Caddy = gateway
Route 53 = DNS
```

Ou seja:

- A **DevEx Platform** decide o que deve existir.
- O **DevEx Agent** aplica localmente o estado desejado.
- O **Docker** executa os containers.
- O **Caddy** recebe tráfego HTTP/HTTPS e roteia para os backends.
- O **Route 53** resolve os domínios para o Caddy Gateway.

O agente não deve tomar decisões globais de scheduling.

O agente não deve ser a fonte da verdade.

A fonte da verdade é a DevEx Platform.

---

## Público-alvo

O produto atende principalmente três perfis.

### Desenvolvedores

São os usuários finais da experiência de deploy.

Eles precisam:

- Publicar aplicações rapidamente.
- Escolher ambiente.
- Escolher imagem/tag.
- Receber URL de acesso.
- Consultar status.
- Visualizar logs.
- Fazer rollback quando necessário.

Eles não devem precisar entender:

- Como o Caddy é configurado.
- Como portas são alocadas.
- Qual EC2 está executando o container.
- Como o Route 53 está configurado.
- Como o Gateway Agent aplica rotas.
- Como o Runtime Agent executa Docker.

---

### Administradores da plataforma

São responsáveis por configurar ambientes e capacidade.

Eles precisam:

- Cadastrar ambientes.
- Configurar instâncias EC2.
- Configurar roles de agents.
- Definir limites de containers.
- Definir faixas de portas.
- Configurar Caddy Gateway.
- Configurar domínio base.
- Auditar deploys.
- Diagnosticar falhas operacionais.

---

### Operadores/SREs

São responsáveis pela confiabilidade operacional.

Eles precisam:

- Monitorar agents.
- Verificar heartbeats.
- Diagnosticar falhas de deploy.
- Acompanhar health checks.
- Auditar comandos executados.
- Verificar estado real versus estado desejado.
- Executar ações corretivas.

---

## Escopo funcional do produto

O DevEx Agent deve suportar dois modos principais:

```text
runtime
gateway
```

---

## Runtime Agent

O **Runtime Agent** roda nas EC2s responsáveis por executar aplicações.

Responsabilidades principais:

- Registrar a EC2 na DevEx Platform.
- Enviar heartbeat.
- Buscar comandos pendentes.
- Fazer claim atômico dos comandos.
- Fazer pull de imagens Docker.
- Subir containers.
- Parar containers.
- Remover containers.
- Alocar portas.
- Persistir estado local.
- Reconciliar estado local com Docker.
- Executar health checks locais.
- Reportar resultado para a plataforma.
- Manter versões antigas em draining quando necessário.
- Executar rollback local quando aplicável.

Exemplo de workload atendido pelo Runtime Agent:

```text
frontend
api
worker
```

O Runtime Agent não deve:

- Criar ou alterar DNS no Route 53.
- Aplicar rotas no Caddy Gateway.
- Escolher globalmente em qual EC2 a aplicação deve rodar.
- Expor uma API pública.

---

## Gateway Agent

O **Gateway Agent** roda na EC2 responsável pelo Caddy Gateway.

Responsabilidades principais:

- Buscar estado desejado de rotas na DevEx Platform.
- Gerar a configuração completa do Caddy.
- Validar o `caddy.json`.
- Aplicar configuração via Caddy Admin API `/load`.
- Validar rotas.
- Reportar status das rotas.
- Manter o Caddy operacional.
- Usar fallback de configuração quando necessário.

O Gateway Agent não deve:

- Executar containers de aplicação.
- Fazer pull de imagens de aplicação.
- Alocar portas de aplicação.
- Decidir onde a aplicação deve rodar.

---

## Experiência desejada para o desenvolvedor

O desenvolvedor acessa a DevEx Platform e solicita um deploy.

Exemplo:

```text
Aplicação: billing-api
Ambiente: dev
Imagem: ghcr.io/useclarus/billing-api:v42
Porta interna: 3000
Health check: /health
Subdomínio: billing-api.dev.useclarus.app
```

A plataforma executa o fluxo completo.

Ao final, o desenvolvedor vê:

```text
Deploy concluído com sucesso.

URL:
https://billing-api.dev.useclarus.app

Status:
Healthy

Versão:
v42
```

O desenvolvedor não precisa saber:

- Qual porta do host foi usada.
- Qual EC2 recebeu o container.
- Como o Caddy foi atualizado.
- Como o health check foi executado.
- Como a rota foi aplicada.
- Como o rollback seria feito.

---

## Fluxo de deploy esperado

Fluxo simplificado:

```text
1. Desenvolvedor solicita deploy.
2. DevEx Platform registra o deployment.
3. DevEx Platform escolhe o Runtime Agent adequado.
4. DevEx Platform cria comando de deploy.
5. Runtime Agent busca o comando por polling.
6. Runtime Agent faz claim do comando.
7. Runtime Agent faz pull da imagem.
8. Runtime Agent aloca porta.
9. Runtime Agent sobe container.
10. Runtime Agent executa health check local.
11. Runtime Agent reporta endpoint saudável.
12. DevEx Platform atualiza estado desejado das rotas.
13. Gateway Agent busca o novo desired state.
14. Gateway Agent gera caddy.json.
15. Gateway Agent aplica configuração no Caddy via /load.
16. Gateway Agent valida a rota.
17. DevEx Platform marca o deploy como healthy.
18. Desenvolvedor recebe a URL final.
```

---

## Atualização de aplicações

Quando uma aplicação já está em execução, a atualização da imagem deve ser tratada como um novo deployment.

O agente não deve atualizar o container existente diretamente.

Fluxo esperado:

```text
1. Manter versão atual rodando.
2. Baixar nova imagem.
3. Alocar nova porta.
4. Subir novo container.
5. Validar health check.
6. Atualizar rota no Caddy.
7. Validar acesso via Gateway.
8. Marcar nova versão como ativa.
9. Manter versão antiga em draining.
10. Remover versão antiga após janela de segurança.
```

Exemplo:

```text
Versão atual:
billing-api-dev-v41 -> 10.0.2.25:4101

Nova versão:
billing-api-dev-v42 -> 10.0.2.25:4102

Rota após atualização:
billing-api.dev.useclarus.app -> 10.0.2.25:4102
```

---

## Gestão de portas

A gestão de portas é uma responsabilidade central do Runtime Agent.

Regras:

```text
O desenvolvedor não escolhe a porta do host.
A aplicação não define porta fixa no host.
O Runtime Agent aloca a porta automaticamente.
A Platform API armazena o endpoint ativo.
O Caddy aponta para IP privado + porta alocada.
```

Exemplo de configuração:

```yaml
runtime:
  max_active_containers: 10

ports:
  from: 4100
  to: 4114
```

Esse modelo permite:

- 10 containers ativos.
- Portas extras para deploys blue/green.
- Draining de versões antigas.
- Rollback rápido.
- Controle centralizado de capacidade.

---

## DNS e roteamento

O DNS deve levar o tráfego até o Caddy Gateway.

Exemplo no Route 53:

```text
*.dev.useclarus.app    -> IP público do Caddy Gateway
*.stage.useclarus.app  -> IP público do Caddy Gateway
```

O Caddy decide para qual aplicação enviar a requisição.

Exemplo:

```text
billing-api.dev.useclarus.app -> 10.0.2.25:4102
orders-api.dev.useclarus.app  -> 10.0.2.31:4103
```

O Route 53 não deve apontar diretamente para as EC2s runtime das aplicações.

O Route 53 aponta para o Gateway.

O Gateway roteia internamente para as aplicações.

---

## Relação entre Platform, Agent, Docker, Caddy e Route 53

```text
Developer
   ↓
DevEx Platform
   ↓
Runtime Agent
   ↓
Docker Container
```

Para publicação HTTP/HTTPS:

```text
Route 53
   ↓
Caddy Gateway
   ↓
EC2 Runtime
   ↓
Docker Container
```

O fluxo completo:

```text
DevEx Platform cria o estado desejado.
Runtime Agent executa containers.
Gateway Agent aplica rotas.
Caddy roteia tráfego.
Route 53 resolve nomes.
```

---

## Estado desejado e estado real

A DevEx Platform mantém o estado desejado.

Exemplo:

```json
{
  "application": "billing-api",
  "environment": "dev",
  "image": "ghcr.io/useclarus/billing-api:v42",
  "domain": "billing-api.dev.useclarus.app",
  "runtime_agent": "agent-dev-api-001",
  "runtime_private_ip": "10.0.2.25",
  "host_port": 4102,
  "status": "healthy"
}
```

O agente mantém estado local apenas para fins operacionais.

Exemplos:

```text
/var/lib/devex-agent/state.json
/var/lib/devex-agent/ports.json
/var/lib/devex-agent/agent.json
```

O estado local serve para:

- Recuperação.
- Reconciliação.
- Evitar duplicidade.
- Controlar portas.
- Controlar deployments em draining.

O estado local não substitui a DevEx Platform.

---

## Modelo de operação

O agente deve operar como daemon.

Em EC2 Linux, o agente deve rodar via systemd.

Exemplo:

```text
devex-agent.service
```

O agente deve iniciar junto com a instância e permanecer rodando continuamente.

Responsabilidades contínuas:

- Enviar heartbeat.
- Buscar comandos.
- Reconciliar estado.
- Reportar status.
- Executar limpeza de recursos antigos.
- Detectar divergências.

---

## Comunicação

O agente deve usar comunicação outbound com a DevEx Platform.

Modelo recomendado:

```text
Agent -> Platform API
```

Evitar no MVP:

```text
Platform API -> Agent
```

Motivo:

- Não exige expor porta pública no agente.
- Funciona melhor atrás de NAT.
- Reduz superfície de ataque.
- Simplifica segurança.
- Simplifica operação em EC2.

---

## Segurança

Princípios mínimos:

- O agente não deve expor API pública.
- A Caddy Admin API deve ficar acessível apenas localmente.
- Tokens devem ficar em arquivos protegidos.
- Secrets não devem ser logados.
- O Docker socket deve ser tratado como recurso privilegiado.
- Portas de aplicações em EC2 runtime devem aceitar tráfego apenas do Security Group do Caddy Gateway.
- Credenciais AWS devem usar IAM Role quando possível.

---

## Observabilidade

O produto deve permitir diagnóstico operacional.

O agente deve registrar logs estruturados contendo:

```text
agent_id
mode
environment
command_id
deployment_id
application
container_name
status
error_code
```

Eventos importantes:

- Agent iniciado.
- Registro na plataforma.
- Heartbeat enviado.
- Comando recebido.
- Comando reivindicado.
- Docker pull iniciado/concluído.
- Container iniciado.
- Porta alocada.
- Health check executado.
- Deploy concluído.
- Deploy falhou.
- Rollback executado.
- Caddy config aplicada.
- Rota validada.

---

## Métricas futuras

O MVP pode começar apenas com logs, mas o produto deve permitir evolução para métricas como:

```text
agent_heartbeat_total
agent_commands_processed_total
agent_command_errors_total
agent_deploy_duration_seconds
agent_running_containers
agent_allocated_ports
agent_health_check_failures_total
gateway_routes_total
gateway_caddy_load_errors_total
```

---

## Escopo do MVP

O MVP deve entregar:

- Runtime Agent.
- Gateway Agent.
- Polling na Platform API.
- Execução Docker via Docker CLI.
- Alocação de portas.
- Deploy de aplicação.
- Atualização de imagem com nova porta.
- Health check HTTP.
- Geração de `caddy.json`.
- Aplicação no Caddy via `/load`.
- Persistência de estado local.
- Logs estruturados.
- Instalação via systemd.

---

## Fora do escopo do MVP

Não fazem parte do MVP:

- Kubernetes.
- ECS.
- Nomad.
- Multi-cloud.
- Service mesh.
- Canary deployment.
- Traffic splitting.
- Autoscaling automático.
- Scheduler avançado por CPU/memória.
- UI completa de observabilidade.
- API pública do agente.
- Criação dinâmica avançada de DNS por agente.
- Integração direta do Runtime Agent com Route 53.

---

## Critérios de sucesso

O produto será considerado bem-sucedido quando:

```text
1. Um desenvolvedor conseguir solicitar deploy de uma aplicação pela plataforma.
2. A aplicação for executada em uma EC2 runtime.
3. Uma porta for alocada automaticamente.
4. O health check local for executado.
5. O Caddy Gateway for atualizado automaticamente.
6. A aplicação ficar acessível por um subdomínio.
7. O status final for reportado para a plataforma.
8. A versão antiga puder ser mantida temporariamente para rollback.
9. O desenvolvedor não precisar manipular Docker, Caddy ou DNS manualmente.
```

---

## Relação com outros documentos

Este documento apresenta a visão geral do produto.

Para detalhes específicos, consulte:

- `docs/specs/01-architecture.md` para arquitetura técnica.
- `docs/specs/02-agent-runtime-spec.md` para detalhes do Runtime Agent.
- `docs/specs/03-agent-gateway-spec.md` para detalhes do Gateway Agent.
- `docs/specs/04-platform-api-contracts.md` para contratos HTTP.
- `docs/specs/05-command-lifecycle.md` para ciclo de vida dos comandos.
- `docs/specs/06-deployment-flow.md` para fluxo de deploy.
- `docs/specs/07-port-management.md` para gestão de portas.
- `docs/specs/08-caddy-integration.md` para integração com Caddy.
- `docs/specs/09-docker-runtime.md` para operações Docker.
- `docs/specs/10-local-state.md` para persistência local.
- `docs/specs/11-health-checks.md` para validação de saúde.
- `docs/specs/12-security.md` para requisitos de segurança.
- `docs/specs/13-observability.md` para logs e métricas.
- `docs/specs/14-error-handling-and-retry.md` para tratamento de erros.
- `docs/specs/15-configuration.md` para configuração do agente.
- `docs/specs/16-systemd-installation.md` para instalação.
- `docs/specs/17-testing-strategy.md` para estratégia de testes.
- `docs/specs/18-implementation-roadmap.md` para roadmap de implementação.

---

## Regra final

O DevEx Agent deve ser simples, previsível e seguro.

Ele não deve tentar substituir uma plataforma de orquestração completa.

Ele deve cumprir bem seu papel:

```text
Receber comandos.
Executar localmente.
Validar.
Reportar.
Reconciliar.
```

A inteligência de produto e scheduling pertence à DevEx Platform.

A execução local pertence ao DevEx Agent.

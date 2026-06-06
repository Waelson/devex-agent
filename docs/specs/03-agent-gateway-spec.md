# 03 — Especificação do Gateway Agent

## Objetivo deste documento

Este documento define a especificação funcional e técnica do **Gateway Agent**.

O Gateway Agent é o agente responsável por operar o **Caddy Gateway** em uma instância EC2 dedicada ao roteamento HTTP/HTTPS. Ele busca o estado desejado de rotas na DevEx Platform, gera a configuração completa do Caddy, aplica a configuração via Admin API `/load`, valida as rotas e reporta o resultado para a Platform API.

Este documento deve ser lido junto com:

- `docs/specs/00-product-overview.md`
- `docs/specs/01-architecture.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/05-command-lifecycle.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/12-security.md`
- `docs/specs/13-observability.md`
- `docs/specs/14-error-handling-and-retry.md`
- `docs/specs/15-configuration.md`

---

## Definição

O **Gateway Agent** é um processo instalado na EC2 responsável pelo Caddy Gateway.

Ele atua como reconciliador local entre:

```text
estado desejado de rotas na DevEx Platform
        ↓
configuração gerada do Caddy
        ↓
configuração ativa no Caddy Gateway
```

A Platform API decide quais rotas devem existir.

O Gateway Agent aplica essas rotas no Caddy.

---

## Princípio central

O Gateway Agent não é a fonte da verdade.

A fonte da verdade é a **DevEx Platform**.

O Gateway Agent deve ser um executor local, previsível e seguro.

Regra:

```text
Platform API define rotas.
Gateway Agent gera configuração.
Caddy aplica roteamento.
```

---

## Responsabilidades

O Gateway Agent deve ser responsável por:

- Registrar a instância gateway na Platform API.
- Enviar heartbeat periódico.
- Buscar o desired state de rotas.
- Comparar a versão desejada com a última versão aplicada.
- Gerar o arquivo completo `caddy.json`.
- Validar hosts e upstreams.
- Validar a estrutura da configuração.
- Aplicar a configuração no Caddy usando `/load`.
- Validar rotas após aplicação.
- Salvar `current-caddy.json`.
- Salvar `previous-caddy.json`.
- Salvar `last-good-caddy.json`.
- Restaurar last-good em caso de falha.
- Reportar sucesso ou falha para a Platform API.
- Emitir logs estruturados.
- Reconciliar configuração ativa com desired state.

---

## Fora do escopo

O Gateway Agent não deve:

- Executar containers de aplicação.
- Fazer `docker pull` de imagens de aplicação.
- Fazer `docker run` de workloads de produto.
- Alocar portas de aplicação.
- Escolher em qual EC2 uma aplicação deve rodar.
- Criar ou alterar registros no Route 53 no MVP.
- Fazer scheduling de workloads.
- Expor uma API pública.
- Manipular Docker de aplicações.
- Aceitar rotas de fonte não autenticada.
- Usar Caddy autosave como fonte primária da verdade.

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
  mode: "gateway"
  environment: "dev"
  role: "gateway"

platform:
  base_url: "https://platform.useclarus.app"
  token_file: "/etc/devex-agent/token"

caddy:
  admin_url: "http://127.0.0.1:2019"
  current_config_path: "/var/lib/devex-agent/gateway/current-caddy.json"
  previous_config_path: "/var/lib/devex-agent/gateway/previous-caddy.json"
  last_good_config_path: "/var/lib/devex-agent/gateway/last-good-caddy.json"
  load_timeout_seconds: 10
  route_validation_timeout_seconds: 3

reconcile:
  interval_seconds: 10

retry:
  max_attempts: 3
  initial_interval_seconds: 1
  max_interval_seconds: 10
  multiplier: 2.0
  jitter: true

state:
  dir: "/var/lib/devex-agent"

logging:
  level: "info"
  format: "json"
```

Regras:

- `agent.mode` deve ser `gateway`.
- `caddy.admin_url` deve apontar para `127.0.0.1` ou `localhost`.
- A Caddy Admin API não deve ser exposta publicamente.
- Configuração inválida deve falhar com `CONFIG_INVALID`.

---

## Registro do Gateway Agent

No primeiro boot, o Gateway Agent deve registrar-se na Platform API.

Endpoint:

```http
POST /api/agents/register
```

Payload exemplo:

```json
{
  "mode": "gateway",
  "environment": "dev",
  "role": "gateway",
  "hostname": "ip-10-0-1-10",
  "instance_id": "i-gateway123",
  "private_ip": "10.0.1.10",
  "public_ip": "54.233.10.20",
  "version": "0.1.0",
  "capabilities": {
    "gateway": true,
    "caddy_admin_url": "http://127.0.0.1:2019"
  }
}
```

Resposta:

```json
{
  "agent_id": "agent-dev-gateway-001",
  "status": "registered"
}
```

O `agent_id` deve ser persistido localmente em:

```text
/var/lib/devex-agent/agent.json
```

---

## Heartbeat

O Gateway Agent deve enviar heartbeat periódico.

Endpoint:

```http
POST /api/agents/{agent_id}/heartbeat
```

Payload exemplo:

```json
{
  "status": "online",
  "mode": "gateway",
  "environment": "dev",
  "role": "gateway",
  "version": "0.1.0",
  "caddy_status": "healthy",
  "routes_total": 12,
  "last_applied_desired_state_version": 43,
  "last_successful_desired_state_version": 43
}
```

O heartbeat deve continuar mesmo quando não houver alteração de rotas.

---

## Desired State de rotas

O Gateway Agent deve buscar o estado desejado de rotas na Platform API.

Endpoint:

```http
GET /api/agents/{agent_id}/desired-state
```

Resposta exemplo:

```json
{
  "version": 43,
  "environment": "dev",
  "type": "gateway_routes",
  "routes": [
    {
      "id": "route_001",
      "host": "billing-api.dev.useclarus.app",
      "path": "/",
      "upstream": "10.0.2.25:4102",
      "deployment_id": "dep_456",
      "health_check_path": "/health"
    },
    {
      "id": "route_002",
      "host": "orders-api.dev.useclarus.app",
      "path": "/",
      "upstream": "10.0.2.31:4103",
      "deployment_id": "dep_789",
      "health_check_path": "/health"
    }
  ]
}
```

O Gateway Agent deve aplicar o desired state apenas quando a versão remota for diferente da última versão aplicada com sucesso.

---

## Loop principal

Fluxo recomendado:

```text
1. Carregar configuração.
2. Registrar agent, se necessário.
3. Verificar Caddy Admin API.
4. Enviar heartbeat.
5. Buscar desired state.
6. Se versão nova:
   6.1 Validar desired state.
   6.2 Gerar caddy.json.
   6.3 Salvar previous/current config.
   6.4 Aplicar via /load.
   6.5 Validar rotas.
   6.6 Salvar last-good.
   6.7 Reportar sucesso.
7. Se falhar:
   7.1 Restaurar last-good, quando aplicável.
   7.2 Reportar falha.
8. Aguardar próximo ciclo.
```

---

## Geração do caddy.json

O Gateway Agent deve gerar a configuração completa do Caddy.

Exemplo básico:

```json
{
  "admin": {
    "listen": "0.0.0.0:2019"
  },
  "apps": {
    "http": {
      "servers": {
        "srv0": {
          "listen": [":80", ":443"],
          "routes": [
            {
              "match": [
                {
                  "host": ["billing-api.dev.useclarus.app"]
                }
              ],
              "handle": [
                {
                  "handler": "reverse_proxy",
                  "upstreams": [
                    {
                      "dial": "10.0.2.25:4102"
                    }
                  ]
                }
              ]
            }
          ]
        }
      }
    }
  }
}
```

A configuração deve ser gerada de forma determinística para facilitar diff, auditoria e testes.

---

## Uso do /load

O Gateway Agent deve aplicar a configuração com:

```http
POST http://127.0.0.1:2019/load
Content-Type: application/json
```

Não usar como fluxo principal:

```text
PATCH incremental de rotas
POST incremental em /config
edição manual de Caddyfile
docker restart caddy
```

Motivo:

```text
/load com configuração completa reduz drift e facilita rollback.
```

---

## Validação de hosts

O Gateway Agent deve validar hosts recebidos no desired state.

Exemplos válidos:

```text
billing-api.dev.useclarus.app
orders-api.stage.useclarus.app
api.useclarus.app
```

Exemplos inválidos:

```text
localhost
127.0.0.1
0.0.0.0
host com espaço
host com caracteres inválidos
domínios externos não autorizados
```

A política de domínios permitidos deve vir da Platform API ou configuração.

---

## Validação de upstreams

O Gateway Agent deve validar upstreams antes de gerar o `caddy.json`.

Permitido no MVP:

```text
IP privado da VPC + porta
```

Exemplos válidos:

```text
10.0.2.25:4102
10.0.3.31:4103
```

Rejeitar:

```text
169.254.169.254:80
127.0.0.1:22
0.0.0.0:80
8.8.8.8:80
domínios externos arbitrários
```

Bloquear `169.254.169.254` é obrigatório para evitar acesso indevido ao metadata service da AWS.

---

## Validação de rotas

Após aplicar `/load`, o Gateway Agent deve validar as rotas.

Para cada rota com `health_check_path`:

```text
GET http://127.0.0.1{health_check_path}
Host: {route.host}
```

Exemplo:

```bash
curl -f   -H "Host: billing-api.dev.useclarus.app"   http://127.0.0.1/health
```

Essa validação confirma:

```text
Caddy aceitou a configuração.
Host matching funciona.
Reverse proxy alcança upstream.
Aplicação responde ao health check.
```

---

## Last-good config

O Gateway Agent deve manter uma configuração last-good.

Arquivos:

```text
/var/lib/devex-agent/gateway/current-caddy.json
/var/lib/devex-agent/gateway/previous-caddy.json
/var/lib/devex-agent/gateway/last-good-caddy.json
```

Fluxo:

```text
1. Antes de aplicar nova config, salvar config atual como previous.
2. Salvar candidata como current.
3. Aplicar /load.
4. Validar rotas.
5. Se sucesso, promover current para last-good.
6. Se falha, reaplicar last-good.
```

---

## Rollback de rota

Se a nova configuração falhar:

```text
1. Não promover desired state como aplicado.
2. Restaurar last-good-caddy.json.
3. Reportar falha à Platform API.
4. Manter last_successful_desired_state_version inalterado.
```

Erro esperado:

```text
CADDY_ROUTE_VALIDATION_FAILED
```

ou:

```text
CADDY_LOAD_FAILED
```

---

## Autosave do Caddy

O Caddy pode salvar configuração em:

```text
/config/caddy/autosave.json
```

E iniciar com:

```bash
caddy run --resume
```

Mas o autosave deve ser usado apenas como fallback.

Fonte primária:

```text
DevEx Platform desired state
```

---

## Modo de emergência

Se não for possível buscar desired state nem usar last-good, o Gateway Agent pode aplicar uma configuração de emergência.

Resposta padrão:

```text
HTTP 503 — Caddy Gateway em modo de emergência.
```

Configuração exemplo:

```json
{
  "admin": {
    "listen": "0.0.0.0:2019"
  },
  "apps": {
    "http": {
      "servers": {
        "srv0": {
          "listen": [":80"],
          "routes": [
            {
              "handle": [
                {
                  "handler": "static_response",
                  "status_code": 503,
                  "body": "Caddy Gateway em modo de emergencia."
                }
              ]
            }
          ]
        }
      }
    }
  }
}
```

---

## Report de desired state

Após aplicar com sucesso:

```http
POST /api/agents/{agent_id}/desired-state/report
```

Payload:

```json
{
  "status": "applied",
  "desired_state_version": 43,
  "routes_total": 12,
  "validated_routes": 12,
  "failed_routes": 0
}
```

Em falha:

```json
{
  "status": "failed",
  "desired_state_version": 43,
  "error": {
    "code": "CADDY_ROUTE_VALIDATION_FAILED",
    "message": "Route billing-api.dev.useclarus.app did not pass health check"
  }
}
```

---

## Segurança

Regras obrigatórias:

```text
Não expor Caddy Admin API publicamente.
Não aceitar desired state de fonte não autenticada.
Validar hosts.
Validar upstreams.
Bloquear metadata service.
Não logar tokens.
Não logar headers Authorization.
Não aplicar configuração inválida.
```

A porta 2019 deve estar disponível apenas localmente.

Docker publish recomendado:

```yaml
127.0.0.1:2019:2019
```

---

## Observabilidade

Eventos importantes:

```text
gateway_agent_started
gateway_desired_state_fetched
gateway_desired_state_unchanged
caddy_config_generated
caddy_config_saved
caddy_load_started
caddy_load_succeeded
caddy_load_failed
route_validation_started
route_validation_succeeded
route_validation_failed
last_good_restored
last_good_restore_failed
gateway_report_sent
```

Campos recomendados:

```text
agent_id
environment
desired_state_version
route_host
upstream
routes_total
duration_ms
error_code
```

---

## Erros esperados

Códigos:

```text
DESIRED_STATE_FETCH_FAILED
CADDY_ADMIN_UNAVAILABLE
CADDY_CONFIG_GENERATION_FAILED
CADDY_CONFIG_INVALID
CADDY_LOAD_FAILED
CADDY_ROUTE_VALIDATION_FAILED
CADDY_LAST_GOOD_RESTORE_FAILED
INVALID_HOST
INVALID_UPSTREAM
STATE_STORE_FAILED
```

---

## Critérios de aceite

O Gateway Agent estará funcional para o MVP quando conseguir:

```text
1. Iniciar em modo gateway.
2. Registrar-se na Platform API.
3. Enviar heartbeat.
4. Buscar desired state de rotas.
5. Gerar caddy.json completo.
6. Validar hosts e upstreams.
7. Aplicar configuração via /load.
8. Validar rotas via Host header.
9. Salvar last-good-caddy.json.
10. Restaurar last-good em falha.
11. Reportar sucesso ou falha à Platform API.
12. Não expor Caddy Admin API publicamente.
```

---

## Regra final

O Gateway Agent aplica rotas.

Ele não executa aplicações.

Ele não decide scheduling.

Ele não é fonte da verdade.

Ele transforma desired state confiável em configuração Caddy ativa e validada.

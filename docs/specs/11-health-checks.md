# 11 — Health Checks

## Objetivo deste documento

Este documento define como o DevEx Agent deve executar e interpretar health checks para aplicações, workers, containers e rotas no Caddy Gateway.

Health checks são usados para decidir se:

- Um container iniciou corretamente.
- Uma aplicação está pronta para receber tráfego.
- Uma rota no Caddy está funcional.
- Um worker está operacional.
- Um deploy pode ser considerado saudável.
- Um rollback deve ser acionado.

Este documento deve ser lido junto com:

- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

Nenhuma nova versão deve receber tráfego antes de passar em health check.

Regra:

```text
Subiu container.
Validou localmente.
Atualizou Caddy.
Validou rota.
Só então marca deployment como healthy.
```

---

## Tipos de health check

Tipos suportados:

```text
http
tcp
container
worker
gateway_route
```

Para o MVP, o foco principal será:

```text
http
container
gateway_route
```

---

## Health check HTTP local

Usado para APIs e frontends executados pelo Runtime Agent.

Formato:

```text
GET http://127.0.0.1:{host_port}{path}
```

Exemplo:

```text
GET http://127.0.0.1:4102/health
```

Sucesso:

```text
HTTP 2xx
```

Opcionalmente aceitar HTTP 3xx apenas se configurado explicitamente.

Falha:

```text
timeout
connection refused
HTTP 4xx
HTTP 5xx
resposta inválida
```

---

## Configuração HTTP

Exemplo:

```yaml
health_check:
  type: "http"
  path: "/health"
  timeout_seconds: 2
  interval_seconds: 5
  retries: 6
  success_status_codes:
    - 200
    - 204
```

Para MVP, considerar sucesso:

```text
status code >= 200 e < 300
```

---

## Health check de container

Usado para validar se o container está rodando.

Critérios:

```text
Container existe.
Container está em running=true.
ExitCode é zero ou não finalizado.
Status não é exited/dead.
```

Docker inspect deve ser usado para obter:

```text
State.Running
State.Status
State.ExitCode
```

---

## Health check de worker

Workers normalmente não expõem HTTP.

Para MVP, health check de worker pode validar:

```text
Container está rodando.
Container não saiu com erro.
Restart count não está crescendo rapidamente.
```

Evoluções futuras:

```text
Worker envia heartbeat para Platform API.
Worker expõe endpoint interno.
Worker reporta consumo de fila.
Worker reporta lag da fila.
```

---

## Health check TCP

Valida se uma porta está aceitando conexão TCP.

Formato:

```text
tcp://127.0.0.1:{host_port}
```

Uso:

- Aplicações sem endpoint HTTP.
- Serviços internos.
- Teste rápido de disponibilidade de porta.

Para MVP, TCP é opcional.

---

## Health check via Gateway

Usado pelo Gateway Agent para validar se o Caddy está roteando corretamente.

Formato:

```bash
curl -f   -H "Host: billing-api.dev.useclarus.app"   http://127.0.0.1/health
```

O Gateway Agent deve implementar isso em Go usando HTTP client com header `Host`.

Valida:

```text
Caddy recebeu a rota.
Host match funcionou.
Reverse proxy alcançou upstream.
Aplicação respondeu.
```

---

## Health check público

Opcionalmente, a plataforma pode validar a URL pública:

```text
https://billing-api.dev.useclarus.app/health
```

Isso valida:

```text
DNS
TLS
Caddy
Rota
Aplicação
```

Para o MVP, esse teste pode ser opcional porque DNS e emissão de certificado podem levar algum tempo.

---

## Ordem dos health checks no deploy

Para API/frontend:

```text
1. Container health check.
2. HTTP local health check.
3. Gateway route health check.
4. Opcional: public HTTPS health check.
```

O deployment só deve ser marcado como healthy depois do Gateway route health check passar, quando `requires_route=true`.

Para worker:

```text
1. Container health check.
2. Worker health check.
3. Marcar healthy.
```

---

## Readiness versus liveness

O health check de deploy deve funcionar como readiness.

Ele responde:

```text
A aplicação está pronta para receber tráfego?
```

Não confundir com liveness, que responde:

```text
A aplicação ainda está viva?
```

No MVP, o mesmo endpoint pode ser usado para ambos, mas o significado operacional no deploy é readiness.

---

## Timeouts

Cada tentativa deve ter timeout.

Exemplo:

```yaml
health_check:
  timeout_seconds: 2
```

Se o timeout for atingido, a tentativa falha.

O timeout deve usar `context.Context`.

---

## Retries

O health check deve suportar múltiplas tentativas.

Exemplo:

```yaml
health_check:
  retries: 6
  interval_seconds: 5
```

Isso significa:

```text
Tentar até 6 vezes.
Aguardar 5 segundos entre tentativas.
```

Tempo total aproximado:

```text
retries * interval_seconds + timeouts
```

---

## Backoff

Para MVP, intervalo fixo é suficiente.

Evolução futura:

```text
exponential backoff com jitter
```

Exemplo:

```text
1s, 2s, 4s, 8s
```

---

## Resultado do health check

Modelo sugerido:

```json
{
  "status": "healthy",
  "type": "http",
  "target": "http://127.0.0.1:4102/health",
  "attempts": 3,
  "duration_ms": 1042,
  "status_code": 200
}
```

Falha:

```json
{
  "status": "unhealthy",
  "type": "http",
  "target": "http://127.0.0.1:4102/health",
  "attempts": 6,
  "duration_ms": 31000,
  "error": {
    "code": "HEALTH_CHECK_FAILED",
    "message": "Application did not return a successful response"
  }
}
```

---

## Códigos de erro

Códigos:

```text
HEALTH_CHECK_FAILED
HEALTH_CHECK_TIMEOUT
HEALTH_CHECK_CONNECTION_REFUSED
HEALTH_CHECK_INVALID_RESPONSE
HEALTH_CHECK_UNEXPECTED_STATUS
HEALTH_CHECK_CONTAINER_NOT_RUNNING
HEALTH_CHECK_GATEWAY_ROUTE_FAILED
```

---

## Health check e rollback

Se o health check local falhar:

```text
Não atualizar rota.
Remover container novo.
Liberar porta.
Reportar failed.
Manter versão antiga ativa.
```

Se o health check via Gateway falhar após aplicar Caddy:

```text
Restaurar rota anterior.
Reaplicar last-good Caddy config.
Manter versão antiga ativa.
Reportar failed ou rolled_back.
```

---

## Health check e draining

A versão antiga só deve entrar em draining depois que:

```text
Nova versão passou no health check local.
Caddy foi atualizado.
Rota passou no health check via Gateway.
```

Antes disso, a versão antiga deve permanecer active.

---

## Implementação em Go

Criar pacote:

```text
internal/health
```

Interface sugerida:

```go
type Checker interface {
    CheckHTTP(ctx context.Context, target HTTPCheckTarget) (*Result, error)
    CheckTCP(ctx context.Context, target TCPCheckTarget) (*Result, error)
    CheckContainer(ctx context.Context, info ContainerInfo) (*Result, error)
}
```

Modelo:

```go
type Result struct {
    Status      string
    Type        string
    Target      string
    Attempts    int
    Duration    time.Duration
    StatusCode  int
    ErrorCode   string
    ErrorMessage string
}
```

---

## HTTP client

O HTTP client deve:

- Usar timeout.
- Permitir header Host para Gateway checks.
- Não seguir redirects por padrão, salvo configuração.
- Fechar response body.
- Tratar status code adequadamente.

---

## Segurança

Health checks não devem:

```text
Enviar tokens sensíveis.
Logar headers sensíveis.
Acessar destinos arbitrários não validados.
Usar URLs externas arbitrárias vindas do payload sem validação.
```

O Gateway Agent deve validar hosts e upstreams antes de executar checks.

---

## Observabilidade

Logs relevantes:

```text
health_check_started
health_check_attempt_failed
health_check_succeeded
health_check_failed
gateway_route_health_check_failed
```

Campos:

```text
agent_id
deployment_id
application
container_name
health_check_type
target
attempt
duration_ms
status_code
error_code
```

---

## Critérios de aceite

Health checks estarão corretos quando:

```text
1. Runtime Agent validar container local antes de reportar sucesso.
2. APIs/frontends passarem por HTTP local health check.
3. Gateway Agent validar rota com Host header.
4. Workers puderem ser validados por container check.
5. Timeouts e retries funcionarem.
6. Falhas impedirem atualização de rota.
7. Falhas após update de rota acionarem rollback.
8. Resultado do health check for reportado de forma estruturada.
9. Logs permitirem diagnosticar falhas.
10. Nenhum secret for enviado ou logado indevidamente.
```

---

## Regra final

Health check é gate de segurança do deploy.

Sem health check bem-sucedido, não há promoção da nova versão.

Sem validação via Gateway, não há confirmação final de deploy healthy para aplicações roteadas.

# 08 — Integração com Caddy

## Objetivo deste documento

Este documento define como o **Gateway Agent** deve integrar com o **Caddy Gateway** para aplicar rotas HTTP/HTTPS dinamicamente, sem reiniciar o servidor e sem depender de alterações manuais em arquivos.

Este documento cobre:

- Papel do Caddy na arquitetura.
- Uso da Caddy Admin API.
- Geração de `caddy.json`.
- Aplicação via `/load`.
- Validação de rotas.
- Autosave e fallback.
- Segurança da Admin API.
- Estratégia de rollback.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/12-security.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Papel do Caddy

O Caddy atua como gateway HTTP/HTTPS da plataforma.

Responsabilidades:

- Receber tráfego público.
- Encerrar TLS.
- Gerar certificados HTTPS automaticamente.
- Roteiar requisições por host.
- Fazer reverse proxy para aplicações nas EC2s Runtime.
- Aplicar configuração dinamicamente pela Admin API.

O Caddy não é a fonte da verdade.

A fonte da verdade é a DevEx Platform.

---

## Modelo de roteamento

O Route 53 resolve domínios para o Caddy Gateway.

Exemplo:

```text
*.dev.useclarus.app -> IP público do Caddy Gateway
```

O Caddy decide o backend com base no host.

Exemplo:

```text
billing-api.dev.useclarus.app -> 10.0.2.25:4102
orders-api.dev.useclarus.app  -> 10.0.2.31:4103
```

---

## Admin API

O Gateway Agent deve usar a Caddy Admin API.

Endpoint local esperado:

```text
http://127.0.0.1:2019
```

A Admin API deve ser acessível somente localmente na EC2 Gateway.

Nunca expor publicamente a porta 2019.

---

## Configuração Docker recomendada

Exemplo:

```yaml
services:
  caddy:
    image: caddy:latest
    container_name: caddy
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
      - "127.0.0.1:2019:2019"
    volumes:
      - ./caddy-data:/data
      - ./caddy-config:/config
```

A porta `2019` fica acessível apenas no host local.

---

## Configuração admin no Caddy

Como a Admin API roda dentro do container, a configuração do Caddy deve escutar em:

```json
{
  "admin": {
    "listen": "0.0.0.0:2019"
  }
}
```

Combinado com o bind Docker:

```yaml
127.0.0.1:2019:2019
```

Isso permite:

```text
Gateway Agent no host -> 127.0.0.1:2019 -> Caddy Admin API no container
```

sem expor a Admin API publicamente.

---

## Estratégia principal de aplicação

O Gateway Agent deve aplicar a configuração completa do Caddy usando:

```http
POST /load
```

Não usar como estratégia principal:

```text
PATCH incremental por rota
POST incremental em /config/apps/http/servers/...
```

Motivos para usar `/load` com config completa:

- Evita drift.
- Facilita auditoria.
- Facilita rollback.
- Facilita geração declarativa.
- Facilita reconstrução após restart.
- Facilita versionamento da configuração.

---

## Desired state de rotas

O Gateway Agent recebe da Platform API um desired state.

Exemplo:

```json
{
  "version": 43,
  "routes": [
    {
      "host": "billing-api.dev.useclarus.app",
      "path": "/",
      "upstream": "10.0.2.25:4102",
      "health_check_path": "/health"
    },
    {
      "host": "orders-api.dev.useclarus.app",
      "path": "/",
      "upstream": "10.0.2.31:4103",
      "health_check_path": "/health"
    }
  ]
}
```

O Gateway Agent transforma esse desired state em `caddy.json`.

---

## Estrutura básica do caddy.json

Exemplo:

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
          "routes": []
        }
      }
    }
  }
}
```

---

## Exemplo de rota gerada

```json
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
```

---

## Exemplo completo

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
            },
            {
              "match": [
                {
                  "host": ["orders-api.dev.useclarus.app"]
                }
              ],
              "handle": [
                {
                  "handler": "reverse_proxy",
                  "upstreams": [
                    {
                      "dial": "10.0.2.31:4103"
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

---

## Aplicação da configuração

O Gateway Agent deve enviar:

```http
POST http://127.0.0.1:2019/load
Content-Type: application/json
```

Com o conteúdo completo do `caddy.json`.

Exemplo:

```bash
curl -X POST   http://127.0.0.1:2019/load   -H "Content-Type: application/json"   --data-binary @caddy.json
```

---

## Validação antes de aplicar

Antes de chamar `/load`, o Gateway Agent deve validar a configuração.

Opções:

```text
1. Gerar JSON válido.
2. Validar estrutura internamente.
3. Opcionalmente usar container/CLI do Caddy para validar.
```

Exemplo com CLI:

```bash
docker exec caddy caddy validate --config /etc/caddy/caddy.json
```

No MVP, se a configuração for gerada por código fortemente tipado, a validação estrutural interna pode ser suficiente, mas a validação via Caddy é recomendada para ambientes reais.

---

## Validação após aplicar

Após `/load`, o Gateway Agent deve validar se a rota responde.

Para cada rota relevante:

```bash
curl -f   -H "Host: billing-api.dev.useclarus.app"   http://127.0.0.1/health
```

Esse teste valida:

```text
Caddy recebeu a rota.
Host matching funciona.
Reverse proxy alcança o upstream.
Aplicação responde ao health check.
```

Para HTTPS público, um teste adicional pode ser feito:

```bash
curl -f https://billing-api.dev.useclarus.app/health
```

Mas no MVP, o teste local com header `Host` já é suficiente para validar roteamento interno.

---

## Autosave do Caddy

Quando uma configuração é aplicada via `/load`, o Caddy normalmente salva a configuração ativa em:

```text
/config/caddy/autosave.json
```

Esse arquivo pode ser usado com:

```bash
caddy run --resume
```

Porém:

```text
autosave.json não é fonte primária da verdade.
```

Fonte primária:

```text
DevEx Platform desired state
```

Autosave deve ser usado apenas como fallback operacional.

---

## Estratégia de boot

Fluxo recomendado para boot do Caddy Gateway:

```text
1. Gateway Agent inicia.
2. Gateway Agent tenta buscar desired state da Platform API.
3. Se conseguir, gera caddy.json e inicia/aplica Caddy.
4. Se falhar, tenta iniciar Caddy com --resume.
5. Se --resume falhar, usa emergency config.
```

Emergency config deve responder 503:

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

## Rollback de configuração

O Gateway Agent deve manter a última configuração boa conhecida.

Arquivos sugeridos:

```text
/var/lib/devex-agent/gateway/last-good-caddy.json
/var/lib/devex-agent/gateway/current-caddy.json
/var/lib/devex-agent/gateway/previous-caddy.json
```

Fluxo:

```text
1. Gerar nova configuração.
2. Salvar current candidate.
3. Aplicar via /load.
4. Validar rotas.
5. Se sucesso, promover para last-good.
6. Se falha, reaplicar last-good.
7. Reportar erro para Platform API.
```

---

## Ordenação de rotas

Rotas específicas devem vir antes de fallbacks.

Exemplo:

```text
billing-api.dev.useclarus.app
orders-api.dev.useclarus.app
fallback
```

Como a plataforma gera configuração por host específico, conflitos devem ser tratados na Platform API antes do desired state chegar ao Gateway Agent.

---

## Fallback

Pode haver uma rota fallback para HTTP simples:

```json
{
  "handle": [
    {
      "handler": "static_response",
      "status_code": 404,
      "body": "Aplicação não encontrada na DevEx Platform."
    }
  ]
}
```

Atenção:

```text
Fallback HTTPS para hosts desconhecidos pode falhar por ausência de certificado.
```

Por isso, o fallback é mais útil para HTTP ou para domínios cobertos por certificado wildcard.

---

## Certificados HTTPS

O Caddy pode emitir certificados automaticamente para hosts configurados.

Pré-condições:

```text
Domínio resolve para o Caddy Gateway.
Porta 80 acessível publicamente.
Porta 443 acessível publicamente.
Host está presente na configuração do Caddy.
```

Para wildcards, pode ser necessário DNS challenge, dependendo da estratégia de certificados.

Para MVP, recomenda-se emitir certificados por host específico configurado.

---

## Erros esperados

Códigos:

```text
CADDY_ADMIN_UNAVAILABLE
CADDY_CONFIG_GENERATION_FAILED
CADDY_CONFIG_INVALID
CADDY_LOAD_FAILED
CADDY_ROUTE_VALIDATION_FAILED
CADDY_LAST_GOOD_RESTORE_FAILED
DESIRED_STATE_FETCH_FAILED
```

Exemplo:

```json
{
  "code": "CADDY_LOAD_FAILED",
  "message": "Caddy Admin API returned non-2xx response during /load"
}
```

---

## Segurança

Regras:

```text
Não expor porta 2019 publicamente.
Usar 127.0.0.1:2019:2019 no Docker.
Não aceitar configuração de rota de fontes não confiáveis.
Validar hosts.
Validar upstreams.
Não permitir upstream público arbitrário sem política.
```

O Gateway Agent deve aplicar apenas desired state vindo da Platform API autenticada.

---

## Validação de hosts

O Gateway Agent deve validar hosts recebidos.

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
example.com não autorizado
host com caracteres inválidos
```

A política de domínio deve vir da Platform API, mas o Gateway Agent pode ter validações defensivas.

---

## Validação de upstreams

Upstreams devem apontar para rede privada ou destinos permitidos.

Exemplos válidos:

```text
10.0.2.25:4102
10.0.3.31:4103
```

Exemplos proibidos por padrão:

```text
0.0.0.0:80
127.0.0.1:22
169.254.169.254:80
domínios externos arbitrários
```

A validação deve evitar SSRF e roteamento indevido.

---

## Observabilidade

Eventos relevantes:

```text
gateway_desired_state_fetched
caddy_config_generated
caddy_config_validation_started
caddy_config_validation_succeeded
caddy_load_started
caddy_load_succeeded
caddy_load_failed
route_validation_started
route_validation_succeeded
route_validation_failed
last_good_restored
```

Campos de log:

```text
agent_id
desired_state_version
route_host
upstream
caddy_admin_url
error_code
```

---

## Critérios de aceite

A integração com Caddy estará correta quando:

```text
1. Gateway Agent conseguir buscar desired state.
2. Gateway Agent gerar caddy.json completo.
3. Gateway Agent aplicar configuração via /load.
4. A Caddy Admin API não estiver exposta publicamente.
5. Rotas forem validadas via Host header.
6. Falha de configuração restaurar last-good.
7. Erros forem reportados para a Platform API.
8. Autosave for tratado como fallback, não fonte primária.
9. Upstreams e hosts forem validados.
10. O Caddy rotear para IP privado + porta correta.
```

---

## Regra final

O Caddy é runtime de roteamento.

A Platform API é a fonte da verdade.

O Gateway Agent gera e aplica configuração completa.

Não depender de edição manual de Caddyfile.

Não depender de mutação incremental como fluxo principal.

Não expor Admin API publicamente.

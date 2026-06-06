# Gateway Agent — Fluxos de Operação

O Gateway Agent roda em instâncias EC2 responsáveis pelo Caddy Gateway. Ele busca o estado desejado de rotas na Platform API, gera e aplica o `caddy.json` completo, valida as rotas e reporta o resultado. Em caso de falha, restaura a última configuração conhecida como boa.

---

## 1. Sequência de boot

```mermaid
flowchart TD
    A([Início]) --> B[Descobrir hostname\nIP privado\ninstance ID]
    B --> C{Identidade\npersistida?}
    C -- Sim --> D[Carregar agent_id\ndo disk]
    C -- Não --> E[POST /api/agents/register\nPlataform API]
    E --> F[Salvar identidade em agent.json]
    F --> D
    D --> G[Carregar state.json\ncarregar last-good-caddy.json se existir]
    G --> H[Ping Admin API Caddy\nhttp://127.0.0.1:2019]
    H --> HE{Caddy\nacessível?}
    HE -- Não --> I[Log ERROR: CADDY_UNAVAILABLE\ntentar novamente no loop]
    HE -- Sim --> J[Iniciar goroutine\nheartbeatLoop]
    J --> K[reconcileLoop\nbloqueante até shutdown]
```

---

## 2. Loops concorrentes

O Gateway Agent tem dois loops. `reconcileLoop` bloqueia a goroutine principal; `heartbeatLoop` roda em goroutine separada.

```mermaid
flowchart LR
    subgraph goroutine1["goroutine — heartbeatLoop"]
        H1[POST /heartbeat\nPlataform API] --> H2[Aguardar 30s] --> H1
    end

    subgraph main["goroutine principal — reconcileLoop"]
        R1[reconcileOnce] --> R2[Aguardar intervalo\npadrão 10s] --> R1
    end
```

---

## 3. Loop de reconciliação — reconcileOnce

A cada ciclo, o agente busca o estado desejado da Platform, compara com a versão atual e aplica se houver mudança.

```mermaid
flowchart TD
    A([reconcileOnce]) --> B[GET /api/agents/id/desired-state\nPlataform API]
    B --> BE{Erro HTTP?}
    BE -- Sim --> BERR[Log WARN\nskip ciclo]

    BE -- Não --> C{Resposta vazia\nou sem rotas?}
    C -- Sim --> EMPTY[Log INFO: nenhuma rota\nskip ciclo]

    C -- Não --> D{Versão mudou?\ncompara state.lastSuccessfulVersion}
    D -- Não --> NOOP[Log DEBUG: estado\nainda atual\nskip ciclo]

    D -- Sim --> E[applyDesiredState\nroutes, version]
    E --> EE{Sucesso?}
    EE -- Sim --> OK[Atualizar lastSuccessfulVersion\nno state.json]
    EE -- Não --> FAIL[Erro já reportado\npor applyDesiredState]
```

---

## 4. Aplicação do estado desejado — applyDesiredState

Este é o fluxo central do Gateway Agent. Toda atualização de rotas passa por aqui, com pontos de rollback explícitos em cada etapa crítica.

```mermaid
flowchart TD
    A([applyDesiredState]) --> B[GenerateJSON\nroutes ordenadas deterministicamente]
    B --> BE{Erro na geração?}
    BE -- Sim --> F1[Report FAILED\nCADDY_LOAD_FAILED]

    BE -- Não --> C[Salvar previous-caddy.json\ncopiar current -> previous]
    C --> D[Salvar current-caddy.json\nescrever novo config gerado]

    D --> E[POST /load\nCaddy Admin API\nhttp://127.0.0.1:2019/load]
    E --> EE{HTTP 200?}

    EE -- Não --> F2[Log ERROR: /load falhou\nrestoreLastGood]
    F2 --> F2R{Restore\nbem-sucedido?}
    F2R -- Sim --> F2OK[Log INFO: config restaurada]
    F2R -- Não --> F2FAIL[Log ERROR: restore falhou\nCaddy pode estar com config inválida]
    F2OK --> F1B[Report FAILED\nCADDY_LOAD_FAILED]
    F2FAIL --> F1B

    EE -- Sim --> G[validateRoutes\nverificar cada rota HTTP]
    G --> GE{Todas as rotas\nválidas?}

    GE -- Não --> F3[restoreLastGood\nreaplicar última config boa]
    F3 --> F3R{Restore\nbem-sucedido?}
    F3R -- Sim --> F3OK[Log INFO: rollback aplicado\ntráfego restaurado]
    F3R -- Não --> F3FAIL[Log ERROR: rollback falhou]
    F3OK --> F1C[Report FAILED\nROUTE_VALIDATION_FAILED]
    F3FAIL --> F1C

    GE -- Sim --> H[Promover current como last-good\ncopiar current -> last-good-caddy.json]
    H --> I[lastSuccessfulVersion = version]
    I --> J[Report SUCCESS\nPlataform API]
```

---

## 5. Validação de rotas — validateRoutes

Após cada `/load` bem-sucedido, o agente verifica que o Caddy está realmente servindo as rotas esperadas.

```mermaid
sequenceDiagram
    participant GA as Gateway Agent
    participant Caddy as Caddy (local)

    loop Para cada rota configurada
        GA->>Caddy: GET http://127.0.0.1:80{health_check_path}\nHeader: Host: {hostname}
        Caddy-->>GA: 200 OK | outro status

        alt Status 200
            GA->>GA: Marcar rota como válida
        else Status != 200
            GA->>GA: Marcar rota como inválida\nparar validação
        end
    end

    alt Todas válidas
        GA->>GA: Retornar nil (sucesso)
    else Alguma inválida
        GA->>GA: Retornar ROUTE_VALIDATION_FAILED\napplyDesiredState chama restoreLastGood
    end
```

---

## 6. Rollback — restoreLastGood

O rollback é ativado automaticamente quando `/load` falha ou quando a validação de rotas detecta problemas. Ele reaplicar a última configuração que foi validada com sucesso.

```mermaid
flowchart TD
    A([restoreLastGood]) --> B{last-good-caddy.json\nexiste?}
    B -- Não --> C[Log WARN: nenhuma config\nboa disponível\nnão é possível restaurar]

    B -- Sim --> D[Ler last-good-caddy.json\ndo disco]
    D --> E[POST /load\nCaddy Admin API\ncom config anterior]
    E --> EE{HTTP 200?}

    EE -- Sim --> F[Log INFO: Caddy restaurado\npara última config boa]
    EE -- Não --> G[Log ERROR: falha crítica\nCaddy pode estar com\nconfiguração inválida ou parado]
```

---

## 7. Geração do caddy.json — GenerateJSON

O gerador produz sempre o arquivo completo e determinístico. Não há mutações parciais.

```mermaid
flowchart TD
    A([GenerateJSON - routes]) --> B[Ordenar rotas\npor hostname\nordem alfabética]
    B --> C[Para cada rota:\ngerar match por host]
    C --> D[Adicionar reverse_proxy handler\nupstream: private_ip:host_port]
    D --> E[Compor estrutura completa\napps.http.servers.devex.routes]
    E --> F[Serializar para JSON]
    F --> G[Retornar caddy.json completo\npronto para POST /load]
```

Exemplo de rota gerada para `billing-api.prod.example.com → 10.0.2.25:4102`:

```json
{
  "match": [{ "host": ["billing-api.prod.example.com"] }],
  "handle": [{
    "handler": "reverse_proxy",
    "upstreams": [{ "dial": "10.0.2.25:4102" }]
  }]
}
```

---

## 8. Gestão de arquivos de configuração

O Gateway Agent mantém três arquivos de configuração Caddy para suportar rollback:

```mermaid
flowchart LR
    subgraph disco["Disco — /var/lib/devex-agent/gateway/"]
        P[previous-caddy.json\npenúltima config aplicada]
        C[current-caddy.json\nconfig em uso agora]
        G[last-good-caddy.json\núltima config validada com sucesso]
    end

    APPLY([applyDesiredState]) -->|1. copia current → previous| P
    APPLY -->|2. escreve novo config| C
    APPLY -->|3. POST /load| CADDY[(Caddy\nruntime)]
    APPLY -->|4. valida rotas| CADDY
    APPLY -->|5. se OK: copia current → last-good| G

    ROLLBACK([restoreLastGood]) -->|lê last-good e reenvia| CADDY
```

| Arquivo | Quando é atualizado | Para que serve |
|---|---|---|
| `current-caddy.json` | Toda vez que um novo config é gerado | Referência do que foi enviado ao Caddy |
| `previous-caddy.json` | Antes de sobrescrever o current | Debug e auditoria |
| `last-good-caddy.json` | Somente após validação bem-sucedida | Fonte de rollback |

---

## 9. Fluxo completo — novo deploy de aplicação

Sequência completa desde o deploy de uma nova versão até o Caddy rotear o tráfego, mostrando a interação entre Runtime Agent, Platform e Gateway Agent.

```mermaid
sequenceDiagram
    participant Dev as Desenvolvedor
    participant P as Platform API
    participant RA as Runtime Agent\n(EC2 workload)
    participant GA as Gateway Agent\n(EC2 gateway)
    participant Caddy as Caddy

    Dev->>P: Deploy billing-api v42

    P->>RA: DEPLOY_APPLICATION\nbilling-api-dev-v42
    RA->>RA: pull imagem\naloca porta 4102\nstart container\nhealth check

    RA->>P: Report SUCCESS\n{private_ip: 10.0.2.25, host_port: 4102}

    P->>P: Atualizar desired state\nbilling-api.prod.example.com → 10.0.2.25:4102

    GA->>P: GET /desired-state
    P-->>GA: routes v43\nbilling-api.prod.example.com → 10.0.2.25:4102

    GA->>GA: GenerateJSON(routes)
    GA->>Caddy: POST /load (caddy.json completo)
    Caddy-->>GA: 200 OK

    GA->>Caddy: GET /health (Host: billing-api.prod.example.com)
    Caddy-->>GA: 200 OK (rota válida)

    GA->>GA: Promover config como last-good
    GA->>P: Report SUCCESS\nversão v43 aplicada

    P->>RA: MARK_DRAINING\nbilling-api-dev-v41
    RA->>RA: Marcar v41 como draining\naguardar grace period
    RA->>RA: Stop + remove container v41\nliberar porta 4101
```

---

## 10. Integração com o Caddy

O Gateway Agent não requer que o Caddy rode em Docker. Ele se comunica exclusivamente pela Admin API HTTP local.

| Aspecto | Detalhe |
|---|---|
| Endpoint Admin API | `http://127.0.0.1:2019` (nunca exposto publicamente) |
| Operação de atualização | `POST /load` com `caddy.json` completo |
| Operação de health | `GET /config/` para ping |
| Validação de rota | `GET http://127.0.0.1:80{path}` com `Host` header |
| Restart do Caddy | Não necessário; `/load` é atômico |
| Rollback | Re-envia `last-good-caddy.json` via `/load` |

A Admin API deve estar configurada para ouvir em `0.0.0.0:2019` (dentro do container Caddy ou do processo), mas o Security Group deve bloquear a porta 2019 para acesso externo. O agente acessa sempre via `127.0.0.1`.

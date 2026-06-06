# Runtime Agent — Fluxos de Operação

O Runtime Agent roda em instâncias EC2 responsáveis por executar workloads de aplicação. Ele faz polling na Platform API, reivindica comandos atomicamente, executa operações Docker e reporta resultados.

---

## 1. Sequência de boot

Ao iniciar, o agente descobre as informações do host, garante que está registrado na Platform, carrega o estado local e reconcilia com o Docker antes de entrar nos loops principais.

```mermaid
flowchart TD
    A([Início]) --> B[Descobrir hostname\nIP privado\ninstance ID]
    B --> C{Identidade\npersistida?}
    C -- Sim --> D[Carregar agent_id do disco]
    C -- Não --> E[POST /api/agents/register\nPlataform API]
    E --> F[Salvar identidade em agent.json]
    F --> D
    D --> G[Carregar state.json]
    G --> H[docker ps -a\nlistar containers gerenciados]
    H --> I[reconcilePorts\nalinhar ports.json com Docker]
    I --> J[reconcileDeployments\ndetectar inconsistências]
    J --> K[Iniciar goroutine\nheartbeatLoop]
    K --> L[Iniciar goroutine\ndrainCleanupLoop]
    L --> M[commandPollLoop\nbloqueante até shutdown]
```

---

## 2. Loops concorrentes

Após o boot, três loops rodam em paralelo. `commandPollLoop` bloqueia a goroutine principal; os outros dois rodam em goroutines separadas.

```mermaid
flowchart LR
    subgraph goroutine1["goroutine — heartbeatLoop"]
        H1[Enviar heartbeat] --> H2[Aguardar 30s] --> H1
    end

    subgraph goroutine2["goroutine — drainCleanupLoop"]
        D1[Verificar deployments\nem draining] --> D2[Grace period\nexpirou?]
        D2 -- Não --> D3[Aguardar 30s] --> D1
        D2 -- Sim --> D4[Stop container\nRemove container\nRelease port\nAtualizar state] --> D3
    end

    subgraph main["goroutine principal — commandPollLoop"]
        C1[Buscar comandos\npendentes] --> C2[Processar\ncomandos] --> C3[Aguardar intervalo\n+ jitter] --> C1
    end
```

---

## 3. Ciclo de processamento de comandos

Para cada ciclo do poll loop, o agente busca, reivindica e executa um comando por vez, garantindo a transição atômica `pending → claimed → running`.

```mermaid
sequenceDiagram
    participant A as Runtime Agent
    participant P as Platform API

    loop A cada intervalo + jitter
        A->>P: GET /commands/pending
        P-->>A: [ {id, type, payload} ]

        loop Para cada comando
            A->>P: POST /commands/{id}/claim
            P-->>A: 200 claimed | 409 conflict

            alt Claim aceito
                A->>P: POST /commands/{id}/start
                A->>A: executeCommand(type, payload)
                A->>P: POST /commands/{id}/report\n{status: succeeded|failed}
            else Claim rejeitado
                A->>A: Ignorar comando\n(outro agente reivindicou)
            end
        end
    end
```

---

## 4. Fluxo de deploy — DEPLOY_APPLICATION

O fluxo de deploy é o mais crítico. Cada etapa tem rollback explícito em caso de falha para garantir que nenhum recurso fique alocado sem container ativo.

```mermaid
flowchart TD
    START([DEPLOY_APPLICATION recebido]) --> IDEM{Deployment já\nativo no state?}
    IDEM -- Sim --> IDOK[Report success\nidempotente]

    IDEM -- Não --> S1[Salvar estado: starting\nno state.json]
    S1 --> S2[docker pull imagem]
    S2 --> S2E{Erro?}
    S2E -- Sim --> F1[Remover do state\nReport FAILED\nIMAGE_PULL_FAILED]
    S2E -- Não --> S3[Alocar porta\nno PortManager]

    S3 --> S3E{Erro?}
    S3E -- Sim --> F2[Remover do state\nReport FAILED\nPORT_ALLOCATION_FAILED]
    S3E -- Não --> S4[docker run\nnome versioned\nport binding\nlabels devex.*]

    S4 --> S4E{Erro?}
    S4E -- Sim --> F3[Liberar porta\nRemover do state\nReport FAILED\nCONTAINER_START_FAILED]
    S4E -- Não --> S5[Salvar estado:\nchecking_health]

    S5 --> S6{health_check_path\ndefinido?}
    S6 -- Sim --> S7[HTTP GET\nhttp://127.0.0.1:port/path\ncom retry e backoff]
    S6 -- Não --> S8[Verificar container\nrunning via inspect]

    S7 --> HCE{Saudável?}
    S8 --> HCE

    HCE -- Não --> F4[Stop container\nRemove container\nLiberar porta\nRemover do state\nReport FAILED\nHEALTH_CHECK_FAILED]
    HCE -- Sim --> S9[MarkActive\nno PortManager]

    S9 --> S10[Salvar estado: active]
    S10 --> S11["Report SUCCESS\n{private_ip, host_port,\ncontainer_name, requires_route}"]
```

---

## 5. Fluxo blue/green local

Quando uma aplicação já tem uma versão rodando e recebe um novo deploy, a versão anterior é mantida até a nova ser validada.

```mermaid
sequenceDiagram
    participant P as Platform API
    participant A as Runtime Agent
    participant D as Docker

    Note over A,D: v41 rodando em porta 4101

    P->>A: DEPLOY_APPLICATION\nbilling-api-dev-v42, porta automática

    A->>D: docker pull nova-imagem
    A->>A: Alocar porta 4102
    A->>D: docker run billing-api-dev-v42 -p 4102:8080
    A->>A: Health check 127.0.0.1:4102/health

    alt Health check passou
        A->>P: Report SUCCESS\n{host_port: 4102, private_ip: 10.0.2.25}
        Note over P: Platform atualiza desired state\nde rotas (nova versão do gateway)
        P->>A: MARK_DRAINING\n{deployment_id: v41}
        A->>A: Marcar v41 como draining\nRegistrar DrainingStartedAt
        Note over A: drainCleanupLoop aguarda\ngrace period
        A->>D: docker stop billing-api-dev-v41
        A->>D: docker rm billing-api-dev-v41
        A->>A: Liberar porta 4101
    else Health check falhou
        A->>D: docker stop billing-api-dev-v42
        A->>D: docker rm billing-api-dev-v42
        A->>A: Liberar porta 4102
        A->>P: Report FAILED\nHEALTH_CHECK_FAILED
        Note over D: v41 continua rodando\nsem interrupção
    end
```

---

## 6. Máquina de estados — Deployment

```mermaid
stateDiagram-v2
    [*] --> starting : DEPLOY_APPLICATION recebido

    starting --> checking_health : container iniciado
    starting --> failed : pull / port / start falhou

    checking_health --> active : health check passou
    checking_health --> failed : health check falhou\ncontainer removido

    active --> draining : MARK_DRAINING recebido\n(Platform confirma rota atualizada)
    active --> inconsistent : reconcileDeployments\ncontainer ausente ou parado

    draining --> removed : grace period expirou\ncontainer parado e removido

    failed --> [*]
    removed --> [*]
    inconsistent --> [*]
```

---

## 7. Máquina de estados — Porta

```mermaid
stateDiagram-v2
    [*] --> available : porta na faixa configurada

    available --> reserved : Allocate()\nPortManager reserva antes do docker run

    reserved --> active : MarkActive()\napós health check passar

    reserved --> available : deploy falhou\nRelease() chamado no rollback

    active --> draining : MarkDraining()\napós MARK_DRAINING

    draining --> released : drainCleanupLoop\nRemove container + Release()

    active --> released : Release()\nSTOP ou REMOVE direto

    released --> available : porta retorna ao pool
```

---

## 8. Reconciliação no startup

Ao iniciar, o agente compara o state.json com os containers reais no Docker para detectar inconsistências ocorridas enquanto o agente estava offline.

```mermaid
flowchart TD
    A[Listar containers Docker\ndevex.managed=true] --> B[reconcilePorts]
    B --> C{Container existe\npara a porta?}
    C -- Não --> D[Marcar porta como\nreleased no ports.json]
    C -- Sim --> E[Manter porta como está]

    A --> F[reconcileDeployments]
    F --> G{Para cada deployment\nnão terminal no state}
    G --> H{Container\nexiste no Docker?}

    H -- Não e status esperava running --> I[Marcar deployment\ncomo inconsistent]
    H -- Sim mas stopped\ne status=active --> I
    H -- Sim e running --> J[Manter status]
    H -- Não e status=draining --> K[Manter draining\ncontainer pode ter sido parado\naguardando cleanup]

    F --> L{Container no Docker\nsem entrada no state?}
    L -- Sim --> M[Log WARN: orphaned container\nnão modifica state]
```

---

## 9. Comandos suportados

| Comando | Descrição |
|---|---|
| `DEPLOY_APPLICATION` | Pull + start + health check + report endpoint |
| `STOP_APPLICATION` | Para o container, marca porta como draining |
| `REMOVE_DEPLOYMENT` | Para + remove container, libera porta, limpa state |
| `CLEANUP_DRAINING` | Força limpeza imediata de um deployment em draining |
| `MARK_DRAINING` | Sinaliza que a rota foi atualizada; inicia grace period |
| `RECONCILE` | Força reconciliação de portas, deployments ou ambos |

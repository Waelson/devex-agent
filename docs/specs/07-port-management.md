# 07 — Gestão de Portas

## Objetivo deste documento

Este documento define como o **Runtime Agent** deve gerenciar portas do host para containers Docker executados nas instâncias EC2 Runtime.

A gestão de portas é uma responsabilidade crítica porque, nesta arquitetura, o Caddy Gateway roteia tráfego para aplicações usando:

```text
IP privado da EC2 Runtime + porta publicada no host
```

Exemplo:

```text
billing-api.dev.useclarus.app -> 10.0.2.25:4102
```

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/06-deployment-flow.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/10-local-state.md`
- `docs/specs/11-health-checks.md`
- `docs/specs/14-error-handling-and-retry.md`

---

## Princípio central

O desenvolvedor não escolhe a porta do host.

A aplicação não define uma porta fixa no host.

A DevEx Platform e o Runtime Agent gerenciam portas automaticamente.

Regra:

```text
A porta do container é propriedade da aplicação.
A porta do host é propriedade do Runtime Agent.
A rota pública é propriedade da DevEx Platform.
```

Exemplo:

```text
Aplicação escuta internamente em 3000.
Runtime Agent publica em 4102.
Caddy aponta para 10.0.2.25:4102.
```

---

## Por que gerenciar portas

Como o Caddy Gateway pode estar em uma EC2 diferente da aplicação, o container precisa ser acessível pela rede privada da VPC.

Para isso, o Runtime Agent publica a porta do container em uma porta do host.

Exemplo:

```bash
docker run -d   --name billing-api-dev-v42   -p 4102:3000   ghcr.io/useclarus/billing-api:v42
```

Nesse exemplo:

```text
3000 = porta interna do container
4102 = porta do host EC2
```

O Caddy acessa:

```text
10.0.2.25:4102
```

---

## Modelo recomendado

Cada EC2 Runtime deve possuir uma faixa de portas reservada para containers gerenciados pela DevEx Platform.

Exemplo para EC2 de APIs:

```yaml
runtime:
  max_active_containers: 10

ports:
  from: 4100
  to: 4114
```

Esse modelo permite:

```text
10 containers ativos
até 5 containers temporários/draining
```

A faixa total possui 15 portas, mas apenas 10 deployments devem ficar ativos ao mesmo tempo.

---

## Exemplo de alocação

```text
4100 -> billing-api-dev-v41       active
4101 -> orders-api-dev-v10        active
4102 -> users-api-dev-v5          active
4103 -> contracts-api-dev-v8      active
4104 -> reports-api-dev-v3        active
4105 -> checkout-api-dev-v12      active
4106 -> payments-api-dev-v6       active
4107 -> search-api-dev-v2         active
4108 -> admin-api-dev-v4          active
4109 -> notifications-api-dev-v1  active

4110 -> billing-api-dev-v42       deploying
4111 -> orders-api-dev-v11        draining
4112 -> available
4113 -> available
4114 -> available
```

---

## Estados de uma porta

Uma porta deve possuir um estado explícito.

Estados suportados:

```text
available
reserved
active
draining
failed
released
unmanaged
```

---

## available

A porta está livre e pode ser alocada.

---

## reserved

A porta foi reservada pelo Runtime Agent para um deploy, mas o container ainda não foi iniciado ou ainda não passou no health check.

Uso:

```text
Evitar que outro deploy use a mesma porta durante uma operação em andamento.
```

---

## active

A porta está em uso por um deployment ativo.

Exemplo:

```text
billing-api-dev-v42 -> 4102 active
```

---

## draining

A porta ainda está em uso por uma versão antiga que foi substituída, mas que permanece temporariamente disponível para rollback ou encerramento gracioso.

Exemplo:

```text
billing-api-dev-v41 -> 4101 draining
```

---

## failed

A porta foi usada em uma tentativa de deploy que falhou.

O Runtime Agent deve limpar o recurso e liberar a porta.

---

## released

A porta foi liberada após remoção do container.

Esse estado pode ser transitório antes de voltar para `available`.

---

## unmanaged

A porta está em uso no host, mas não pertence a um container gerenciado pelo DevEx Agent.

Exemplo:

```text
Porta usada por processo manual.
Container sem label devex.managed=true.
Serviço externo ao agente.
```

O Runtime Agent não deve usar portas `unmanaged`.

---

## Arquivo de estado de portas

O Runtime Agent deve persistir o estado de portas localmente.

Arquivo sugerido:

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
      "application": "billing-api",
      "environment": "dev",
      "deployment_id": "dep_456",
      "container_name": "billing-api-dev-v42",
      "container_internal_port": 3000,
      "allocated_at": "2026-06-05T18:00:00Z"
    },
    "4101": {
      "status": "draining",
      "application": "billing-api",
      "environment": "dev",
      "deployment_id": "dep_455",
      "container_name": "billing-api-dev-v41",
      "container_internal_port": 3000,
      "allocated_at": "2026-06-05T17:00:00Z",
      "draining_started_at": "2026-06-05T18:05:00Z"
    }
  }
}
```

---

## Fluxo de alocação

Fluxo esperado:

```text
1. Obter lock local de portas.
2. Carregar ports.json.
3. Reconciliar ports.json com Docker real.
4. Verificar limite de containers ativos.
5. Encontrar porta available.
6. Marcar porta como reserved.
7. Persistir ports.json.
8. Liberar lock.
9. Iniciar container com a porta reservada.
10. Executar health check.
11. Se sucesso, marcar porta como active.
12. Se falha, remover container e liberar porta.
```

---

## Lock de portas

A alocação de portas deve ser protegida por lock.

Arquivo sugerido:

```text
/var/lib/devex-agent/locks/ports.lock
```

Objetivo:

```text
Evitar que duas operações simultâneas reservem a mesma porta.
```

Para o MVP, se o agent processar apenas um comando mutável por vez, um mutex interno pode ser suficiente.

Mesmo assim, a estrutura deve permitir evolução para lock de arquivo.

---

## Verificação de porta livre

Antes de alocar uma porta, o Runtime Agent deve verificar:

```text
A porta não está alocada em ports.json.
A porta não aparece em docker ps/docker inspect.
A porta não está em uso no sistema operacional.
```

Verificações possíveis:

```bash
docker ps
docker inspect
ss -ltn
```

Em Go, pode tentar abrir um socket para validar disponibilidade, mas deve tomar cuidado com race condition. O estado persistido e o Docker real devem ser as fontes principais para o MVP.

---

## Reconciliação com Docker

O Runtime Agent deve reconciliar periodicamente:

```text
ports.json
state.json
docker ps
docker inspect
```

### Caso 1 — ports.json aponta para container inexistente

Exemplo:

```text
ports.json:
4102 -> billing-api-dev-v42 active

docker ps:
billing-api-dev-v42 não existe
```

Ação:

```text
Marcar porta como released ou available.
Reportar evento de inconsistência.
```

### Caso 2 — Docker possui container gerenciado sem porta no state

Exemplo:

```text
docker ps:
container com label devex.managed=true

ports.json:
não conhece a porta
```

Ação:

```text
Importar para estado local, se seguro.
Ou marcar como inconsistent e reportar.
```

### Caso 3 — Porta em uso por processo externo

Ação:

```text
Marcar porta como unmanaged.
Não alocar.
Reportar evento.
```

---

## Limite de containers ativos

A configuração pode definir:

```yaml
runtime:
  max_active_containers: 10
```

Este limite se aplica a deployments em estado `active`.

Deployments em estados temporários podem existir acima desse limite se houver portas disponíveis.

Estados temporários:

```text
reserved
deploying
draining
failed
```

Recomendação para MVP:

```text
max_active_containers = 10
port_range = 15 portas
```

Assim, o agent pode manter algumas versões temporárias durante blue/green e draining.

---

## Blue/green e portas

Durante atualização de imagem, duas versões podem rodar em paralelo:

```text
billing-api-dev-v41 -> 4101 active
billing-api-dev-v42 -> 4102 reserved/deploying
```

Após validação e troca de rota:

```text
billing-api-dev-v41 -> 4101 draining
billing-api-dev-v42 -> 4102 active
```

Depois da janela de draining:

```text
4101 -> available
4102 -> active
```

---

## Liberação de porta

Uma porta só pode ser liberada quando:

```text
O container associado foi parado/removido.
A rota não aponta mais para essa porta.
O deployment não está active.
O período de draining terminou, quando aplicável.
```

Fluxo:

```text
1. Parar container.
2. Remover container.
3. Confirmar remoção via Docker inspect.
4. Marcar porta como released.
5. Persistir estado.
6. Marcar como available.
```

---

## Portas para workers

Workers normalmente não precisam de porta publicada.

Para workers:

```text
requires_route = false
```

Se o worker não expõe health HTTP, não há necessidade de alocar host port.

Health check pode ser baseado em:

```text
container running
exit code
heartbeat
logs
fila consumida
```

Se o worker expuser endpoint administrativo, a porta deve ser alocada pelo Port Manager da mesma forma.

---

## Portas e Security Group

A faixa de portas da EC2 Runtime deve ser liberada apenas para o Security Group da EC2 Gateway.

Exemplo:

```text
EC2 Runtime inbound:
TCP 4100-4114
Source: sg-caddy-gateway
```

Não expor essa faixa para:

```text
0.0.0.0/0
```

O público deve acessar somente:

```text
Caddy Gateway :80/:443
```

---

## Regras de nomeação

O Runtime Agent deve registrar a associação entre:

```text
application
deployment_id
container_name
host_port
container_internal_port
```

Container names devem ser versionados:

```text
billing-api-dev-v42
```

Evitar:

```text
billing-api
```

---

## Erros esperados

Códigos relacionados a portas:

```text
PORT_ALLOCATION_FAILED
PORT_RANGE_EXHAUSTED
PORT_ALREADY_RESERVED
PORT_ALREADY_IN_USE
PORT_STATE_INCONSISTENT
PORT_RELEASE_FAILED
PORT_LOCK_FAILED
```

Exemplo de erro:

```json
{
  "code": "PORT_RANGE_EXHAUSTED",
  "message": "No available ports in range 4100-4114"
}
```

---

## Idempotência

A alocação deve ser idempotente por `deployment_id`.

Se o mesmo deployment já possui porta reservada ou active, o agent não deve alocar outra porta desnecessariamente.

Regra:

```text
deployment_id existente -> reutilizar estado existente se consistente
deployment_id novo -> alocar nova porta
```

---

## Critérios de aceite

A gestão de portas estará correta quando:

```text
1. O agent alocar portas automaticamente.
2. O desenvolvedor não precisar escolher portas.
3. Duas aplicações não receberem a mesma porta.
4. Uma atualização blue/green usar porta diferente.
5. Portas antigas forem liberadas após draining.
6. Portas em uso por recursos externos forem marcadas como unmanaged.
7. O estado local for reconciliado com Docker.
8. O limite de containers ativos for respeitado.
9. Erros de porta forem reportados de forma estruturada.
10. O Caddy receber o IP privado + porta correta para roteamento.
```

---

## Regra final

Portas são detalhes de runtime.

A aplicação declara apenas sua porta interna.

O Runtime Agent aloca a porta do host.

A Platform API registra o endpoint ativo.

O Gateway Agent atualiza o Caddy.

O desenvolvedor não gerencia portas.

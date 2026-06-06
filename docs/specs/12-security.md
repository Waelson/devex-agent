# 12 — Segurança

## Objetivo deste documento

Este documento define os requisitos de segurança para o DevEx Agent, Runtime Agent, Gateway Agent, integração com Docker, Caddy, Platform API e ambiente AWS/EC2.

O DevEx Agent é um componente sensível porque pode:

- Executar containers.
- Manipular Docker.
- Alocar portas.
- Atualizar rotas no Caddy.
- Expor aplicações.
- Acessar a Platform API.
- Ler arquivos de configuração.
- Operar em hosts EC2.

Por isso, a segurança deve ser tratada como requisito central.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/04-platform-api-contracts.md`
- `docs/specs/07-port-management.md`
- `docs/specs/08-caddy-integration.md`
- `docs/specs/09-docker-runtime.md`
- `docs/specs/10-local-state.md`

---

## Princípios de segurança

Princípios principais:

```text
Não expor agent publicamente.
Não expor Caddy Admin API publicamente.
Não logar secrets.
Validar payloads.
Manipular apenas recursos gerenciados.
Usar menor privilégio possível.
Preferir comunicação outbound.
Restringir tráfego por Security Group.
Tratar Docker como superfície privilegiada.
```

---

## Modelo de ameaça

A arquitetura deve considerar riscos como:

- Execução indevida de containers.
- Manipulação de containers não gerenciados.
- Exposição pública de portas internas.
- Exposição da Caddy Admin API.
- Roteamento para upstreams maliciosos.
- Vazamento de tokens em logs.
- Uso indevido do Docker socket.
- Comando forjado contra o agent.
- Payload malformado vindo da Platform API.
- SSRF via configuração de upstream.
- Acesso indevido ao metadata service da AWS.
- Perda ou corrupção de estado local.

---

## Comunicação Agent -> Platform API

O agent deve se comunicar com a Platform API usando HTTPS.

Modelo recomendado:

```text
Agent -> Platform API
```

Evitar no MVP:

```text
Platform API -> Agent
```

Motivos:

- Não exige abrir porta pública no agent.
- Reduz superfície de ataque.
- Funciona melhor em redes privadas.
- Simplifica firewall.
- Simplifica autenticação inicial.

---

## Autenticação do agent

O agent deve autenticar suas chamadas à Platform API.

Para o MVP, usar token lido de arquivo.

Arquivo sugerido:

```text
/etc/devex-agent/token
```

Permissões:

```bash
chmod 600 /etc/devex-agent/token
```

O token deve ser enviado como header:

```http
Authorization: Bearer <token>
```

O token nunca deve ser logado.

---

## Rotação de token

O MVP pode começar com token estático.

Evoluções futuras:

```text
Token de curta duração.
Rotação automática.
mTLS.
Assinatura com identidade da instância.
Integração com IAM Role.
```

---

## Configuração local

Arquivo de configuração:

```text
/etc/devex-agent/config.yaml
```

Permissões recomendadas:

```bash
chmod 600 /etc/devex-agent/config.yaml
```

O arquivo de configuração não deve conter secrets sensíveis quando possível.

Referenciar secrets por arquivos ou mecanismos externos.

---

## Estado local

Diretório:

```text
/var/lib/devex-agent
```

Permissões:

```bash
chmod 700 /var/lib/devex-agent
```

O estado local não deve armazenar:

```text
Tokens
Senhas
AWS credentials
Docker registry credentials
Authorization headers
Variáveis de ambiente sensíveis
```

---

## Logs

Nunca logar:

```text
Tokens
Senhas
Secrets
Authorization headers
Docker registry passwords
AWS credentials
Variáveis de ambiente sensíveis
```

Quando necessário, mascarar valores:

```text
sk_live_****abcd
Bearer ****
```

Logs devem ser estruturados, mas seguros.

---

## Docker security

O Runtime Agent interage com Docker e, portanto, tem poder elevado no host.

Regras:

```text
Executar apenas comandos Docker necessários.
Não aceitar comandos shell arbitrários vindos da Platform API.
Usar exec.Command com argumentos separados.
Validar imagem.
Validar nome do container.
Aplicar labels obrigatórias.
Manipular apenas containers gerenciados.
```

Containers gerenciados devem possuir:

```text
devex.managed=true
devex.agent_id=<agent_id>
devex.application=<application>
devex.environment=<environment>
devex.deployment_id=<deployment_id>
devex.command_id=<command_id>
```

O agent deve evitar manipular containers sem `devex.managed=true`.

---

## Shell injection

Não construir comandos com concatenação de strings.

Evitar:

```go
exec.Command("sh", "-c", "docker run " + userInput)
```

Preferir:

```go
exec.Command("docker", "run", "-d", "--name", containerName, image)
```

Mesmo usando argumentos separados, validar inputs.

---

## Validação de imagem

Imagens devem seguir formato esperado.

Exemplos válidos:

```text
ghcr.io/useclarus/billing-api:v42
123456789012.dkr.ecr.sa-east-1.amazonaws.com/billing-api:v42
billing-api:v42
```

Rejeitar:

```text
imagens vazias
imagens com espaços
imagens com caracteres de shell
imagens com path suspeito
```

A política de registries permitidos pode ser configurável.

---

## Validação de container_name

Container names devem conter apenas caracteres seguros.

Permitido:

```text
a-z
A-Z
0-9
-
_
.
```

Rejeitar:

```text
espaços
;
&
|
`
$
/
\
```

---

## Variáveis de ambiente

Variáveis de ambiente podem ser enviadas no payload de deploy.

Regras:

```text
Não logar valores.
Não persistir valores sensíveis em state.json.
Permitir denylist de nomes sensíveis.
Futuramente integrar com Secrets Manager.
```

Nomes sensíveis:

```text
PASSWORD
SECRET
TOKEN
KEY
CREDENTIAL
AUTH
```

---

## Caddy Admin API

A Caddy Admin API é altamente sensível.

Ela permite alterar rotas e configuração do gateway.

Regras:

```text
Nunca expor 2019 publicamente.
Publicar Docker como 127.0.0.1:2019:2019.
Permitir acesso apenas do Gateway Agent local.
Não abrir Security Group para 2019.
```

Configuração Docker:

```yaml
ports:
  - "127.0.0.1:2019:2019"
```

Configuração Caddy:

```json
{
  "admin": {
    "listen": "0.0.0.0:2019"
  }
}
```

Essa combinação permite acesso local sem exposição pública.

---

## Validação de hosts no Caddy

O Gateway Agent deve validar hosts antes de gerar `caddy.json`.

Permitido:

```text
*.dev.useclarus.app
*.stage.useclarus.app
domínios explicitamente cadastrados na Platform API
```

Exemplos válidos:

```text
billing-api.dev.useclarus.app
orders-api.stage.useclarus.app
api.useclarus.app
```

Rejeitar:

```text
localhost
127.0.0.1
0.0.0.0
domínios externos não autorizados
hosts com caracteres inválidos
```

---

## Validação de upstreams

Upstreams devem apontar para destinos permitidos.

Permitido no MVP:

```text
IPs privados da VPC + portas gerenciadas
```

Exemplos válidos:

```text
10.0.2.25:4102
10.0.3.31:4103
```

Rejeitar por padrão:

```text
127.0.0.1:22
0.0.0.0:80
169.254.169.254:80
8.8.8.8:80
domínios externos arbitrários
```

A rejeição de `169.254.169.254` é importante para evitar acesso ao metadata service da AWS.

---

## Security Groups

### EC2 Gateway

Inbound:

```text
80/tcp de 0.0.0.0/0
443/tcp de 0.0.0.0/0
443/udp de 0.0.0.0/0 opcional
22/tcp apenas IP administrativo, se necessário
```

Não expor:

```text
2019/tcp
```

### EC2 Runtime

Inbound:

```text
Faixa de portas runtime apenas a partir do Security Group do Caddy Gateway.
```

Exemplo:

```text
TCP 4100-4114 from sg-caddy-gateway
```

Não expor portas runtime para:

```text
0.0.0.0/0
```

---

## AWS IAM

Preferir IAM Role da EC2 para qualquer acesso AWS.

Evitar:

```text
Access Key em arquivo
Secret Key em variável de ambiente
Credenciais long-lived no disco
```

Se a Platform API for responsável por Route 53, os agents não precisam de permissão Route 53.

Recomendação:

```text
Route 53 deve ser manipulado pela Platform API.
Runtime Agent não deve alterar DNS.
Gateway Agent não precisa alterar DNS no MVP.
```

---

## Route 53

A alteração de DNS deve ser centralizada na Platform API ou processo controlado.

O agent não deve, no MVP, criar registros Route 53.

Motivos:

```text
Reduz permissão AWS nas EC2s.
Centraliza auditoria.
Evita alteração indevida de DNS.
Simplifica segurança.
```

---

## Metadata service da AWS

O agent e o Gateway Agent devem evitar que configurações de upstream permitam acesso a:

```text
169.254.169.254
```

Esse endereço expõe o metadata service da instância.

O Gateway Agent deve bloquear upstreams para esse IP.

---

## Secrets

No MVP, secrets devem ser tratados de forma conservadora.

Regras:

```text
Não persistir secrets no estado local.
Não logar secrets.
Não enviar secrets em reports.
Não incluir secrets em labels Docker.
```

Evoluções futuras:

```text
AWS Secrets Manager
SSM Parameter Store
Env files temporários com permissões restritas
Sidecar de secrets
```

---

## Reports para Platform API

Reports não devem incluir dados sensíveis.

Permitido:

```text
status
error_code
error_message sanitizada
container_name
image sem credenciais
host_port
deployment_id
```

Não permitido:

```text
tokens
env vars sensíveis
headers
conteúdo de secrets
logs completos com segredos
```

---

## Validação de payloads

Todo payload recebido da Platform API deve ser validado.

Validar:

```text
Campos obrigatórios.
Tipos corretos.
Strings não vazias.
Imagem válida.
Nome de container válido.
Porta interna válida.
Health check path válido.
Labels válidas.
Environment compatível.
```

O agent deve falhar com `COMMAND_INVALID` se o payload for inválido.

---

## Containers não gerenciados

O Runtime Agent não deve manipular containers sem label:

```text
devex.managed=true
```

Se encontrar container sem essa label usando porta da faixa gerenciada:

```text
Marcar porta como unmanaged.
Não remover automaticamente.
Reportar evento.
```

---

## Least privilege

O agent deve ter apenas permissões necessárias.

Na prática, para interagir com Docker, provavelmente rodará como root ou usuário no grupo docker.

Esse privilégio deve ser reconhecido como sensível.

Proteger:

```text
binário do agent
config.yaml
token
diretório de estado
systemd unit
```

---

## Atualizações do agent

O processo de atualização do agent deve ser controlado.

Recomendações:

```text
Versionar binário.
Registrar versão no heartbeat.
Permitir rollback do binário.
Não atualizar agent junto com deployment de aplicação sem controle.
```

Heartbeat deve incluir:

```json
{
  "version": "0.1.0"
}
```

---

## Auditoria

Eventos de segurança relevantes:

```text
agent_registered
agent_token_invalid
command_rejected
invalid_payload
unmanaged_container_detected
invalid_upstream_rejected
invalid_host_rejected
caddy_admin_unavailable
state_corruption_detected
```

Esses eventos devem ser reportados para a Platform API quando possível.

---

## Erros de segurança

Códigos sugeridos:

```text
AUTHENTICATION_FAILED
AUTHORIZATION_FAILED
INVALID_AGENT_TOKEN
COMMAND_INVALID
CONTAINER_NOT_MANAGED
INVALID_CONTAINER_NAME
INVALID_IMAGE_NAME
INVALID_HOST
INVALID_UPSTREAM
SECRET_DETECTED_IN_LOG_CONTEXT
CADDY_ADMIN_EXPOSED
```

---

## Critérios de aceite

A segurança estará adequada para o MVP quando:

```text
1. Agent não expuser API pública.
2. Caddy Admin API estiver restrita a localhost.
3. Tokens forem lidos de arquivo e não logados.
4. Containers criados tiverem labels obrigatórias.
5. Agent manipular apenas containers gerenciados.
6. Payloads forem validados.
7. Upstreams suspeitos forem rejeitados.
8. Portas runtime não forem expostas publicamente.
9. Estado local não armazenar secrets.
10. Reports não incluírem dados sensíveis.
```

---

## Regra final

O DevEx Agent é um componente privilegiado.

Tudo que ele executa deve ser validado.

Tudo que ele expõe deve ser minimizado.

Tudo que ele registra deve ser sanitizado.

Segurança deve ser padrão, não opção.

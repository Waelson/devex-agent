# 16 — Instalação com systemd

## Objetivo deste documento

Este documento define como instalar, configurar, iniciar, parar, atualizar e remover o **DevEx Agent** como serviço `systemd` em instâncias EC2 Linux.

O DevEx Agent deve ser executado como um processo contínuo, iniciado automaticamente no boot da instância e reiniciado em caso de falha.

Este documento cobre:

- Diretórios esperados.
- Arquivo de configuração.
- Arquivo de token.
- Instalação do binário.
- Unit file do systemd.
- Comandos operacionais.
- Logs via journalctl.
- Atualização do agente.
- Remoção do agente.
- Considerações para Runtime Agent e Gateway Agent.

Este documento deve ser lido junto com:

- `docs/specs/01-architecture.md`
- `docs/specs/02-agent-runtime-spec.md`
- `docs/specs/03-agent-gateway-spec.md`
- `docs/specs/10-local-state.md`
- `docs/specs/12-security.md`
- `docs/specs/13-observability.md`
- `docs/specs/15-configuration.md`

---

## Princípio central

O DevEx Agent deve ser tratado como um serviço de infraestrutura da instância.

Ele deve:

```text
Iniciar automaticamente no boot.
Reiniciar em caso de falha.
Registrar logs estruturados.
Usar configuração local.
Persistir estado local.
Encerrar com graceful shutdown.
```

---

## Usuário de execução

Para o MVP, o agente pode rodar como `root`, porque precisa interagir com Docker e manipular recursos locais.

Exemplo:

```ini
User=root
```

Evolução futura:

```text
Criar usuário devex-agent.
Adicionar usuário ao grupo docker.
Restringir permissões de arquivos.
```

Observação: acesso ao grupo `docker` é equivalente a privilégio elevado no host.

---

## Diretórios esperados

Criar:

```text
/etc/devex-agent
/var/lib/devex-agent
/var/log/devex-agent opcional
```

Comandos:

```bash
sudo mkdir -p /etc/devex-agent
sudo mkdir -p /var/lib/devex-agent
sudo mkdir -p /var/lib/devex-agent/locks
sudo mkdir -p /var/lib/devex-agent/gateway
```

Permissões recomendadas:

```bash
sudo chmod 700 /etc/devex-agent
sudo chmod 700 /var/lib/devex-agent
```

---

## Binário

Local padrão do binário:

```text
/usr/local/bin/devex-agent
```

Instalação:

```bash
sudo cp devex-agent /usr/local/bin/devex-agent
sudo chmod 755 /usr/local/bin/devex-agent
```

Verificação:

```bash
/usr/local/bin/devex-agent --version
```

---

## Arquivo de configuração

Caminho padrão:

```text
/etc/devex-agent/config.yaml
```

Exemplo para Runtime Agent:

```yaml
agent:
  id: ""
  mode: "runtime"
  environment: "dev"
  role: "api"

platform:
  base_url: "https://platform.useclarus.app"
  token_file: "/etc/devex-agent/token"

runtime:
  max_active_containers: 10
  draining_grace_period_seconds: 300
  command_poll_interval_seconds: 10

ports:
  from: 4100
  to: 4114

docker:
  command: "docker"
  default_stop_timeout_seconds: 30

health_check:
  timeout_seconds: 2
  interval_seconds: 5
  retries: 6

state:
  dir: "/var/lib/devex-agent"

logging:
  level: "info"
  format: "json"
```

Exemplo para Gateway Agent:

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

reconcile:
  interval_seconds: 10

state:
  dir: "/var/lib/devex-agent"

logging:
  level: "info"
  format: "json"
```

Permissões:

```bash
sudo chmod 600 /etc/devex-agent/config.yaml
```

---

## Arquivo de token

Caminho padrão:

```text
/etc/devex-agent/token
```

Criar:

```bash
echo "TOKEN_DO_AGENT" | sudo tee /etc/devex-agent/token > /dev/null
sudo chmod 600 /etc/devex-agent/token
```

O token nunca deve ser logado.

---

## Unit file systemd

Arquivo:

```text
/etc/systemd/system/devex-agent.service
```

Conteúdo recomendado:

```ini
[Unit]
Description=DevEx Agent
Documentation=https://useclarus.app
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/devex-agent --config /etc/devex-agent/config.yaml
Restart=always
RestartSec=5
User=root
WorkingDirectory=/var/lib/devex-agent

KillSignal=SIGTERM
TimeoutStopSec=60

StandardOutput=journal
StandardError=journal

NoNewPrivileges=false

[Install]
WantedBy=multi-user.target
```

---

## Instalação do serviço

Após criar o unit file:

```bash
sudo systemctl daemon-reload
sudo systemctl enable devex-agent
sudo systemctl start devex-agent
```

Ver status:

```bash
sudo systemctl status devex-agent
```

Ver logs:

```bash
sudo journalctl -u devex-agent -f
```

---

## Comandos operacionais

### Iniciar

```bash
sudo systemctl start devex-agent
```

### Parar

```bash
sudo systemctl stop devex-agent
```

### Reiniciar

```bash
sudo systemctl restart devex-agent
```

### Status

```bash
sudo systemctl status devex-agent
```

### Logs em tempo real

```bash
sudo journalctl -u devex-agent -f
```

### Logs desde o boot

```bash
sudo journalctl -u devex-agent -b
```

---

## Graceful shutdown

Ao receber `SIGTERM`, o agent deve:

```text
1. Parar de buscar novos comandos.
2. Finalizar ou interromper com segurança o comando atual.
3. Persistir estado local.
4. Enviar status final para Platform API, se possível.
5. Encerrar.
```

O agent não deve parar containers de aplicação apenas porque o serviço do agent foi parado.

Containers devem continuar rodando.

---

## Dependência do Docker

O Runtime Agent depende do Docker.

O unit file deve conter:

```ini
After=network-online.target docker.service
Requires=docker.service
```

Se Docker não estiver disponível, o agent deve falhar ou ficar em modo degradado, dependendo da configuração.

Para MVP, falhar rápido é aceitável.

---

## Gateway Agent e Caddy

O Gateway Agent depende do Caddy disponível localmente.

Se o Caddy for executado via Docker Compose, existem duas opções:

### Opção 1 — Caddy gerenciado separadamente

O Caddy sobe por outro serviço ou compose.

O Gateway Agent apenas chama:

```text
http://127.0.0.1:2019
```

### Opção 2 — Gateway Agent também valida/inicia Caddy

O Gateway Agent pode executar scripts auxiliares para garantir que Caddy esteja rodando.

Para MVP, preferir a opção 1.

---

## Script de instalação

Arquivo sugerido:

```text
scripts/install-systemd.sh
```

Conteúdo base:

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT_BIN="${AGENT_BIN:-./devex-agent}"
INSTALL_PATH="/usr/local/bin/devex-agent"
CONFIG_DIR="/etc/devex-agent"
STATE_DIR="/var/lib/devex-agent"
SERVICE_FILE="/etc/systemd/system/devex-agent.service"

if [ ! -f "$AGENT_BIN" ]; then
  echo "Binário não encontrado: $AGENT_BIN"
  exit 1
fi

sudo mkdir -p "$CONFIG_DIR"
sudo mkdir -p "$STATE_DIR/locks"
sudo mkdir -p "$STATE_DIR/gateway"

sudo chmod 700 "$CONFIG_DIR"
sudo chmod 700 "$STATE_DIR"

sudo cp "$AGENT_BIN" "$INSTALL_PATH"
sudo chmod 755 "$INSTALL_PATH"

sudo tee "$SERVICE_FILE" > /dev/null <<'UNIT'
[Unit]
Description=DevEx Agent
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/devex-agent --config /etc/devex-agent/config.yaml
Restart=always
RestartSec=5
User=root
WorkingDirectory=/var/lib/devex-agent
KillSignal=SIGTERM
TimeoutStopSec=60
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable devex-agent

echo "Instalação concluída."
echo "Configure /etc/devex-agent/config.yaml e /etc/devex-agent/token antes de iniciar."
```

---

## Script de remoção

Arquivo sugerido:

```text
scripts/uninstall-systemd.sh
```

Conteúdo base:

```bash
#!/usr/bin/env bash
set -euo pipefail

sudo systemctl stop devex-agent || true
sudo systemctl disable devex-agent || true
sudo rm -f /etc/systemd/system/devex-agent.service
sudo systemctl daemon-reload

echo "Serviço devex-agent removido."
echo "O estado local em /var/lib/devex-agent não foi removido."
echo "A configuração em /etc/devex-agent não foi removida."
```

Não remover estado local automaticamente para evitar perda acidental.

---

## Atualização do binário

Fluxo recomendado:

```text
1. Baixar novo binário.
2. Validar versão.
3. Parar agent.
4. Substituir binário.
5. Iniciar agent.
6. Verificar status.
7. Verificar heartbeat na Platform API.
```

Comandos:

```bash
sudo systemctl stop devex-agent
sudo cp devex-agent /usr/local/bin/devex-agent
sudo chmod 755 /usr/local/bin/devex-agent
sudo systemctl start devex-agent
sudo systemctl status devex-agent
```

---

## Rollback do binário

Manter cópia anterior:

```bash
sudo cp /usr/local/bin/devex-agent /usr/local/bin/devex-agent.previous
```

Rollback:

```bash
sudo systemctl stop devex-agent
sudo cp /usr/local/bin/devex-agent.previous /usr/local/bin/devex-agent
sudo systemctl start devex-agent
```

---

## Validação pós-instalação

Checklist:

```text
Binário existe em /usr/local/bin/devex-agent.
config.yaml existe em /etc/devex-agent/config.yaml.
token existe em /etc/devex-agent/token.
Diretório /var/lib/devex-agent existe.
Serviço systemd está enabled.
Serviço systemd está active.
Logs aparecem no journalctl.
Agent envia heartbeat.
Agent aparece online na Platform API.
```

---

## Troubleshooting

### Serviço não inicia

Verificar:

```bash
sudo systemctl status devex-agent
sudo journalctl -u devex-agent -n 100
```

### Configuração inválida

Verificar:

```bash
sudo cat /etc/devex-agent/config.yaml
```

O agent deve logar erro `CONFIG_INVALID`.

### Docker indisponível

Verificar:

```bash
sudo systemctl status docker
docker ps
```

### Token inválido

O agent deve logar:

```text
AUTHENTICATION_FAILED
INVALID_AGENT_TOKEN
```

### Caddy indisponível no Gateway Agent

Verificar:

```bash
curl http://127.0.0.1:2019/config/
docker ps | grep caddy
```

---

## Critérios de aceite

A instalação estará correta quando:

```text
1. O agent rodar como serviço systemd.
2. O agent iniciar automaticamente no boot.
3. O agent reiniciar após falha.
4. Logs estiverem disponíveis via journalctl.
5. Configuração estiver em /etc/devex-agent/config.yaml.
6. Token estiver em /etc/devex-agent/token.
7. Estado local estiver em /var/lib/devex-agent.
8. Runtime Agent depender de Docker.
9. Gateway Agent conseguir acessar Caddy localmente.
10. Shutdown graceful funcionar.
```

---

## Regra final

O DevEx Agent deve ser instalado como serviço de infraestrutura.

Ele deve sobreviver a restarts, registrar logs, preservar estado local e reiniciar automaticamente em caso de falha.

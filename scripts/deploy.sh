#!/usr/bin/env bash
# deploy.sh — Compila e implanta o DevEx Agent em uma instância EC2.
#
# Uso:
#   bash scripts/deploy.sh --host <IP> --key <KEY.pem> --mode <runtime|gateway> --token-file <arquivo>
#   bash scripts/deploy.sh --host <IP> --key <KEY.pem> --mode <runtime|gateway> --token <TOKEN>
#
# Exemplos:
#   bash scripts/deploy.sh --host 203.0.113.10 --key ~/.ssh/dev.pem --mode runtime --token-file ~/.devex-token
#   bash scripts/deploy.sh --host 203.0.113.10 --key ~/.ssh/dev.pem --mode gateway --token "tk_abc123" --user ec2-user
#
# Flags opcionais:
#   --user         Usuário SSH (padrão: ec2-user)
#   --skip-build   Reutilizar binário já compilado (devex-agent-linux-amd64)

set -euo pipefail

# ─── Helpers ──────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()    { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()     { error "$*"; exit 1; }
section() { echo -e "\n${BLUE}══ $* ══${NC}"; }

usage() {
  echo "Uso: bash scripts/deploy.sh [flags]"
  echo ""
  echo "Flags obrigatórias:"
  echo "  --host         IP ou hostname da instância EC2"
  echo "  --key          Caminho para o arquivo .pem do Key Pair"
  echo "  --mode         Modo do agente: runtime ou gateway"
  echo "  --token-file   Arquivo contendo o token do agente (recomendado)"
  echo "  --token        Token do agente como string (evite em produção)"
  echo ""
  echo "Flags opcionais:"
  echo "  --user         Usuário SSH (padrão: ec2-user)"
  echo "  --skip-build   Não recompilar; usar devex-agent-linux-amd64 existente"
  echo "  --help         Exibir esta ajuda"
  exit 0
}

# ─── Argumentos ───────────────────────────────────────────────────────────────

HOST=""
KEY=""
MODE=""
TOKEN=""
TOKEN_FILE=""
SSH_USER="ec2-user"
SKIP_BUILD=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)        HOST="$2";       shift 2 ;;
    --key)         KEY="$2";        shift 2 ;;
    --mode)        MODE="$2";       shift 2 ;;
    --token)       TOKEN="$2";      shift 2 ;;
    --token-file)  TOKEN_FILE="$2"; shift 2 ;;
    --user)        SSH_USER="$2";   shift 2 ;;
    --skip-build)  SKIP_BUILD=true; shift   ;;
    --help)        usage ;;
    *) die "Flag desconhecida: $1. Use --help para ver as opções." ;;
  esac
done

# ─── Validações ───────────────────────────────────────────────────────────────

section "Validando parâmetros"

[ -z "$HOST" ]  && die "--host é obrigatório."
[ -z "$KEY" ]   && die "--key é obrigatório."
[ -z "$MODE" ]  && die "--mode é obrigatório."

[[ "$MODE" != "runtime" && "$MODE" != "gateway" ]] && \
  die "--mode deve ser 'runtime' ou 'gateway'. Recebido: '$MODE'"

[ ! -f "$KEY" ] && die "Arquivo de chave não encontrado: $KEY"
chmod 400 "$KEY" 2>/dev/null || true

if [ -n "$TOKEN_FILE" ]; then
  [ ! -f "$TOKEN_FILE" ] && die "Arquivo de token não encontrado: $TOKEN_FILE"
  TOKEN="$(cat "$TOKEN_FILE")"
elif [ -z "$TOKEN" ]; then
  die "Informe --token-file ou --token."
else
  warn "--token passado como argumento. Prefira --token-file em produção."
fi

[ -z "$TOKEN" ] && die "Token está vazio."

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="$SCRIPT_DIR/config-${MODE}.yaml"
SERVICE_FILE="$SCRIPT_DIR/devex-agent.service"
INSTALL_SCRIPT="$SCRIPT_DIR/install-systemd.sh"
BINARY="$REPO_ROOT/devex-agent-linux-amd64"

[ ! -f "$CONFIG_FILE" ]    && die "Config não encontrada: $CONFIG_FILE"
[ ! -f "$SERVICE_FILE" ]   && die "Unit file não encontrado: $SERVICE_FILE"
[ ! -f "$INSTALL_SCRIPT" ] && die "Script de instalação não encontrado: $INSTALL_SCRIPT"

ok "Host  : $SSH_USER@$HOST"
ok "Modo  : $MODE"
ok "Chave : $KEY"

# ─── SSH helper ───────────────────────────────────────────────────────────────

SSH_OPTS="-i $KEY -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 -o BatchMode=yes"

ssh_run() {
  # shellcheck disable=SC2086
  ssh $SSH_OPTS "${SSH_USER}@${HOST}" "$@"
}

scp_put() {
  local src="$1" dst="$2"
  # shellcheck disable=SC2086
  scp -q $SSH_OPTS "$src" "${SSH_USER}@${HOST}:${dst}"
}

# ─── 1. Compilar binário ──────────────────────────────────────────────────────

section "Compilando binário para Linux x86_64"

if [ "$SKIP_BUILD" = true ]; then
  [ ! -f "$BINARY" ] && die "Binário não encontrado: $BINARY. Remova --skip-build para compilar."
  warn "Build ignorado (--skip-build). Usando: $BINARY"
else
  command -v go &>/dev/null || die "Go não encontrado. Instale Go ou use --skip-build com um binário existente."
  info "Compilando..."
  GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$BINARY" "$REPO_ROOT/cmd/devex-agent"
  ok "Binário gerado: $BINARY ($(du -sh "$BINARY" | cut -f1))"
fi

# ─── 2. Verificar conectividade SSH ──────────────────────────────────────────

section "Verificando conectividade com $HOST"

ssh_run "echo ok" > /dev/null || die "Não foi possível conectar via SSH. Verifique o host, a chave e o Security Group."
ok "Conexão SSH estabelecida"

# ─── 3. Enviar artefatos ──────────────────────────────────────────────────────

section "Enviando artefatos para a instância"

REMOTE_TMP="/tmp/devex-deploy-$$"
ssh_run "mkdir -p ${REMOTE_TMP}/scripts"

info "Enviando binário..."
scp_put "$BINARY" "${REMOTE_TMP}/devex-agent"

info "Enviando unit systemd..."
scp_put "$SERVICE_FILE" "${REMOTE_TMP}/scripts/devex-agent.service"

info "Enviando script de instalação..."
scp_put "$INSTALL_SCRIPT" "${REMOTE_TMP}/scripts/install-systemd.sh"

info "Enviando config ($MODE)..."
scp_put "$CONFIG_FILE" "${REMOTE_TMP}/scripts/config.yaml"

ok "Todos os artefatos enviados para ${REMOTE_TMP}"

# ─── 4. Instalar na instância ────────────────────────────────────────────────

section "Instalando na instância"

ssh_run bash <<REMOTE
set -euo pipefail

chmod +x ${REMOTE_TMP}/scripts/install-systemd.sh
cd ${REMOTE_TMP}

echo "[REMOTE] Executando install-systemd.sh..."
sudo AGENT_BIN=./devex-agent bash scripts/install-systemd.sh

echo "[REMOTE] Copiando config para /etc/devex-agent/config.yaml..."
sudo cp scripts/config.yaml /etc/devex-agent/config.yaml
sudo chmod 600 /etc/devex-agent/config.yaml

echo "[REMOTE] Limpando artefatos temporários..."
rm -rf ${REMOTE_TMP}
REMOTE

ok "Instalação concluída"

# ─── 5. Configurar token ─────────────────────────────────────────────────────

section "Configurando token"

# Token é enviado via stdin para não aparecer em ps ou logs remotos.
printf '%s' "$TOKEN" | ssh_run \
  "sudo tee /etc/devex-agent/token > /dev/null && sudo chmod 600 /etc/devex-agent/token"

ok "Token configurado"

# ─── 6. Iniciar serviço ───────────────────────────────────────────────────────

section "Iniciando serviço"

ssh_run bash <<'REMOTE'
set -euo pipefail
sudo systemctl daemon-reload
sudo systemctl restart devex-agent
REMOTE

# ─── 7. Verificar ─────────────────────────────────────────────────────────────

section "Verificando serviço"

info "Aguardando serviço estabilizar..."
MAX_WAIT=20
ELAPSED=0
STATUS=""
while [ "$ELAPSED" -lt "$MAX_WAIT" ]; do
  STATUS="$(ssh_run "systemctl is-active devex-agent 2>/dev/null || true")"
  case "$STATUS" in
    active)           break ;;
    failed|inactive)  break ;;
    activating)       sleep 2; ELAPSED=$((ELAPSED + 2)) ;;
    *)                sleep 2; ELAPSED=$((ELAPSED + 2)) ;;
  esac
done

if [ "$STATUS" = "active" ]; then
  ok "Serviço devex-agent está ATIVO"
else
  error "Serviço devex-agent não está ativo (status: $STATUS)"
  echo ""
  ssh_run "sudo journalctl -u devex-agent -n 20 --no-pager" >&2 || true
  exit 1
fi

# ─── Resumo ───────────────────────────────────────────────────────────────────

echo ""
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo -e "${GREEN}  DevEx Agent implantado com sucesso!${NC}"
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo ""
echo "  Host   : $HOST"
echo "  Modo   : $MODE"
echo "  Usuário: $SSH_USER"
echo ""
echo "Comandos úteis na instância:"
echo "  ssh -i $KEY ${SSH_USER}@${HOST}"
echo "  sudo systemctl status devex-agent"
echo "  sudo journalctl -u devex-agent -f"
echo ""

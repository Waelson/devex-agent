#!/usr/bin/env bash
# install-caddy.sh — Instala o Caddy no Amazon Linux 2023 (x86_64)
# Uso: sudo bash scripts/install-caddy.sh
#
# Variáveis de ambiente opcionais:
#   CADDY_VERSION   Versão a instalar (padrão: detecta a mais recente via GitHub API)
#   SKIP_CHECKSUM   Pular verificação de checksum: "true" (não recomendado)
set -euo pipefail

# ─── Helpers de output ────────────────────────────────────────────────────────

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

# ─── Caminhos fixos ───────────────────────────────────────────────────────────

INSTALL_PATH="/usr/local/bin/caddy"
CONFIG_DIR="/etc/caddy"
DATA_DIR="/var/lib/caddy"
LOG_DIR="/var/log/caddy"
CONFIG_FILE="$CONFIG_DIR/caddy.json"
SERVICE_FILE="/etc/systemd/system/caddy.service"
SKIP_CHECKSUM="${SKIP_CHECKSUM:-false}"

# ─── 1. Validações de pré-requisitos ─────────────────────────────────────────

section "Validando pré-requisitos"

if [ "$(id -u)" -ne 0 ]; then
  die "Este script deve ser executado como root. Use: sudo bash $0"
fi
ok "Executando como root"

ARCH="$(uname -m)"
if [ "$ARCH" != "x86_64" ]; then
  die "Arquitetura não suportada: $ARCH. Este script requer x86_64."
fi
ok "Arquitetura: $ARCH"

if [ -f /etc/os-release ]; then
  # shellcheck source=/dev/null
  source /etc/os-release
  if [[ "${ID:-}" != "amzn" && "${ID_LIKE:-}" != *"fedora"* ]]; then
    warn "Sistema detectado: ${PRETTY_NAME:-desconhecido}. Este script foi testado no Amazon Linux 2023."
  else
    ok "Sistema operacional: ${PRETTY_NAME:-Amazon Linux}"
  fi
else
  warn "Não foi possível detectar o sistema operacional (/etc/os-release ausente)."
fi

REQUIRED_TOOLS=(curl sha512sum tar systemctl)
MISSING_TOOLS=()
for tool in "${REQUIRED_TOOLS[@]}"; do
  if ! command -v "$tool" &>/dev/null; then
    MISSING_TOOLS+=("$tool")
  fi
done
if [ ${#MISSING_TOOLS[@]} -gt 0 ]; then
  die "Ferramentas ausentes: ${MISSING_TOOLS[*]}. Instale-as antes de continuar."
fi
ok "Ferramentas disponíveis: ${REQUIRED_TOOLS[*]}"

info "Verificando conectividade com github.com..."
if ! curl -sf --max-time 10 "https://github.com" -o /dev/null; then
  die "Sem acesso a github.com. Verifique a conectividade da instância."
fi
ok "Conectividade com internet OK"

for port in 80 2019; do
  if ss -tlnp 2>/dev/null | grep -q ":${port} "; then
    warn "Porta $port já está em uso. Será liberada quando o serviço atual for parado."
  fi
done

# ─── 2. Parar serviço existente (se houver) ───────────────────────────────────

section "Verificando instalação existente"

if systemctl is-active --quiet caddy 2>/dev/null; then
  info "Serviço caddy está rodando. Parando para reinstalação..."
  systemctl stop caddy
  ok "Serviço caddy parado"
else
  info "Nenhum serviço caddy ativo encontrado"
fi

if [ -f "$INSTALL_PATH" ]; then
  EXISTING_VERSION="$("$INSTALL_PATH" version 2>/dev/null | awk '{print $1}' || echo "desconhecida")"
  warn "Caddy já instalado ($EXISTING_VERSION). O binário será substituído."
fi

# ─── 3. Detecção da versão ────────────────────────────────────────────────────

section "Detectando versão do Caddy"

if [ -n "${CADDY_VERSION:-}" ]; then
  info "Versão definida via variável de ambiente: $CADDY_VERSION"
else
  info "Consultando GitHub API para versão mais recente..."
  CADDY_VERSION="$(curl -fsSL --max-time 10 \
    "https://api.github.com/repos/caddyserver/caddy/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"v\([^"]*\)".*/\1/')"
  if [ -z "$CADDY_VERSION" ]; then
    die "Não foi possível detectar a versão mais recente. Defina CADDY_VERSION manualmente."
  fi
fi
ok "Versão a instalar: $CADDY_VERSION"

DOWNLOAD_BASE="https://github.com/caddyserver/caddy/releases/download"
ARCHIVE="caddy_${CADDY_VERSION}_linux_amd64.tar.gz"
CHECKSUMS_FILENAME="caddy_${CADDY_VERSION}_checksums.txt"
DOWNLOAD_URL="$DOWNLOAD_BASE/v${CADDY_VERSION}/$ARCHIVE"
CHECKSUMS_URL="$DOWNLOAD_BASE/v${CADDY_VERSION}/$CHECKSUMS_FILENAME"

# ─── 4. Download e verificação ────────────────────────────────────────────────

section "Baixando Caddy v${CADDY_VERSION}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

info "Baixando $ARCHIVE..."
if ! curl -fL --progress-bar "$DOWNLOAD_URL" -o "$TMPDIR/$ARCHIVE"; then
  die "Falha ao baixar $DOWNLOAD_URL. Verifique se a versão $CADDY_VERSION existe."
fi
ok "Download concluído"

if [ "$SKIP_CHECKSUM" != "true" ]; then
  info "Baixando arquivo de checksums..."
  if ! curl -fsSL "$CHECKSUMS_URL" -o "$TMPDIR/$CHECKSUMS_FILENAME"; then
    die "Falha ao baixar $CHECKSUMS_URL"
  fi

  info "Verificando checksum..."
  EXPECTED_HASH="$(grep "$ARCHIVE" "$TMPDIR/$CHECKSUMS_FILENAME" | awk '{print $1}')"
  if [ -z "$EXPECTED_HASH" ]; then
    die "Hash não encontrado no arquivo de checksums para $ARCHIVE."
  fi

  # O Caddy usa SHA-512; detectamos pelo comprimento do hash (≥100 chars).
  if [ "${#EXPECTED_HASH}" -ge 100 ]; then
    ACTUAL_HASH="$(sha512sum "$TMPDIR/$ARCHIVE" | awk '{print $1}')"
    ALGO="SHA-512"
  else
    ACTUAL_HASH="$(sha256sum "$TMPDIR/$ARCHIVE" | awk '{print $1}')"
    ALGO="SHA-256"
  fi

  if [ "$EXPECTED_HASH" != "$ACTUAL_HASH" ]; then
    error "Algoritmo     : $ALGO"
    error "Hash esperado : $EXPECTED_HASH"
    error "Hash calculado: $ACTUAL_HASH"
    die "Checksum inválido para $ARCHIVE. O download pode estar corrompido."
  fi
  ok "Checksum $ALGO verificado com sucesso"
else
  warn "Verificação de checksum ignorada (SKIP_CHECKSUM=true)."
fi

info "Extraindo binário..."
tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR" caddy
if [ ! -f "$TMPDIR/caddy" ]; then
  die "Binário 'caddy' não encontrado no arquivo extraído."
fi
ok "Extração concluída"

# ─── 5. Instalação do binário ─────────────────────────────────────────────────

section "Instalando binário"

cp "$TMPDIR/caddy" "$INSTALL_PATH"
chmod 755 "$INSTALL_PATH"

INSTALLED_VERSION="$("$INSTALL_PATH" version 2>/dev/null | awk '{print $1}' || echo "?")"
ok "Caddy instalado em $INSTALL_PATH (versão: $INSTALLED_VERSION)"

# ─── 6. Criação de diretórios ─────────────────────────────────────────────────

section "Criando diretórios"

mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chmod 755 "$CONFIG_DIR"
chmod 700 "$DATA_DIR"
chmod 755 "$LOG_DIR"

ok "  $CONFIG_DIR  (configuração)"
ok "  $DATA_DIR    (dados/certificados TLS)"
ok "  $LOG_DIR     (logs)"

# ─── 7. Arquivo de configuração ───────────────────────────────────────────────

section "Gravando configuração inicial"

# Sempre sobrescreve para garantir que o config esteja correto.
# O Gateway Agent substituirá esta configuração ao aplicar o desired state.
info "Gravando $CONFIG_FILE..."
cat > "$CONFIG_FILE" <<'JSON'
{
  "admin": {
    "listen": "127.0.0.1:2019"
  },
  "apps": {
    "http": {
      "servers": {
        "devex": {
          "listen": [":80"],
          "routes": []
        }
      }
    }
  }
}
JSON
chmod 640 "$CONFIG_FILE"
ok "Configuração gravada: $CONFIG_FILE"

# ─── 8. Unit file do systemd ──────────────────────────────────────────────────

section "Configurando serviço systemd"

# Sempre sobrescreve o unit file para garantir que o ExecStart esteja correto.
cat > "$SERVICE_FILE" <<UNIT
[Unit]
Description=Caddy HTTP/HTTPS Server (DevEx Gateway)
Documentation=https://caddyserver.com/docs
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart=$INSTALL_PATH run --config $CONFIG_FILE
ExecReload=/bin/kill -USR1 \$MAINPID
Restart=on-failure
RestartSec=5
User=root

Environment=HOME=$DATA_DIR
WorkingDirectory=$DATA_DIR

KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=30

StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

ok "Unit file gravado: $SERVICE_FILE"

systemctl daemon-reload
systemctl enable caddy
ok "Serviço habilitado para iniciar no boot"

# ─── 9. Inicialização e validação ─────────────────────────────────────────────

section "Iniciando serviço"

if ! systemctl start caddy; then
  error "Falha ao iniciar o Caddy."
  journalctl -u caddy -n 30 --no-pager >&2
  exit 1
fi

sleep 3

section "Validando instalação"

if ! systemctl is-active --quiet caddy; then
  error "Serviço caddy não está ativo após inicialização."
  journalctl -u caddy -n 30 --no-pager >&2
  exit 1
fi
ok "Serviço caddy ATIVO"

if systemctl is-enabled --quiet caddy; then
  ok "Serviço caddy habilitado no boot"
else
  warn "Serviço caddy NÃO está habilitado no boot. Execute: systemctl enable caddy"
fi

info "Verificando Admin API em 127.0.0.1:2019..."
for i in $(seq 1 5); do
  if curl -sf --max-time 3 "http://127.0.0.1:2019/config/" -o /dev/null; then
    ok "Admin API acessível em http://127.0.0.1:2019"
    break
  fi
  if [ "$i" -eq 5 ]; then
    error "Admin API não acessível após 5 tentativas."
    journalctl -u caddy -n 30 --no-pager >&2
    exit 1
  fi
  info "Tentativa $i/5 — aguardando Admin API..."
  sleep 2
done

FINAL_VERSION="$("$INSTALL_PATH" version 2>/dev/null | awk '{print $1}' || echo "?")"

# ─── Resumo ───────────────────────────────────────────────────────────────────

echo ""
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo -e "${GREEN}  Caddy instalado com sucesso!${NC}"
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo ""
echo "  Versão         : $FINAL_VERSION"
echo "  Binário        : $INSTALL_PATH"
echo "  Configuração   : $CONFIG_FILE"
echo "  Dados/TLS      : $DATA_DIR"
echo "  Admin API      : http://127.0.0.1:2019"
echo ""
echo "Comandos úteis:"
echo "  systemctl status caddy"
echo "  journalctl -u caddy -f"
echo "  curl http://127.0.0.1:2019/config/"
echo ""
echo -e "${YELLOW}ATENÇÃO — Security Group:${NC}"
echo "  Porta 2019 NÃO deve ser acessível externamente."
echo "  Apenas as portas 80 e 443 devem ser abertas publicamente."
echo ""

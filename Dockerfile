# ==========================================
# ESTÁGIO 1: Compilação Ultra-Rápida do Go
# ==========================================
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copia e instala as dependências primeiro (aproveita o cache do Docker)
COPY go.mod go.sum ./
RUN go mod download

# Copia o restante do código fonte do projeto
COPY . .

# Compila o binário estático DESATIVANDO o CGO (Go Puro = Zero Erros de Compilação)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o main .

# ==========================================
# ESTÁGIO 2: Container Final de Alta Performance
# ==========================================
FROM alpine:latest

# Instala certificados de segurança essenciais para o Go fazer chamadas HTTPS externas se necessário
RUN apk add --no-cache ca-certificates

WORKDIR /

# Cria a pasta blindada onde o SQLite salvará o banco paginas.db de forma persistente
RUN mkdir /data

# Copia apenas o executável leve gerado no estágio anterior (reduz o tamanho do container)
COPY --from=builder /app/main .

# Informa ao Coolify que o nosso servidor Go escuta na porta 8080
EXPOSE 8080

# Liga o servidor de subdomínios imediatamente
CMD ["./main"]

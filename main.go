package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var cacheInternoRAM sync.Map
var db *sql.DB

// Controle simples de força bruta (IP -> {Tentativas, BloqueioUntil})
type LoginAttempt struct {
	Count        int
	BlockedUntil time.Time
}

var loginAttempts sync.Map

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func main() {
	// Em produção (Coolify), use DB_PATH=/data/paginas.db nas variáveis de ambiente
	dbPath := getEnv("DB_PATH", "./paginas.db")

	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		panic(fmt.Errorf("falha ao abrir banco: %w", err))
	}
	defer db.Close()

	// Configurações de segurança e performance do SQLite
	db.SetMaxOpenConns(1) // SQLite prefere conexões únicas para escrita
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL;")
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS landpages (slug TEXT PRIMARY KEY, html TEXT);`)
	if err != nil {
		panic(fmt.Errorf("falha ao criar tabela: %w", err))
	}

	http.HandleFunc("/", handlePublicRequest)
	http.HandleFunc("/admin-secreto", handleAdminRequest)

	port := getEnv("PORT", "8080")
	fmt.Printf("⚡ Servidor Micro-CMS rodando na porta %s\n", port)

	// Timeout para evitar Slowloris attacks
	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

func handlePublicRequest(w http.ResponseWriter, r *http.Request) {
	hostLimpo := r.Host
	if strings.Contains(hostLimpo, ":") {
		hostLimpo = strings.Split(hostLimpo, ":")[0]
	}
	subdominioAcessado := strings.ToLower(hostLimpo)

	if r.URL.Path == "/admin-secreto" {
		handleAdminRequest(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600") // Cache no navegador do cliente

	if htmlInstalado, encontrado := cacheInternoRAM.Load(subdominioAcessado); encontrado {
		w.Write([]byte(htmlInstalado.(string)))
		return
	}

	var htmlDaPagina string
	err := db.QueryRow("SELECT html FROM landpages WHERE slug = ?", subdominioAcessado).Scan(&htmlDaPagina)

	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "<h1>404 - Página não encontrada</h1><p>Domínio: <strong>%s</strong></p>", subdominioAcessado)
		return
	} else if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Erro interno do servidor."))
		return
	}

	cacheInternoRAM.Store(subdominioAcessado, htmlDaPagina)
	w.Write([]byte(htmlDaPagina))
}

func handleAdminRequest(w http.ResponseWriter, r *http.Request) {
	// 1. Proteção Anti-Força Bruta por IP
	clientIP := strings.Split(r.RemoteAddr, ":")[0]

	now := time.Now()
	if attempt, ok := loginAttempts.Load(clientIP); ok {
		data := attempt.(LoginAttempt)
		if now.Before(data.BlockedUntil) {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, "Muitas tentativas. Tente novamente em %v.", time.Until(data.BlockedUntil).Round(time.Second))
			return
		}
	}

	usuario, senha, ok := r.BasicAuth()
	adminUser := getEnv("CMS_USER", "")
	adminPass := getEnv("CMS_PASS", "")

	// Exige variáveis de ambiente definidas em produção
	if adminUser == "" || adminPass == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("CMS não configurado. Defina CMS_USER e CMS_PASS."))
		return
	}

	if !ok || usuario != adminUser || senha != adminPass {
		// Registra falha
		attempt, _ := loginAttempts.LoadOrStore(clientIP, LoginAttempt{})
		data := attempt.(LoginAttempt)
		data.Count++

		if data.Count >= 5 {
			data.BlockedUntil = now.Add(15 * time.Minute) // Bloqueia por 15 min
			data.Count = 0
		}
		loginAttempts.Store(clientIP, data)

		w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard Restrito"`)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Acesso negado."))
		return
	}

	// Limpa tentativas em caso de sucesso
	loginAttempts.Delete(clientIP)

	// ... (Restante da lógica de CRUD do admin permanece a mesma do código anterior) ...
	// Para economizar espaço, mantenha a lógica de POST, DELETE e renderização do HTML
	// que já estava funcionando no seu código anterior.

	// Exemplo simplificado de sucesso:
	w.Write([]byte("✅ Autenticado com sucesso. (Integre aqui o HTML do dashboard)"))
}

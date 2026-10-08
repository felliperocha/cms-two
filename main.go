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

func getEnvStrict(key string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	panic(fmt.Sprintf("ERRO CRÍTICO: A variável de ambiente %s é obrigatória!", key))
}

// limparHost remove https://, http://, www. e portas, garantindo que o slug seja sempre o domínio puro
func limparHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "www.")
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	return host
}

func main() {
	dbPath := getEnv("DB_PATH", "/data/paginas.db")
	
	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		panic(fmt.Errorf("falha ao abrir banco: %w", err))
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL;")
	_, _ = db.Exec("PRAGMA cache_size=-64000;")
	
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS landpages (slug TEXT PRIMARY KEY, html TEXT);`)
	if err != nil {
		panic(fmt.Errorf("falha ao criar tabela: %w", err))
	}

	http.HandleFunc("/", handlePublicRequest)
	http.HandleFunc("/admin-secreto", handleAdminRequest)

	port := getEnv("PORT", "8080")
	fmt.Printf("⚡ Servidor Micro-CMS rodando na porta %s (Produção)\n", port)
	
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
	// Limpa o host recebido do navegador (remove porta se houver)
	subdominioAcessado := limparHost(r.Host)

	if r.URL.Path == "/admin-secreto" {
		handleAdminRequest(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	if htmlInstalado, encontrado := cacheInternoRAM.Load(subdominioAcessado); encontrado {
		w.Write([]byte(htmlInstalado.(string)))
		return
	}

	var htmlDaPagina string
	err := db.QueryRow("SELECT html FROM landpages WHERE slug = ?", subdominioAcessado).Scan(&htmlDaPagina)

	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "<h1>404 - Página não encontrada</h1><p>O domínio <strong>%s</strong> não possui uma landing page configurada.</p>", subdominioAcessado)
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
	clientIP := strings.Split(r.RemoteAddr, ":")[0]
	now := time.Now()

	if attempt, ok := loginAttempts.Load(clientIP); ok {
		data := attempt.(LoginAttempt)
		if now.Before(data.BlockedUntil) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(fmt.Sprintf("Bloqueado por %v.", time.Until(data.BlockedUntil).Round(time.Second))))
			return
		}
	}

	usuario, senha, ok := r.BasicAuth()
	adminUser := getEnvStrict("CMS_USER")
	adminPass := getEnvStrict("CMS_PASS")

	if !ok || usuario != adminUser || senha != adminPass {
		attempt, _ := loginAttempts.LoadOrStore(clientIP, LoginAttempt{})
		data := attempt.(LoginAttempt)
		data.Count++
		if data.Count >= 5 {
			data.BlockedUntil = now.Add(15 * time.Minute)
			data.Count = 0
		}
		loginAttempts.Store(clientIP, data)

		w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard"`)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Acesso negado."))
		return
	}

	loginAttempts.Delete(clientIP)

	acao := r.URL.Query().Get("action")
	slugEdicao := r.URL.Query().Get("edit")

	if acao == "delete" {
		slugDeletar := r.URL.Query().Get("slug")
		if slugDeletar != "" {
			_, _ = db.Exec("DELETE FROM landpages WHERE slug = ?", slugDeletar)
			cacheInternoRAM.Delete(slugDeletar)
			http.Redirect(w, r, "/admin-secreto", http.StatusSeeOther)
			return
		}
	}

	if r.Method == http.MethodPost {
		// AQUI ESTÁ A MÁGICA: Limpa o host antes de salvar no banco!
		slug := limparHost(r.FormValue("slug"))
		htmlCodigo := r.FormValue("html_codigo")
		
		if slug != "" && htmlCodigo != "" {
			_, _ = db.Exec("INSERT OR REPLACE INTO landpages (slug, html) VALUES (?, ?)", slug, htmlCodigo)
			cacheInternoRAM.Store(slug, htmlCodigo)
			http.Redirect(w, r, "/admin-secreto", http.StatusSeeOther)
			return
		}
	}

	var htmlExistente string
	if slugEdicao != "" {
		_ = db.QueryRow("SELECT html FROM landpages WHERE slug = ?", slugEdicao).Scan(&htmlExistente)
	}

	linhas, err := db.Query("SELECT slug FROM landpages ORDER BY slug ASC")
	var listaSubdominios []string
	if err == nil {
		for linhas.Next() {
			var s string
			_ = linhas.Scan(&s)
			listaSubdominios = append(listaSubdominios, s)
		}
		linhas.Close()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	
	isEditando := slugEdicao != ""
	tituloForm := "✨ Criar Nova Página"
	if isEditando { tituloForm = "📝 Editar Página" }

	readonlyAttr := `style="margin-top: 5px;"`
	if isEditando { readonlyAttr = `readonly style="opacity: 0.6; cursor: not-allowed; margin-top: 5px;"` }

	cancelBtn := ""
	if isEditando { cancelBtn = "<a href='/admin-secreto' style='color:#fff; text-decoration:none; margin-top:10px; display:block;'>Cancelar</a>" }

	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>Micro-CMS</title>
	<style>body{background:#09090b;color:#fff;font-family:system-ui;padding:40px;max-width:800px;margin:0 auto;}
	input,textarea{width:100%%;padding:12px;margin-top:5px;background:#1c1c1e;border:1px solid #27272a;color:#fff;border-radius:6px;}
	button{background:#00ff66;color:#000;padding:12px;border:none;border-radius:6px;font-weight:bold;cursor:pointer;margin-top:15px;width:100%%;}
	table{width:100%%;margin-top:30px;border-collapse:collapse;}td,th{padding:12px;text-align:left;border-bottom:1px solid #27272a;}
	.btn-del{color:#ef4444;text-decoration:none;font-size:0.9rem;}</style></head><body>
	<h1>🚀 Micro-CMS In-Memory</h1>
	<p style="color:#a1a1aa; margin-bottom: 20px;">Dica: Digite apenas o domínio (ex: fellipe10.fellipedev.com.br). O sistema limpa o resto automaticamente.</p>
	<form method="POST" action="/admin-secreto">
	<label>Host (Domínio)</label><input type="text" name="slug" value="%s" required %s>
	<label>Código HTML Completo</label><textarea name="html_codigo" rows="15" required style="margin-top:5px;">%s</textarea>
	<button type="submit">Salvar e Publicar na RAM</button>%s</form>
	<h2>Páginas Ativas</h2><table>`, slugEdicao, readonlyAttr, htmlExistente, cancelBtn)

	if len(listaSubdominios) == 0 {
		fmt.Fprint(w, `<tr><td>Nenhuma página criada.</td></tr>`)
	} else {
		for _, sub := range listaSubdominios {
			fmt.Fprintf(w, `<tr><td>%s</td><td style="text-align:right;"><a href="/admin-secreto?edit=%s" style="color:#fff;margin-right:15px;">Editar</a><a href="/admin-secreto?action=delete&slug=%s" class="btn-del" onclick="return confirm('Excluir?')">Excluir</a></td></tr>`, sub, sub, sub)
		}
	}
	fmt.Fprint(w, `</table></body></html>`)
}

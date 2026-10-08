package main

import (
	"database/sql"
	"encoding/json"
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

// verificaLogin checa se o cookie de autenticação é válido
func verificarLogin(r *http.Request) bool {
	cookie, err := r.Cookie("cms_auth")
	if err != nil {
		return false
	}
	// Compara o valor do cookie com o segredo definido nas variáveis de ambiente
	return cookie.Value == getEnvStrict("CMS_SECRET")
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

	// Rotas
	http.HandleFunc("/", handlePublicRequest)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/admin", handleAdmin)
	http.HandleFunc("/api/salvar-edicao", handleSalvarEdicao)

	port := getEnv("PORT", "8080")
	fmt.Printf("⚡ Micro-CMS Visual rodando na porta %s\n", port)

	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

// --- ROTA PÚBLICA (Com Injeção de Editor se Logado) ---
func handlePublicRequest(w http.ResponseWriter, r *http.Request) {
	subdominioAcessado := limparHost(r.Host)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	if htmlInstalado, encontrado := cacheInternoRAM.Load(subdominioAcessado); encontrado {
		htmlFinal := htmlInstalado.(string)
		if verificarLogin(r) {
			htmlFinal = injetarEditorInline(htmlFinal)
		}
		w.Write([]byte(htmlFinal))
		return
	}

	var htmlDaPagina string
	err := db.QueryRow("SELECT html FROM landpages WHERE slug = ?", subdominioAcessado).Scan(&htmlDaPagina)

	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "<h1>404</h1><p>Domínio <strong>%s</strong> não configurado. <a href='/login'>Login Admin</a></p>", subdominioAcessado)
		return
	} else if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Erro interno."))
		return
	}

	cacheInternoRAM.Store(subdominioAcessado, htmlDaPagina)
	htmlFinal := htmlDaPagina
	
	if verificarLogin(r) {
		htmlFinal = injetarEditorInline(htmlFinal)
	}
	
	w.Write([]byte(htmlFinal))
}

// --- INJETOR DE EDITOR INLINE ---
func injetarEditorInline(html string) string {
	script := `
	<script>
	document.addEventListener('DOMContentLoaded', () => {
		// 1. Torna elementos editáveis
		document.querySelectorAll('h1, h2, h3, h4, p, a, span, li, td, th, div, section, article').forEach(el => {
			if (el.id === 'micro-cms-save-btn' || el.closest('#micro-cms-save-btn')) return;
			el.contentEditable = "true";
			el.style.outline = "2px dashed #00ff66";
			el.style.cursor = "text";
		});

		// 2. Botão Flutuante de Salvar
		const btn = document.createElement('button');
		btn.id = 'micro-cms-save-btn';
		btn.innerText = '💾 Salvar Alterações';
		btn.style.cssText = 'position:fixed; bottom:20px; right:20px; background:#00ff66; color:#000; padding:15px 25px; border-radius:50px; font-weight:bold; cursor:pointer; z-index:99999; border:none; box-shadow:0 4px 15px rgba(0,0,0,0.3); font-size:16px;';
		document.body.appendChild(btn);

		// 3. Ação de Salvar
		btn.onclick = async () => {
			btn.innerText = '⏳ Salvando...';
			btn.disabled = true;
			
			// Remove outlines antes de salvar
			document.querySelectorAll('[contenteditable="true"]').forEach(el => el.style.outline = "none");
			
			try {
				const response = await fetch('/api/salvar-edicao', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify({ html: document.documentElement.outerHTML })
				});
				
				if(response.ok) {
					btn.innerText = '✅ Salvo!';
					setTimeout(() => location.reload(), 1000);
				} else {
					throw new Error('Falha no servidor');
				}
			} catch (e) {
				btn.innerText = '❌ Erro!';
				btn.style.background = '#ef4444';
				btn.style.color = '#fff';
			}
		};
	});
	</script>`

	if strings.Contains(html, "</body>") {
		return strings.Replace(html, "</body>", script+"</body>", 1)
	}
	return html + script
}

// --- LOGIN E LOGOUT ---
func handleLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := strings.Split(r.RemoteAddr, ":")[0]
	now := time.Now()

	if attempt, ok := loginAttempts.Load(clientIP); ok {
		data := attempt.(LoginAttempt)
		if now.Before(data.BlockedUntil) {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, "Bloqueado por %v.", time.Until(data.BlockedUntil).Round(time.Second))
			return
		}
	}

	if r.Method == http.MethodPost {
		user := r.FormValue("user")
		pass := r.FormValue("pass")

		if user == getEnvStrict("CMS_USER") && pass == getEnvStrict("CMS_PASS") {
			loginAttempts.Delete(clientIP) // Limpa tentativas falhas
			
			// Define o cookie seguro
			http.SetCookie(w, &http.Cookie{
				Name:     "cms_auth",
				Value:    getEnvStrict("CMS_SECRET"),
				Path:     "/",
				HttpOnly: true,
				Secure:   true, // Exige HTTPS (Obrigatório no Coolify/Cloudflare)
				SameSite: http.SameSiteStrictMode,
				MaxAge:   86400 * 7, // 7 dias
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		// Falha no login
		attempt, _ := loginAttempts.LoadOrStore(clientIP, LoginAttempt{})
		data := attempt.(LoginAttempt)
		data.Count++
		if data.Count >= 5 {
			data.BlockedUntil = now.Add(15 * time.Minute)
		}
		loginAttempts.Store(clientIP, data)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Login</title>
	<style>body{background:#09090b;color:#fff;font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;}
	form{background:#141416;padding:40px;border-radius:12px;border:1px solid #27272a;width:300px;}
	input{width:100%;padding:12px;margin-top:10px;background:#1c1c1e;border:1px solid #27272a;color:#fff;border-radius:6px;box-sizing:border-box;}
	button{width:100%;background:#00ff66;color:#000;padding:12px;border:none;border-radius:6px;font-weight:bold;cursor:pointer;margin-top:20px;}
	</style></head><body>
	<form method="POST"><h2>🔒 Acesso Admin</h2>
	<input name="user" placeholder="Usuário" required>
	<input type="password" name="pass" placeholder="Senha" required>
	<button type="submit">Entrar</button></form></body></html>`)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "cms_auth",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- API DE SALVAMENTO (CRUD INLINE) ---
func handleSalvarEdicao(w http.ResponseWriter, r *http.Request) {
	if !verificarLogin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var payload struct {
		HTML string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	host := limparHost(r.Host)
	
	// Remove o script do editor antes de salvar no banco para não poluir o HTML final
	htmlLimpo := strings.Replace(payload.HTML, `<script>`, `<!-- SCRIPT_REMOVIDO -->`, 1) 
	// Nota: Para uma remoção perfeita, usaríamos uma regex ou parser HTML, mas para um Micro-CMS, 
	// como o script é reinjetado dinamicamente pelo Go a cada requisição de admin, 
	// o ideal é salvar o HTML como vem, pois na próxima visita pública (sem cookie), o Go não injeta o script.
	
	_, err := db.Exec("UPDATE landpages SET html = ? WHERE slug = ?", payload.HTML, host)
	if err == nil {
		cacheInternoRAM.Store(host, payload.HTML)
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// --- PAINEL ADMIN (CRUD DE DOMÍNIOS) ---
func handleAdmin(w http.ResponseWriter, r *http.Request) {
	if !verificarLogin(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	acao := r.URL.Query().Get("action")
	if acao == "delete" {
		slug := r.URL.Query().Get("slug")
		if slug != "" {
			_, _ = db.Exec("DELETE FROM landpages WHERE slug = ?", slug)
			cacheInternoRAM.Delete(slug)
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
	}

	if r.Method == http.MethodPost {
		slug := limparHost(r.FormValue("slug"))
		if slug != "" {
			// Cria uma página em branco para o novo domínio
			_, _ = db.Exec("INSERT OR IGNORE INTO landpages (slug, html) VALUES (?, ?)", slug, "<html><body><h1>Nova Página</h1><p>Clique para editar.</p></body></html>")
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
	}

	linhas, _ := db.Query("SELECT slug FROM landpages ORDER BY slug ASC")
	var lista []string
	if linhas != nil {
		for linhas.Next() {
			var s string
			linhas.Scan(&s)
			lista = append(lista, s)
		}
		linhas.Close()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Admin - Domínios</title>
	<style>body{background:#09090b;color:#fff;font-family:system-ui;padding:40px;max-width:800px;margin:0 auto;}
	input{width:100%%;padding:12px;margin-top:5px;background:#1c1c1e;border:1px solid #27272a;color:#fff;border-radius:6px;box-sizing:border-box;}
	button{background:#00ff66;color:#000;padding:12px;border:none;border-radius:6px;font-weight:bold;cursor:pointer;margin-top:15px;}
	table{width:100%%;margin-top:30px;border-collapse:collapse;}td,th{padding:12px;text-align:left;border-bottom:1px solid #27272a;}
	.btn{padding:8px 12px;border-radius:4px;text-decoration:none;font-size:0.9rem;margin-right:10px;}
	.btn-edit{background:#27272a;color:#fff;}
	.btn-del{background:rgba(239,68,68,0.15);color:#ef4444;}
	.logout{float:right;color:#ef4444;text-decoration:none;font-weight:bold;}
	</style></head><body>
	<a href="/logout" class="logout">Sair</a>
	<h1>🌐 Gerenciar Domínios</h1>
	<p style="color:#a1a1aa">Crie o domínio aqui. Depois, acesse o domínio para editar o conteúdo inline.</p>
	
	<form method="POST" action="/admin">
	<label>Novo Domínio (ex: cliente.seusite.com)</label>
	<input type="text" name="slug" placeholder="dominio.com" required>
	<button type="submit">Criar Domínio</button>
	</form>

	<h2>Domínios Ativos</h2>
	<table>`, )

	if len(lista) == 0 {
		fmt.Fprint(w, `<tr><td>Nenhum domínio criado.</td></tr>`)
	} else {
		for _, sub := range lista {
			fmt.Fprintf(w, `<tr>
				<td>%s</td>
				<td style="text-align:right;">
					<a href="http://%s" target="_blank" class="btn btn-edit">Editar Inline</a>
					<a href="/admin?action=delete&slug=%s" class="btn btn-del" onclick="return confirm('Excluir domínio?')">Excluir</a>
				</td>
			</tr>`, sub, sub, sub)
		}
	}
	fmt.Fprint(w, `</table></body></html>`)
}

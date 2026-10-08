package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // DRIVER GO PURO (Sem CGO)
)

var cacheInternoRAM sync.Map
var db *sql.DB

func obtenerAdminUsuario() string {
	usuario := os.Getenv("CMS_USER")
	if usuario == "" {
		return "admin"
	}
	return usuario
}

func obtenerAdminSenha() string {
	senha := os.Getenv("CMS_PASS")
	if senha == "" {
		return "admin"
	}
	return senha
}

func main() {
	// Carregador nativo de .env
	if arquivo, err := os.Open(".env"); err == nil {
		scanner := bufio.NewScanner(arquivo)
		for scanner.Scan() {
			linha := strings.TrimSpace(scanner.Text())
			if linha == "" || strings.HasPrefix(linha, "#") {
				continue
			}
			if partes := strings.SplitN(linha, "=", 2); len(partes) == 2 {
				os.Setenv(strings.TrimSpace(partes[0]), strings.TrimSpace(partes[1]))
			}
		}
		// Correção do warning: verificação de erro do scanner
		if err := scanner.Err(); err != nil {
			fmt.Printf("⚠️ Erro ao ler .env: %v\n", err)
		}
		arquivo.Close()
		fmt.Println("🌱 Arquivo .env carregado localmente com sucesso!")
	}

	var err error
	db, err = sql.Open("sqlite", "./paginas.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS landpages (slug TEXT PRIMARY KEY, html TEXT);`)
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		subdominioAcessado := strings.ToLower(r.Host)
		caminho := r.URL.Path

		if caminho == "/admin-secreto" {
			executarAdminSecreto(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		if htmlInstalado, encontrado := cacheInternoRAM.Load(subdominioAcessado); encontrado {
			w.Write([]byte(htmlInstalado.(string)))
			return
		}

		var htmlDaPagina string
		err := db.QueryRow("SELECT html FROM landpages WHERE slug = ?", subdominioAcessado).Scan(&htmlDaPagina)

		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "<h1>404 - Subdomínio não encontrado</h1><p>Para colocar este site no ar, vá ao painel e crie uma página com o nome exato: <strong>%s</strong></p>", subdominioAcessado)
			return
		} else if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "<h1>500 - Erro interno</h1><p>%v</p>", err)
			return
		}

		cacheInternoRAM.Store(subdominioAcessado, htmlDaPagina)
		w.Write([]byte(htmlDaPagina))
	})

	fmt.Println("⚡ Servidor Micro-CMS rodando em http://localhost:80")
	http.ListenAndServe(":80", nil)
}

func executarAdminSecreto(w http.ResponseWriter, r *http.Request) {
	usuario, senha, ok := r.BasicAuth()
	if !ok || usuario != obtenerAdminUsuario() || senha != obtenerAdminSenha() {
		w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard Restrito"`)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Acesso negado."))
		return
	}

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
		slug := strings.ToLower(strings.TrimSpace(r.FormValue("slug")))
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
		// Correção do warning: verificação de erro do sql.Rows
		if err := linhas.Err(); err != nil {
			fmt.Printf("⚠️ Erro ao ler banco de dados: %v\n", err)
		}
		linhas.Close()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	isEditando := slugEdicao != ""
	tituloForm := "✨ Criar Nova Landing Page"
	if isEditando {
		tituloForm = "📝 Editar Subdomínio"
	}

	readonlyAttr := `style="margin-top: 5px;"`
	if isEditando {
		readonlyAttr = `readonly style="opacity: 0.6; cursor: not-allowed; margin-top: 5px;"`
	}

	cancelBtn := ""
	if isEditando {
		cancelBtn = "<a href='/admin-secreto' class='cancel-btn'>Cancelar Edição</a>"
	}

	// Renderização direta com fmt.Fprintf (padrão Go, eficiente e sem warnings de concatenação)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="pt-BR">
<head>
    <meta charset="UTF-8">
    <title>Dashboard Micro-CMS</title>
    <style>
        :root { --bg: #09090b; --card: #141416; --primary: #00ff66; --border: #27272a; --text: #ffffff; --text-muted: #a1a1aa; }
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { background: var(--bg); color: var(--text); font-family: system-ui, sans-serif; padding: 40px; }
        .header-container { max-width: 1200px; margin: 0 auto 30px auto; display: flex; justify-content: space-between; align-items: flex-start; }
        .btn-logout { background: rgba(239, 68, 68, 0.1); color: #ef4444; border: 1px solid rgba(239, 68, 68, 0.2); padding: 10px 20px; font-weight: 600; border-radius: 6px; cursor: pointer; transition: background 0.2s; font-size: 0.9rem; }
        .btn-logout:hover { background: rgba(239, 68, 68, 0.2); }
        .dashboard { max-width: 1200px; margin: 0 auto; display: grid; grid-template-columns: 1fr 1fr; gap: 40px; }
        @media (max-width: 900px) { .dashboard { grid-template-columns: 1fr; } }
        .panel { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 30px; height: fit-content; }
        h2 { font-size: 1.5rem; font-weight: 800; margin-bottom: 20px; display: flex; align-items: center; gap: 10px; }
        form { display: flex; flex-direction: column; gap: 15px; }
        label { font-size: 0.9rem; color: var(--text-muted); font-weight: 500; }
        input, textarea { padding: 12px; border-radius: 8px; border: 1px solid var(--border); background: #1c1c1e; color: #fff; font-size: 1rem; width: 100%%; font-family: monospace; }
        input:focus, textarea:focus { border-color: var(--primary); outline: none; }
        button[type="submit"] { background: var(--primary); color: #000; padding: 14px; font-weight: bold; font-size: 1rem; border: none; border-radius: 8px; cursor: pointer; transition: background 0.2s; text-transform: uppercase; letter-spacing: 0.5px; }
        button[type="submit"]:hover { background: #00e65c; }
        .cancel-btn { background: #27272a; color: #fff; text-decoration: none; padding: 10px; text-align: center; border-radius: 8px; font-size: 0.9rem; transition: background 0.2s; margin-top: 10px; display: block; }
        .cancel-btn:hover { background: #3f3f46; }
        table { width: 100%%; border-collapse: collapse; margin-top: 10px; }
        th { text-align: left; padding: 12px; color: var(--text-muted); font-size: 0.85rem; text-transform: uppercase; border-bottom: 1px solid var(--border); }
        td { padding: 16px 12px; border-bottom: 1px solid var(--border); font-size: 0.95rem; }
        .actions { display: flex; gap: 10px; justify-content: flex-end; }
        .btn-action { text-decoration: none; font-weight: 600; font-size: 0.85rem; padding: 6px 12px; border-radius: 4px; transition: opacity 0.2s; }
        .btn-action:hover { opacity: 0.8; }
        .btn-edit { background: #27272a; color: #fff; }
        .btn-delete { background: rgba(239, 68, 68, 0.15); color: #ef4444; }
        .empty-state { text-align: center; color: var(--text-muted); padding: 40px 0; font-size: 0.95rem; }
    </style>
    <script>
        function deslogarCms() {
            const ajax = new XMLHttpRequest();
            ajax.open("GET", "/admin-secreto", true, "logout_user", "logout_pass");
            ajax.send();
            ajax.onreadystatechange = function() {
                if (ajax.status == 401) {
                    alert("Você foi deslogado com segurança!");
                    window.location.href = "/";
                }
            };
        }
    </script>
</head>
<body>
    <div class="header-container">
        <div>
            <h1 style="font-size: 2.2rem; font-weight: 900; letter-spacing: -1px;">🚀 Micro-CMS <span style="color:var(--primary)">In-Memory</span></h1>
            <p style="color: var(--text-muted); margin-top: 5px;">Gerenciamento de Landing Pages na velocidade máxima.</p>
        </div>
        <button class="btn-logout" onclick="deslogarCms()">Sair do Painel</button>
    </div>

    <div class="dashboard">
        <div class="panel">
            <h2>%s</h2>
            <form method="POST" action="/admin-secreto">
                <div>
                    <label>Subdomínio ou Host Completo</label>
                    <input type="text" name="slug" placeholder="ex: fellipe10.local" value="%s" required %s>
                </div>
                <div>
                    <label>Código HTML/CSS da IA</label>
                    <textarea name="html_codigo" rows="18" placeholder="Cole o código HTML completo aqui..." required style="margin-top: 5px;">%s</textarea>
                </div>
                <button type="submit">Salvar e Publicar na RAM</button>
                %s
            </form>
        </div>

        <div class="panel">
            <h2>🌐 Subdomínios Ativos</h2>
            <table>
                <thead>
                    <tr>
                        <th>Endereço / Host</th>
                        <th style="text-align: right;">Ações</th>
                    </tr>
                </thead>
                <tbody>
`, tituloForm, slugEdicao, readonlyAttr, htmlExistente, cancelBtn)

	if len(listaSubdominios) == 0 {
		fmt.Fprint(w, `<tr><td colspan="2" class="empty-state">Nenhum subdomínio configurado ainda. Use o formulário ao lado!</td></tr>`)
	} else {
		for _, sub := range listaSubdominios {
			fmt.Fprintf(w, `
                    <tr>
                        <td>%s</td>
                        <td style="text-align: right;">
                            <div class="actions">
                                <a href="/admin-secreto?edit=%s" class="btn-action btn-edit">Editar</a>
                                <a href="/admin-secreto?action=delete&slug=%s" class="btn-action btn-delete" onclick="return confirm('Tem certeza que deseja excluir %s?')">Excluir</a>
                            </div>
                        </td>
                    </tr>
                `, sub, sub, sub, sub)
		}
	}

	fmt.Fprint(w, `
                </tbody>
            </table>
        </div>
    </div>
</body>
</html>
`)
}

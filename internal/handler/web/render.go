package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	webui "homework/web"
)

type Renderer struct {
	pages     map[string]*template.Template
	fragments *template.Template
}

func NewRenderer() (*Renderer, error) {
	const layout = "templates/layout.html"
	const partials = "templates/partials/"
	partialFiles := []string{
		partials + "flash.html",
		partials + "nav.html",
		partials + "movie_row.html",
		partials + "movie_rows.html",
	}
	renderer := &Renderer{pages: make(map[string]*template.Template, 5)}
	fragmentFiles := make([]string, 0, len(partialFiles))
	for _, file := range partialFiles {
		fragmentFiles = append(fragmentFiles, file)
	}
	var err error
	renderer.fragments, err = template.New("fragments").ParseFS(webui.TemplateFS, fragmentFiles...)
	if err != nil {
		return nil, fmt.Errorf("parse web fragment templates: %w", err)
	}
	for page, file := range map[string]string{
		"catalog":    "templates/pages/catalog.html",
		"chat":       "templates/pages/chat.html",
		"error":      "templates/pages/error.html",
		"login":      "templates/pages/login.html",
		"movie_form": "templates/pages/movie_form.html",
		"register":   "templates/pages/register.html",
	} {
		files := append([]string{layout}, partialFiles...)
		files = append(files, file)
		parsed, parseErr := template.New("layout").ParseFS(webui.TemplateFS, files...)
		if parseErr != nil {
			return nil, fmt.Errorf("parse web %s templates: %w", page, parseErr)
		}
		renderer.pages[page] = parsed
	}
	return renderer, nil
}

func (renderer *Renderer) Page(c *gin.Context, status int, name string, data any) {
	parsed, ok := renderer.pages[name]
	if !ok {
		renderer.renderFailure(c, fmt.Errorf("unknown page template %q", name))
		return
	}
	data = withCSRFToken(data, CSRFTokenFrom(c))
	var output bytes.Buffer
	if err := parsed.ExecuteTemplate(&output, "layout", data); err != nil {
		renderer.renderFailure(c, err)
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	_, _ = c.Writer.Write(output.Bytes())
}

func withCSRFToken(data any, token string) any {
	switch page := data.(type) {
	case CatalogPage:
		page.CSRFToken = token
		return page
	case ErrorPage:
		page.CSRFToken = token
		return page
	case LoginPage:
		page.CSRFToken = token
		return page
	case RegisterPage:
		page.CSRFToken = token
		return page
	case MovieFormPage:
		page.CSRFToken = token
		return page
	case ChatPage:
		page.CSRFToken = token
		return page
	default:
		return data
	}
}

func (renderer *Renderer) Fragment(c *gin.Context, status int, name string, data any) {
	var output bytes.Buffer
	if err := renderer.fragments.ExecuteTemplate(&output, name, data); err != nil {
		renderer.renderFailure(c, err)
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	_, _ = c.Writer.Write(output.Bytes())
}

func (renderer *Renderer) renderFailure(c *gin.Context, err error) {
	log.Printf("render web template: %v", err)
	data := ErrorPage{StatusText: "Internal server error", Message: "Please try again later."}
	var output bytes.Buffer
	if templateErr := renderer.pages["error"].ExecuteTemplate(&output, "layout", data); templateErr != nil {
		log.Printf("render generic web error page: %v", templateErr)
		output.WriteString("<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\"><title>Internal server error</title></head><body><h1>Internal server error</h1><p>Please try again later.</p><a href=\"/catalog\">Back to the catalog</a></body></html>")
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusInternalServerError)
	_, _ = c.Writer.Write(output.Bytes())
}

package web

import (
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"homework/internal/middleware"
	"homework/internal/model"
	"homework/internal/service"
)

type CatalogHandler struct {
	catalog  *service.CatalogService
	movies   *service.MovieService
	renderer *Renderer
	now      func() time.Time
	// chatEnabled adds a chat link to each row for logged-in users.
	chatEnabled bool
}

var catalogNotices = map[string]string{
	"created":    "Movie created successfully.",
	"updated":    "Movie updated successfully.",
	"registered": "User account created successfully.",
}

func NewCatalogHandler(catalog *service.CatalogService, movies *service.MovieService, renderer *Renderer, chatEnabled bool) *CatalogHandler {
	return &CatalogHandler{catalog: catalog, movies: movies, renderer: renderer, now: time.Now, chatEnabled: chatEnabled}
}

func (handler *CatalogHandler) Root(c *gin.Context) {
	c.Redirect(http.StatusFound, "/catalog")
}

func (handler *CatalogHandler) Catalog(c *gin.Context) {
	pageValue, hasPage := c.GetQuery("page")
	limitValue, hasLimit := c.GetQuery("limit")
	page, limit, err := service.ParseCatalogPagination(pageValue, limitValue, hasPage, hasLimit)
	if err != nil {
		handler.renderer.Page(c, http.StatusBadRequest, "error", ErrorPage{
			StatusText: "Bad Request",
			Message:    "Please provide a valid page and limit.",
		})
		return
	}
	movies, hasNext, err := handler.catalog.Search(c.Request.Context(), service.SearchParams{
		Q: c.Query("q"), Genre: c.Query("genre"), Page: page, Limit: limit,
	})
	if err != nil {
		var invalidPagination service.InvalidPaginationError
		if errors.As(err, &invalidPagination) {
			handler.renderer.Page(c, http.StatusBadRequest, "error", ErrorPage{
				StatusText: "Bad Request",
				Message:    "Please provide a valid page and limit.",
			})
			return
		}
		var validation service.ValidationError
		if errors.As(err, &validation) {
			handler.renderer.Page(c, http.StatusBadRequest, "error", ErrorPage{
				StatusText: "Bad Request",
				Message:    "Please shorten your search query.",
			})
			return
		}
		log.Printf("load catalog page: %v", err)
		handler.renderer.Page(c, http.StatusInternalServerError, "error", ErrorPage{
			StatusText: "Internal server error",
			Message:    "Please try again later.",
		})
		return
	}
	genres, err := handler.catalog.Genres(c.Request.Context())
	if err != nil {
		log.Printf("load catalog genres: %v", err)
		handler.renderer.Page(c, http.StatusInternalServerError, "error", ErrorPage{
			StatusText: "Internal server error",
			Message:    "Please try again later.",
		})
		return
	}

	now := handler.now()
	view := CatalogPage{
		BasePage: BasePage{Title: "Catalog - Movies"},
		Q:        c.Query("q"),
		Genre:    c.Query("genre"),
		Page:     page,
		Limit:    limit,
		HasPrev:  page > 1,
		HasNext:  hasNext,
	}
	view.Flash = catalogNotices[c.Query("notice")]
	if userID, ok := middleware.UserIDFrom(c); ok && userID > 0 {
		role, _ := middleware.RoleFrom(c)
		name, _ := middleware.DisplayNameFrom(c)
		view.User = &UserView{
			Name: name, RoleLabel: roleLabel(role),
			CanAddMovie: middleware.RoleAllowed(role, "editor", "admin"),
			CanRegister: role == "admin",
		}
	}
	view.Movies = make([]MovieRowView, 0, len(movies))
	view.Movies = handler.movieRows(c, movies, now)
	for _, genre := range genres {
		view.Genres = append(view.Genres, GenreOption{Value: genre, Label: genre, Selected: genre == view.Genre})
	}
	if view.HasPrev {
		view.PrevURL = pageURL(view.Q, view.Genre, limit, page-1)
	}
	if view.HasNext {
		view.NextURL = pageURL(view.Q, view.Genre, limit, page+1)
	}
	handler.renderer.Page(c, http.StatusOK, "catalog", view)
}

func (handler *CatalogHandler) NewMoviePage(c *gin.Context) {
	handler.renderMovieForm(c, http.StatusOK, MovieFormPage{
		BasePage:   BasePage{Title: "Add movie", User: userView(c)},
		Heading:    "Add movie",
		Action:     "/movies/new",
		SubmitText: "Create movie",
	})
}

func (handler *CatalogHandler) CreateMovie(c *gin.Context) {
	values, err := parseMovieForm(c)
	if err != nil {
		handler.renderFormError(c, http.StatusBadRequest, "Bad Request", "Please submit a valid form.")
		return
	}
	body, err := movieFormJSON(values)
	if err != nil {
		log.Printf("encode movie form: %v", err)
		handler.renderFormError(c, http.StatusInternalServerError, "Internal server error", "Please try again later.")
		return
	}
	if _, err := handler.movies.Create(c.Request.Context(), body); err != nil {
		var validation service.ValidationError
		if errors.As(err, &validation) {
			handler.renderMovieForm(c, http.StatusUnprocessableEntity, newMovieFormPage(c, values, []string{validation.Message}))
			return
		}
		log.Printf("create web movie: %v", err)
		handler.renderFormError(c, http.StatusInternalServerError, "Internal server error", "Please try again later.")
		return
	}
	c.Redirect(http.StatusSeeOther, "/catalog?notice=created")
}

func (handler *CatalogHandler) EditMoviePage(c *gin.Context) {
	id, err := service.ParseMovieID(c.Param("id"))
	if err != nil {
		handler.renderFormError(c, http.StatusBadRequest, "Bad Request", "Please provide a valid movie ID.")
		return
	}
	movie, err := handler.movies.GetByIDWithError(c.Request.Context(), id)
	if err != nil {
		handler.renderMovieLookupError(c, err)
		return
	}
	values := movieFormValues{
		title: movie.Title, director: movie.Director, country: movie.Country, genre: movie.Genre,
		synopsis: optionalString(movie.Synopsis),
	}
	if movie.ReleaseYear != nil {
		values.releaseYear = strconv.Itoa(*movie.ReleaseYear)
	}
	handler.renderMovieForm(c, http.StatusOK, editMovieFormPage(c, id, values, nil))
}

func (handler *CatalogHandler) UpdateMovie(c *gin.Context) {
	id, err := service.ParseMovieID(c.Param("id"))
	if err != nil {
		handler.renderFormError(c, http.StatusBadRequest, "Bad Request", "Please provide a valid movie ID.")
		return
	}
	if _, err := handler.movies.GetByIDWithError(c.Request.Context(), id); err != nil {
		handler.renderMovieLookupError(c, err)
		return
	}
	values, err := parseMovieForm(c)
	if err != nil {
		handler.renderFormError(c, http.StatusBadRequest, "Bad Request", "Please submit a valid form.")
		return
	}
	body, err := movieFormJSON(values)
	if err != nil {
		log.Printf("encode movie form: %v", err)
		handler.renderFormError(c, http.StatusInternalServerError, "Internal server error", "Please try again later.")
		return
	}
	if _, err := handler.movies.Replace(c.Request.Context(), id, body); err != nil {
		var validation service.ValidationError
		if errors.As(err, &validation) {
			handler.renderMovieForm(c, http.StatusUnprocessableEntity, editMovieFormPage(c, id, values, []string{validation.Message}))
			return
		}
		handler.renderMovieLookupError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, "/catalog?notice=updated")
}

func (handler *CatalogHandler) ChatPage(c *gin.Context) {
	id, err := service.ParseMovieID(c.Param("id"))
	if err != nil {
		handler.renderFormError(c, http.StatusBadRequest, "Bad Request", "Please provide a valid movie ID.")
		return
	}
	movie, err := handler.movies.GetByIDWithError(c.Request.Context(), id)
	if err != nil {
		handler.renderMovieLookupError(c, err)
		return
	}
	handler.renderer.Page(c, http.StatusOK, "chat", ChatPage{
		BasePage:   BasePage{Title: "Chat - " + movie.Title, User: userView(c)},
		MovieID:    id,
		MovieTitle: movie.Title,
	})
}

type movieFormValues struct {
	title       string
	director    string
	country     string
	genre       string
	releaseYear string
	synopsis    string
}

func parseMovieForm(c *gin.Context) (movieFormValues, error) {
	if err := c.Request.ParseForm(); err != nil {
		return movieFormValues{}, err
	}
	form := c.Request.PostForm
	return movieFormValues{
		title: form.Get("title"), director: form.Get("director"),
		country: form.Get("country"), genre: form.Get("genre"),
		releaseYear: form.Get("release_year"), synopsis: form.Get("synopsis"),
	}, nil
}

func movieFormJSON(values movieFormValues) ([]byte, error) {
	input := map[string]any{
		"title": strings.TrimSpace(values.title), "director": strings.TrimSpace(values.director),
		"country": strings.TrimSpace(values.country), "genre": strings.TrimSpace(values.genre),
	}
	releaseYear := strings.TrimSpace(values.releaseYear)
	if releaseYear != "" {
		if year, err := strconv.ParseInt(releaseYear, 10, strconv.IntSize); err == nil {
			input["release_year"] = int(year)
		} else {
			input["release_year"] = releaseYear
		}
	}
	if synopsis := strings.TrimSpace(values.synopsis); synopsis != "" {
		input["synopsis"] = synopsis
	}
	return json.Marshal(input)
}

func newMovieFormPage(c *gin.Context, values movieFormValues, formErrors []string) MovieFormPage {
	return MovieFormPage{
		BasePage:    BasePage{Title: "Add movie", User: userView(c)},
		Heading:     "Add movie",
		Action:      "/movies/new",
		SubmitText:  "Create movie",
		TitleValue:  values.title,
		Director:    values.director,
		Country:     values.country,
		Genre:       values.genre,
		ReleaseYear: values.releaseYear,
		Synopsis:    values.synopsis,
		Errors:      formErrors,
	}
}

func editMovieFormPage(c *gin.Context, id uint32, values movieFormValues, formErrors []string) MovieFormPage {
	return MovieFormPage{
		BasePage:    BasePage{Title: "Edit movie", User: userView(c)},
		Heading:     "Edit movie",
		Action:      "/movies/" + strconv.FormatUint(uint64(id), 10) + "/edit",
		SubmitText:  "Save changes",
		TitleValue:  values.title,
		Director:    values.director,
		Country:     values.country,
		Genre:       values.genre,
		ReleaseYear: values.releaseYear,
		Synopsis:    values.synopsis,
		Errors:      formErrors,
	}
}

func (handler *CatalogHandler) renderMovieForm(c *gin.Context, status int, page MovieFormPage) {
	genres, err := handler.catalog.Genres(c.Request.Context())
	if err != nil {
		log.Printf("load movie form genres: %v", err)
		handler.renderFormError(c, http.StatusInternalServerError, "Internal server error", "Please try again later.")
		return
	}
	page.Genres = genres
	handler.renderer.Page(c, status, "movie_form", page)
}

func (handler *CatalogHandler) renderMovieLookupError(c *gin.Context, err error) {
	var notFound service.NotFoundError
	if errors.As(err, &notFound) {
		handler.renderFormError(c, http.StatusNotFound, "Not Found", "The requested movie could not be found.")
		return
	}
	log.Printf("load web movie: %v", err)
	handler.renderFormError(c, http.StatusInternalServerError, "Internal server error", "Please try again later.")
}

func (handler *CatalogHandler) renderFormError(c *gin.Context, status int, title, message string) {
	handler.renderer.Page(c, status, "error", ErrorPage{
		BasePage:   BasePage{Title: title, User: userView(c)},
		StatusText: title,
		Message:    message,
	})
}

func userView(c *gin.Context) *UserView {
	userID, ok := middleware.UserIDFrom(c)
	if !ok || userID == 0 {
		return nil
	}
	role, _ := middleware.RoleFrom(c)
	name, _ := middleware.DisplayNameFrom(c)
	return &UserView{
		Name: name, RoleLabel: roleLabel(role),
		CanAddMovie: middleware.RoleAllowed(role, "editor", "admin"),
		CanRegister: role == "admin",
	}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (handler *CatalogHandler) Movies(c *gin.Context) {
	if !isHTMX(c) {
		query := c.Request.URL.Query()
		if encoded := query.Encode(); encoded != "" {
			c.Redirect(http.StatusSeeOther, "/catalog?"+encoded)
		} else {
			c.Redirect(http.StatusSeeOther, "/catalog")
		}
		return
	}

	pageValue, hasPage := c.GetQuery("page")
	limitValue, hasLimit := c.GetQuery("limit")
	page, limit, err := service.ParseCatalogPagination(pageValue, limitValue, hasPage, hasLimit)
	if err != nil {
		handler.searchError(c, http.StatusBadRequest, "Please provide a valid page and limit.")
		return
	}
	movies, _, err := handler.catalog.Search(c.Request.Context(), service.SearchParams{
		Q: c.Query("q"), Genre: c.Query("genre"), Page: page, Limit: limit,
	})
	if err != nil {
		var invalidPagination service.InvalidPaginationError
		if errors.As(err, &invalidPagination) {
			handler.searchError(c, http.StatusBadRequest, "Please provide a valid page and limit.")
			return
		}
		var validation service.ValidationError
		if errors.As(err, &validation) {
			handler.searchError(c, http.StatusBadRequest, "Please shorten your search query.")
			return
		}
		log.Printf("search catalog fragment: %v", err)
		handler.searchError(c, http.StatusInternalServerError, "Please try again later.")
		return
	}

	query := url.Values{}
	if q := c.Query("q"); q != "" {
		query.Set("q", q)
	}
	if genre := c.Query("genre"); genre != "" {
		query.Set("genre", genre)
	}
	pushURL := "/catalog"
	if encoded := query.Encode(); encoded != "" {
		pushURL += "?" + encoded
	}
	c.Header("HX-Push-Url", pushURL)
	handler.renderer.Fragment(c, http.StatusOK, "movie_rows", CatalogPage{
		Movies: handler.movieRows(c, movies, handler.now()),
	})
}

func (handler *CatalogHandler) DeleteMovie(c *gin.Context) {
	id, err := service.ParseMovieID(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if err := handler.movies.Delete(c.Request.Context(), id); err != nil {
		var notFound service.NotFoundError
		if errors.As(err, &notFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		log.Printf("delete web movie: %v", err)
		c.String(http.StatusInternalServerError, "Unable to delete movie.")
		return
	}
	// app.js shows the toast on this event; the body stays empty so HTMX removes the row.
	c.Header("HX-Trigger", "movieDeleted")
	c.Status(http.StatusOK)
}

func (handler *CatalogHandler) movieRows(c *gin.Context, movies []model.Movie, now time.Time) []MovieRowView {
	rows := make([]MovieRowView, 0, len(movies))
	userID, loggedIn := middleware.UserIDFrom(c)
	canChat := handler.chatEnabled && loggedIn && userID != 0
	role, _ := middleware.RoleFrom(c)
	for _, movie := range movies {
		row := MapMovie(movie, now)
		row.CanEdit = middleware.RoleAllowed(role, "editor", "admin")
		row.CanDelete = role == "admin"
		row.CanChat = canChat
		rows = append(rows, row)
	}
	return rows
}

func (handler *CatalogHandler) searchError(c *gin.Context, status int, message string) {
	c.Data(status, "text/html; charset=utf-8", []byte("<p>"+html.EscapeString(message)+"</p>"))
}

func roleLabel(role string) string {
	if role == "" {
		return ""
	}
	return strings.ToUpper(role[:1]) + role[1:]
}

func pageURL(query, genre string, limit, page int) string {
	values := url.Values{}
	values.Set("page", strconv.Itoa(page))
	values.Set("limit", strconv.Itoa(limit))
	if query != "" {
		values.Set("q", query)
	}
	if genre != "" {
		values.Set("genre", genre)
	}
	return "/catalog?" + values.Encode()
}
